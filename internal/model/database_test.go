package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDatabaseModelsJSONShape(t *testing.T) {
	link := DatabaseLink{
		ID:        "lnk_1",
		SiteID:    "site_1",
		ServiceID: "mysql",
		Database:  "app",
		Username:  "app",
		Primary:   true,
	}
	raw, err := json.Marshal(link)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"password":`) {
		t.Fatalf("link leaks a password field: %s", raw)
	}
	if !strings.Contains(string(raw), `"database":"app"`) {
		t.Fatalf("link JSON = %s", raw)
	}
}
