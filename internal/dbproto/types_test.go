package dbproto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserUpdateRequestKeepsEmptyGrantList(t *testing.T) {
	raw, err := json.Marshal(UserUpdateRequest{ID: "mysql", UserID: "usr_1", Grants: []GrantInfo{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"grants":[]`) {
		t.Fatalf("empty grant list was omitted from the wire request: %s", raw)
	}
	var decoded UserUpdateRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Grants == nil {
		t.Fatal("empty grant list decoded as nil; revoke-all would be lost")
	}
}
