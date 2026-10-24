// Package proxy implements the builtin devstack reverse proxy: it routes
// requests by exact Host to static, Node, or PHP upstreams and selects TLS
// certificates by SNI.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
)

// Kind is the backend kind behind a route. Static upstreams are served by the
// internal static server; Node upstreams are long-lived development servers;
// PHP upstreams are FastCGI pools served by the FastCGI transport.
type Kind string

const (
	KindStatic Kind = "static"
	KindNode   Kind = "node"
	KindPHP    Kind = "php"
)

const (
	// Node development servers are restarted often and may spend longer before
	// writing response headers, so they get twice the static budget.
	staticResponseHeaderTimeout = 60 * time.Second
	nodeResponseHeaderTimeout   = 120 * time.Second
)

// Route is the immutable description of one site listener. DocumentRoot and
// FrontController are carried for PHP routes so the FastCGI transport can
// resolve script paths; the static and Node transports ignore them.
type Route struct {
	SiteID          string
	Host            string
	Kind            Kind
	Upstream        string
	DocumentRoot    string
	FrontController bool
	ForceHTTPS      bool
	TLS             bool
	// State and DesiredState prevent a stopped or failed site from being
	// proxied to a stale/dead upstream. Empty values are treated as running
	// for compatibility with standalone routes.
	State        string
	DesiredState string
}

// fcgiClient is the FastCGI client behavior the proxy needs. One client is one
// connection and carries at most one request.
type fcgiClient interface {
	Do(ctx context.Context, req fcgi.Request) (*fcgi.Response, error)
	Close() error
}

// fcgiDialer opens a FastCGI connection. Tests replace Router.fcgiDial to
// script the transport without opening a socket.
type fcgiDialer func(ctx context.Context, addr string) (fcgiClient, error)

// Snapshot is the complete routing table and certificate set at one point in
// time. Callers must not mutate a snapshot after passing it to Replace.
type Snapshot struct {
	Routes      map[string]Route
	Certs       map[string]*tls.Certificate
	DefaultCert *tls.Certificate
}

var emptySnapshot = &Snapshot{}

// Router serves the current snapshot through an atomic pointer. Replacing a
// snapshot is safe while requests are in flight.
type Router struct {
	snapshot atomic.Pointer[Snapshot]

	staticTransport *http.Transport
	nodeTransport   *http.Transport
	fcgiDial        fcgiDialer
	handler         http.Handler
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	r := &Router{
		staticTransport: newUpstreamTransport(staticResponseHeaderTimeout),
		nodeTransport:   newUpstreamTransport(nodeResponseHeaderTimeout),
		fcgiDial: func(ctx context.Context, addr string) (fcgiClient, error) {
			return fcgi.Dial(ctx, addr)
		},
	}
	r.handler = http.HandlerFunc(r.serveHTTP)
	return r
}

// Replace atomically installs a new snapshot. Maps are copied so later changes
// to the caller's maps cannot alter the live routing table.
func (r *Router) Replace(s Snapshot) {
	routes := make(map[string]Route, len(s.Routes))
	for host, route := range s.Routes {
		normalized := normalizeHost(host)
		if normalized == "" {
			continue
		}
		if route.Host == "" {
			route.Host = normalized
		}
		routes[normalized] = route
	}
	certs := make(map[string]*tls.Certificate, len(s.Certs))
	for host, cert := range s.Certs {
		if normalized := normalizeHost(host); normalized != "" && cert != nil {
			certs[normalized] = cert
		}
	}
	r.snapshot.Store(&Snapshot{
		Routes:      routes,
		Certs:       certs,
		DefaultCert: s.DefaultCert,
	})
}

// Lookup returns the route for host. Matching is exact after lowercasing,
// stripping a trailing dot, and removing a port.
func (r *Router) Lookup(host string) (Route, bool) {
	route, ok := r.load().Routes[normalizeHost(host)]
	return route, ok
}

// Certificate selects the SNI certificate for hello, falling back to
// DefaultCert for unknown or absent server names.
func (r *Router) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	snapshot := r.load()
	name := ""
	if hello != nil {
		name = normalizeHost(hello.ServerName)
	}
	if cert := snapshot.Certs[name]; cert != nil {
		return cert, nil
	}
	if snapshot.DefaultCert != nil {
		return snapshot.DefaultCert, nil
	}
	if name == "" {
		return nil, errors.New("proxy: no TLS certificate configured")
	}
	return nil, fmt.Errorf("proxy: no TLS certificate for %q", name)
}

// Handler returns the HTTP handler for the current and future snapshots.
func (r *Router) Handler() http.Handler {
	return r.handler
}

// CloseIdleConnections releases pooled connections to upstreams.
func (r *Router) CloseIdleConnections() {
	if r == nil {
		return
	}
	if r.staticTransport != nil {
		r.staticTransport.CloseIdleConnections()
	}
	if r.nodeTransport != nil {
		r.nodeTransport.CloseIdleConnections()
	}
}

func (r *Router) load() *Snapshot {
	if snapshot := r.snapshot.Load(); snapshot != nil {
		return snapshot
	}
	return emptySnapshot
}

func newUpstreamTransport(responseHeaderTimeout time.Duration) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		// The proxy must not transparently gzip/decompress streams; Node dev
		// servers and SSE endpoints depend on the bytes they wrote.
		DisableCompression: true,
	}
}

// normalizeHost canonicalizes a Host header or SNI name for exact matching.
func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if withoutPort, _, err := net.SplitHostPort(host); err == nil {
		host = withoutPort
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}
