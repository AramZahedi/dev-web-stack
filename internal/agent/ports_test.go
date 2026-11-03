package agent

import "testing"

func TestPortAllocatorBackendSlotPartitionsRange(t *testing.T) {
	t.Setenv("DEVSTACK_E2E_BACKEND_SLOT", "1")
	t.Setenv("DEVSTACK_E2E_BACKEND_SLOTS", "4")
	skipIfHostReservesRange(t, 43250, 43499)
	a := NewPortAllocator()
	httpPort, _, err := a.Allocate("site-a", "http", 0)
	if err != nil {
		t.Fatal(err)
	}
	if httpPort < 41500 || httpPort > 41999 {
		t.Fatalf("HTTP port %d outside slot 1 range 41500-41999", httpPort)
	}
	fcgiPort, _, err := a.Allocate("site-a", "fastcgi", 0)
	if err != nil {
		t.Fatal(err)
	}
	if fcgiPort < 43250 || fcgiPort > 43499 {
		t.Fatalf("FastCGI port %d outside slot 1 range 43250-43499", fcgiPort)
	}
}

func TestPortAllocatorInvalidBackendSlotUsesFullRange(t *testing.T) {
	t.Setenv("DEVSTACK_E2E_BACKEND_SLOT", "9")
	t.Setenv("DEVSTACK_E2E_BACKEND_SLOTS", "4")
	a := NewPortAllocator()
	httpPort, _, err := a.Allocate("site-a", "http", 0)
	if err != nil {
		t.Fatal(err)
	}
	if httpPort < agentHTTPPortLow || httpPort > agentHTTPPortHigh {
		t.Fatalf("HTTP port %d outside full range", httpPort)
	}
}

// skipIfHostReservesRange avoids a spurious failure when Windows (Hyper-V/WSL)
// has excluded the whole E2E slot range from loopback binds. The allocator
// still has to handle that on a real host; this test only checks partitioning.
func skipIfHostReservesRange(t *testing.T, low, high int) {
	t.Helper()
	for port := low; port <= high; port++ {
		if portAvailable(port) {
			return
		}
	}
	t.Skipf("host reserves every port in %d-%d", low, high)
}
