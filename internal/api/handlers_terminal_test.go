package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/terminal"
)

type terminalServeCall struct {
	Site   model.Site
	Ticket string
}

type fakeTerminalService struct {
	mu       sync.Mutex
	ticket   terminal.Ticket
	issueErr error
	serveErr error
	issued   []model.Site
	served   []terminalServeCall
}

func (f *fakeTerminalService) IssueTicket(_ context.Context, site model.Site) (terminal.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued = append(f.issued, site)
	if f.issueErr != nil {
		return terminal.Ticket{}, f.issueErr
	}
	if f.ticket.Token == "" {
		f.ticket = terminal.Ticket{
			Token:     "test-terminal-ticket",
			ExpiresAt: time.Date(2026, 10, 26, 12, 0, 30, 0, time.UTC),
		}
	}
	return f.ticket, nil
}

func (f *fakeTerminalService) Serve(_ context.Context, w http.ResponseWriter, _ *http.Request, site model.Site, ticket string) error {
	f.mu.Lock()
	f.served = append(f.served, terminalServeCall{Site: site, Ticket: ticket})
	err := f.serveErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (f *fakeTerminalService) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.issued), len(f.served)
}

func addTerminalSite(fx *apiFixture, site model.Site) {
	fx.sites.mu.Lock()
	defer fx.sites.mu.Unlock()
	fx.sites.sites[site.ID] = site
}

func TestTerminalSessionIssuesTicketWithSessionAndCSRF(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	site := model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"}
	addTerminalSite(fx, site)

	rec := fx.request(t, http.MethodPost, "/api/v1/sites/site-1/terminal/session", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ticket terminal.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	if ticket.Token == "" || ticket.ExpiresAt.IsZero() {
		t.Fatalf("ticket = %+v", ticket)
	}
	issued, _ := fx.terminal.calls()
	if issued != 1 {
		t.Fatalf("issued tickets = %d, want 1", issued)
	}
	if got := fx.terminal.issued[0].ID; got != "site-1" {
		t.Fatalf("issued for site %q", got)
	}
}

func TestTerminalSessionRequiresCSRF(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/sites/site-1/terminal/session", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without session/CSRF = %d, want 403", rec.Code)
	}
	issued, _ := fx.terminal.calls()
	if issued != 0 {
		t.Fatalf("unauthenticated request issued %d tickets", issued)
	}
}

func TestTerminalSessionRejectsUnknownSite(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	rec := fx.request(t, http.MethodPost, "/api/v1/sites/missing/terminal/session", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	issued, _ := fx.terminal.calls()
	if issued != 0 {
		t.Fatalf("unknown site issued %d tickets", issued)
	}
}

func TestTerminalSessionUnavailableWithoutService(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	fx.server.terminal = nil
	rec := fx.request(t, http.MethodPost, "/api/v1/sites/site-1/terminal/session", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTerminalWebSocketRequiresTicket(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/sites/site-1/terminal", nil)
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTerminalWebSocketRejectsOriginViolations(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	origins := []string{
		"",
		"https://evil.test",
		"null",
		"http://127.0.0.1.evil.test",
		"http://127.0.0.1:7070/path",
		"http://user:pass@127.0.0.1:7070",
		"ftp://127.0.0.1:7070",
	}
	for _, origin := range origins {
		req := httptest.NewRequest(http.MethodGet,
			"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=test-terminal-ticket", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("origin %q status = %d, body = %s", origin, rec.Code, rec.Body.String())
		}
	}
	_, served := fx.terminal.calls()
	if served != 0 {
		t.Fatalf("origin violations reached the terminal service %d times", served)
	}
}

func TestTerminalWebSocketAcceptsAllowedOrigins(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	for _, origin := range []string{
		"http://127.0.0.1:7070",
		"http://localhost:7070",
		"https://devstack.test",
	} {
		req := httptest.NewRequest(http.MethodGet,
			"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=test-terminal-ticket", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %q status = %d, body = %s", origin, rec.Code, rec.Body.String())
		}
	}
	_, served := fx.terminal.calls()
	if served != 3 {
		t.Fatalf("served = %d, want 3", served)
	}
}

func TestTerminalWebSocketRejectsForeignHost(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	req := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=test-terminal-ticket", nil)
	req.Host = "evil.test"
	req.Header.Set("Origin", "http://evil.test")
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestTerminalWebSocketMapsTicketAndLimitErrors(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})

	fx.terminal.serveErr = terminal.ErrTicketInvalid
	req := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=bad", nil)
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ticket error status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if envelope.Code != codeTerminalTicketInvalid {
		t.Fatalf("error code = %q, want %q", envelope.Code, codeTerminalTicketInvalid)
	}

	fx.terminal.serveErr = terminal.ErrLimit
	req = httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=limit", nil)
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec = httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limit status = %d, body = %s", rec.Code, rec.Body.String())
	}

	fx.terminal.serveErr = terminal.ErrEnvironment
	req = httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/sites/site-1/terminal?ticket=env", nil)
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec = httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("environment status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode environment error: %v", err)
	}
	if envelope.Code != codeTerminalUnavailable {
		t.Fatalf("environment code = %q", envelope.Code)
	}
}

func TestTerminalWebSocketResolvesSiteByRef(t *testing.T) {
	_, fx := newTestAPIWithFakes(t)
	addTerminalSite(fx, model.Site{ID: "site-1", Name: "app", Kind: model.SitePHP, HostPath: "/tmp/app"})
	req := httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1/api/v1/sites/app/terminal?ticket="+url.QueryEscape("test-terminal-ticket"), nil)
	req.Header.Set("Origin", "http://127.0.0.1:7070")
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	fx.terminal.mu.Lock()
	defer fx.terminal.mu.Unlock()
	if len(fx.terminal.served) != 1 || fx.terminal.served[0].Site.Name != "app" {
		t.Fatalf("served = %+v", fx.terminal.served)
	}
}

func TestTerminalTicketResponseDateIsUTC(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	writeJSON(rec, http.StatusOK, terminal.Ticket{
		Token:     strings.Repeat("a", 64),
		ExpiresAt: time.Date(2026, 10, 26, 12, 0, 30, 0, time.UTC),
	})
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ticket"] == "" || body["expires_at"] == "" {
		t.Fatalf("body = %+v", body)
	}
}
