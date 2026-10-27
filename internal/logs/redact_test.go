package logs

import (
	"strings"
	"testing"
)

func TestRedactPasswordsAndCredentials(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"DB_PASS assignment", "DB_PASS=secret", "DB_PASS=***"},
		{"MYSQL_PASS assignment", "MYSQL_PASS=secret", "MYSQL_PASS=***"},
		{"DB_PWD assignment", "DB_PWD=secret", "DB_PWD=***"},
		{"PG_PASS assignment", "PG_PASS=secret", "PG_PASS=***"},
		{"PGPASSWORD assignment", "PGPASSWORD=super_secret_pg", "PGPASSWORD=***"},
		{"DB_PASS with quotes", `DB_PASS="secret"`, "DB_PASS=***"},
		{"MYSQL_PASS with single quotes", "MYSQL_PASS='secret'", "MYSQL_PASS=***"},
		{"DB_PWD with colon", "DB_PWD: secret", "DB_PWD: ***"},
		{"PG_PASS in json", `{"PG_PASS":"secret"}`, `{"PG_PASS":"***"}`},
		{"DB_PASS in json unquoted", `{"db_pass":secret}`, `{"db_pass":***}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			if got != tc.want {
				t.Fatalf("Redact(%q) = %q; want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "secret") {
				t.Fatalf("Redact(%q) leaked secret: %q", tc.in, got)
			}
		})
	}
}
