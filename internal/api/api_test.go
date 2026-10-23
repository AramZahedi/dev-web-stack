package api

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	runtimemgr "github.com/AramZahedi/dev-web-stack/internal/runtime"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// testSessionValue is a valid 32-byte session value expressed as hex.
const testSessionValue = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// apiFixture wires the real store/log store to deterministic fakes.
type apiFixture struct {
	server   *Server
	raw      http.Handler
	sites    *fakeSiteService
	runtimes *fakeRuntimeService
	doctor   *fakeDoctorService
	helper   *fakeHelperClient
	logs     *logs.Store
	store    *store.Store
	session  string
}

// sessionInjector simulates a browser that already called GET /session: it
// attaches the session cookie when the request has none. Mutations still need
// an explicit CSRF header, exactly as in production.
type sessionInjector struct {
	next    http.Handler
	session string
}

func (h sessionInjector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(sessionCookieName); err != nil {
		r = r.Clone(r.Context())
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: h.session})
	}
	h.next.ServeHTTP(w, r)
}

func newTestAPI(t *testing.T) http.Handler {
	t.Helper()
	h, _ := newTestAPIWithFakes(t)
	return h
}

func newTestAPIWithFakes(t *testing.T) (http.Handler, *apiFixture) {
	t.Helper()
	fx := newFixture(t, Config{})
	return fx.authedHandler(), fx
}

func newFixture(t *testing.T, cfg Config) *apiFixture {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	logStore, err := logs.New(filepath.Join(dir, "logs"), 10, 5)
	if err != nil {
		_ = st.Close()
		t.Fatalf("open log store: %v", err)
	}
	t.Cleanup(func() {
		_ = logStore.Close()
		_ = st.Close()
	})

	fakeSites := &fakeSiteService{sites: make(map[string]model.Site)}
	fakeRuntimes := &fakeRuntimeService{
		builds: []model.RuntimeBuild{{
			ID:          "node-24",
			Kind:        "node",
			Version:     "24.9.0",
			Backend:     model.BackendLocal,
			Path:        "/tmp/runtimes/node/24.9.0",
			InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		}},
		available: []catalog.Entry{{
			ID:      "node-24",
			Kind:    "node",
			Version: "24.9.0",
			Build:   "linux-x64",
			URL:     "https://example.invalid/node.tar.gz",
			SHA256:  strings.Repeat("a", 64),
			Size:    1024,
		}},
	}
	fakeDoctor := &fakeDoctorService{}
	fakeHelper := &fakeHelperClient{
		status: helperproto.StatusResponse{
			Version:       helperproto.Version,
			CAFingerprint: strings.Repeat("b", 64),
			HostsSynced:   true,
		},
		targets: []trust.Target{{Name: "System trust store", OK: true, Detail: "installed"}},
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	srv := NewServer(st, fakeSites, fakeRuntimes, fakeDoctor, logStore, fakeHelper, cfg)

	return &apiFixture{
		server:   srv,
		raw:      srv.Handler(),
		sites:    fakeSites,
		runtimes: fakeRuntimes,
		doctor:   fakeDoctor,
		helper:   fakeHelper,
		logs:     logStore,
		store:    st,
		session:  testSessionValue,
	}
}

func (fx *apiFixture) authedHandler() http.Handler {
	return sessionInjector{next: fx.raw, session: fx.session}
}

// request performs a cookie-authenticated request with a valid CSRF token for
// mutating methods.
func (fx *apiFixture) request(t *testing.T, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, body)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.session})
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("X-CSRF-Token", fx.server.csrfFor(fx.session))
	}
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	return rec
}

func TestAPIRejectsForeignHostAndMissingCSRF(t *testing.T) {
	h := newTestAPI(t)
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites", nil)
	req.Host = "evil.test"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host code = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("csrf code = %d", rec.Code)
	}
}

