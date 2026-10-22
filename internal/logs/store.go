// Package logs captures per-site process output in rotated files and keeps a
// bounded in-memory history that the daemon and dashboard stream to clients.
//
// Each site writes to <dir>/<site-id>/current.log. The file is rotated when the
// next record would push it past the configured size, keeping the newest
// rotated files. Lines are stamped with a UTC timestamp and their source
// (app, fpm, proxy, supervisor), and secrets are redacted before they reach
// disk, the ring, or a subscriber.
package logs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// ringSize is the number of most recent lines retained per site.
	ringSize = 5000

	currentName   = "current.log"
	dirMode       = 0o750
	fileMode      = 0o640
	rotatedPrefix = "current."
	rotatedSuffix = ".log"

	// maxLineBytes caps a single unterminated line. A process that never emits
	// a newline must not grow the pending buffer without bound.
	maxLineBytes = 64 << 10
)

// ErrClosed is returned by operations on a closed Store.
var ErrClosed = errors.New("logs: store closed")

// Line is one log record. Text never contains a trailing newline and has
// secrets redacted.
type Line struct {
	Time   time.Time `json:"time"`
	SiteID string    `json:"site_id"`
	Source string    `json:"source"`
	Text   string    `json:"text"`
}

// Store writes and tails per-site logs. It is safe for concurrent use.
type Store struct {
	dir      string
	maxBytes int64 // 0 is the test hook: rotate before every write after the first
	keep     int

	mu      sync.Mutex
	sites   map[string]*site
	nextSub uint64
	closed  bool
}

// site is the in-memory state for one site.
type site struct {
	id      string
	dir     string
	file    *os.File
	size    int64
	ring    ring
	subs    map[uint64]*subscriber
	pending map[*writer][]byte
}

// New returns a store rooted at dir, creating it when needed. maxMB is the
// rotation threshold in megabytes and keep is the number of rotated files
// retained per site. A maxMB of zero is a test hook that rotates before every
// write after the first, so each write lands in its own file; production uses
// the configured 10 MB threshold.
func New(dir string, maxMB, keep int) (*Store, error) {
	if dir == "" {
		return nil, errors.New("logs: empty directory")
	}
	if maxMB < 0 {
		return nil, fmt.Errorf("logs: maxMB %d must not be negative", maxMB)
	}
	if keep < 0 {
		return nil, fmt.Errorf("logs: keep %d must not be negative", keep)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("logs: create %s: %w", dir, err)
	}
	return &Store{
		dir:      dir,
		maxBytes: int64(maxMB) << 20,
		keep:     keep,
		sites:    make(map[string]*site),
	}, nil
}

// writer is the io.Writer returned by Store.Writer. A writer keeps its own
// unterminated line so two processes writing to the same site cannot merge
// partial lines.
type writer struct {
	store  *Store
	siteID string
	source string
	err    error
}

// Writer returns an io.Writer that appends line-oriented output for siteID.
// source labels each line (app, fpm, proxy, supervisor) and defaults to
// "unknown" when empty. An invalid site id yields a writer whose Write always
// returns an error; the writer never creates files outside the store root.
func (s *Store) Writer(siteID, source string) io.Writer {
	w := &writer{store: s, siteID: siteID, source: sanitizeSource(source)}
	if err := validateSiteID(siteID); err != nil {
		w.err = err
	}
	return w
}

// Write splits p into lines, stamps each with the current UTC time and the
// writer's source, redacts secrets, and appends them to the site log. A
// trailing fragment without a newline is held until the next write or Close.
func (w *writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(p) == 0 {
		return 0, nil
	}

	s := w.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}

	st := s.siteLocked(w.siteID)

	buf := append(st.pending[w], p...)
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		text := strings.TrimSuffix(string(buf[:i]), "\r")
		if err := s.appendLocked(st, w.source, text); err != nil {
			st.setPending(w, buf[i+1:])
			return len(p), err
		}
		buf = buf[i+1:]
	}

	// Flush an overlong fragment rather than buffering it indefinitely.
	if len(buf) > maxLineBytes {
		if err := s.appendLocked(st, w.source, string(buf)); err != nil {
			st.setPending(w, nil)
			return len(p), err
		}
		buf = nil
	}

	st.setPending(w, buf)
	return len(p), nil
}

