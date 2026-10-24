package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// phpSettingsRequest is the transport shape for PUT /sites/{ref}/php. The
// service's PHPChange stays transport-neutral: nil maps and a nil ini_raw keep
// the current overrides, while present values replace them.
type phpSettingsRequest struct {
	Version    string            `json:"version"`
	Build      string            `json:"build"`
	Extensions map[string]bool   `json:"extensions"`
	Ini        map[string]string `json:"ini"`
	IniRaw     *string           `json:"ini_raw"`
}

// phpExecRequest carries the argv forwarded to the site's PHP binary.
type phpExecRequest struct {
	Argv []string `json:"argv"`
}

// phpExecResponse is the transport shape for POST /sites/{ref}/php/exec.
type phpExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// phpMigrationRequest is the transport shape for POST /php/migrate.
type phpMigrationRequest struct {
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	ToBuild     string `json:"to_build"`
}

// phpDefaultsRequest is the editable part of model.PHPDefaults. The version is
// taken from the path so a body cannot retarget another line.
type phpDefaultsRequest struct {
	Ini        map[string]string `json:"ini"`
	Extensions map[string]bool   `json:"extensions"`
}

// handlePHPInfo returns the effective PHP configuration of one site.
func (s *Server) handlePHPInfo(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	info, err := s.sites.PHPInfo(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleApplyPHP merges and applies one PHP configuration change.
func (s *Server) handleApplyPHP(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req phpSettingsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	site, err := s.sites.ApplyPHP(r.Context(), r.PathValue("ref"), sites.PHPChange{
		Version:    req.Version,
		Build:      req.Build,
		Extensions: req.Extensions,
		Ini:        req.Ini,
		IniRaw:     req.IniRaw,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// handlePHPExec runs the site's PHP binary with the generated php.ini.
func (s *Server) handlePHPExec(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req phpExecRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.sites.PHPExec(r.Context(), r.PathValue("ref"), req.Argv)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, phpExecResponse{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	})
}

// handlePHPExtensions returns the manifest extensions available on one PHP
// line, sorted by name.
func (s *Server) handlePHPExtensions(w http.ResponseWriter, r *http.Request) {
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if version == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "version is required (for example 8.4)", nil)
		return
	}
	manifest, err := catalog.LoadPHP()
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if _, ok := manifest.Versions[version]; !ok {
		writeError(w, http.StatusBadRequest, codeInvalidRequest,
			"unsupported PHP version \""+version+"\" (want 8.2, 8.3, 8.4, or 8.5)", nil)
		return
	}
	builtins := manifest.BuiltinsFor(version)
	builtinSet := make(map[string]bool, len(builtins))
	for _, name := range builtins {
		builtinSet[name] = true
	}
	extensions := make([]catalog.Extension, 0, len(manifest.Extensions))
	for _, extension := range manifest.Extensions {
		if builtinSet[extension.Name] {
			continue
		}
		for _, supported := range extension.Versions {
			if supported == version {
				extensions = append(extensions, extension)
				break
			}
		}
	}
	sort.Slice(extensions, func(i, j int) bool { return extensions[i].Name < extensions[j].Name })
	writeJSON(w, http.StatusOK, extensions)
}

// handlePHPDefaults returns the editable defaults for one PHP line.
func (s *Server) handlePHPDefaults(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	defaults, err := s.sites.PHPDefaults(r.Context(), r.PathValue("version"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, defaults)
}

// handleSavePHPDefaults saves the defaults for the path's PHP line.
func (s *Server) handleSavePHPDefaults(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req phpDefaultsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := s.sites.SavePHPDefaults(r.Context(), model.PHPDefaults{
		Version:    r.PathValue("version"),
		Ini:        req.Ini,
		Extensions: req.Extensions,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleMigratePHP moves every site on a PHP line to a target build.
func (s *Server) handleMigratePHP(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req phpMigrationRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	results, err := s.sites.MigratePHP(r.Context(), sites.PHPMigrationRequest{
		FromVersion: req.FromVersion,
		ToVersion:   req.ToVersion,
		ToBuild:     req.ToBuild,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if results == nil {
		results = []sites.PHPMigrationResult{}
	}
	writeJSON(w, http.StatusOK, results)
}
