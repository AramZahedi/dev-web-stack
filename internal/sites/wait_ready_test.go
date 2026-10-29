package sites

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestStartWaitsForBackendReadiness(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	stopCalls := fakes.backend.stopCalls

	probed := false
	svc.deps.WaitReady = func(_ context.Context, candidate model.Site) error {
		probed = true
		if candidate.ID != site.ID || candidate.HTTPPort == 0 {
			t.Fatalf("readiness probe received %+v", candidate)
		}
		return nil
	}
	if err := svc.Start(context.Background(), site.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !probed {
		t.Fatal("Start did not wait for backend readiness")
	}
	if fakes.backend.stopCalls != stopCalls {
		t.Fatal("successful Start stopped the backend")
	}
}

func TestStartReadinessFailureStopsBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	stopCalls := fakes.backend.stopCalls

	svc.deps.WaitReady = func(context.Context, model.Site) error {
		return errors.New("WSL localhost forwarding is unavailable")
	}
	err = svc.Start(context.Background(), site.ID)
	if err == nil {
		t.Fatal("Start succeeded without host-side readiness")
	}
	if fakes.backend.stopCalls <= stopCalls {
		t.Fatal("failed readiness did not stop the backend")
	}
	state, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != model.StatusStopped {
		t.Fatalf("site status after failed readiness = %s, want stopped", state.Status)
	}
}