// Tail returns up to n of the most recent in-memory lines for siteID, oldest
// first. It returns an empty slice for an unknown site or n <= 0. Tail reads
// the ring only; output written by an earlier daemon process is on disk but is
// not loaded back into memory.
func (s *Store) Tail(siteID string, n int) ([]Line, error) {
	if err := validateSiteID(siteID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if n <= 0 {
		return []Line{}, nil
	}
	st := s.sites[siteID]
	if st == nil {
		return []Line{}, nil
	}
	return st.ring.tail(n), nil
}

// Close flushes unterminated lines, closes every log file, and closes all
// subscriber channels. It is idempotent.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var errs []error
	ids := make([]string, 0, len(s.sites))
	for id := range s.sites {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := s.sites[id]
		type pendingLine struct {
			source string
			text   string
		}
		pending := make([]pendingLine, 0, len(st.pending))
		for w, buf := range st.pending {
			if len(buf) > 0 {
				pending = append(pending, pendingLine{w.source, string(buf)})
			}
		}
		sort.Slice(pending, func(i, j int) bool { return pending[i].source < pending[j].source })
		for _, p := range pending {
			if err := s.appendLocked(st, p.source, p.text); err != nil {
				errs = append(errs, err)
			}
		}
		st.pending = nil
		if st.file != nil {
			if err := st.file.Close(); err != nil {
				errs = append(errs, fmt.Errorf("logs: close site %q: %w", id, err))
			}
			st.file = nil
		}
		for subID, sub := range st.subs {
			close(sub.ch)
			delete(st.subs, subID)
		}
		st.subs = nil
	}
	s.sites = nil
	return errors.Join(errs...)
}

// siteLocked returns the state for siteID, creating it on first use. It does
// not touch the filesystem; the directory and file are opened on the first
// complete line.
func (s *Store) siteLocked(siteID string) *site {
	if st, ok := s.sites[siteID]; ok {
		return st
	}
	st := &site{
		id:      siteID,
		dir:     filepath.Join(s.dir, siteID),
		subs:    make(map[uint64]*subscriber),
		pending: make(map[*writer][]byte),
	}
	s.sites[siteID] = st
	return st
}

// appendLocked records one line in the ring and to subscribers, then writes it
// to the site's current log. The in-memory copy is kept even when the disk
// write fails so a full disk does not hide output from the dashboard.
func (s *Store) appendLocked(st *site, source, text string) error {
	line := Line{
		Time:   time.Now().UTC(),
		SiteID: st.id,
		Source: source,
		Text:   Redact(text),
	}
	st.ring.add(line)
	s.fanOutLocked(st, line)

	record := formatLine(line)
	if s.shouldRotate(st, int64(len(record))) {
		if err := s.rotateLocked(st); err != nil {
			return err
		}
	}
	if err := s.openLocked(st); err != nil {
		return err
	}
	n, err := st.file.WriteString(record)
	st.size += int64(n)
	if err != nil {
		return fmt.Errorf("logs: write site %q: %w", st.id, err)
	}
	return nil
}

// shouldRotate reports whether writing add more bytes requires a rotation.
// maxBytes <= 0 rotates whenever the current file is non-empty, which is the
// zero-value test hook documented on New.
func (s *Store) shouldRotate(st *site, add int64) bool {
	if st.size == 0 {
		return false
	}
	if s.maxBytes <= 0 {
		return true
	}
	return st.size+add > s.maxBytes
}

// openLocked opens current.log for append, rotating it first when it is already
// at or past the threshold (for example after a daemon restart).
func (s *Store) openLocked(st *site) error {
	if st.file != nil {
		return nil
	}
	if err := os.MkdirAll(st.dir, dirMode); err != nil {
		return fmt.Errorf("logs: create %s: %w", st.dir, err)
	}

	path := filepath.Join(st.dir, currentName)
	info, err := os.Stat(path)
	switch {
	case err == nil && s.maxBytes > 0 && info.Size() >= s.maxBytes:
		return s.rotateLocked(st)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("logs: stat %s: %w", path, err)
	}
	return s.openFreshLocked(st)
}

// openFreshLocked creates or appends to current.log and records its size.
func (s *Store) openFreshLocked(st *site) error {
	path := filepath.Join(st.dir, currentName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return fmt.Errorf("logs: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logs: stat %s: %w", path, err)
	}
	st.file = f
	st.size = info.Size()
	return nil
}

