package services

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStateConstants(t *testing.T) {
	states := []State{
		StateNotInstalled,
		StateInstalled,
		StateInstalledButStopped,
		StateStarting,
		StateActive,
		StateStopping,
		StateError,
	}
	for _, s := range states {
		if s == "" {
			t.Errorf("empty State constant")
		}
	}
	if StateInstalledButStopped != "installed_but_stopped" {
		t.Errorf("StateInstalledButStopped = %q, want installed_but_stopped", StateInstalledButStopped)
	}
}

func TestRecordFields(t *testing.T) {
	now := time.Now().UTC()
	r := Record{
		ID:            IDMySQL,
		State:         StateActive,
		Image:         "mysql@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Active:        true,
		LastError:     "",
		LastOperation: "activate",
		UpdatedAt:     now,
	}
	if r.ID != IDMySQL || r.State != StateActive || !r.Active || r.UpdatedAt != now {
		t.Fatalf("unexpected record values: %+v", r)
	}
}

func TestViewNewFieldsJSON(t *testing.T) {
	data, err := json.Marshal(View{
		Health:      "healthy",
		ContainerID: "cid-123",
		Installed:   true,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, key := range []string{"health", "container_id", "installed"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("View JSON missing %q key, got: %s", key, data)
		}
	}

	var decoded struct {
		Health      string `json:"health"`
		ContainerID string `json:"container_id"`
		Installed   bool   `json:"installed"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal values: %v", err)
	}
	if decoded.Health != "healthy" || decoded.ContainerID != "cid-123" || !decoded.Installed {
		t.Errorf("View JSON values = %+v, want health=healthy container_id=cid-123 installed=true", decoded)
	}

	zero, err := json.Marshal(View{})
	if err != nil {
		t.Fatalf("json.Marshal zero View: %v", err)
	}
	fields = nil
	if err := json.Unmarshal(zero, &fields); err != nil {
		t.Fatalf("json.Unmarshal zero View: %v", err)
	}
	for _, key := range []string{"health", "container_id", "installed"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("zero View JSON missing %q key, got: %s", key, zero)
		}
	}
	if err := json.Unmarshal(zero, &decoded); err != nil {
		t.Fatalf("json.Unmarshal zero View values: %v", err)
	}
	if decoded.Health != "" || decoded.ContainerID != "" || decoded.Installed {
		t.Errorf("zero View JSON values = %+v, want empty strings and false", decoded)
	}
}
