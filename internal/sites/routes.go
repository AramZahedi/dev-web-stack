package sites

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
)

const (
	// dashboardHost is the reserved host served by the dashboard UI.
	dashboardHost = "devstack.test"
	// dashboardFallbackPort is used when the caller supplies a zero-value
	// config; config.Default uses the same port.
	dashboardFallbackPort = 7070
)

// RebuildRoutes publishes one immutable routing snapshot containing every
// site, its certificate, the dashboard route, and the dashboard certificate as
// the SNI fallback.
func (s *Service) RebuildRoutes(ctx context.Context) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(false, false); err != nil {
		return err
	}
	return s.rebuildRoutesLocked(ctx)
}

// rebuildRoutesLocked builds and installs the snapshot. The caller must hold
// s.mu. A site certificate that is missing from the store is skipped rather
// than failing the whole routing table; a certificate that exists but cannot
// be loaded is a hard error so a broken key/cert pair is visible.
func (s *Service) rebuildRoutesLocked(ctx context.Context) error {
	dashboardRecord, err := s.deps.Certs.Ensure(ctx, certs.DashboardOwner, []string{dashboardHost})
	if err != nil {
		return fmt.Errorf("sites: ensure dashboard certificate: %w", err)
	}
	dashboardCert, err := s.deps.Certs.Load(dashboardRecord)
	if err != nil {
		return fmt.Errorf("sites: load dashboard certificate: %w", err)
	}

	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list for routes: %w", err)
	}
	certRecords, err := s.deps.Store.ListCerts(ctx)
	if err != nil {
		return fmt.Errorf("sites: list certificates for routes: %w", err)
	}
	bySite := make(map[string]model.CertRecord, len(certRecords))
	for _, rec := range certRecords {
		bySite[rec.SiteID] = rec
	}

	upstreamPort := dashboardPort(s.deps.Config)
	routes := map[string]proxy.Route{
		dashboardHost: {
			SiteID:   certs.DashboardOwner,
			Host:     dashboardHost,
			Kind:     proxy.KindStatic,
			Upstream: net.JoinHostPort("127.0.0.1", strconv.Itoa(upstreamPort)),
			TLS:      true,
		},
	}
	certMap := map[string]*tls.Certificate{dashboardHost: dashboardCert}

	for _, site := range stored {
		kind, err := proxyKind(site.Kind)
		if err != nil {
			return fmt.Errorf("sites: route for %q: %w", site.Name, err)
		}
		upstreamPort := site.HTTPPort
		if site.Kind == model.SitePHP {
			upstreamPort = site.FastCGIPort
		}
		if upstreamPort > 0 {
			upstream := net.JoinHostPort("127.0.0.1", strconv.Itoa(upstreamPort))
			route := proxy.Route{
				SiteID:       site.ID,
				Kind:         kind,
				Upstream:     upstream,
				ForceHTTPS:   site.ForceHTTPS,
				TLS:          site.TLS,
				State:        string(site.Status),
				DesiredState: string(site.DesiredState),
			}
			if site.Kind == model.SitePHP {
				route.DocumentRoot = absoluteDocumentRoot(site)
				route.FrontController = site.FrontController
			}
			for _, host := range siteDomains(site) {
				host = strings.ToLower(host)
				route.Host = host
				routes[host] = route
			}
		}

		rec, ok := bySite[site.ID]
		if !ok {
			continue
		}
		// Repair an expired/rotated certificate before publishing routes. A
		// stale cert must not prevent daemon startup; the renewal ticker starts
		// only after readiness.
		if fresh, ensureErr := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site)); ensureErr == nil {
			rec = fresh
			bySite[site.ID] = fresh
		}
		cert, err := s.deps.Certs.Load(rec)
		if err != nil {
			// Skip this site's routes rather than blocking every other site and
			// the daemon readiness path. The next renewal/rebuild retries.
			for _, host := range siteDomains(site) {
				delete(routes, strings.ToLower(host))
			}
			continue
		}
		domains := rec.Domains
		if len(domains) == 0 {
			domains = siteDomains(site)
		}
		for _, host := range domains {
			host = strings.ToLower(strings.TrimSpace(host))
			if host != "" {
				certMap[host] = cert
			}
		}
	}

	s.deps.Router.Replace(proxy.Snapshot{
		Routes:      routes,
		Certs:       certMap,
		DefaultCert: dashboardCert,
	})
	return nil
}

// proxyKind maps a site kind onto a proxy route kind.
func proxyKind(kind model.SiteKind) (proxy.Kind, error) {
	switch kind {
	case model.SiteStatic:
		return proxy.KindStatic, nil
	case model.SiteNode:
		return proxy.KindNode, nil
	case model.SitePHP:
		return proxy.KindPHP, nil
	default:
		return "", fmt.Errorf("unsupported site kind %q", kind)
	}
}

// dashboardPort returns the configured dashboard port, falling back to the
// documented default for a zero-value config.
func dashboardPort(cfg config.Config) int {
	if cfg.UIPort > 0 {
		return cfg.UIPort
	}
	return dashboardFallbackPort
}
