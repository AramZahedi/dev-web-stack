package logs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newStore returns a store rooted in a temporary directory. The store is
// closed at the end of the test.
func newStore(t *testing.T, maxMB, keep int) *Store {
	t.Helper()
	s, err := New(t.TempDir(), maxMB, keep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close logs: %v", err)
		}
	})
	return s
}

func TestRotateAndTail(t *testing.T) {
	s := newStore(t, 0, 2) // 0 MB => rotate after each write in test hook
	w := s.Writer("site1", "app")
	if _, err := io.WriteString(w, "one\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "two\n"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Tail("site1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[1].Text != "two" {
		t.Fatalf("%+v", lines)
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "APP_KEY=abc SECRET_TOKEN=xyz password=hunter2"
	out := Redact(in)
	for _, secret := range []string{"abc", "xyz", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Fatalf("leaked %q in %q", secret, out)
		}
	}
}

func TestRedactAuthorizationAndVariants(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		secret string
	}{
		{"authorization header", "Authorization: Bearer abc123", "abc123"},
		{"lowercase authorization", "authorization: Basic dXNlcjpwYXNz", "dXNlcjpwYXNz"},
		{"authorization in command", `curl -H "Authorization: Bearer tok-456"`, "tok-456"},
		{"token assignment", "api_token=xyz", "xyz"},
		{"key in the middle", "MY_KEY_ID=abc123", "abc123"},
		{"dotted key", "secret.foo=barval", "barval"},
		{"quoted password", `password="hunter2"`, "hunter2"},
		{"colon secret", "secret: s3cr3t", "s3cr3t"},
		{"json password", `{"password":"hunter2","user":"me"}`, "hunter2"},
		{"json authorization", `{"authorization":"Bearer tok-789"}`, "tok-789"},
		{"database url", "DATABASE_URL=postgres://user:s3cret@db.example/app", "s3cret"},
		{"cookie header", "Cookie: session=abc123; theme=dark", "abc123"},
		{"set-cookie header", "Set-Cookie: session=xyz789; HttpOnly", "xyz789"},
		{"json cookie", `{"headers":{"Cookie":"session=abc123"}}`, "abc123"},
		{"json set-cookie", `{"headers":{"Set-Cookie":"session=xyz789"}}`, "xyz789"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := Redact(tc.in)
			if strings.Contains(out, tc.secret) {
				t.Fatalf("leaked %q in %q", tc.secret, out)
			}
		})
	}

	if got, want := Redact("listening on 127.0.0.1:41000"), "listening on 127.0.0.1:41000"; got != want {
		t.Fatalf("Redact changed non-secret text: %q", got)
	}
	if got := Redact(`{"password":"hunter2","user":"me"}`); !json.Valid([]byte(got)) {
		t.Fatalf("redacted JSON is invalid: %q", got)
	}
}

func TestWriterSplitsLinesAndStampsSource(t *testing.T) {
	s := newStore(t, 10, 5)
	w := s.Writer("site1", "app")
	before := time.Now().Add(-time.Second)

	if _, err := io.WriteString(w, "alpha\r\nbeta\n"); err != nil {
		t.Fatal(err)
	}
	// A trailing fragment is not a line until its newline arrives.
	if _, err := io.WriteString(w, "gamma"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Tail("site1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Text != "alpha" || lines[1].Text != "beta" {
		t.Fatalf("after first write: %+v", lines)
	}

	if _, err := io.WriteString(w, "\ndelta\n"); err != nil {
		t.Fatal(err)
	}
	lines, err = s.Tail("site1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[2].Text != "gamma" || lines[3].Text != "delta" {
		t.Fatalf("split lines = %+v", lines)
	}
	for _, line := range lines {
		if line.SiteID != "site1" || line.Source != "app" {
			t.Fatalf("line not stamped: %+v", line)
		}
		if line.Time.Before(before) || line.Time.After(time.Now().Add(time.Second)) {
			t.Fatalf("line time out of range: %v", line.Time)
		}
	}
}

func TestRotationKeepsConfiguredFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 0, 2) // 0 MB => rotate before every write after the first
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close logs: %v", err)
		}
	}()

	w := s.Writer("site1", "app")
	for i := 0; i < 5; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(dir, "site1"))
	if err != nil {
		t.Fatal(err)
	}
	var rotated []string
	current := false
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case name == "current.log":
			current = true
		case strings.HasPrefix(name, "current.") && strings.HasSuffix(name, ".log"):
			rotated = append(rotated, name)
		}
	}
	if !current {
		t.Fatal("current.log missing")
	}
	if len(rotated) != 2 {
		t.Fatalf("rotated files = %v, want 2", rotated)
	}

	data, err := os.ReadFile(filepath.Join(dir, "site1", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[app] line-4") {
		t.Fatalf("current.log = %q", data)
	}
	if strings.Contains(string(data), "line-0") {
		t.Fatalf("current.log kept an old line: %q", data)
	}
}

