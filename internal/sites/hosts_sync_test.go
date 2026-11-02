package sites

import (
	"context"
	"testing"
)

func TestSyncHostsRestoresDashboardAndRegisteredSites(t *testing.T) {
	svc, fakes := newTestService(t)
	if _, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true,
	}); err != nil {
		t.Fatal(err)
	}
	fakes.hosts.mu.Lock()
	fakes.hosts.calls = nil
	fakes.hosts.mu.Unlock()

	if err := svc.SyncHosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	fakes.hosts.mu.Lock()
	calls := append([][]string(nil), fakes.hosts.calls...)
	fakes.hosts.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("hosts sync calls = %d, want 1", len(calls))
	}
	got := calls[0]
	want := []string{"blog.test", dashboardHost}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
	}
}

func TestSyncHostsWithoutSitesRestoresDashboard(t *testing.T) {
	svc, fakes := newTestService(t)
	if err := svc.SyncHosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	fakes.hosts.mu.Lock()
	calls := append([][]string(nil), fakes.hosts.calls...)
	fakes.hosts.mu.Unlock()
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != dashboardHost {
		t.Fatalf("hosts sync calls = %v, want [[%s]]", calls, dashboardHost)
	}
}
