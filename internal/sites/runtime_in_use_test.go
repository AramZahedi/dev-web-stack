package sites

import (
	"strings"
	"testing"
)

func TestRuntimeInUseErrorMessageNamesTheRuntimeKind(t *testing.T) {
	node := (&RuntimeInUseError{BuildID: "node@24.22.1", Sites: []string{"app"}}).Error()
	if !strings.Contains(node, "Node runtime") || strings.Contains(node, "PHP runtime") {
		t.Fatalf("node in-use message = %q", node)
	}
	php := (&RuntimeInUseError{BuildID: "php@8.4.27-r1", Sites: []string{"blog"}}).Error()
	if !strings.Contains(php, "PHP runtime") || strings.Contains(php, "Node runtime") {
		t.Fatalf("PHP in-use message = %q", php)
	}
	unknown := (&RuntimeInUseError{BuildID: "runtime@1", Sites: []string{"app"}}).Error()
	if !strings.Contains(unknown, "runtime") || strings.Contains(unknown, "PHP runtime") {
		t.Fatalf("unknown in-use message = %q", unknown)
	}
}
