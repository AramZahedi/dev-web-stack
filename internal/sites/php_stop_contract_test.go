package sites

import "testing"

// A site stop must ask the PHP backend to stop every generation. Stopping only
// the default generation leaves a hash-named master from a staged apply alive.
func TestStopPHPProcessStopsEveryGeneration(t *testing.T) {
	svc, fakes := newTestService(t)

	if err := svc.stopPHPProcessLocked(ctx, "site"); err != nil {
		t.Fatalf("stopPHPProcessLocked = %v", err)
	}

	last := ""
	found := false
	for _, event := range fakes.backend.phpEvents {
		if event.Op != "stop" || event.SiteID != "site" {
			continue
		}
		last = event.Generation
		found = true
	}
	if !found {
		t.Fatal("stopPHPProcessLocked issued no PHP stop")
	}
	if last != "" {
		t.Fatalf("last stop generation = %q, want a stop-all request", last)
	}
}
