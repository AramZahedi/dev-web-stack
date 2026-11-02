package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
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
		if raw == "" {
			raw = s.wslProjectsRoot()
		}
		if strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") || hasParentPathSegment(raw) {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory path must be absolute and must not contain '..' segments", nil)
			return
		}
		if !pathmode.ContainsPath(s.wslProjectsRoot(), raw, false) {
			writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the managed WSL projects root", nil)
			return
		}
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
		raw = s.projectsRoot()
		if raw == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path is required", nil)
			return
		}
	}
	if strings.ContainsRune(raw, 0) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path contains a NUL byte", nil)
		return
	}
	if hasParentPathSegment(raw) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must not contain '..' segments", nil)
		return
	}
	if !filepath.IsAbs(raw) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must be absolute", nil)
		return
	}

	target := filepath.Clean(raw)
	baseLexical, baseResolved, err := s.projectsRootPaths()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "projects root cannot be resolved", nil)
		return
	}
	if !withinDirectory(baseLexical, target) && !withinDirectory(baseResolved, target) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured projects root", nil)
		return
	}

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

	if !withinDirectory(baseResolved, resolved) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured projects root", nil)
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

type fsMkdirRequest struct {
	Mode string `json:"mode"`
	Path string `json:"path"`
	Name string `json:"name"`
}

func (s *Server) handleFSMkdir(w http.ResponseWriter, r *http.Request) {
	var req fsMkdirRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "invalid folder request", nil)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	raw := strings.TrimSpace(req.Path)
	windowsNames := mode == "windows" || (mode == "" && raw != "" && filepath.VolumeName(raw) != "")
	name, err := validateFolderName(req.Name, windowsNames)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}

	if mode == "wsl" || (mode == "" && raw != "" && strings.HasPrefix(raw, "/") && !filepath.IsAbs(raw)) {
		if raw == "" {
			raw = s.wslProjectsRoot()
		}
		if strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") || hasParentPathSegment(raw) {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory path must be absolute and must not contain '..' segments", nil)
			return
		}
		if !pathmode.ContainsPath(s.wslProjectsRoot(), raw, false) {
			writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the managed WSL projects root", nil)
			return
		}
		browser := s.wslFS
		if browser == nil {
			browser = s.cfg.WSLFS
		}
		if browser == nil {
			writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "WSL agent is not available", nil)
			return
		}
		listing, err := browser.MkdirFS(r.Context(), raw, name)
		if err != nil {
			if errors.Is(err, agentproto.ErrConflict) {
				writeError(w, http.StatusConflict, codeConflict, "directory already exists", nil)
				return
			}
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory create failed: "+err.Error(), nil)
			return
		}
		writeJSON(w, http.StatusOK, listing)
		return
	}

	parent := raw
	if parent == "" {
		parent = s.projectsRoot()
		if parent == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path is required", nil)
			return
		}
	}
	if strings.ContainsRune(parent, 0) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path contains a NUL byte", nil)
		return
	}
	if hasParentPathSegment(parent) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must not contain '..' segments", nil)
		return
	}
	if !filepath.IsAbs(parent) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must be absolute", nil)
		return
	}
	parent = filepath.Clean(parent)
	baseLexical, baseResolved, err := s.projectsRootPaths()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "projects root cannot be resolved", nil)
		return
	}
	if !withinDirectory(baseLexical, parent) && !withinDirectory(baseResolved, parent) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured projects root", nil)
		return
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, codeNotFound, "directory does not exist", nil)
			return
		}
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory cannot be resolved: "+err.Error(), nil)
		return
	}
	info, err := os.Stat(resolvedParent)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "path is not a directory", nil)
		return
	}
	if !withinDirectory(baseResolved, resolvedParent) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured projects root", nil)
		return
	}
	target := filepath.Join(resolvedParent, name)
	if !withinDirectory(baseResolved, target) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured projects root", nil)
		return
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeError(w, http.StatusConflict, codeConflict, "directory already exists", nil)
			return
		}
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory cannot be created: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, fsListing{Path: target, Entries: []fsEntry{}})
}

func validateFolderName(raw string, windows bool) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("folder name is required")
	}
	if name == "." || name == ".." {
		return "", errors.New("folder name is not allowed")
	}
	if strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", errors.New("folder name must be a single path segment")
	}
	if strings.HasPrefix(name, ".") {
		return "", errors.New("folder name must not start with a dot")
	}
	if len(name) > 128 {
		return "", errors.New("folder name is too long")
	}
	if windows {
		trimmed := strings.TrimRight(name, ". ")
		if trimmed != name {
			return "", errors.New("folder name must not end with a dot or space")
		}
		switch strings.ToUpper(name) {
		case "CON", "PRN", "AUX", "NUL":
			return "", errors.New("folder name is reserved on Windows")
		}
		if len(name) == 4 && (strings.HasPrefix(strings.ToUpper(name), "COM") || strings.HasPrefix(strings.ToUpper(name), "LPT")) {
			if name[3] >= '1' && name[3] <= '9' {
				return "", errors.New("folder name is reserved on Windows")
			}
		}
	}
	return name, nil
}

// withinDirectory reports whether target is base or a descendant of base. Both
// paths must already be absolute (and, when possible, symlink-resolved). The
// comparison is component-aware and case-insensitive on Windows.
func withinDirectory(base, target string) bool {
	return pathmode.ContainsPath(base, target, runtime.GOOS == "windows")
}

// projectsRootPaths returns the absolute projects root and its symlink-resolved
// form. The lexical form rejects outside paths before touching the filesystem;
// the resolved form catches a symlink or reparse point that escapes the root.
// A root that cannot be resolved yet (for example, one that has not been
// created) keeps its lexical form.
func (s *Server) projectsRootPaths() (lexical, resolved string, err error) {
	root := s.projectsRoot()
	if root == "" {
		return "", "", errors.New("projects root is not configured")
	}
	lexical, err = filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	lexical = filepath.Clean(lexical)
	resolved = lexical
	if evaluated, err := filepath.EvalSymlinks(lexical); err == nil {
		resolved = evaluated
	}
	return lexical, resolved, nil
}

func hasParentPathSegment(p string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}
