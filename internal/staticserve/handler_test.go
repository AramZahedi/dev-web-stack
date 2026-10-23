package staticserve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T, root string, spa bool) *StaticHandler {
	t.Helper()
	h, err := NewStaticHandler(root, spa)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("close static handler: %v", err)
		}
	})
	return h
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStaticHandlerRejectsTraversalAndServesIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "ok")
	writeFile(t, filepath.Join(root, "secret"), "inside secret")

	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret"), "outside secret")
	if err := os.Symlink(outside, filepath.Join(root, "link-outside")); err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, root, false)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != "ok" {
		t.Fatal("index not served")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	for _, p := range []string{"/../secret", "/%2e%2e/secret", "/link-outside"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("traversal %q served", p)
		}
		if strings.Contains(rec.Body.String(), "outside secret") || strings.Contains(rec.Body.String(), "inside secret") {
			t.Fatalf("traversal %q leaked file contents: %q", p, rec.Body.String())
		}
	}
}

func TestStaticHandlerServesDirectoryIndexWithoutListing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "root index")
	writeFile(t, filepath.Join(root, "docs", "index.html"), "docs index")
	writeFile(t, filepath.Join(root, "assets", "app.js"), "console.log(1)")

	h := newTestHandler(t, root, false)

	for _, tc := range []struct {
		path     string
		wantLoc  string
		wantBody string
	}{
		{"/docs", "/docs/", ""},
		{"/docs?x=1", "/docs/?x=1", ""},
		{"/docs/", "", "docs index"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if tc.wantLoc != "" {
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("%s: code=%d, want 301", tc.path, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLoc {
				t.Fatalf("%s: Location = %q, want %q", tc.path, got, tc.wantLoc)
			}
			continue
		}
		if rec.Code != http.StatusOK || rec.Body.String() != tc.wantBody {
			t.Fatalf("%s: code=%d body=%q", tc.path, rec.Code, rec.Body.String())
		}
	}

	// A directory without index.html is a 404, never a generated listing.
	for _, p := range []string{"/assets", "/assets/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: code=%d", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "app.js") {
			t.Fatalf("%s: directory listing leaked: %q", p, rec.Body.String())
		}
	}
}

func TestStaticHandlerMIMETypesAndMissingFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.js"), "console.log(1)")
	writeFile(t, filepath.Join(root, "style.css"), "body{}")
	writeFile(t, filepath.Join(root, "data.json"), "{}")
	h := newTestHandler(t, root, false)

	cases := map[string]string{
		"/app.js":    "javascript",
		"/style.css": "text/css",
		"/data.json": "application/json",
	}
	for p, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: code=%d", p, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, want) {
			t.Fatalf("%s: Content-Type = %q, want %q", p, got, want)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q", p, got)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/missing.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing file code = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("404 Cache-Control = %q", got)
	}
}

func TestStaticHandlerServesRanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "range.txt"), "0123456789")
	h := newTestHandler(t, root, false)

	req := httptest.NewRequest("GET", "/range.txt", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("code = %d, want 206", rec.Code)
	}
	if got := rec.Body.String(); got != "2345" {
		t.Fatalf("body = %q", got)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
}

func TestStaticHandlerSPAFallbackOnlyForNonFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "<html>spa</html>")
	writeFile(t, filepath.Join(root, "main.js"), "console.log(2)")
	h := newTestHandler(t, root, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sites/blog", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>spa</html>" {
		t.Fatalf("SPA fallback: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("SPA fallback Content-Type = %q", got)
	}

	for _, p := range []string{"/missing.js", "/missing.css", "/missing.png"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: fallback served for an asset path (code=%d)", p, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/main.js", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(2)" {
		t.Fatalf("existing asset not served: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestStaticHandlerSPAFallbackRequiresIndex(t *testing.T) {
	root := t.TempDir()
	h := newTestHandler(t, root, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/route", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 without index.html", rec.Code)
	}
}

func TestStaticHandlerRejectsUnsupportedMethods(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "ok")
	h := newTestHandler(t, root, false)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("x")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST code = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); !strings.Contains(got, "GET") {
		t.Fatalf("Allow = %q", got)
	}
}

func TestNewStaticHandlerRejectsInvalidRoots(t *testing.T) {
	if _, err := NewStaticHandler("", false); err == nil {
		t.Fatal("empty root accepted")
	}
	if _, err := NewStaticHandler(filepath.Join(t.TempDir(), "missing"), false); err == nil {
		t.Fatal("missing root accepted")
	}

	file := filepath.Join(t.TempDir(), "file.txt")
	writeFile(t, file, "x")
	if _, err := NewStaticHandler(file, false); err == nil {
		t.Fatal("regular file accepted as a root")
	}
}

func TestStaticHandlerCloseIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "index.html"), "ok")
	h, err := NewStaticHandler(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
