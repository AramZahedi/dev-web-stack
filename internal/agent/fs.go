package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func (s *Server) handleFS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if strings.ContainsRune(target, 0) {
		http.Error(w, "path contains a NUL byte", http.StatusBadRequest)
		return
	}
	projectsRoot := filepath.Join(s.config.Root, "projects")
	if target == "" || target == "/" {
		target = projectsRoot
	}
	if !filepath.IsAbs(target) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	target = filepath.Clean(target)

	root, err := filepath.EvalSymlinks(projectsRoot)
	if err != nil {
		http.Error(w, "projects directory does not exist", http.StatusNotFound)
		return
	}
	if !pathInside(projectsRoot, target) {
		http.Error(w, "directory is outside the managed projects root", http.StatusForbidden)
		return
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "directory does not exist", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		http.Error(w, "directory is outside the managed projects root", http.StatusForbidden)
		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "directory does not exist", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !info.IsDir() {
		http.Error(w, "path is not a directory", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out := make([]agentproto.FSEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, agentproto.FSEntry{
			Name: name,
			Path: filepath.ToSlash(filepath.Join(resolved, name)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentproto.FSListing{
		Path:    filepath.ToSlash(resolved),
		Entries: out,
	})
}

func pathInside(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
