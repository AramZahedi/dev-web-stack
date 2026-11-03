package services

import (
	"context"
	"path"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// SiteSource enumerates persisted sites.
type SiteSource interface {
	ListSites(ctx context.Context) ([]model.Site, error)
}

// AgentTargetSource converts persisted site models into typed agentproto.WebTarget
// values for private web server configuration in WSL.
type AgentTargetSource struct {
	store SiteSource
}

// NewAgentTargetSource constructs a target source for the given store.
func NewAgentTargetSource(store SiteSource) *AgentTargetSource {
	return &AgentTargetSource{store: store}
}

// PrivateWebTargets builds typed WSL backend targets for one active Docker web server.
func (s *AgentTargetSource) PrivateWebTargets(ctx context.Context, server string) ([]agentproto.WebTarget, error) {
	if s.store == nil {
		return nil, nil
	}
	sites, err := s.store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var targets []agentproto.WebTarget
	for _, site := range sites {
		if strings.TrimSpace(site.ID) == "" || site.ID == "__dashboard__" {
			continue
		}
		if !s.DockerRoutable(site) {
			continue
		}
		domain := strings.TrimSpace(site.Domain)
		if domain == "" || domain == "devstack.test" {
			continue
		}
		kind := ""
		address := ""
		port := 0
		switch site.Kind {
		case model.SiteNode:
			// A zero port means the site is stopped or has not been allocated
			// yet. Never fall back to a fixed backend port: another site may
			// own it, and the private vhost would then serve the wrong site.
			if site.HTTPPort == 0 {
				continue
			}
			kind = "node"
			address = "127.0.0.1"
			port = site.HTTPPort
		case model.SitePHP:
			if site.FastCGIPort == 0 {
				continue
			}
			kind = "php"
			address = agentDocumentRoot(site)
			port = site.FastCGIPort
		case model.SiteStatic:
			kind = "static"
			address = agentDocumentRoot(site)
			port = 0
		default:
			continue
		}
		targets = append(targets, agentproto.WebTarget{
			SiteID:              site.ID,
			Domain:              domain,
			Kind:                kind,
			Address:             address,
			Port:                port,
			TLS:                 false,
			WWW:                 site.WWW,
			HostDocumentRoot:    address,
			FrontController:     site.FrontController,
			FrontControllerFile: site.FrontControllerFile,
			FrontControllerMode: site.FrontControllerMode,
			SPAFallback:         site.SPAFallback,
			SPAFallbackFile:     site.SPAFallbackFile,
			ApacheAllowOverride: site.ApacheAllowOverride,
			DesiredState:        string(site.DesiredState),
		})
	}
	return targets, nil
}

// agentDocumentRoot resolves a site's document root against its WSL backend
// path. It mirrors sites.absoluteDocumentRoot for the agent-facing target.
func agentDocumentRoot(site model.Site) string {
	backend := strings.ReplaceAll(strings.TrimSpace(site.BackendPath), `\`, "/")
	root := strings.ReplaceAll(strings.TrimSpace(site.DocumentRoot), `\`, "/")
	if root == "" || root == "." {
		return path.Clean(backend)
	}
	if path.IsAbs(root) {
		return path.Clean(root)
	}
	return path.Clean(path.Join(backend, root))
}

// DockerRoutable reports whether a site can be served by the WSL Docker web
// server. Node sites are proxied, so only their loopback port matters. Static
// and PHP sites need a document root inside the ext4 projects mount; Windows
// source paths stay on the daemon's direct route.
func (s *AgentTargetSource) DockerRoutable(site model.Site) bool {
	if site.Kind == model.SiteNode {
		return true
	}
	if site.SourceMode == model.SourceWindows {
		return false
	}
	root := agentDocumentRoot(site)
	return root == "/data/devstack/projects" || strings.HasPrefix(root, "/data/devstack/projects/")
}