func TestCreateSiteAndList(t *testing.T) {
	h, fake := newTestAPIWithFakes(t)
	_ = h

	rec := httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("session code = %d body=%s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookies = %d", len(cookies))
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	req.AddCookie(cookies[0])
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec = httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create code = %d body=%s", rec.Code, rec.Body.String())
	}
	var created model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode site: %v", err)
	}
	if created.Domain != "blog.test" || created.Name != "blog" {
		t.Fatalf("created site = %+v", created)
	}

	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	fake.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list code = %d body=%s", rec.Code, rec.Body.String())
	}
	var list []model.Site
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Domain != "blog.test" {
		t.Fatalf("list = %+v", list)
	}
}

func TestLogsSSEStreamsLines(t *testing.T) {
	h, fake := newTestAPIWithFakes(t)
	fake.server.keepAliveInterval = time.Hour
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/sites/s1/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("logs content-type = %q", ct)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("logs cache-control = %q", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("logs x-accel-buffering = %q", got)
	}

	if _, err := io.WriteString(fake.logs.Writer("s1", "app"), "hello\n"); err != nil {
		t.Fatalf("write log: %v", err)
	}
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "hello") {
		t.Fatalf("stream = %q", buf[:n])
	}
}

func TestFSBrowserListsDirsAndRejectsFiles(t *testing.T) {
	h := newTestAPI(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/fs?path="+url.QueryEscape(dir), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sub") || strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("browse response = %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/fs?path="+url.QueryEscape(filepath.Join(dir, "secret.txt")), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("file browse code = %d", rec.Code)
	}
}

