package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
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
		if raw != "" {
			if strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") || hasParentPathSegment(raw) {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory path must be absolute and must not contain '..' segments", nil)
				return
			}
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
		if s.cfg.ProjectsRoot != "" {
			raw = s.cfg.ProjectsRoot
		} else if s.cfg.UserHome != "" {
			raw = filepath.Join(s.cfg.UserHome, "projects")
		} else {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path is required", nil)
			return
		}
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
		if raw != "" {
			if strings.ContainsRune(raw, 0) || !strings.HasPrefix(raw, "/") || hasParentPathSegment(raw) {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, "WSL directory path must be absolute and must not contain '..' segments", nil)
				return
			}
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
		if s.cfg.ProjectsRoot != "" {
			parent = s.cfg.ProjectsRoot
		} else if s.cfg.UserHome != "" {
			parent = filepath.Join(s.cfg.UserHome, "projects")
		} else {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path is required", nil)
			return
		}
	}
	if strings.ContainsRune(parent, 0) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path contains a NUL byte", nil)
		return
	}
	if !filepath.IsAbs(parent) {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "directory path must be absolute", nil)
		return
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Clean(parent))
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
	if !withinDirectory(base, resolvedParent) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured user home", nil)
		return
	}
	target := filepath.Join(resolvedParent, name)
	if !withinDirectory(base, target) {
		writeError(w, http.StatusForbidden, codeForbidden, "directory is outside the configured user home", nil)
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

func hasParentPathSegment(p string) bool {
	for _, segment := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}
