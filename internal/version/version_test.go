package version

import "testing"

func TestString(t *testing.T) {
	oldV, oldC := Version, Commit
	t.Cleanup(func() { Version, Commit = oldV, oldC })
	Version, Commit = "1.2.3", "abc123"
	if got := String(); got != "1.2.3 (abc123)" {
		t.Fatalf("String() = %q", got)
	}
	Commit = ""
	if got := String(); got != "1.2.3" {
		t.Fatalf("String() = %q", got)
	}
}
