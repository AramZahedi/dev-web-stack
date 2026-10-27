package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

// capturedFCGIRequest records one request handed to the FastCGI transport.
type capturedFCGIRequest struct {
	Params     map[string]string
	Body       string
	BodyLength int64
}

// captureFCGIClient is a scripted fcgiClient. It captures requests and returns
// the configured response, or an error before any headers.
type captureFCGIClient struct {
	mu sync.Mutex

	requests []capturedFCGIRequest
	dials    int
	dialAddr string
	closes   int

	status int
	header http.Header
	body   string
	doErr  error
}

func newCaptureFCGIClient() *captureFCGIClient {
	return &captureFCGIClient{status: http.StatusOK, header: make(http.Header)}
}

func (c *captureFCGIClient) Do(_ context.Context, req fcgi.Request) (*fcgi.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}

	c.mu.Lock()
	c.requests = append(c.requests, capturedFCGIRequest{
		Params:     cloneTestParams(req.Params),
		Body:       string(body),
		BodyLength: req.BodyLength,
	})
	doErr := c.doErr
	status := c.status
	header := c.header.Clone()
	responseBody := c.body
	c.mu.Unlock()

	if doErr != nil {
		return nil, doErr
	}
	if status == 0 {
		status = http.StatusOK
	}
	if header == nil {
		header = make(http.Header)
	}
	return &fcgi.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}, nil
}

func (c *captureFCGIClient) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}

func (c *captureFCGIClient) reset() {
	c.mu.Lock()
	c.requests = nil
	c.mu.Unlock()
}

func (c *captureFCGIClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *captureFCGIClient) last(t *testing.T) capturedFCGIRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("no FastCGI request was sent")
	}
	return c.requests[len(c.requests)-1]
}

func (c *captureFCGIClient) dialInfo() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dials, c.dialAddr
}

func cloneTestParams(params map[string]string) map[string]string {
	cloned := make(map[string]string, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}

// installCaptureClient replaces the router's FastCGI dialer with one that
// returns client.
func installCaptureClient(r *Router, client *captureFCGIClient) {
	r.fcgiDial = func(_ context.Context, addr string) (fcgiClient, error) {
		client.mu.Lock()
		client.dials++
		client.dialAddr = addr
		client.mu.Unlock()
		return client, nil
	}
}

// newPHPRoute builds a router with one PHP route and a capture FastCGI client.
func newPHPRoute(t *testing.T, root string, frontController, tlsEnabled bool) (*Router, *captureFCGIClient) {
	t.Helper()
	r := NewRouter()
	r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {
		SiteID:          "php-site",
		Host:            "blog.test",
		Kind:            KindPHP,
		Upstream:        "127.0.0.1:43001",
		DocumentRoot:    root,
		FrontController: frontController,
		TLS:             tlsEnabled,
	}}})
	client := newCaptureFCGIClient()
	installCaptureClient(r, client)
	return r, client
}

func servePHPTest(r *Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)
	return rec
}

func writeTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func evalTestRoot(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve test root: %v", err)
	}
	return resolved
}

