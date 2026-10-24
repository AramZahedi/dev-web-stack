// Package hosts manages the marked block of the system hosts file that
// resolves devstack domains to loopback. Lines outside the block are left
// untouched.
package hosts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

const (
	// BeginMarker opens the managed block.
	BeginMarker = "# BEGIN devstack"
	// EndMarker closes the managed block.
	EndMarker = "# END devstack"

	// defaultMode is used when WriteAtomic creates a new file and the caller
	// does not provide a mode.
	defaultMode fs.FileMode = 0o644
)

// Render returns existing with the managed devstack block replaced by the
// supplied domains. Only lines between BeginMarker and EndMarker are replaced;
// everything else is preserved. Domains are validated, sorted, and deduped,
// and every entry resolves to 127.0.0.1. An empty domain list leaves an empty
// managed block so uninstall can remove the mappings without touching the
// rest of the file.
func Render(existing string, domains []string) (string, error) {
	managed, err := normalizeDomains(domains)
	if err != nil {
		return "", err
	}
	block := make([]string, 0, len(managed)+2)
	block = append(block, BeginMarker)
	for _, d := range managed {
		block = append(block, "127.0.0.1 "+d)
	}
	block = append(block, EndMarker)

	lines := splitLines(existing)
	begin, end := -1, -1
	begins, ends := 0, 0
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case BeginMarker:
			begins++
			if begin < 0 {
				begin = i
			}
		case EndMarker:
			ends++
			if end < 0 {
				end = i
			}
		}
	}

	if begins == 0 && ends == 0 {
		return joinLines(append(lines, block...)), nil
	}
	if begins != 1 || ends != 1 || begin > end {
		return "", fmt.Errorf("hosts: malformed devstack block: found %d begin and %d end markers", begins, ends)
	}

	out := make([]string, 0, len(lines)-(end-begin+1)+len(block))
	out = append(out, lines[:begin]...)
	out = append(out, block...)
	out = append(out, lines[end+1:]...)
	return joinLines(out), nil
}

// Parse returns the domains inside the managed block and whether a complete
// block was present. Only 127.0.0.1 entries are returned.
func Parse(content string) (domains []string, found bool) {
	lines := splitLines(content)
	begin, end := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case BeginMarker:
			if begin < 0 {
				begin = i
			}
		case EndMarker:
			if begin >= 0 && end < 0 {
				end = i
			}
		}
	}
	if begin < 0 || end < begin {
		return nil, false
	}
	for _, line := range lines[begin+1 : end] {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "127.0.0.1" {
			domains = append(domains, fields[1])
		}
	}
	return domains, true
}

// WriteAtomic replaces path with data using a temporary file in the same
// directory, fsyncs the data, preserves the mode of an existing file (or uses
// mode, defaulting to 0644), and renames the temporary file into place.
func WriteAtomic(path string, data []byte, mode fs.FileMode) error {
	if path == "" {
		return errors.New("hosts: path is empty")
	}
	if mode == 0 {
		mode = defaultMode
	}
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return fmt.Errorf("hosts: %s is a directory", path)
		}
		mode = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("hosts: stat %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		// systemd's ProtectSystem=strict plus a file-level ReadWritePaths entry
		// for /etc/hosts makes the file writable while its parent directory
		// remains read-only. The helper cannot create a sibling temp file in
		// that namespace, so fall back to an in-place rewrite. The file is
		// small and this path is only used by the privileged helper.
		if errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) {
			return writeInPlace(path, data, mode)
		}
		return fmt.Errorf("hosts: create temp file in %s: %w", dir, err)
	}
	tmp := f.Name()
	remove := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if _, err := f.Write(data); err != nil {
		remove()
		return fmt.Errorf("hosts: write %s: %w", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		remove()
		return fmt.Errorf("hosts: chmod %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		remove()
		return fmt.Errorf("hosts: sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosts: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hosts: rename %s to %s: %w", tmp, path, err)
	}
	// Make the rename itself durable. Directory fsync is best effort because
	// some filesystems do not support it.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// writeInPlace is the reduced-atomicity fallback for a file-level writable
// mount whose parent directory is read-only (systemd ProtectSystem=strict with
// ReadWritePaths=/etc/hosts). It keeps the existing mode and fsyncs the file.
func writeInPlace(path string, data []byte, mode fs.FileMode) error {
	// Do not use O_TRUNC here. Under ProtectSystem=strict this is the only
	// writable path, and a crash between open and write must not leave
	// /etc/hosts empty. Overwrite first, then truncate to the new length.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, mode)
	if err != nil {
		return fmt.Errorf("hosts: write %s in place: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("hosts: lock %s: %w", path, err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	if _, err := f.WriteAt(data, 0); err != nil {
		return fmt.Errorf("hosts: write %s in place: %w", path, err)
	}
	if err := f.Truncate(int64(len(data))); err != nil {
		return fmt.Errorf("hosts: truncate %s: %w", path, err)
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("hosts: chmod %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("hosts: sync %s: %w", path, err)
	}
	return nil
}

func normalizeDomains(domains []string) ([]string, error) {
	set := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		if err := domain.ValidateDomain(d); err != nil {
			return nil, fmt.Errorf("hosts: invalid domain: %w", err)
		}
		set[d] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// splitLines splits content into lines without the trailing newline sentinel,
// preserving interior blank lines.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
