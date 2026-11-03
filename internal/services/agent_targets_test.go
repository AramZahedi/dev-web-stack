package services_test

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeSiteSource struct {
	sites []model.Site
}

func (s fakeSiteSource) ListSites(context.Context) ([]model.Site, error) {
	return s.sites, nil
}

func TestAgentTargetSourceBuildsTypedTargets(t *testing.T) {
	source := services.NewAgentTargetSource(fakeSiteSource{
		sites: []model.Site{
			{
				ID:       "dashboard",
				Domain:   "devstack.test",
				Kind:     model.SiteStatic,
				HTTPPort: 7070,
			},
			{
				ID:           "app-php",
				Domain:       "app.test",
				Kind:         model.SitePHP,
				BackendPath:  "/data/devstack/projects/app",
				DocumentRoot: "public",
				FastCGIPort:  43001,
				WWW:          true,
			},
			{
				ID:       "api-node",
				Domain:   "api.test",
				Kind:     model.SiteNode,
				HTTPPort: 41005,
			},
			{
				ID:          "site-static",
				Domain:      "static.test",
				Kind:        model.SiteStatic,
				BackendPath: "/data/devstack/projects/static",
			},
			{
				ID:          "windows-static",
				Domain:      "windows-static.test",
				Kind:        model.SiteStatic,
				BackendPath: "/mnt/c/Users/Aram/.devstack/projects/windows-static",
				SourceMode:  model.SourceWindows,
			},
			{
				ID:         "windows-node",
				Domain:     "windows-node.test",
				Kind:       model.SiteNode,
				HTTPPort:   41006,
				SourceMode: model.SourceWindows,
			},
			{
				ID:          "stopped-node",
				Domain:      "stopped-node.test",
				Kind:        model.SiteNode,
				BackendPath: "/data/devstack/projects/stopped-node",
			},
			{
				ID:          "stopped-php",
				Domain:      "stopped-php.test",
				Kind:        model.SitePHP,
				BackendPath: "/data/devstack/projects/stopped-php",
			},
		},
	})

	targets, err := source.PrivateWebTargets(context.Background(), "nginx")
	if err != nil {
		t.Fatal(err)
	}

	byDomain := make(map[string]string)
	byPort := make(map[string]int)
	byKind := make(map[string]string)
	byTarget := make(map[string]agentproto.WebTarget)
	for _, target := range targets {
		byDomain[target.Domain] = target.Address
		byPort[target.Domain] = target.Port
		byKind[target.Domain] = target.Kind
		byTarget[target.Domain] = target
	}

	// Dashboard is excluded
	if _, ok := byDomain["devstack.test"]; ok {
		t.Error("dashboard domain devstack.test was not excluded")
	}

	// PHP site carries the full vhost metadata and a www alias on one target.
	php := byTarget["app.test"]
	if php.Kind != "php" || php.Port != 43001 || php.Address != "/data/devstack/projects/app/public" {
		t.Errorf("app.test target = kind:%s port:%d addr:%s", php.Kind, php.Port, php.Address)
	}
	if !php.WWW || php.HostDocumentRoot != "/data/devstack/projects/app/public" {
		t.Errorf("app.test metadata = %+v", php)
	}
	if _, ok := byTarget["www.app.test"]; ok {
		t.Error("www alias was emitted as a separate target")
	}

	// Node site
	if byKind["api.test"] != "node" || byPort["api.test"] != 41005 || byDomain["api.test"] != "127.0.0.1" {
		t.Errorf("api.test target = kind:%s port:%d addr:%s", byKind["api.test"], byPort["api.test"], byDomain["api.test"])
	}

	// Static site
	if byKind["static.test"] != "static" || byPort["static.test"] != 0 || byDomain["static.test"] != "/data/devstack/projects/static" {
		t.Errorf("static.test target = kind:%s port:%d addr:%s", byKind["static.test"], byPort["static.test"], byDomain["static.test"])
	}

	// Stopped Node/PHP sites have no allocated backend and must not be pointed
	// at a fixed port that another site could own.
	for _, domain := range []string{"stopped-node.test", "stopped-php.test"} {
		if _, ok := byDomain[domain]; ok {
			t.Errorf("stopped site %s was included as a private web target", domain)
		}
	}

	// Windows-source static sites stay on the daemon's direct route.
	if _, ok := byTarget["windows-static.test"]; ok {
		t.Error("Windows-source static site was included in the Docker target set")
	}
	if target, ok := byTarget["windows-node.test"]; !ok || target.Kind != "node" {
		t.Errorf("Windows-source Node site was not included as a proxy target: %+v", target)
	}
}