func TestPHPProxyFrontControllerParams(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.php", "<?php echo 'front';")
	resolvedRoot := evalTestRoot(t, root)

	r, client := newPHPRoute(t, root, true, true)
	client.status = http.StatusCreated
	client.header.Set("X-Upstream", "yes")
	client.body = "from-fpm"

	req := httptest.NewRequest(http.MethodGet, "https://blog.test/users/42?x=1", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "evil.test")
	req.Header.Set("X-Client", "one")
	rec := servePHPTest(r, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Body.String(); got != "from-fpm" {
		t.Fatalf("body = %q", got)
	}
	if got := rec.Header().Get("X-Upstream"); got != "yes" {
		t.Fatalf("upstream header = %q", got)
	}

	request := client.last(t)
	want := map[string]string{
		"GATEWAY_INTERFACE":      "CGI/1.1",
		"SERVER_SOFTWARE":        "devstack",
		"SERVER_PROTOCOL":        req.Proto,
		"REQUEST_METHOD":         http.MethodGet,
		"SCRIPT_FILENAME":        filepath.Join(resolvedRoot, "index.php"),
		"SCRIPT_NAME":            "/index.php",
		"PATH_INFO":              "/users/42",
		"DOCUMENT_ROOT":          resolvedRoot,
		"REQUEST_URI":            "/users/42?x=1",
		"QUERY_STRING":           "x=1",
		"SERVER_NAME":            "blog.test",
		"SERVER_PORT":            "443",
		"HTTPS":                  "on",
		"REMOTE_ADDR":            "192.0.2.1",
		"REMOTE_PORT":            "1234",
		"CONTENT_LENGTH":         "0",
		"HTTP_HOST":              "blog.test",
		"HTTP_X_FORWARDED_FOR":   "192.0.2.1",
		"HTTP_X_FORWARDED_PROTO": "https",
		"HTTP_X_FORWARDED_HOST":  "blog.test",
		"HTTP_X_CLIENT":          "one",
	}
	for key, value := range want {
		if got := request.Params[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, ok := request.Params["CONTENT_TYPE"]; ok {
		t.Errorf("CONTENT_TYPE set for a request without one")
	}
	if request.BodyLength != 0 {
		t.Errorf("BodyLength = %d, want 0", request.BodyLength)
	}
	dials, addr := client.dialInfo()
	if dials != 1 || addr != "127.0.0.1:43001" {
		t.Errorf("dial = %d to %q", dials, addr)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS header emitted: %q", got)
	}
}

func TestPHPProxyServesExistingFilesDirectly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "home page")
	writeTestFile(t, root, "style.css", "body { color: red; }")
	writeTestFile(t, root, "app.php", "<?php echo 'app';")
	resolvedRoot := evalTestRoot(t, root)

	r, client := newPHPRoute(t, root, false, false)

	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/index.html", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "home page" {
		t.Fatalf("index.html = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if n := client.count(); n != 0 {
		t.Fatalf("static index.html used FastCGI: %d calls", n)
	}

	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/style.css", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "body { color: red; }" {
		t.Fatalf("style.css = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("style.css Content-Type = %q", got)
	}
	if n := client.count(); n != 0 {
		t.Fatalf("static style.css used FastCGI: %d calls", n)
	}

	client.body = "executed"
	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/app.php", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "executed" {
		t.Fatalf("app.php = %d %q", rec.Code, rec.Body.String())
	}
	request := client.last(t)
	if got, want := request.Params["SCRIPT_FILENAME"], filepath.Join(resolvedRoot, "app.php"); got != want {
		t.Fatalf("SCRIPT_FILENAME = %q, want %q", got, want)
	}
	if request.Params["SCRIPT_NAME"] != "/app.php" || request.Params["PATH_INFO"] != "" {
		t.Fatalf("SCRIPT_NAME/PATH_INFO = %q/%q", request.Params["SCRIPT_NAME"], request.Params["PATH_INFO"])
	}
	if request.Params["SERVER_PORT"] != "80" {
		t.Fatalf("SERVER_PORT = %q, want 80", request.Params["SERVER_PORT"])
	}
	if _, ok := request.Params["HTTPS"]; ok {
		t.Fatal("HTTPS set for an HTTP request")
	}
}

func TestPHPProxyFrontControllerMissingIndexIsNotFound(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "style.css", "body {}")

	r, client := newPHPRoute(t, root, true, false)
	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/users/42", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if n := client.count(); n != 0 {
		t.Fatalf("missing front controller used FastCGI: %d calls", n)
	}
	if !strings.Contains(rec.Body.String(), "devstack") {
		t.Fatalf("404 page is not branded: %q", rec.Body.String())
	}
}

func TestPHPProxyDirectoryPrefersIndexPHPThenIndexHTML(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "with-php/index.php", "<?php echo 'php index';")
	writeTestFile(t, root, "with-php/index.html", "html index must lose")
	writeTestFile(t, root, "html-only/index.html", "html index")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedRoot := evalTestRoot(t, root)

	r, client := newPHPRoute(t, root, false, false)
	client.body = "php index"

	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/with-php/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "php index" {
		t.Fatalf("with-php = %d %q", rec.Code, rec.Body.String())
	}
	if n := client.count(); n != 1 {
		t.Fatalf("with-php FastCGI calls = %d", n)
	}
	request := client.last(t)
	if got, want := request.Params["SCRIPT_FILENAME"], filepath.Join(resolvedRoot, "with-php", "index.php"); got != want {
		t.Fatalf("SCRIPT_FILENAME = %q, want %q", got, want)
	}
	if got, want := request.Params["SCRIPT_NAME"], "/with-php/index.php"; got != want {
		t.Fatalf("SCRIPT_NAME = %q, want %q", got, want)
	}
	if request.Params["PATH_INFO"] != "" {
		t.Fatalf("PATH_INFO = %q", request.Params["PATH_INFO"])
	}

	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/html-only", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/html-only/" {
		t.Fatalf("html-only redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/html-only/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "html index" {
		t.Fatalf("html-only = %d %q", rec.Code, rec.Body.String())
	}
	if n := client.count(); n != 1 {
		t.Fatalf("html-only directory used FastCGI: %d calls", n)
	}

	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/empty/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty directory status = %d, want 404", rec.Code)
	}
	if n := client.count(); n != 1 {
		t.Fatalf("empty directory used FastCGI: %d calls", n)
	}
}

func TestPHPProxyRejectsTraversalAndSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "index.php", "<?php echo 'front';")
	writeTestFile(t, root, ".env", "TOPSECRET=dotfile")
	outside := writeTestFile(t, parent, "outside.txt", "TOPSECRET=outside")
	for _, name := range []string{"link-outside", "link-outside.php"} {
		if err := os.Symlink(outside, filepath.Join(root, name)); err != nil {
			t.Fatalf("create symlink %s: %v", name, err)
		}
	}

	r, client := newPHPRoute(t, root, true, false)

	for _, target := range []string{
		"/../outside.txt",
		"/%2e%2e/outside.txt",
		"/link-outside",
		"/link-outside.php",
		"/link-outside.php/extra",
		"/.env",
	} {
		t.Run(target, func(t *testing.T) {
			client.reset()
			req := httptest.NewRequest(http.MethodGet, "http://blog.test"+target, nil)
			rec := servePHPTest(r, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "TOPSECRET") {
				t.Fatalf("response disclosed a file: %q", rec.Body.String())
			}
			if n := client.count(); n != 0 {
				t.Fatalf("denied path used FastCGI: %d calls", n)
			}
		})
	}
}

func TestPHPProxyNeverServesPHPSource(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app.PHP", "<?php SECRET_UPPER_PHP")
	writeTestFile(t, root, "app.php", "<?php SECRET_ORIGINAL")
	backups := []string{
		"app.php~", "app.php.bak", "app.php.orig", "app.php.save", "app.php.old",
		"app.php.swp", "app.php.swo", "app.php.tmp",
	}
	for _, name := range backups {
		writeTestFile(t, root, name, "<?php SECRET_BACKUP_"+name)
	}
	resolvedRoot := evalTestRoot(t, root)

	r, client := newPHPRoute(t, root, false, false)
	client.body = "executed"

	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/app.PHP", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "executed" {
		t.Fatalf("app.PHP = %d %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("PHP source was served: %q", rec.Body.String())
	}
	request := client.last(t)
	if got, want := request.Params["SCRIPT_FILENAME"], filepath.Join(resolvedRoot, "app.PHP"); got != want {
		t.Fatalf("SCRIPT_FILENAME = %q, want %q", got, want)
	}
	if got := request.Params["SCRIPT_NAME"]; got != "/app.PHP" {
		t.Fatalf("SCRIPT_NAME = %q", got)
	}

	for _, name := range backups {
		t.Run(name, func(t *testing.T) {
			client.reset()
			rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/"+name, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "SECRET") {
				t.Fatalf("backup source was served: %q", rec.Body.String())
			}
			if n := client.count(); n != 0 {
				t.Fatalf("backup used FastCGI: %d calls", n)
			}
		})
	}
}

func TestPHPProxyBackupSuffixesMatchNativeDenyList(t *testing.T) {
	native := vhost.PHPSourceBackupSuffixes()
	want := make([]string, 0, len(native))
	for _, suffix := range native {
		want = append(want, ".php"+suffix)
	}
	if len(phpBackupSuffixes) != len(want) {
		t.Fatalf("proxy backup suffixes = %v, native deny suffixes = %v", phpBackupSuffixes, want)
	}
	for index, suffix := range phpBackupSuffixes {
		if suffix != want[index] {
			t.Fatalf("proxy backup suffix %d = %q, native deny suffix = %q", index, suffix, want[index])
		}
	}
}