func TestSessionCookieAttributesAndCSRF(t *testing.T) {
	fx := newFixture(t, Config{})
	raw := fx.raw

	rec := httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("session code = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName {
		t.Fatalf("cookie name = %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Fatal("cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("SameSite = %v", cookie.SameSite)
	}
	if cookie.Secure {
		t.Fatal("cookie is Secure over plain HTTP")
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie path = %q", cookie.Path)
	}
	if len(cookie.Value) != 64 {
		t.Fatalf("session length = %d", len(cookie.Value))
	}
	if _, err := hex.DecodeString(cookie.Value); err != nil {
		t.Fatalf("session is not hex: %v", err)
	}

	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	mac := hmac.New(sha256.New, fx.server.secret[:])
	_, _ = mac.Write([]byte(cookie.Value))
	wantToken := hex.EncodeToString(mac.Sum(nil))
	if session.CSRFToken != wantToken {
		t.Fatalf("csrf token = %q, want HMAC %q", session.CSRFToken, wantToken)
	}
	if session.CSRFToken != fx.server.csrfFor(cookie.Value) {
		t.Fatal("csrfFor disagrees with HMAC")
	}

	// A request that already has a session cookie must not set a new one.
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, req)
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("session reissued cookie: %q", got)
	}

	// HTTPS termination is signalled by X-Forwarded-Proto.
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, req)
	cookies = rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("forwarded https cookie = %+v", cookies)
	}

	// A direct TLS request also sets Secure.
	req = httptest.NewRequest("GET", "http://127.0.0.1/api/v1/session", nil)
	req.TLS = &tls.ConnectionState{}
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, req)
	cookies = rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("TLS cookie = %+v", cookies)
	}

	// Mutations require the exact CSRF token for the presented session.
	req = httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", "not-the-token")
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf code = %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid csrf code = %d body=%s", rec.Code, rec.Body.String())
	}

	// Read-only API GETs are allowed without a session cookie so the dashboard
	// can load its first page while GET /session is still in flight.
	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cold-start read code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadsAndSSEAllowedWithoutSession(t *testing.T) {
	home := t.TempDir()
	fx := newFixture(t, Config{UserHome: home})
	for _, path := range []string{
		"/api/v1/sites",
		"/api/v1/runtimes",
		"/api/v1/runtimes/available",
		"/api/v1/settings",
		"/api/v1/doctor",
		"/api/v1/trust",
		"/api/v1/fs?path=" + url.QueryEscape(home),
	} {
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1"+path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s without session = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	// SSE reads are also unauthenticated on the loopback/Host-checked API.
	srv := httptest.NewServer(fx.raw)
	defer srv.Close()
	for _, path := range []string{"/api/v1/events", "/api/v1/sites/s1/logs/stream"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			cancel()
			t.Fatalf("GET %s without session = %d", path, resp.StatusCode)
		}
		_ = resp.Body.Close()
		cancel()
	}

	// Mutations still require the session and CSRF token.
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`)))
	assertError(t, rec, http.StatusForbidden, codeForbidden)
}

func TestSocketAuthBypassesCSRFButNotHost(t *testing.T) {
	fx := newFixture(t, Config{})
	h := WithSocketAuth(fx.raw)

	req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites",
		strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("socket create code = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("socket list code = %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(`{}`))
	req.Host = "evil.test"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("socket foreign host code = %d", rec.Code)
	}
}

func TestUnknownHostOnEveryRoute(t *testing.T) {
	fx := newFixture(t, Config{})
	for _, route := range fx.server.routes() {
		path := concretePath(route.pattern)
		method := route.method
		t.Run(method+" "+path, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://evil.test"+path, nil)
			req.Host = "evil.test:7070"
			rec := httptest.NewRecorder()
			fx.raw.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s host code = %d", method, path, rec.Code)
			}
		})
	}
}

func TestAllowedHostsAcceptPorts(t *testing.T) {
	fx := newFixture(t, Config{})
	for _, host := range []string{"127.0.0.1", "127.0.0.1:7070", "localhost:1234", "LOCALHOST", "devstack.test:8443", "devstack.test."} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("host %q code = %d", host, rec.Code)
		}
	}
	for _, host := range []string{"evil.test", "127.0.0.1.evil.test", "devstack.test.evil.test", "localhost:bad"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("host %q code = %d", host, rec.Code)
		}
	}
}

func TestConfiguredAllowedHostsUnionWithLoopback(t *testing.T) {
	fx := newFixture(t, Config{AllowedHosts: []string{"custom.test"}})
	for _, host := range []string{"custom.test", "custom.test:7070", "127.0.0.1", "127.0.0.1:7070", "localhost", "devstack.test"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		fx.raw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("allowed host %q code = %d", host, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/health", nil)
	req.Host = "evil.test"
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host code = %d", rec.Code)
	}
}

func TestAPIMethodMismatchReturnsJSON(t *testing.T) {
	fx := newFixture(t, Config{})
	rec := fx.request(t, "PUT", "/api/v1/sites", strings.NewReader(`{}`))
	assertError(t, rec, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("405 content-type = %q", ct)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Fatal("405 response is missing Allow")
	}
}

func TestBodyLimitReturns413(t *testing.T) {
	fx := newFixture(t, Config{MaxBodyBytes: 32})
	h := WithSocketAuth(fx.raw)
	body := `{"name":"` + strings.Repeat("a", 100) + `","path":"/tmp","kind":"static"}`
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit code = %d body=%s", rec.Code, rec.Body.String())
	}
	assertError(t, rec, http.StatusRequestEntityTooLarge, codePayloadTooLarge)

	// A chunked body has no Content-Length; the limit must still apply while
	// the JSON decoder reads it.
	req = httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", nil)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertError(t, rec, http.StatusRequestEntityTooLarge, codePayloadTooLarge)
}

func TestServiceErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"hosts sync", errors.New("sites: sync hosts for blog: write /etc/hosts: permission denied"), http.StatusConflict, codeHostsSyncFailed},
		{"domain conflict", errors.New(`sites: domain "blog.test" is already used by site "shop"`), http.StatusConflict, codeDomainConflict},
		{"runtime missing", errors.New("runtime: install node-missing: no catalog entry"), http.StatusConflict, codeRuntimeMissing},
		{"port exhaustion", errors.New("sites: allocate HTTP port: no free http port in range 41000-42999"), http.StatusConflict, codePortInUse},
		{"validation", errors.New(`sites: invalid project name "Bad Name"`), http.StatusBadRequest, codeInvalidRequest},
		{"unknown", errors.New("kaboom"), http.StatusInternalServerError, codeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, Config{})
			fx.sites.mu.Lock()
			fx.sites.err = tc.err
			fx.sites.mu.Unlock()
			rec := fx.request(t, "POST", "/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
			assertError(t, rec, tc.status, tc.code)
		})
	}
}

func TestSiteCRUDAndActions(t *testing.T) {
	fx := newFixture(t, Config{UserHome: t.TempDir()})

	rec := fx.request(t, "POST", "/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create code = %d body=%s", rec.Code, rec.Body.String())
	}
	var created model.Site
	decode(t, rec, &created)
	if created.ID == "" {
		t.Fatal("created site has no id")
	}

	rec = fx.request(t, "GET", "/api/v1/sites/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get code = %d", rec.Code)
	}
	var got model.Site
	decode(t, rec, &got)
	if got.Name != "blog" {
		t.Fatalf("got site = %+v", got)
	}

	rec = fx.request(t, "PATCH", "/api/v1/sites/"+created.ID, strings.NewReader(`{"domain":"shop.test","www":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch code = %d body=%s", rec.Code, rec.Body.String())
	}
	var updated model.Site
	decode(t, rec, &updated)
	if updated.Domain != "shop.test" || !updated.WWW {
		t.Fatalf("updated site = %+v", updated)
	}

	for _, action := range []string{"start", "stop", "restart"} {
		rec = fx.request(t, "POST", "/api/v1/sites/"+created.ID+"/"+action, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s code = %d body=%s", action, rec.Code, rec.Body.String())
		}
	}
	fx.sites.mu.Lock()
	actions := append([]string(nil), fx.sites.actions...)
	fx.sites.mu.Unlock()
	if strings.Join(actions, ",") != "start,stop,restart" {
		t.Fatalf("actions = %v", actions)
	}

	rec = fx.request(t, "DELETE", "/api/v1/sites/"+created.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete code = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, "GET", "/api/v1/sites/"+created.ID, nil)
	assertError(t, rec, http.StatusNotFound, codeNotFound)
}

