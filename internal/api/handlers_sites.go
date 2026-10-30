package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// siteNodeToolchainResolver is the narrow optional surface used to attach the
// response-only Node summary. The site service implements it; lightweight test
// doubles may not, in which case the site's existing summary is left as-is.
type siteNodeToolchainResolver interface {
	ResolveNodeToolchain(context.Context, model.Site) (model.NodeToolchain, error)
}

// siteResponse attaches the Node toolchain summary when the service exposes the
// resolver and the service result did not already carry one.
func (s *Server) siteResponse(r *http.Request, site model.Site) model.Site {
	if site.NodeToolchain != nil {
		return site
	}
	resolver, ok := s.sites.(siteNodeToolchainResolver)
	if !ok {
		return site
	}
	summary, err := resolver.ResolveNodeToolchain(r.Context(), site)
	if err != nil && summary.Version == "" {
		return site
	}
	site.NodeToolchain = &summary
	return site
}

// createSiteRequest is the dashboard/CLI create payload. domain is derived
// from name by the site service; host_path is accepted as an alias for path so
// the shape in the M1 design appendix also works.
type createSiteRequest struct {
	Name                string                         `json:"name"`
	Path                string                         `json:"path"`
	HostPath            string                         `json:"host_path"`
	Kind                string                         `json:"kind"`
	SourceMode          string                         `json:"source_mode"`
	NodeVersion         string                         `json:"node_version"`
	Command             string                         `json:"command"`
	DocumentRoot        string                         `json:"document_root"`
	WWW                 bool                           `json:"www"`
	TLS                 bool                           `json:"tls"`
	ForceHTTPS          bool                           `json:"force_https"`
	SPAFallback         bool                           `json:"spa_fallback"`
	SPAFallbackFile     string                         `json:"spa_fallback_file"`
	Env                 map[string]string              `json:"env"`
	PHPVersion          string                         `json:"php_version"`
	PHPBuild            string                         `json:"php_build"`
	PHPExtensions       map[string]bool                `json:"php_extensions"`
	PHPIni              map[string]string              `json:"php_ini"`
	PHPIniRaw           string                         `json:"php_ini_raw"`
	FrontController     bool                           `json:"front_controller"`
	FrontControllerFile string                         `json:"front_controller_file"`
	FrontControllerMode string                         `json:"front_controller_mode"`
	ApacheAllowOverride string                         `json:"apache_allow_override"`
	Build               *model.BuildProfile            `json:"build"`
	Database            *databases.SiteDatabaseRequest `json:"database,omitempty"`
}

type changeSiteSourceRequest struct {
	Mode         string `json:"mode"`
	Path         string `json:"path"`
	RemoveSource bool   `json:"remove_source"`
}

func (s *Server) handleChangeSiteSourceMode(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req changeSiteSourceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Mode) == "" || strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "mode and path are required", nil)
		return
	}
	site, err := s.sites.ChangeSourceMode(r.Context(), r.PathValue("ref"), model.SourceMode(strings.TrimSpace(req.Mode)), strings.TrimSpace(req.Path), req.RemoveSource)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.siteResponse(r, site))
}