func TestPHPProxyPreservesPathInfoForPHPFile(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app.php", "<?php echo 'app';")
	resolvedRoot := evalTestRoot(t, root)

	r, client := newPHPRoute(t, root, false, false)
	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/app.php/extra/path?q=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	request := client.last(t)
	want := map[string]string{
		"SCRIPT_FILENAME": filepath.Join(resolvedRoot, "app.php"),
		"SCRIPT_NAME":     "/app.php",
		"PATH_INFO":       "/extra/path",
		"QUERY_STRING":    "q=1",
		"REQUEST_URI":     "/app.php/extra/path?q=1",
	}
	for key, value := range want {
		if got := request.Params[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}

	client.reset()
	rec = servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/app.php/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("trailing slash status = %d", rec.Code)
	}
	request = client.last(t)
	if got, want := request.Params["SCRIPT_NAME"], "/app.php"; got != want {
		t.Fatalf("trailing slash SCRIPT_NAME = %q, want %q", got, want)
	}
	if got, want := request.Params["PATH_INFO"], "/"; got != want {
		t.Fatalf("trailing slash PATH_INFO = %q, want %q", got, want)
	}
}

func TestPHPProxyStripsHSTSAndHopByHopResponseHeaders(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.php", "<?php echo 'front';")

	r, client := newPHPRoute(t, root, true, true)
	client.header.Set("Strict-Transport-Security", "max-age=31536000")
	client.header.Set("X-Upstream", "kept")
	client.header.Set("Content-Type", "text/plain; charset=utf-8")
	client.header.Set("Connection", "X-Hop, keep-alive")
	client.header.Set("X-Hop", "drop")
	client.header.Set("Keep-Alive", "timeout=5")
	client.header.Set("Transfer-Encoding", "chunked")
	client.header.Set("Status", "200 OK")
	client.body = "response body"

	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "https://blog.test/anything", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "response body" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Upstream"); got != "kept" {
		t.Fatalf("X-Upstream = %q", got)
	}
	for _, name := range []string{
		"Strict-Transport-Security",
		"Connection",
		"X-Hop",
		"Keep-Alive",
		"Transfer-Encoding",
		"Status",
	} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("%s was not stripped: %q", name, got)
		}
	}
}

func TestPHPProxyReturnsBranded502BeforeHeaders(t *testing.T) {
	t.Run("dial failure", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.php", "<?php echo 'front';")
		r, _ := newPHPRoute(t, root, true, true)
		r.fcgiDial = func(context.Context, string) (fcgiClient, error) {
			return nil, errors.New("connection refused")
		}

		rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "https://blog.test/users", nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "blog.test") || !strings.Contains(body, "devstack") {
			t.Fatalf("502 page is not branded with the site: %q", body)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Fatalf("HSTS header emitted on 502: %q", got)
		}
	})

	t.Run("read failure before headers", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "index.php", "<?php echo 'front';")
		r, client := newPHPRoute(t, root, true, true)
		client.doErr = errors.New("connection reset while reading headers")

		rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "https://blog.test/users", nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "blog.test") {
			t.Fatalf("502 page does not name the site: %q", body)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Fatalf("HSTS header emitted on 502: %q", got)
		}
	})
}

