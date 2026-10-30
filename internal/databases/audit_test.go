package databases

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestDBConnectionValueUsesMariaDBForMariaDB(t *testing.T) {
	if got := dbConnectionValue(model.DatabaseMariaDB); got != "mariadb" {
		t.Fatalf("dbConnectionValue(MariaDB) = %q, want mariadb", got)
	}
	if got := dbConnectionValue(model.DatabaseMySQL); got != "mysql" {
		t.Fatalf("dbConnectionValue(MySQL) = %q, want mysql", got)
	}
}

func TestStableUserIDMatchesAgentShape(t *testing.T) {
	id := stableUserID("mysql", "app", "%")
	if !strings.HasPrefix(id, "usr_") {
		t.Fatalf("stableUserID = %q, want usr_ prefix", id)
	}
	if len(id) != len("usr_")+16 {
		t.Fatalf("stableUserID = %q (len %d), want usr_ plus 16 hex chars", id, len(id))
	}
	for _, r := range id[len("usr_"):] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("stableUserID = %q, want lowercase hex suffix", id)
		}
	}
	if again := stableUserID("mysql", "app", "%"); again != id {
		t.Fatalf("stableUserID not deterministic: %q != %q", again, id)
	}
	if other := stableUserID("postgresql", "app", "%"); other == id {
		t.Fatalf("stableUserID collision across services: %q", other)
	}
}
