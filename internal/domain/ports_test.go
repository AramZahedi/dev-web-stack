package domain

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestAllocateRangesAndCollisions(t *testing.T) {
	a := NewPortAllocator()
	p1, _ := a.Allocate(PortHTTP)
	p2, _ := a.Allocate(PortHTTP)
	if p1 < 41000 || p1 > 42999 || p1 == p2 {
		t.Fatalf("ports %d %d", p1, p2)
	}
	f, _ := a.Allocate(PortFastCGI)
	if f < 43000 || f > 43999 {
		t.Fatalf("fastcgi %d", f)
	}
}

func TestAllocateDistinctPorts(t *testing.T) {
	a := NewPortAllocator()
	seen := make(map[int]bool)
	for i := 0; i < 100; i++ {
		p, err := a.Allocate(PortHTTP)
		if err != nil {
			t.Fatalf("allocation %d: %v", i, err)
		}
		if p < httpPortLow || p > httpPortHigh {
			t.Fatalf("port %d outside HTTP range", p)
		}
		if seen[p] {
			t.Fatalf("port %d allocated twice", p)
		}
		seen[p] = true
	}
	for i := 0; i < 100; i++ {
		p, err := a.Allocate(PortFastCGI)
		if err != nil {
			t.Fatalf("fastcgi allocation %d: %v", i, err)
		}
		if p < fastCGIPortLow || p > fastCGIPortHigh {
			t.Fatalf("port %d outside FastCGI range", p)
		}
		if seen[p] {
			t.Fatalf("port %d allocated twice", p)
		}
		seen[p] = true
	}
}

func TestAllocateConcurrentIsUnique(t *testing.T) {
	a := NewPortAllocator()
	const n = 100
	ports := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ports[i], errs[i] = a.Allocate(PortHTTP)
		}(i)
	}
	wg.Wait()

	seen := make(map[int]bool, n)
	for i, p := range ports {
		if errs[i] != nil {
			t.Fatalf("allocation %d: %v", i, errs[i])
		}
		if p < httpPortLow || p > httpPortHigh {
			t.Fatalf("port %d outside HTTP range", p)
		}
		if seen[p] {
			t.Fatalf("port %d allocated twice", p)
		}
		seen[p] = true
	}
}

func TestAllocateSkipsReservedPorts(t *testing.T) {
	a := NewPortAllocator(httpPortLow, httpPortLow+1, httpPortLow+2)
	p, err := a.Allocate(PortHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if p <= httpPortLow+2 {
		t.Fatalf("allocated reserved port %d", p)
	}
}

func TestAllocateSkipsPortsInUse(t *testing.T) {
	// Hold a port inside the HTTP range so the allocator must skip it.
	var (
		held net.Listener
		port int
	)
	for p := httpPortLow; p <= httpPortLow+200; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			held, port = ln, p
			break
		}
	}
	if held == nil {
		t.Skip("no free port available in the probed range")
	}
	defer func() { _ = held.Close() }()

	// Reserve everything below the held port so allocation starts at it.
	reserved := make([]int, 0, port-httpPortLow)
	for p := httpPortLow; p < port; p++ {
		reserved = append(reserved, p)
	}
	a := NewPortAllocator(reserved...)
	got, err := a.Allocate(PortHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if got == port {
		t.Fatalf("allocated port %d that is already in use", got)
	}
	if got < httpPortLow || got > httpPortHigh {
		t.Fatalf("port %d outside HTTP range", got)
	}
}

func TestReserveAndRelease(t *testing.T) {
	a := NewPortAllocator()
	p, err := a.Allocate(PortHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reserve(p); err == nil {
		t.Fatalf("Reserve(%d) accepted an allocated port", p)
	}

	a.Release(p)
	if err := a.Reserve(p); err != nil {
		t.Fatalf("Reserve(%d) after Release = %v", p, err)
	}
	for i := 0; i < 10; i++ {
		got, err := a.Allocate(PortHTTP)
		if err != nil {
			t.Fatal(err)
		}
		if got == p {
			t.Fatalf("allocated reserved port %d", p)
		}
	}
}

func TestReserveRejectsOutOfRange(t *testing.T) {
	a := NewPortAllocator()
	for _, p := range []int{-1, 0, 80, 40999, 44000, 65535} {
		if err := a.Reserve(p); err == nil {
			t.Errorf("Reserve(%d) = nil, want error", p)
		}
	}
}

func TestReleaseUnknownPortIsHarmless(t *testing.T) {
	a := NewPortAllocator()
	a.Release(fastCGIPortLow)
	if err := a.Reserve(fastCGIPortLow); err != nil {
		t.Fatalf("Reserve after releasing an unknown port = %v", err)
	}
}

func TestAllocateUnknownKind(t *testing.T) {
	a := NewPortAllocator()
	if _, err := a.Allocate(PortKind(99)); err == nil {
		t.Fatal("Allocate(PortKind(99)) = nil, want error")
	}
}

func TestAllocateExhausted(t *testing.T) {
	reserved := allPorts(httpPortLow, httpPortHigh)
	a := NewPortAllocator(reserved...)
	if _, err := a.Allocate(PortHTTP); err == nil {
		t.Fatal("Allocate on exhausted HTTP range = nil, want error")
	} else if want := fmt.Sprintf("%d-%d", httpPortLow, httpPortHigh); !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not mention range %s", err, want)
	}
	// The FastCGI range must remain usable.
	if _, err := a.Allocate(PortFastCGI); err != nil {
		t.Fatalf("FastCGI allocation failed: %v", err)
	}
}

func TestReleaseAllowsReuse(t *testing.T) {
	port := freePortInRange(t, httpPortLow, httpPortHigh)
	a := NewPortAllocator(allPorts(httpPortLow, httpPortHigh)...)
	a.Release(port)

	got, err := a.Allocate(PortHTTP)
	if err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	if got != port {
		t.Fatalf("Allocate = %d, want released port %d", got, port)
	}
}

func allPorts(low, high int) []int {
	ports := make([]int, 0, high-low+1)
	for p := low; p <= high; p++ {
		ports = append(ports, p)
	}
	return ports
}

func freePortInRange(t *testing.T, low, high int) int {
	t.Helper()
	for p := low; p <= high; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p
	}
	t.Skip("no free port in range")
	return 0
}
