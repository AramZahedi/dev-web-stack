//go:build !windows

package client

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func TestUpdateSiteSendsNodeVersion(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{
		"site-1": {ID: "site-1", Name: "docs", Kind: model.SiteStatic},
	}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	t.Run("set", func(t *testing.T) {
		pin := "22.11.0"
		if _, err := c.UpdateSite(ctx, "site-1", sites.UpdateRequest{NodeVersion: &pin}); err != nil {
			t.Fatal(err)
		}
		body := api.last(t, http.MethodPatch, "/api/v1/sites/site-1").body
		if !strings.Contains(body, `"node_version":"22.11.0"`) {
			t.Fatalf("update payload = %s", body)
		}
	})

	t.Run("clear", func(t *testing.T) {
		clear := ""
		if _, err := c.UpdateSite(ctx, "site-1", sites.UpdateRequest{NodeVersion: &clear}); err != nil {
			t.Fatal(err)
		}
		body := api.last(t, http.MethodPatch, "/api/v1/sites/site-1").body
		if !strings.Contains(body, `"node_version":null`) {
			t.Fatalf("clear payload = %s", body)
		}
	})

	t.Run("omitted", func(t *testing.T) {
		if _, err := c.UpdateSite(ctx, "site-1", sites.UpdateRequest{Domain: "docs.test"}); err != nil {
			t.Fatal(err)
		}
		body := api.last(t, http.MethodPatch, "/api/v1/sites/site-1").body
		if strings.Contains(body, "node_version") {
			t.Fatalf("omitted payload sent a pin: %s", body)
		}
	})
}
