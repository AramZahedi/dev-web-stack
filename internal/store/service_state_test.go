package store

import (
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestServiceStateRoundTrip(t *testing.T) {
	st := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)
	rec := services.Record{
		ID:            services.IDMySQL,
		State:         services.StateActive,
		Image:         "mysql@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Active:        true,
		LastError:     "",
		LastOperation: "activate",
		UpdatedAt:     now,
	}

	if err := st.SaveServiceState(ctx, rec); err != nil {
		t.Fatalf("SaveServiceState failed: %v", err)
	}

	got, err := st.GetServiceState(ctx, services.IDMySQL)
	if err != nil {
		t.Fatalf("GetServiceState failed: %v", err)
	}
	if got.ID != rec.ID || got.State != rec.State || got.Image != rec.Image || !got.Active || got.LastOperation != rec.LastOperation {
		t.Fatalf("GetServiceState returned %+v, want %+v", got, rec)
	}

	list, err := st.ListServiceStates(ctx)
	if err != nil {
		t.Fatalf("ListServiceStates failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != services.IDMySQL {
		t.Fatalf("ListServiceStates returned %+v, want 1 record", list)
	}

	// Update existing record
	rec.State = services.StateStopping
	rec.Active = false
	rec.LastOperation = "deactivate"
	if err := st.SaveServiceState(ctx, rec); err != nil {
		t.Fatalf("SaveServiceState update failed: %v", err)
	}
	gotUpdated, err := st.GetServiceState(ctx, services.IDMySQL)
	if err != nil {
		t.Fatalf("GetServiceState after update failed: %v", err)
	}
	if gotUpdated.State != services.StateStopping || gotUpdated.Active {
		t.Fatalf("updated record mismatch: %+v", gotUpdated)
	}

	// Delete record
	if err := st.DeleteServiceState(ctx, services.IDMySQL); err != nil {
		t.Fatalf("DeleteServiceState failed: %v", err)
	}
	_, err = st.GetServiceState(ctx, services.IDMySQL)
	if err != ErrNotFound {
		t.Fatalf("GetServiceState after delete returned err = %v, want ErrNotFound", err)
	}
}

func TestGetServiceStateNotFound(t *testing.T) {
	st := openTemp(t)
	_, err := st.GetServiceState(ctx, services.IDApache)
	if err != ErrNotFound {
		t.Fatalf("GetServiceState(absent) = %v, want ErrNotFound", err)
	}
}
