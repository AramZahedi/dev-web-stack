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
	if target == "" || target == "/" {
		target = filepath.Join(s.config.Root, "projects")
		if _, err := os.Stat(target); err != nil {
			target = s.config.Root
		}
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
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

	entries, err := os.ReadDir(target)
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
			Path: filepath.ToSlash(filepath.Join(target, name)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentproto.FSListing{
		Path:    filepath.ToSlash(target),
		Entries: out,
	})
}