func TestRuntimesDoctorAndTrustHandlers(t *testing.T) {
	home := t.TempDir()
	fx := newFixture(t, Config{UserHome: home})

	rec := fx.request(t, "GET", "/api/v1/runtimes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("runtimes code = %d", rec.Code)
	}
	var builds []model.RuntimeBuild
	decode(t, rec, &builds)
	if len(builds) != 1 || builds[0].ID != "node-24" {
		t.Fatalf("builds = %+v", builds)
	}

	rec = fx.request(t, "GET", "/api/v1/runtimes/available", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("available code = %d", rec.Code)
	}
	var available []catalog.Entry
	decode(t, rec, &available)
	if len(available) != 1 || available[0].ID != "node-24" {
		t.Fatalf("available = %+v", available)
	}

	rec = fx.request(t, "POST", "/api/v1/runtimes/install", strings.NewReader(`{"id":"node-24"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("install code = %d body=%s", rec.Code, rec.Body.String())
	}
	fx.runtimes.mu.Lock()
	if len(fx.runtimes.installed) != 1 || fx.runtimes.installed[0] != "node-24" {
		t.Fatalf("installed = %v", fx.runtimes.installed)
	}
	fx.runtimes.mu.Unlock()

	rec = fx.request(t, "DELETE", "/api/v1/runtimes/node-24", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove runtime code = %d body=%s", rec.Code, rec.Body.String())
	}

	fx.runtimes.mu.Lock()
	fx.runtimes.installErr = runtimemgr.ErrNoCatalogEntry
	fx.runtimes.mu.Unlock()
	rec = fx.request(t, "POST", "/api/v1/runtimes/install", strings.NewReader(`{"id":"node-missing"}`))
	assertError(t, rec, http.StatusConflict, codeRuntimeMissing)
	fx.runtimes.mu.Lock()
	fx.runtimes.installErr = nil
	fx.runtimes.mu.Unlock()

	rec = fx.request(t, "GET", "/api/v1/doctor", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("doctor code = %d", rec.Code)
	}
	var report doctor.Report
	decode(t, rec, &report)
	if len(report.Checks) != 1 || !report.OK {
		t.Fatalf("report = %+v", report)
	}

	if rec = fx.request(t, "POST", "/api/v1/doctor/run", nil); rec.Code != http.StatusOK {
		t.Fatalf("doctor run code = %d", rec.Code)
	}
	if rec = fx.request(t, "POST", "/api/v1/doctor/repair", nil); rec.Code != http.StatusOK {
		t.Fatalf("doctor repair code = %d", rec.Code)
	}
	if fx.doctor.runs() != 2 || fx.doctor.repairs() != 1 {
		t.Fatalf("doctor calls = run %d repair %d", fx.doctor.runs(), fx.doctor.repairs())
	}

	rec = fx.request(t, "GET", "/api/v1/trust", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("trust code = %d", rec.Code)
	}
	var targets []trust.Target
	decode(t, rec, &targets)
	if len(targets) != 1 || targets[0].Name != "System trust store" {
		t.Fatalf("targets = %+v", targets)
	}
	if rec = fx.request(t, "POST", "/api/v1/trust/repair", nil); rec.Code != http.StatusOK {
		t.Fatalf("trust repair code = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec = fx.request(t, "DELETE", "/api/v1/trust", nil); rec.Code != http.StatusOK {
		t.Fatalf("trust remove code = %d body=%s", rec.Code, rec.Body.String())
	}

	fx.helper.mu.Lock()
	homes := append([]string(nil), fx.helper.homes...)
	installs, removes, statuses := fx.helper.installs, fx.helper.removes, fx.helper.statuses
	fx.helper.mu.Unlock()
	for _, got := range homes {
		if got != home {
			t.Fatalf("helper home = %q, want %q", got, home)
		}
	}
	if installs != 1 || removes != 1 || statuses != 1 {
		t.Fatalf("trust calls = install %d remove %d status %d", installs, removes, statuses)
	}
}

func TestSPAFallbackAndAssetMIME(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":     {Data: []byte("<!doctype html><div id=\"app\"></div>")},
		"favicon.svg":    {Data: []byte("<svg></svg>")},
		"assets/app.js":  {Data: []byte("console.log('app')")},
		"assets/app.css": {Data: []byte("body{color:red}")},
	}
	fx := newFixture(t, Config{UI: ui})
	raw := fx.raw

	rec := httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="app"`) {
		t.Fatalf("index = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("index content-type = %q", ct)
	}

	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/sites/abc", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="app"`) {
		t.Fatalf("spa fallback = %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("js asset = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("js content-type = %q", ct)
	}

	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/assets/app.css", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("css content-type = %q", ct)
	}

	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/favicon.svg", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Fatalf("svg content-type = %q", ct)
	}

	rec = httptest.NewRecorder()
	raw.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/assets/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset code = %d", rec.Code)
	}

	rec = fx.request(t, "GET", "/api/v1/does-not-exist", nil)
	assertError(t, rec, http.StatusNotFound, codeNotFound)
}

func TestFSBrowserConfinedToUserHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	fx := newFixture(t, Config{UserHome: home})

	rec := fx.request(t, "GET", "/api/v1/fs?path="+url.QueryEscape(home), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("home browse code = %d body=%s", rec.Code, rec.Body.String())
	}
	var listing fsListing
	decode(t, rec, &listing)
	if listing.Path != home || len(listing.Entries) != 2 ||
		listing.Entries[0].Name != "alpha" || listing.Entries[1].Name != "projects" {
		t.Fatalf("listing = %+v", listing)
	}

	rec = fx.request(t, "GET", "/api/v1/fs?path="+url.QueryEscape(outside), nil)
	assertError(t, rec, http.StatusForbidden, codeForbidden)

	if err := os.Symlink(outside, filepath.Join(home, "escape")); err == nil {
		rec = fx.request(t, "GET", "/api/v1/fs?path="+url.QueryEscape(filepath.Join(home, "escape")), nil)
		assertError(t, rec, http.StatusForbidden, codeForbidden)
	}

	rec = fx.request(t, "GET", "/api/v1/fs?path=relative/path", nil)
	assertError(t, rec, http.StatusBadRequest, codeInvalidRequest)

	rec = fx.request(t, "GET", "/api/v1/fs?path="+url.QueryEscape(filepath.Join(home, "missing")), nil)
	assertError(t, rec, http.StatusNotFound, codeNotFound)
}

