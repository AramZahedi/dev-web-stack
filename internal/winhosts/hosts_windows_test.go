//go:build windows

package winhosts

import (
	"strings"
	"testing"
)

func TestRenderPreservesOutsideHostsEntries(t *testing.T) {
	existing := "127.0.0.1 localhost\r\n192.0.2.3 intranet.example\r\n# BEGIN devstack\r\n127.0.0.1 old.test\r\n# END devstack\r\n::1 localhost\r\n"
	got, err := Render(existing, []string{"new.test", "devstack.test", "new.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"127.0.0.1 localhost", "192.0.2.3 intranet.example", "::1 localhost"} {
		if !strings.Contains(got, keep) {
			t.Errorf("rendered hosts lost unrelated entry %q:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "old.test") || !strings.Contains(got, "127.0.0.1 devstack.test") || !strings.Contains(got, "127.0.0.1 new.test") {
		t.Fatalf("managed block was not replaced/deduplicated:\n%s", got)
	}
}

func TestRenderRejectsMalformedManagedBlock(t *testing.T) {
	if _, err := Render("# BEGIN devstack\n127.0.0.1 user.test\n", []string{"site.test"}); err == nil {
		t.Fatal("Render accepted a malformed managed block")
	}
}

func TestRenderKeepsWindowsLineEndingsConsistent(t *testing.T) {
	got, err := Render("127.0.0.1 localhost\r\n", []string{"site.test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("rendered Windows hosts block contains mixed LF endings: %q", got)
	}
}
