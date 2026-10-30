package sites

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func listEvents(t *testing.T, fakes *testFakes) []model.Event {
	t.Helper()
	events, err := fakes.store.ListEvents(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestCreateEmitsOneSiteCreatedEvent(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	var created []string
	for _, event := range listEvents(t, fakes) {
		if event.SiteID == site.ID && event.Type == "site.created" {
			created = append(created, event.Message)
		}
	}
	if len(created) != 1 {
		t.Fatalf("site.created events = %v, want exactly one", created)
	}
	if created[0] != "site blog created" {
		t.Fatalf("site.created message = %q", created[0])
	}
}

func TestCreateFailureEmitsNoSiteCreatedEvent(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.backend.prepareErr = errors.New("backend prepare failed")
	if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil {
		t.Fatal("Create succeeded with a failing backend")
	}
	for _, event := range listEvents(t, fakes) {
		if event.Type == "site.created" {
			t.Fatalf("failed create emitted site.created: %+v", event)
		}
	}
}

func TestCreateMissingBuildOutputKeepsSiteCreatedMessage(t *testing.T) {
	svc, fakes := newTestService(t)
	profile := &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"}
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "app", Path: fakes.root, Kind: "static", Build: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range listEvents(t, fakes) {
		if event.SiteID == site.ID && event.Type == "site.created" {
			found = true
			if !strings.Contains(event.Message, "build output dist is missing") {
				t.Fatalf("site.created message = %q", event.Message)
			}
		}
	}
	if !found {
		t.Fatal("missing-build create did not emit site.created")
	}
}