// updateSiteRequest is a partial update. Pointer fields distinguish an omitted
// value from an explicit false; a non-nil Env replaces the environment and an
// empty object clears it. PHP version, extensions, and ini changes use
// PUT /sites/{ref}/php instead.
type updateSiteRequest struct {
	Domain              string              `json:"domain"`
	WWW                 *bool               `json:"www"`
	TLS                 *bool               `json:"tls"`
	ForceHTTPS          *bool               `json:"force_https"`
	SPAFallback         *bool               `json:"spa_fallback"`
	SPAFallbackFile     string              `json:"spa_fallback_file"`
	FrontController     *bool               `json:"front_controller"`
	FrontControllerFile string              `json:"front_controller_file"`
	FrontControllerMode string              `json:"front_controller_mode"`
	ApacheAllowOverride string              `json:"apache_allow_override"`
	Build               *model.BuildProfile `json:"build"`
	Command             string              `json:"command"`
	DocumentRoot        string              `json:"document_root"`
	Env                 *map[string]string  `json:"env"`
	// NodeVersion is raw so an omitted key stays nil while an explicit null
	// clears the pin. A JSON string sets or normalizes it.
	NodeVersion json.RawMessage `json:"node_version"`
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	list, err := s.sites.List(r.Context())
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []model.Site{}
	}
	for i := range list {
		list[i] = s.siteResponse(r, list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req createSiteRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		req.Path = req.HostPath
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Path) == "" || strings.TrimSpace(req.Kind) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "name, path, and kind are required", nil)
		return
	}

	if err := model.ValidateSiteContracts(req.FrontControllerFile, req.FrontControllerMode, req.ApacheAllowOverride, req.SPAFallbackFile, req.Build); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}

	site, err := s.sites.Create(r.Context(), sites.CreateRequest{
		Name:                req.Name,
		Path:                req.Path,
		Kind:                req.Kind,
		SourceMode:          req.SourceMode,
		NodeVersion:         req.NodeVersion,
		Command:             req.Command,
		DocumentRoot:        req.DocumentRoot,
		WWW:                 req.WWW,
		TLS:                 req.TLS,
		ForceHTTPS:          req.ForceHTTPS,
		SPAFallback:         req.SPAFallback,
		Env:                 req.Env,
		PHPVersion:          req.PHPVersion,
		PHPBuild:            req.PHPBuild,
		PHPExtensions:       req.PHPExtensions,
		PHPIni:              req.PHPIni,
		PHPIniRaw:           req.PHPIniRaw,
		FrontController:     req.FrontController,
		FrontControllerFile: req.FrontControllerFile,
		FrontControllerMode: req.FrontControllerMode,
		ApacheAllowOverride: req.ApacheAllowOverride,
		SPAFallbackFile:     req.SPAFallbackFile,
		Build:               req.Build,
		Database:            req.Database,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.siteResponse(r, site))
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	site, err := s.sites.Get(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.siteResponse(r, site))
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	var req updateSiteRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := model.ValidateSiteContracts(req.FrontControllerFile, req.FrontControllerMode, req.ApacheAllowOverride, req.SPAFallbackFile, req.Build); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}
	update := sites.UpdateRequest{
		Domain:              req.Domain,
		WWW:                 req.WWW,
		TLS:                 req.TLS,
		ForceHTTPS:          req.ForceHTTPS,
		SPAFallback:         req.SPAFallback,
		SPAFallbackFile:     req.SPAFallbackFile,
		FrontController:     req.FrontController,
		FrontControllerFile: req.FrontControllerFile,
		FrontControllerMode: req.FrontControllerMode,
		ApacheAllowOverride: req.ApacheAllowOverride,
		Build:               req.Build,
		Command:             req.Command,
		DocumentRoot:        req.DocumentRoot,
	}
	if req.Env != nil {
		update.Env = *req.Env
	}
	if len(req.NodeVersion) != 0 {
		// json.Unmarshal maps null (and "") to the empty string, which the
		// service treats as an explicit clear for static and PHP sites.
		var pin string
		if err := json.Unmarshal(req.NodeVersion, &pin); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "node_version must be a string or null", nil)
			return
		}
		update.NodeVersion = &pin
	}
	site, err := s.sites.Update(r.Context(), r.PathValue("ref"), update)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.siteResponse(r, site))
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	if err := s.sites.Delete(r.Context(), r.PathValue("ref")); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStartSite(w http.ResponseWriter, r *http.Request) {
	s.handleSiteAction(w, r, "start")
}

func (s *Server) handleStopSite(w http.ResponseWriter, r *http.Request) {
	s.handleSiteAction(w, r, "stop")
}

func (s *Server) handleRestartSite(w http.ResponseWriter, r *http.Request) {
	s.handleSiteAction(w, r, "restart")
}

// handleSiteAction runs one lifecycle action and returns 202. The action is
// asynchronous from the client's perspective even though the service call is
// synchronous.
func (s *Server) handleSiteAction(w http.ResponseWriter, r *http.Request, action string) {
	if s.sites == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "site service is not available", nil)
		return
	}
	ref := r.PathValue("ref")
	var err error
	switch action {
	case "start":
		err = s.sites.Start(r.Context(), ref)
	case "stop":
		err = s.sites.Stop(r.Context(), ref)
	case "restart":
		err = s.sites.Restart(r.Context(), ref)
	default:
		err = nil
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
