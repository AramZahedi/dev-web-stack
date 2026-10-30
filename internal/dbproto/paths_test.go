package dbproto

import "testing"

func TestPathsEscapeSegments(t *testing.T) {
	if got := ServiceDatabase("mysql", "app db"); got != "/v1/services/mysql/databases/app%20db" {
		t.Fatalf("path = %q", got)
	}
	if got := ServiceUser("mysql", "usr/1"); got != "/v1/services/mysql/users/usr%2F1" {
		t.Fatalf("path = %q", got)
	}
}
