package services

import (
	"context"
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
		domain := strings.TrimSpace(site.Domain)
		if domain == "" || domain == "devstack.test" {
			continue
		}
		kind := ""
		address := ""
		port := 0
		switch site.Kind {
		case model.SiteNode:
			kind = "node"
			address = "127.0.0.1"
			port = site.HTTPPort
			if port == 0 {
				port = 41000
			}
		case model.SitePHP:
			kind = "php"
			address = strings.TrimSpace(site.BackendPath)
			if address == "" {
				address = strings.TrimSpace(site.HostPath)
			}
			port = site.FastCGIPort
			if port == 0 {
				port = 43000
			}
		case model.SiteStatic:
			kind = "static"
			address = strings.TrimSpace(site.BackendPath)
			if address == "" {
				address = strings.TrimSpace(site.HostPath)
			}
			port = 0
		default:
			continue
		}
		targets = append(targets, agentproto.WebTarget{
			SiteID:  site.ID,
			Domain:  domain,
			Kind:    kind,
			Address: address,
			Port:    port,
			TLS:     false,
		})
		if site.WWW {
			targets = append(targets, agentproto.WebTarget{
				SiteID:  site.ID + "-www",
				Domain:  "www." + domain,
				Kind:    kind,
				Address: address,
				Port:    port,
				TLS:     false,
			})
		}
	}
	return targets, nil
}