// rotateLocked moves current.log to a timestamped name, prunes old rotated
// files, and opens a fresh current.log so the path never disappears. The file
// is closed before the rename so the operation also works on platforms that
// cannot rename open files.
func (s *Store) rotateLocked(st *site) error {
	if st.file != nil {
		f := st.file
		st.file = nil
		if err := f.Close(); err != nil {
			return fmt.Errorf("logs: close site %q for rotation: %w", st.id, err)
		}
	}
	st.size = 0

	path := filepath.Join(st.dir, currentName)
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, rotatedPath(st.dir)); err != nil {
			return fmt.Errorf("logs: rotate %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("logs: stat %s: %w", path, err)
	}

	pruneErr := s.pruneLocked(st.dir)
	openErr := s.openFreshLocked(st)
	return errors.Join(pruneErr, openErr)
}

// rotatedPath returns an unused rotated filename. The timestamp sorts
// chronologically and the counter disambiguates same-nanosecond rotations.
func rotatedPath(dir string) string {
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	for i := 0; ; i++ {
		name := rotatedPrefix + stamp
		if i > 0 {
			name += fmt.Sprintf("-%d", i)
		}
		path := filepath.Join(dir, name+rotatedSuffix)
		_, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return path
		case err != nil:
			// Let Rename surface the real error.
			return path
		}
	}
}

// pruneLocked removes the oldest rotated files so at most keep remain.
func (s *Store) pruneLocked(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("logs: list %s: %w", dir, err)
	}
	var rotated []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == currentName {
			continue
		}
		if strings.HasPrefix(name, rotatedPrefix) && strings.HasSuffix(name, rotatedSuffix) {
			rotated = append(rotated, name)
		}
	}
	sort.Strings(rotated)
	for len(rotated) > s.keep {
		if err := os.Remove(filepath.Join(dir, rotated[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("logs: remove rotated file %s: %w", rotated[0], err)
		}
		rotated = rotated[1:]
	}
	return nil
}

// setPending replaces the writer's buffered fragment. The bytes are copied so
// the buffer never aliases the caller's slice.
func (st *site) setPending(w *writer, buf []byte) {
	if len(buf) == 0 {
		delete(st.pending, w)
		return
	}
	st.pending[w] = bytes.Clone(buf)
}

// formatLine renders a line in the on-disk format:
// <RFC3339Nano UTC> [<source>] <text>\n.
func formatLine(line Line) string {
	return line.Time.UTC().Format(time.RFC3339Nano) + " [" + line.Source + "] " + line.Text + "\n"
}

// sanitizeSource keeps a source label on one line and defaults an empty label.
func sanitizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(source))
	for _, r := range source {
		if unicode.IsControl(r) {
			b.WriteRune('-')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validateSiteID rejects ids that could escape the store directory or create
// ambiguous paths.
func validateSiteID(siteID string) error {
	if siteID == "" {
		return errors.New("logs: empty site id")
	}
	if siteID == "." || siteID == ".." {
		return fmt.Errorf("logs: invalid site id %q", siteID)
	}
	if strings.ContainsAny(siteID, `/\`) || strings.ContainsRune(siteID, 0) {
		return fmt.Errorf("logs: invalid site id %q", siteID)
	}
	if filepath.Base(siteID) != siteID {
		return fmt.Errorf("logs: invalid site id %q", siteID)
	}
	for _, r := range siteID {
		if unicode.IsControl(r) {
			return fmt.Errorf("logs: invalid site id %q", siteID)
		}
	}
	return nil
}

// ring is a fixed-capacity circular buffer of lines.
type ring struct {
	buf   []Line
	start int
	count int
}

// add appends line, dropping the oldest entry when full.
func (r *ring) add(line Line) {
	if len(r.buf) == 0 {
		r.buf = make([]Line, ringSize)
	}
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = line
		r.count++
		return
	}
	r.buf[r.start] = line
	r.start = (r.start + 1) % len(r.buf)
}

// tail returns a copy of the newest n lines in chronological order.
func (r *ring) tail(n int) []Line {
	if n > r.count {
		n = r.count
	}
	out := make([]Line, n)
	for i := 0; i < n; i++ {
		out[i] = r.buf[(r.start+r.count-n+i)%len(r.buf)]
	}
	return out
}
