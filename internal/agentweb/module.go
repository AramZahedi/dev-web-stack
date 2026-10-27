package agentweb

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// Config binds the module to the agent's WSL root and projects directory.
type Config struct {
	Root         string
	ProjectsRoot string
}

// Module handles the agent's private web-server configuration and WSL leaf
// copy operations.
type Module struct {
	root         string
	projectsRoot string
	mu           sync.RWMutex
	targets      []agentproto.WebTarget
}

var _ agentapi.Module = (*Module)(nil)

// New constructs the private web module.
func New(cfg Config) (*Module, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = "/data/devstack"
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("agentweb: root must be absolute")
	}
	root = filepath.Clean(root)
	projects := strings.TrimSpace(cfg.ProjectsRoot)
	if projects == "" {
		projects = filepath.Join(root, "projects")
	}
	if !filepath.IsAbs(projects) || filepath.Clean(projects) != projects {
		return nil, errors.New("agentweb: projects root must be a clean absolute WSL path")
	}
	return &Module{root: root, projectsRoot: projects}, nil
}

// Name implements agentapi.Module.
func (*Module) Name() string { return "private-web" }

// MountPath implements agentapi.Module.
func (*Module) MountPath() string { return "/v1/web/" }

// Root returns the configured WSL root.
func (m *Module) Root() string { return m.root }

// Handler serves the authenticated private web endpoints.
func (m *Module) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/web/targets", m.handleTargets)
	mux.HandleFunc("POST /v1/web/sync", m.handleSync)
	mux.HandleFunc("POST /v1/web/certs/copy", m.handleCopyCertificate)
	return mux
}
