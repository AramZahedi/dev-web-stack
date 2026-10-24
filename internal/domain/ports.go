package domain

import (
	"fmt"
	"net"
	"strconv"
	"sync"
)

// Backend port ranges. HTTP upstreams (static and Node sites) use the first
// range and PHP FastCGI pools use the second. Both are bound to loopback.
const (
	httpPortLow     = 41000
	httpPortHigh    = 42999
	fastCGIPortLow  = 43000
	fastCGIPortHigh = 43999
)

// PortKind identifies the transport a backend port is allocated for.
type PortKind int

const (
	// PortHTTP is a port for an HTTP upstream.
	PortHTTP PortKind = iota
	// PortFastCGI is a port for a PHP FastCGI pool.
	PortFastCGI
)

// String returns the range name used in error messages.
func (k PortKind) String() string {
	switch k {
	case PortHTTP:
		return "http"
	case PortFastCGI:
		return "fastcgi"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// PortAllocator hands out loopback backend ports and tracks which are in use.
// It is safe for concurrent use. Reservations are kept in memory; a port is
// only handed out after a successful bind probe on 127.0.0.1.
type PortAllocator struct {
	mu       sync.Mutex
	used     map[int]struct{}
	nextHTTP int
	nextFCGI int
}

// NewPortAllocator returns an allocator that never hands out the given
// reserved ports, for example ports restored from persisted sites at startup.
// Ports outside the backend ranges are ignored.
func NewPortAllocator(reserved ...int) *PortAllocator {
	p := &PortAllocator{
		used:     make(map[int]struct{}, len(reserved)),
		nextHTTP: httpPortLow,
		nextFCGI: fastCGIPortLow,
	}
	for _, port := range reserved {
		if _, _, ok := portRange(port); ok {
			p.used[port] = struct{}{}
		}
	}
	return p
}

// Allocate reserves and returns a free port of the requested kind. Ports are
// searched sequentially within the kind's range, wrapping around, and ports
// already reserved or already bound on 127.0.0.1 are skipped. It returns an
// error when the range is exhausted.
func (p *PortAllocator) Allocate(kind PortKind) (int, error) {
	low, high, next, err := p.bounds(kind)
	if err != nil {
		return 0, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	size := high - low + 1
	for i := 0; i < size; i++ {
		port := low + (*next-low+i)%size
		if _, taken := p.used[port]; taken {
			continue
		}
		if !portAvailable(port) {
			continue
		}
		p.used[port] = struct{}{}
		*next = low + (port-low+1)%size
		return port, nil
	}
	return 0, fmt.Errorf("no free %s port in range %d-%d", kind, low, high)
}

// Reserve marks port as in use without probing it. It is used to restore
// allocations from persisted state, including ports currently bound by a
// backend that survived a daemon restart, so it deliberately does not test the
// bind. It returns an error for ports outside the backend ranges and for ports
// that are already reserved.
func (p *PortAllocator) Reserve(port int) error {
	if _, _, ok := portRange(port); !ok {
		return fmt.Errorf("port %d is outside the backend port ranges %d-%d and %d-%d",
			port, httpPortLow, httpPortHigh, fastCGIPortLow, fastCGIPortHigh)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, taken := p.used[port]; taken {
		return fmt.Errorf("port %d is already reserved", port)
	}
	p.used[port] = struct{}{}
	return nil
}

// Release marks port as free. Releasing a port that is not reserved is a
// no-op.
func (p *PortAllocator) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, port)
}

// bounds returns the range and next-cursor for kind. The caller must hold the
// mutex before dereferencing the cursor.
func (p *PortAllocator) bounds(kind PortKind) (low, high int, next *int, err error) {
	switch kind {
	case PortHTTP:
		return httpPortLow, httpPortHigh, &p.nextHTTP, nil
	case PortFastCGI:
		return fastCGIPortLow, fastCGIPortHigh, &p.nextFCGI, nil
	default:
		return 0, 0, nil, fmt.Errorf("unknown port kind %d", int(kind))
	}
}

// portRange reports which backend range contains port.
func portRange(port int) (low, high int, ok bool) {
	switch {
	case port >= httpPortLow && port <= httpPortHigh:
		return httpPortLow, httpPortHigh, true
	case port >= fastCGIPortLow && port <= fastCGIPortHigh:
		return fastCGIPortLow, fastCGIPortHigh, true
	default:
		return 0, 0, false
	}
}

// portAvailable reports whether port can be bound on loopback.
func portAvailable(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