func TestExistingOversizedFileRotatesOnNextWrite(t *testing.T) {
	dir := t.TempDir()
	siteDir := filepath.Join(dir, "site1")
	if err := os.MkdirAll(siteDir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("x", (1<<20)+1) + "\n"
	if err := os.WriteFile(filepath.Join(siteDir, "current.log"), []byte(old), 0o640); err != nil {
		t.Fatal(err)
	}

	s, err := New(dir, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close logs: %v", err)
		}
	}()

	if _, err := io.WriteString(s.Writer("site1", "app"), "new\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(siteDir, "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[app] new") || strings.Contains(string(data), "xxxx") {
		t.Fatalf("current.log = %q", data)
	}
	entries, err := os.ReadDir(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	rotated := 0
	for _, entry := range entries {
		name := entry.Name()
		if name != "current.log" && strings.HasPrefix(name, "current.") && strings.HasSuffix(name, ".log") {
			rotated++
		}
	}
	if rotated != 1 {
		t.Fatalf("rotated files = %d, want 1", rotated)
	}
}

func TestTailLimitsAndOrdersLines(t *testing.T) {
	s := newStore(t, 10, 5)
	w := s.Writer("site1", "app")
	for i := 0; i < 10; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := s.Tail("site1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("len = %d, want 3", len(lines))
	}
	for i, want := range []string{"line-7", "line-8", "line-9"} {
		if lines[i].Text != want {
			t.Fatalf("lines[%d] = %q, want %q", i, lines[i].Text, want)
		}
	}

	// The returned slice is a copy; mutating it must not corrupt the ring.
	lines[0].Text = "mutated"
	again, err := s.Tail("site1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Text != "line-7" {
		t.Fatalf("ring was mutated: %+v", again)
	}

	if got, err := s.Tail("site1", 0); err != nil || len(got) != 0 {
		t.Fatalf("Tail(0) = %v, %v", got, err)
	}
	if got, err := s.Tail("site1", 100); err != nil || len(got) != 10 {
		t.Fatalf("Tail(100) = %d lines, %v", len(got), err)
	}
	if got, err := s.Tail("missing", 5); err != nil || len(got) != 0 {
		t.Fatalf("Tail(missing) = %v, %v", got, err)
	}
}

func TestRingKeepsFiveThousandLines(t *testing.T) {
	s := newStore(t, 10, 5)
	w := s.Writer("site1", "app")
	const extra = 100
	for i := 0; i < ringSize+extra; i++ {
		if _, err := fmt.Fprintf(w, "line-%04d\n", i); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := s.Tail("site1", ringSize+extra)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != ringSize {
		t.Fatalf("len = %d, want %d", len(lines), ringSize)
	}
	if got, want := lines[0].Text, "line-0100"; got != want {
		t.Fatalf("first = %q, want %q", got, want)
	}
	if got, want := lines[len(lines)-1].Text, fmt.Sprintf("line-%04d", ringSize+extra-1); got != want {
		t.Fatalf("last = %q, want %q", got, want)
	}
}

func TestSourceIsSanitized(t *testing.T) {
	s := newStore(t, 10, 5)
	if _, err := io.WriteString(s.Writer("site1", "fpm\npool"), "one\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(s.Writer("site1", ""), "two\n"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Tail("site1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].Source != "fpm-pool" {
		t.Fatalf("source = %q, want fpm-pool", lines[0].Source)
	}
	if lines[1].Source != "unknown" {
		t.Fatalf("empty source = %q, want unknown", lines[1].Source)
	}
}

func TestOverlongPartialLineIsFlushed(t *testing.T) {
	s := newStore(t, 10, 5)
	w := s.Writer("site1", "app")
	long := strings.Repeat("a", maxLineBytes+100)
	if _, err := io.WriteString(w, long); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Tail("site1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0].Text) != len(long) {
		t.Fatalf("overlong line not flushed: %d lines", len(lines))
	}
}

func TestKeepZeroRemovesRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close logs: %v", err)
		}
	}()
	w := s.Writer("site1", "app")
	for i := 0; i < 3; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "site1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "current.log" {
		t.Fatalf("entries = %v, want only current.log", entries)
	}
}

