package store

import (
	"errors"
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

	// Update existing record to the installed-but-stopped state.
	rec.State = services.StateInstalledButStopped
	rec.Active = false
	rec.LastOperation = "deactivate"
	if err := st.SaveServiceState(ctx, rec); err != nil {
		t.Fatalf("SaveServiceState update failed: %v", err)
	}
	gotUpdated, err := st.GetServiceState(ctx, services.IDMySQL)
	if err != nil {
		t.Fatalf("GetServiceState after update failed: %v", err)
	}
	if gotUpdated.State != services.StateInstalledButStopped || gotUpdated.Active {
		t.Fatalf("updated record mismatch: %+v", gotUpdated)
	}

	// Delete record
	if err := st.DeleteServiceState(ctx, services.IDMySQL); err != nil {
		t.Fatalf("DeleteServiceState failed: %v", err)
	}
	_, err = st.GetServiceState(ctx, services.IDMySQL)
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("GetServiceState after delete returned err = %v, want services.ErrNotFound", err)
	}
}

func TestGetServiceStateNotFound(t *testing.T) {
	st := openTemp(t)
	_, err := st.GetServiceState(ctx, services.IDApache)
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("GetServiceState(absent) = %v, want services.ErrNotFound", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("GetServiceState(absent) returned store.ErrNotFound, want services.ErrNotFound")
	}

	// Empty list should be non-nil slice
	list, err := st.ListServiceStates(ctx)
	if err != nil {
		t.Fatalf("ListServiceStates failed: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", list)
	}

	// Empty ID rejection
	if err := st.SaveServiceState(ctx, services.Record{}); err == nil {
		t.Fatal("expected error saving record with empty ID")
	}

	// Zero UpdatedAt auto-population without s.now override
	err = st.SaveServiceState(ctx, services.Record{
		ID:    services.IDRedis,
		State: services.StateActive,
	})
	if err != nil {
		t.Fatalf("SaveServiceState with zero UpdatedAt failed: %v", err)
	}
	redisRec, err := st.GetServiceState(ctx, services.IDRedis)
	if err != nil {
		t.Fatalf("GetServiceState failed: %v", err)
	}
	if redisRec.UpdatedAt.IsZero() {
		t.Fatal("expected UpdatedAt to be populated automatically")
	}
}

func TestDeleteServiceStateNotFound(t *testing.T) {
	st := openTemp(t)
	err := st.DeleteServiceState(ctx, services.IDApache)
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("DeleteServiceState(absent) = %v, want services.ErrNotFound", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteServiceState(absent) returned store.ErrNotFound, want services.ErrNotFound")
	}
}

func TestServiceStateRoundTripsAllStates(t *testing.T) {
	st := openTemp(t)
	states := []services.State{
		services.StateNotInstalled,
		services.StateInstalled,
		services.StateInstalledButStopped,
		services.StateStarting,
		services.StateActive,
		services.StateStopping,
		services.StateError,
	}
	for _, state := range states {
		rec := services.Record{ID: services.IDRedis, State: state}
		if err := st.SaveServiceState(ctx, rec); err != nil {
			t.Fatalf("SaveServiceState(%q) failed: %v", state, err)
		}
		got, err := st.GetServiceState(ctx, services.IDRedis)
		if err != nil {
			t.Fatalf("GetServiceState(%q) failed: %v", state, err)
		}
		if got.State != state {
			t.Fatalf("GetServiceState state = %q, want %q", got.State, state)
		}
	}
}

func TestServiceStatePreservesUnknownCatalogID(t *testing.T) {
	st := openTemp(t)
	const unknownID services.ID = "custom-service"
	rec := services.Record{
		ID:            unknownID,
		State:         services.StateInstalledButStopped,
		LastOperation: "install",
	}
	if err := st.SaveServiceState(ctx, rec); err != nil {
		t.Fatalf("SaveServiceState failed: %v", err)
	}

	got, err := st.GetServiceState(ctx, unknownID)
	if err != nil {
		t.Fatalf("GetServiceState failed: %v", err)
	}
	if got.ID != unknownID || got.State != services.StateInstalledButStopped {
		t.Fatalf("GetServiceState returned %+v, want ID=%q state=%q", got, unknownID, services.StateInstalledButStopped)
	}

	list, err := st.ListServiceStates(ctx)
	if err != nil {
		t.Fatalf("ListServiceStates failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != unknownID {
		t.Fatalf("ListServiceStates returned %+v, want unknown catalog ID preserved", list)
	}
}
