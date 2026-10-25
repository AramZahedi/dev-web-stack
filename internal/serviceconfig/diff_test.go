package serviceconfig

import (
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	orig := []byte("line1\nline2\nline3\n")
	mod := []byte("line1\nline2 modified\nline3\n")

	diff := UnifiedDiff("test.conf", orig, mod)
	if !strings.Contains(diff, "-line2") || !strings.Contains(diff, "+line2 modified") {
		t.Fatalf("unexpected diff:\n%s", diff)
	}
}

func TestUnifiedDiffNoChanges(t *testing.T) {
	orig := []byte("line1\nline2\n")
	diff := UnifiedDiff("test.conf", orig, orig)
	if diff != "" {
		t.Fatalf("expected empty diff, got %q", diff)
	}
}
