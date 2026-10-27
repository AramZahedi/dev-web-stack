package agent

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

const (
	agentHTTPPortLow     = 41000
	agentHTTPPortHigh    = 42999
	agentFastCGIPortLow  = 43000
	agentFastCGIPortHigh = 43999
)

var ErrPortUnavailable = errors.New("agent: backend port unavailable")

type portOwner struct {
	site string
	kind string
}

// PortAllocator reserves ports in the WSL backend ranges. Port probes always
// bind loopback; no site backend binds to a public interface.
type PortAllocator struct {
	mu     sync.Mutex
	bySite map[string]map[string]int
	owners map[int]portOwner
}

func NewPortAllocator() *PortAllocator {
	return &PortAllocator{bySite: make(map[string]map[string]int), owners: make(map[int]portOwner)}
}

// Allocate obtains a port for site and kind. An existing allocation is reused
// when requested is zero or equals that allocation.
func (a *PortAllocator) Allocate(site, kind string, requested int) (port int, reused bool, err error) {
	if err := validateSiteID(site); err != nil {
		return 0, false, err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	low, high, err := portBounds(kind)
	if err != nil {
		return 0, false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if old := a.bySite[site][kind]; old != 0 && (requested == 0 || requested == old) {
		return old, true, nil
	}
	if requested != 0 {
		if requested < low || requested > high {
			return 0, false, fmt.Errorf("%w: %s port %d is outside %d-%d", ErrPortUnavailable, kind, requested, low, high)
		}
		if owner, used := a.owners[requested]; used {
			return 0, false, fmt.Errorf("%w: port %d belongs to site %q", ErrPortUnavailable, requested, owner.site)
		}
		if !portAvailable(requested) {
			return 0, false, fmt.Errorf("%w: port %d is already bound", ErrPortUnavailable, requested)
		}
		return a.reserveLocked(site, kind, requested), false, nil
	}
	for candidate := low; candidate <= high; candidate++ {
		if _, used := a.owners[candidate]; used || !portAvailable(candidate) {
			continue
		}
		return a.reserveLocked(site, kind, candidate), false, nil
	}
	return 0, false, fmt.Errorf("%w: exhausted %s range %d-%d", ErrPortUnavailable, kind, low, high)
}

// Reserve restores or reasserts an explicit site allocation. It probes the
// socket unless that exact mapping is already held by the same owner.
func (a *PortAllocator) Reserve(site, kind string, port int) error {
	_, _, err := a.Allocate(site, kind, port)
	return err
}

// Restore reinstates a persisted assignment without probing it. The runtime
// probes again when it starts the backend, after stale processes are gone.
func (a *PortAllocator) Restore(site, kind string, port int) error {
	if err := validateSiteID(site); err != nil {
		return err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	low, high, err := portBounds(kind)
	if err != nil {
		return err
	}
	if port < low || port > high {
		return fmt.Errorf("%w: persisted port %d is outside %d-%d", ErrPortUnavailable, port, low, high)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if owner, used := a.owners[port]; used && (owner.site != site || owner.kind != kind) {
		return fmt.Errorf("%w: persisted port %d conflicts with site %q", ErrPortUnavailable, port, owner.site)
	}
	a.reserveLocked(site, kind, port)
	return nil
}

// Validate reports whether a port is available to the requested site/kind.
func (a *PortAllocator) Validate(site, kind string, port int) (bool, error) {
	if err := validateSiteID(site); err != nil {
		return false, err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	low, high, err := portBounds(kind)
	if err != nil {
		return false, err
	}
	if port < low || port > high {
		return false, fmt.Errorf("agent: %s port %d is outside %d-%d", kind, port, low, high)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if owner, used := a.owners[port]; used {
		return owner.site == site && owner.kind == kind, nil
	}
	return portAvailable(port), nil
}

// Release frees one port, or all ports when port is zero.
func (a *PortAllocator) Release(site string, port int) error {
	if err := validateSiteID(site); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	allocations := a.bySite[site]
	for kind, ownedPort := range allocations {
		if port != 0 && port != ownedPort {
			continue
		}
		delete(a.owners, ownedPort)
		delete(allocations, kind)
	}
	if len(allocations) == 0 {
		delete(a.bySite, site)
	}
	return nil
}

func (a *PortAllocator) reserveLocked(site, kind string, port int) int {
	if a.bySite[site] == nil {
		a.bySite[site] = make(map[string]int)
	}
	if old := a.bySite[site][kind]; old != 0 && old != port {
		delete(a.owners, old)
	}
	a.bySite[site][kind] = port
	a.owners[port] = portOwner{site: site, kind: kind}
	return port
}

func portBounds(kind string) (int, int, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "http":
		return agentHTTPPortLow, agentHTTPPortHigh, nil
	case "fastcgi":
		return agentFastCGIPortLow, agentFastCGIPortHigh, nil
	default:
		return 0, 0, fmt.Errorf("agent: unsupported backend port kind %q", kind)
	}
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
