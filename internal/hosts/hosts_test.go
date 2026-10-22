package hosts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRenderPreservesUnrelatedLines(t *testing.T) {
	existing := "127.0.0.1 localhost\n# keep me\n# BEGIN devstack\n127.0.0.1 old.test\n# END devstack\n10.0.0.1 intranet\n"
	got, err := Render(existing, []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"# keep me", "127.0.0.1 localhost", "10.0.0.1 intranet", "127.0.0.1 blog.test", "127.0.0.1 www.blog.test"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in %q", s, got)
		}
	}
	if strings.Contains(got, "old.test") {
		t.Fatal("old block not replaced")
	}
}

func TestRenderRejectsBadDomains(t *testing.T) {
	if _, err := Render("", []string{"evil.com"}); err == nil {
		t.Fatal("want error")
	}
}

func TestWriteAtomicPreservesMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q", data)
	}
}

func TestWriteAtomicDefaultsTo0644(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hosts")
	if err := WriteAtomic(p, []byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestRenderSortsDedupesAndIsIdempotent(t *testing.T) {
	got, err := Render("", []string{"www.blog.test", "blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	want := BeginMarker + "\n127.0.0.1 blog.test\n127.0.0.1 www.blog.test\n" + EndMarker + "\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	again, err := Render(got, []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("Render is not idempotent: %q != %q", again, got)
	}
}

func TestRenderAppendsBlockAndSupportsEmptySet(t *testing.T) {
	got, err := Render("127.0.0.1 localhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1 localhost\n" + BeginMarker + "\n" + EndMarker + "\n"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderRejectsUnbalancedMarkers(t *testing.T) {
	for _, existing := range []string{
		BeginMarker + "\n127.0.0.1 blog.test\n",
		"127.0.0.1 blog.test\n" + EndMarker + "\n",
		BeginMarker + "\n" + EndMarker + "\n" + BeginMarker + "\n" + EndMarker + "\n",
	} {
		if _, err := Render(existing, []string{"blog.test"}); err == nil {
			t.Fatalf("Render(%q) want error", existing)
		}
	}
}

func TestParseReturnsManagedDomains(t *testing.T) {
	content := "127.0.0.1 localhost\n" + BeginMarker + "\n127.0.0.1 blog.test\n127.0.0.1 www.blog.test\n" + EndMarker + "\n"
	domains, found := Parse(content)
	if !found {
		t.Fatal("managed block not found")
	}
	want := []string{"blog.test", "www.blog.test"}
	if !reflect.DeepEqual(domains, want) {
		t.Fatalf("domains = %v, want %v", domains, want)
	}
	if _, found := Parse("127.0.0.1 localhost\n"); found {
		t.Fatal("found managed block in unrelated content")
	}
}