func TestPHPProxySuppressesBodyForHEAD(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app.php", "<?php echo 'app';")

	r, client := newPHPRoute(t, root, false, false)
	client.header.Set("Content-Length", "17")
	client.body = "should not be sent"

	rec := servePHPTest(r, httptest.NewRequest(http.MethodHead, "http://blog.test/app.php", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD response body = %q", rec.Body.String())
	}
	if got := client.last(t).Params["REQUEST_METHOD"]; got != http.MethodHead {
		t.Fatalf("REQUEST_METHOD = %q", got)
	}
}

func TestPHPProxyStreamsRequestBody(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "app.php", "<?php echo 'app';")

	r, client := newPHPRoute(t, root, false, false)
	client.body = "ok"

	body := "name=devstack&x=1"
	req := httptest.NewRequest(http.MethodPost, "http://blog.test/app.php", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := servePHPTest(r, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	request := client.last(t)
	if request.Body != body {
		t.Fatalf("streamed body = %q, want %q", request.Body, body)
	}
	if want := int64(len(body)); request.BodyLength != want {
		t.Fatalf("BodyLength = %d, want %d", request.BodyLength, want)
	}
	if got := request.Params["REQUEST_METHOD"]; got != http.MethodPost {
		t.Fatalf("REQUEST_METHOD = %q", got)
	}
	if got := request.Params["CONTENT_TYPE"]; got != "application/x-www-form-urlencoded" {
		t.Fatalf("CONTENT_TYPE = %q", got)
	}
	if got, want := request.Params["CONTENT_LENGTH"], strconv.Itoa(len(body)); got != want {
		t.Fatalf("CONTENT_LENGTH = %q, want %q", got, want)
	}
	if _, ok := request.Params["HTTP_CONTENT_TYPE"]; ok {
		t.Fatal("CONTENT_TYPE was also exposed as HTTP_CONTENT_TYPE")
	}
	if _, ok := request.Params["HTTP_CONTENT_LENGTH"]; ok {
		t.Fatal("CONTENT_LENGTH was also exposed as HTTP_CONTENT_LENGTH")
	}

	client.reset()
	req = httptest.NewRequest(http.MethodPost, "http://blog.test/app.php", strings.NewReader("chunked-data"))
	req.ContentLength = -1
	if rec := servePHPTest(r, req); rec.Code != http.StatusOK {
		t.Fatalf("chunked status = %d", rec.Code)
	}
	request = client.last(t)
	if request.BodyLength != -1 {
		t.Fatalf("unknown-length BodyLength = %d, want -1", request.BodyLength)
	}
	if _, ok := request.Params["CONTENT_LENGTH"]; ok {
		t.Fatal("CONTENT_LENGTH set for an unknown-length body")
	}
}

func TestPHPProxyBuildsCGIHeadersAndIgnoresClientForwarding(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.php", "<?php echo 'front';")

	r, client := newPHPRoute(t, root, true, false)
	req := httptest.NewRequest(http.MethodGet, "http://Blog.TEST:8080/users", nil)
	req.Host = "Blog.TEST:8080"
	req.Header["Host"] = []string{"evil.test"}
	req.Header.Set("X-Multi", "one")
	req.Header.Add("X-Multi", "two")
	req.Header.Set("Connection", "X-Hop, keep-alive")
	req.Header.Set("X-Hop", "drop")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("TE", "trailers")
	req.Header.Set("Proxy", "http://attacker.example:8080")
	req.Header.Set("Proxy-Authorization", "Basic abc")
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.test")
	req.Header.Set("X-Forwarded-Custom", "untrusted")
	req.RemoteAddr = "198.51.100.7:4567"

	rec := servePHPTest(r, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	request := client.last(t)
	want := map[string]string{
		"SERVER_NAME":            "blog.test",
		"SERVER_PORT":            "8080",
		"HTTP_HOST":              "Blog.TEST:8080",
		"HTTP_X_MULTI":           "one, two",
		"HTTP_X_FORWARDED_FOR":   "198.51.100.7",
		"HTTP_X_FORWARDED_PROTO": "http",
		"HTTP_X_FORWARDED_HOST":  "Blog.TEST:8080",
	}
	for key, value := range want {
		if got := request.Params[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, ok := request.Params["HTTPS"]; ok {
		t.Errorf("HTTPS set for an HTTP request: %q", request.Params["HTTPS"])
	}
	for _, key := range []string{
		"HTTP_X_HOP",
		"HTTP_KEEP_ALIVE",
		"HTTP_TE",
		"HTTP_PROXY",
		"HTTP_PROXY_AUTHORIZATION",
		"HTTP_X_FORWARDED_CUSTOM",
		"HTTP_CONTENT_TYPE",
		"HTTP_CONTENT_LENGTH",
	} {
		if value, ok := request.Params[key]; ok {
			t.Errorf("%s was forwarded: %q", key, value)
		}
	}
}

func TestPHPProxyMapsMaxBytesErrorTo413(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.php", "<?php echo 'front';")
	r, client := newPHPRoute(t, root, true, false)
	client.doErr = &http.MaxBytesError{Limit: 1024}
	req := httptest.NewRequest(http.MethodPost, "http://blog.test/upload", strings.NewReader("body"))
	rec := servePHPTest(r, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%q", rec.Code, rec.Body.String())
	}
}

func TestPHPProxyFlushesStreamingResponse(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.php", "<?php echo 'stream';")
	r, client := newPHPRoute(t, root, true, false)
	client.body = "streamed body"
	req := httptest.NewRequest(http.MethodGet, "http://blog.test/stream", nil)
	rec := servePHPTest(r, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "streamed body" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if !rec.Flushed {
		t.Fatal("FastCGI response was not flushed")
	}
}

func TestPHPDirectoryRedirectEscapesLocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow '?' in directory names")
	}
	root := t.TempDir()
	writeTestFile(t, root, "a?b/index.html", "index")
	r, _ := newPHPRoute(t, root, false, false)
	rec := servePHPTest(r, httptest.NewRequest(http.MethodGet, "http://blog.test/a%3Fb", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("code = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/a%3Fb/" {
		t.Fatalf("Location = %q, want /a%%3Fb/", got)
	}
}