func TestSettingsUsesStoreOverrides(t *testing.T) {
	home := t.TempDir()
	fx := newFixture(t, Config{UserHome: home})

	for key, value := range map[string]string{
		"home":                  filepath.Join(home, "custom-devstack"),
		"wsl_distro":            "Devstack-Test",
		"http_port":             "9091",
		"https_port":            "9443",
		"ui_port":               "7071",
		"auto_start_sites":      "false",
		"graceful_stop_seconds": "7",
		"max_request_body_mb":   "256",
		"log_max_mb":            "20",
		"log_keep":              "8",
	} {
		if err := fx.store.SetSetting(context.Background(), key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	rec := fx.request(t, "GET", "/api/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings code = %d body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	decode(t, rec, &raw)
	for _, key := range []string{
		"home", "user_home", "wsl_distro", "http_port", "https_port", "ui_port", "auto_start_sites",
		"graceful_stop_seconds", "max_request_body_mb", "log_max_mb", "log_keep",
	} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("settings missing %q: %v", key, raw)
		}
	}
	if raw["home"] != filepath.Join(home, "custom-devstack") {
		t.Fatalf("home = %v", raw["home"])
	}
	if raw["wsl_distro"] != "Devstack-Test" {
		t.Fatalf("wsl_distro = %v", raw["wsl_distro"])
	}
	if raw["user_home"] != home {
		t.Fatalf("user_home = %v", raw["user_home"])
	}
	if raw["http_port"].(float64) != 9091 || raw["https_port"].(float64) != 9443 || raw["ui_port"].(float64) != 7071 {
		t.Fatalf("ports = %v", raw)
	}
	if raw["auto_start_sites"] != false {
		t.Fatalf("auto_start_sites = %v", raw["auto_start_sites"])
	}
	if raw["graceful_stop_seconds"].(float64) != 7 || raw["max_request_body_mb"].(float64) != 256 {
		t.Fatalf("timeouts = %v", raw)
	}
	if raw["log_max_mb"].(float64) != 20 || raw["log_keep"].(float64) != 8 {
		t.Fatalf("log settings = %v", raw)
	}
}

func TestSettingsDefaultBodyLimit(t *testing.T) {
	fx := newFixture(t, Config{})
	if fx.server.cfg.MaxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("default body limit = %d, want %d", fx.server.cfg.MaxBodyBytes, defaultMaxBodyBytes)
	}
	if defaultMaxBodyBytes != int64(1024)<<20 {
		t.Fatalf("default body limit = %d, want 1024 MiB", defaultMaxBodyBytes)
	}
	rec := fx.request(t, "GET", "/api/v1/settings", nil)
	var raw map[string]any
	decode(t, rec, &raw)
	if raw["max_request_body_mb"].(float64) != 1024 {
		t.Fatalf("max_request_body_mb = %v", raw["max_request_body_mb"])
	}
}

