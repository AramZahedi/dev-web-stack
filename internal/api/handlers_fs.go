package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fsEntry is one browsable directory.
type fsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// fsListing is the response of GET /api/v1/fs. Files are never listed and file
// contents are never returned.
type fsListing struct {
	Path    string    `json:"path"`
	Entries []fsEntry `json:"entries"`
}

func (s *Server) handleFS(w http.ResponseWriter, r *http.Request) {
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	raw := strings.TrimSpace(r.URL.Query().Get("path"))

	if mode == "wsl" || (raw != "" && strings.HasPrefix(raw, "/") && !filepath.IsAbs(raw)) {
		browser := s.wslFS
		if browser == nil {
			browser = s.cfg.WSLFS
		}
		if browser == nil {
			writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "WSL agent is not available", nil)
			return
		}
		listing, err := browser.BrowseFS(r.Context(), raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory browse failed: "+err.Error(), nil)
			return
		}
		writeJSON(w, http.StatusOK, listing)
		return
	}

	if raw == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path is required", nil)
		return
	}
	if strings.ContainsRune(raw, 0) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path contains a NUL byte", nil)
		return
	}
	if !filepath.IsAbs(raw) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must be absolute", nil)
		return
	}

	target := filepath.Clean(raw)
	info, err := os.Stat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, codeNotFound, "directory does not exist", nil)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory is not readable: "+err.Error(), nil)
		return
	case !info.IsDir():
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "path is not a directory", nil)
		return
	}

	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory cannot be resolved: "+err.Error(), nil)
		return
	}

	home := strings.TrimSpace(s.userHome())
	if home == "" {
		writeError(w, http.StatusInternalServerError, codeInternal, "user home is not configured", nil)
		return
	}
	base, err := filepath.Abs(home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "user home cannot be resolved", nil)
		return
	}
	if evaluated, err := filepath.EvalSymlinks(base); err == nil {
		base = evaluated
	}
	if !withinDirectory(base, resolved) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured user home", nil)
		return
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory is not readable: "+err.Error(), nil)
		return
	}

	out := make([]fsEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		out = append(out, fsEntry{
			Name: entry.Name(),
			Path: filepath.Join(resolved, entry.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, fsListing{Path: resolved, Entries: out})
}

// withinDirectory reports whether target is base or a descendant of base. Both
// paths must already be absolute (and, when possible, symlink-resolved).
func withinDirectory(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
