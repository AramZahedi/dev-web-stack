// Package staticserve serves a static directory for a devstack site. Every
// request is confined to the configured root with os.Root, so neither .. path
// segments nor symlinks can escape it.
package staticserve

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
)

// StaticHandler serves files from one directory. It is safe for concurrent
// use until Close is called.
type StaticHandler struct {
	root        *os.Root
	spaFallback bool

	closeOnce sync.Once
	closeErr  error
}

// NewStaticHandler opens root and returns a handler for it. When spaFallback
// is true, a request for a missing path without a file extension serves the
// root index.html; missing asset paths still return 404.
func NewStaticHandler(root string, spaFallback bool) (*StaticHandler, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("staticserve: root is empty")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("staticserve: root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("staticserve: root %s is not a directory", root)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("staticserve: open root %s: %w", root, err)
	}
	return &StaticHandler{root: opened, spaFallback: spaFallback}, nil
}

// Close releases the root directory handle. It is safe to call more than once.
func (h *StaticHandler) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		if h.root != nil {
			h.closeErr = h.root.Close()
		}
	})
	return h.closeErr
}

// ServeHTTP serves GET and HEAD requests. Responses are never cached and
// directories are only served through their index.html; the handler never
// generates a directory listing.
func (h *StaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	rel, ok := cleanRequestPath(r.URL.Path)
	if !ok {
		h.notFound(w, r)
		return
	}

	info, err := h.root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && h.spaFallback && isNonFile(rel) {
			h.serveIndex(w, r)
			return
		}
		h.notFound(w, r)
		return
	}

	switch {
	case info.IsDir():
		h.serveDirectory(w, r, rel)
	case info.Mode().IsRegular():
		h.serveFile(w, r, rel)
	default:
		// Never serve devices, sockets, or FIFOs from a static root.
		h.notFound(w, r)
	}
}

// serveDirectory serves rel/index.html. A directory without an index.html is a
// 404 rather than a generated listing. Requests without a trailing slash are
// redirected so relative links inside the document resolve under the directory.
func (h *StaticHandler) serveDirectory(w http.ResponseWriter, r *http.Request, rel string) {
	index := path.Join(rel, "index.html")
	info, err := h.root.Stat(index)
	if err != nil || !info.Mode().IsRegular() {
		h.notFound(w, r)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/") {
		// Use the cleaned relative path, never the raw request path: a raw
		// "//attacker.example" would become a protocol-relative redirect.
		location := "/"
		if rel != "." {
			location = "/" + rel + "/"
		}
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	h.serveFile(w, r, index)
}

// serveFile opens name inside the root and lets http.ServeContent handle MIME
// types, conditional requests, HEAD, and ranges.
func (h *StaticHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.root.Open(name)
	if err != nil {
		h.notFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		h.notFound(w, r)
		return
	}

	// ServeContent infers the content type from the name. Use the final path
	// element so directory index files are typed by their own extension.
	displayName := path.Base(name)
	http.ServeContent(w, r, displayName, info.ModTime(), f)
}

// serveIndex serves the root index.html for an SPA fallback.
func (h *StaticHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	h.serveFile(w, r, "index.html")
}

func (h *StaticHandler) notFound(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// cleanRequestPath returns a slash-relative path confined to the root. It
// rejects NUL bytes and any literal or percent-decoded .. segment instead of
// silently normalizing an escape attempt.
func cleanRequestPath(raw string) (string, bool) {
	if strings.ContainsRune(raw, 0) {
		return "", false
	}
	if raw == "" {
		raw = "/"
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", false
		}
	}
	cleaned := path.Clean("/" + raw)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" {
		rel = "."
	}
	return rel, true
}

// isNonFile reports whether rel looks like an application route rather than an
// asset. The SPA fallback only applies to these paths.
func isNonFile(rel string) bool {
	return path.Ext(rel) == ""
}