func TestEventsSSEPollsNewEvents(t *testing.T) {
	fx := newFixture(t, Config{})
	fx.server.eventPollInterval = 10 * time.Millisecond
	fx.server.keepAliveInterval = 20 * time.Millisecond
	srv := httptest.NewServer(fx.authedHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	if err := fx.store.AddEvent(context.Background(), model.Event{
		Type:    "site.running",
		SiteID:  "s1",
		Message: "site blog is running",
	}); err != nil {
		t.Fatalf("add event: %v", err)
	}

	data := readSSEData(t, resp.Body, 3*time.Second)
	if !strings.Contains(data, "site.running") || !strings.Contains(data, "blog") {
		t.Fatalf("event data = %q", data)
	}
}

func TestOpenAPIDocumentParsesAndDocumentsRoutes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc struct {
		OpenAPI string                    `yaml:"openapi"`
		Info    map[string]any            `yaml:"info"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	if doc.OpenAPI == "" || doc.Info == nil || len(doc.Paths) == 0 {
		t.Fatalf("openapi document is incomplete: %+v", doc)
	}

	fx := newFixture(t, Config{})
	for _, route := range fx.server.apiRoutes() {
		path := strings.ReplaceAll(route.pattern, "{path...}", "{path}")
		methods, ok := doc.Paths[path]
		if !ok {
			t.Fatalf("openapi missing path %q", path)
		}
		if _, ok := methods[strings.ToLower(route.method)]; !ok {
			t.Fatalf("openapi missing %s %s", route.method, path)
		}
	}
	for _, path := range []string{"/", "/assets/{path}"} {
		if _, ok := doc.Paths[path]; !ok {
			t.Fatalf("openapi missing UI path %q", path)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %T: %v body=%s", dst, err, rec.Body.String())
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, status, rec.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, rec.Body.String())
	}
	if body.Code != code {
		t.Fatalf("error code = %q, want %q body=%s", body.Code, code, rec.Body.String())
	}
	if body.Message == "" {
		t.Fatal("error message is empty")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode error fields: %v body=%s", err, rec.Body.String())
	}
	if _, ok := fields["details"]; !ok {
		t.Fatalf("error envelope is missing details: %s", rec.Body.String())
	}
}

func readSSEData(t *testing.T, body io.Reader, timeout time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				ch <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
		ch <- ""
	}()
	select {
	case data := <-ch:
		return data
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE data")
		return ""
	}
}

// concretePath substitutes path wildcards with representative values.
func concretePath(pattern string) string {
	path := strings.ReplaceAll(pattern, "{path...}", "app.js")
	path = strings.ReplaceAll(path, "{ref}", "s1")
	path = strings.ReplaceAll(path, "{id}", "node-24")
	return path
}

// --- fakes -----------------------------------------------------------------

type fakeSiteService struct {
	mu      sync.Mutex
	sites   map[string]model.Site
	nextID  int
	created []sites.CreateRequest
	updates []sites.UpdateRequest
	actions []string
	err     error
}

func (f *fakeSiteService) Create(_ context.Context, req sites.CreateRequest) (model.Site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		err := f.err
		f.err = nil
		return model.Site{}, err
	}
	f.nextID++
	site := model.Site{
		ID:           fmt.Sprintf("s%d", f.nextID),
		Name:         req.Name,
		Domain:       strings.ToLower(req.Name) + ".test",
		WWW:          req.WWW,
		Backend:      model.BackendLocal,
		HostPath:     req.Path,
		BackendPath:  req.Path,
		Kind:         model.SiteKind(req.Kind),
		NodeVersion:  req.NodeVersion,
		Command:      req.Command,
		Env:          req.Env,
		DocumentRoot: req.DocumentRoot,
		TLS:          req.TLS,
		ForceHTTPS:   req.ForceHTTPS,
		SPAFallback:  req.SPAFallback,
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		CreatedAt:    time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	f.sites[site.ID] = site
	f.created = append(f.created, req)
	return site, nil
}

func (f *fakeSiteService) Update(_ context.Context, ref string, req sites.UpdateRequest) (model.Site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	site, ok := f.find(ref)
	if !ok {
		return model.Site{}, fmt.Errorf("sites: site %q: %w", ref, store.ErrNotFound)
	}
	if req.Domain != "" {
		site.Domain = req.Domain
	}
	if req.WWW != nil {
		site.WWW = *req.WWW
	}
	if req.TLS != nil {
		site.TLS = *req.TLS
	}
	if req.ForceHTTPS != nil {
		site.ForceHTTPS = *req.ForceHTTPS
	}
	if req.SPAFallback != nil {
		site.SPAFallback = *req.SPAFallback
	}
	if req.Command != "" {
		site.Command = req.Command
	}
	if req.DocumentRoot != "" {
		site.DocumentRoot = req.DocumentRoot
	}
	if req.Env != nil {
		site.Env = req.Env
	}
	f.sites[site.ID] = site
	f.updates = append(f.updates, req)
	return site, nil
}

func (f *fakeSiteService) Get(_ context.Context, ref string) (model.Site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	site, ok := f.find(ref)
	if !ok {
		return model.Site{}, fmt.Errorf("sites: site %q: %w", ref, store.ErrNotFound)
	}
	return site, nil
}

func (f *fakeSiteService) List(_ context.Context) ([]model.Site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]model.Site, 0, len(f.sites))
	for _, site := range f.sites {
		list = append(list, site)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func (f *fakeSiteService) Start(_ context.Context, ref string) error {
	return f.action("start", ref)
}

func (f *fakeSiteService) Stop(_ context.Context, ref string) error {
	return f.action("stop", ref)
}

func (f *fakeSiteService) Restart(_ context.Context, ref string) error {
	return f.action("restart", ref)
}

func (f *fakeSiteService) Delete(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	site, ok := f.find(ref)
	if !ok {
		return fmt.Errorf("sites: site %q: %w", ref, store.ErrNotFound)
	}
	delete(f.sites, site.ID)
	return nil
}

func (f *fakeSiteService) action(action, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.find(ref); !ok {
		return fmt.Errorf("sites: site %q: %w", ref, store.ErrNotFound)
	}
	f.actions = append(f.actions, action)
	return nil
}

func (f *fakeSiteService) find(ref string) (model.Site, bool) {
	if site, ok := f.sites[ref]; ok {
		return site, true
	}
	for _, site := range f.sites {
		if site.Name == ref {
			return site, true
		}
	}
	return model.Site{}, false
}

type fakeRuntimeService struct {
	mu         sync.Mutex
	builds     []model.RuntimeBuild
	available  []catalog.Entry
	installed  []string
	removed    []string
	installErr error
	removeErr  error
}

func (f *fakeRuntimeService) List(context.Context) ([]model.RuntimeBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.RuntimeBuild(nil), f.builds...), nil
}

func (f *fakeRuntimeService) Install(_ context.Context, id string) (model.RuntimeBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installErr != nil {
		return model.RuntimeBuild{}, f.installErr
	}
	f.installed = append(f.installed, id)
	build := model.RuntimeBuild{
		ID:          id,
		Kind:        "node",
		Version:     "24.9.0",
		Backend:     model.BackendLocal,
		Path:        "/tmp/runtimes/" + id,
		InstalledAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	f.builds = append(f.builds, build)
	return build, nil
}

func (f *fakeRuntimeService) Remove(_ context.Context, buildID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, buildID)
	return nil
}

func (f *fakeRuntimeService) Available() []catalog.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]catalog.Entry(nil), f.available...)
}

type fakeDoctorService struct {
	mu      sync.Mutex
	runN    int
	repairN int
	err     error
}

func (f *fakeDoctorService) Run(context.Context) (doctor.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runN++
	if f.err != nil {
		return doctor.Report{}, f.err
	}
	return doctor.Report{
		Checks: []doctor.Check{{ID: "helper", Title: "Helper", Status: doctor.StatusOK, Detail: "ok"}},
		OK:     true,
	}, nil
}

func (f *fakeDoctorService) Repair(context.Context) (doctor.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repairN++
	return doctor.Report{
		Checks: []doctor.Check{{ID: "helper", Title: "Helper", Status: doctor.StatusOK, Detail: "repaired"}},
		OK:     true,
	}, nil
}

func (f *fakeDoctorService) runs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runN
}

func (f *fakeDoctorService) repairs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repairN
}

type fakeHelperClient struct {
	mu        sync.Mutex
	status    helperproto.StatusResponse
	statusErr error
	targets   []trust.Target
	trustErr  error
	syncs     [][]string
	homes     []string
	installs  int
	removes   int
	statuses  int
}

func (f *fakeHelperClient) Status(context.Context) (helperproto.StatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.statusErr
}

func (f *fakeHelperClient) SyncHosts(_ context.Context, domains []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs = append(f.syncs, append([]string(nil), domains...))
	return nil
}

func (f *fakeHelperClient) InstallTrust(_ context.Context, userHome string) ([]trust.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.homes = append(f.homes, userHome)
	f.installs++
	if f.trustErr != nil {
		return nil, f.trustErr
	}
	return append([]trust.Target(nil), f.targets...), nil
}

func (f *fakeHelperClient) RemoveTrust(_ context.Context, userHome string) ([]trust.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.homes = append(f.homes, userHome)
	f.removes++
	if f.trustErr != nil {
		return nil, f.trustErr
	}
	return append([]trust.Target(nil), f.targets...), nil
}

func (f *fakeHelperClient) TrustStatus(_ context.Context, userHome string) ([]trust.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.homes = append(f.homes, userHome)
	f.statuses++
	if f.trustErr != nil {
		return nil, f.trustErr
	}
	return append([]trust.Target(nil), f.targets...), nil
}
