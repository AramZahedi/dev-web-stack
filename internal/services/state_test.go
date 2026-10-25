package services

import (
	"testing"
	"time"
)

func TestStateConstants(t *testing.T) {
	states := []State{
		StateNotInstalled,
		StateInstalled,
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