func TestSubscribeFansOutAndUnsubscribes(t *testing.T) {
	s := newStore(t, 10, 5)
	ch1, cancel1 := s.Subscribe("site1")
	defer cancel1()
	ch2, cancel2 := s.Subscribe("site1")
	defer cancel2()

	w := s.Writer("site1", "app")
	if _, err := io.WriteString(w, "hello\n"); err != nil {
		t.Fatal(err)
	}
	for i, ch := range []<-chan Line{ch1, ch2} {
		select {
		case line, ok := <-ch:
			if !ok || line.Text != "hello" {
				t.Fatalf("subscriber %d got %+v (open=%v)", i, line, ok)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}

	cancel1()
	if _, ok := <-ch1; ok {
		t.Fatal("unsubscribed channel is still open")
	}
	cancel1() // unsubscribing twice is a no-op

	if _, err := io.WriteString(w, "world\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line, ok := <-ch2:
		if !ok || line.Text != "world" {
			t.Fatalf("remaining subscriber got %+v (open=%v)", line, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remaining subscriber timed out")
	}
}

func TestSubscribeIsolatesSites(t *testing.T) {
	s := newStore(t, 10, 5)
	ch, cancel := s.Subscribe("site1")
	defer cancel()

	if _, err := io.WriteString(s.Writer("site2", "app"), "other\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-ch:
		t.Fatalf("site1 subscriber received %+v", line)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := io.WriteString(s.Writer("site1", "app"), "mine\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-ch:
		if line.Text != "mine" {
			t.Fatalf("got %+v", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("site1 subscriber timed out")
	}
}

func TestSlowSubscriberDoesNotBlockWrites(t *testing.T) {
	s := newStore(t, 10, 5)
	_, cancel := s.Subscribe("site1")
	defer cancel()

	w := s.Writer("site1", "app")
	done := make(chan error, 1)
	go func() {
		for i := 0; i < subBuffer*2; i++ {
			if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writes blocked on a slow subscriber")
	}
}

func TestRedactionAppliesToFileAndStream(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close logs: %v", err)
		}
	}()

	ch, cancel := s.Subscribe("site1")
	defer cancel()
	if _, err := io.WriteString(s.Writer("site1", "app"), "password=hunter2\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case line := <-ch:
		if strings.Contains(line.Text, "hunter2") {
			t.Fatalf("stream leaked secret: %q", line.Text)
		}
		if !strings.Contains(line.Text, "password=***") {
			t.Fatalf("stream line = %q", line.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for line")
	}

	data, err := os.ReadFile(filepath.Join(dir, "site1", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("file leaked secret: %q", data)
	}
}

func TestInvalidSiteIDIsRejected(t *testing.T) {
	s := newStore(t, 10, 5)
	for _, id := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "a\x00b"} {
		w := s.Writer(id, "app")
		if _, err := io.WriteString(w, "x\n"); err == nil {
			t.Fatalf("Writer(%q) accepted the id", id)
		}
		if _, err := s.Tail(id, 1); err == nil {
			t.Fatalf("Tail(%q) accepted the id", id)
		}
		ch, cancel := s.Subscribe(id)
		cancel()
		if _, ok := <-ch; ok {
			t.Fatalf("Subscribe(%q) returned an open channel", id)
		}
	}
}

func TestCloseFlushesPartialLineAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	w := s.Writer("site1", "app")
	if _, err := io.WriteString(w, "no newline"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "site1", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "no newline") {
		t.Fatalf("partial line not flushed: %q", data)
	}

	if _, err := io.WriteString(w, "after\n"); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if _, err := s.Tail("site1", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("tail after close: %v", err)
	}
}

func TestCloseClosesSubscribers(t *testing.T) {
	s, err := New(t.TempDir(), 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel := s.Subscribe("site1")
	defer cancel()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("subscriber channel is still open after Close")
	}

	ch2, cancel2 := s.Subscribe("site1")
	defer cancel2()
	if _, ok := <-ch2; ok {
		t.Fatal("Subscribe after Close returned an open channel")
	}
}

func TestConcurrentWritesStayLineOriented(t *testing.T) {
	s := newStore(t, 10, 5)
	const (
		writers    = 8
		perWriter  = 100
		totalLines = writers * perWriter
	)

	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			w := s.Writer("site1", "app")
			for i := 0; i < perWriter; i++ {
				if _, err := fmt.Fprintf(w, "g%d-%d\n", g, i); err != nil {
					t.Errorf("writer %d: %v", g, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	lines, err := s.Tail("site1", totalLines)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != totalLines {
		t.Fatalf("len = %d, want %d", len(lines), totalLines)
	}
	seen := make(map[string]int, totalLines)
	for _, line := range lines {
		seen[line.Text]++
	}
	for g := 0; g < writers; g++ {
		for i := 0; i < perWriter; i++ {
			want := fmt.Sprintf("g%d-%d", g, i)
			if seen[want] != 1 {
				t.Fatalf("%q seen %d times", want, seen[want])
			}
		}
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name  string
		dir   string
		maxMB int
		keep  int
	}{
		{"empty dir", "", 10, 5},
		{"negative max", t.TempDir(), -1, 5},
		{"negative keep", t.TempDir(), 10, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.dir, tc.maxMB, tc.keep); err == nil {
				t.Fatal("New accepted invalid config")
			}
		})
	}
}
