package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeInstallAndRemoveEmitEvents(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	ctx := context.Background()

	rec := fx.request(t, http.MethodPost, "/api/v1/runtimes/install", strings.NewReader(`{"id":"node@24.9.0"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("install status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodDelete, "/api/v1/runtimes/node@24.9.0", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d, body = %s", rec.Code, rec.Body.String())
	}

	events, err := fx.store.ListEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var install, remove bool
	for _, event := range events {
		switch event.Type {
		case "runtime.installed":
			install = strings.Contains(event.Message, "node@24.9.0")
		case "runtime.removed":
			remove = strings.Contains(event.Message, "node@24.9.0")
		}
	}
	if !install || !remove {
		t.Fatalf("runtime events install=%t remove=%t, events=%+v", install, remove, events)
	}
}
