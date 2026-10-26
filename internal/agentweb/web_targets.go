// Package agentweb implements private, HTTP-only web configuration and leaf
// certificate copies in the WSL agent. Public TLS remains the daemon's job.
package agentweb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

const PrivateHTTPPort = 18080

var ownerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

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

func (*Module) Name() string      { return "private-web" }
func (*Module) MountPath() string { return "/v1/web/" }

func (m *Module) Root() string { return m.root }

func (m *Module) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/web/targets", m.handleTargets)
	mux.HandleFunc("POST /v1/web/sync", m.handleSync)
	mux.HandleFunc("POST /v1/web/certs/copy", m.handleCopyCertificate)
	return mux
}

func (m *Module) ValidateTarget(target agentproto.WebTarget) error {
	if !ownerPattern.MatchString(target.SiteID) || target.SiteID == "__dashboard__" {
		return errors.New("agentweb: invalid site ID")
	}
	if err := domain.ValidateSiteDomain(target.Domain); err != nil {
		return fmt.Errorf("agentweb: invalid site domain: %w", err)
	}
	if target.Domain == "devstack.test" {
		return errors.New("agentweb: dashboard remains a daemon route, not a container vhost")
	}
	if target.TLS {
		return errors.New("agentweb: private web targets are HTTP-only; public TLS terminates in the daemon")
	}
	switch target.Kind {
	case "node":
		if target.Address != "127.0.0.1" || target.Port < 41000 || target.Port > 42999 {
			return errors.New("agentweb: node target must use WSL loopback port 41000-42999")
		}
	case "php":
		if !m.validProjectPath(target.Address) || target.Port < 43000 || target.Port > 43999 {
			return errors.New("agentweb: PHP target requires a project path and FastCGI port 43000-43999")
		}
	case "static":
		if !m.validProjectPath(target.Address) || target.Port != 0 {
			return errors.New("agentweb: static target requires a project document root and no backend port")
		}
	default:
		return fmt.Errorf("agentweb: unsupported site kind %q", target.Kind)
	}
	return nil
}

func (m *Module) validProjectPath(root string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}
	projects, err := filepath.EvalSymlinks(m.projectsRoot)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return false
	}
	rel, err := filepath.Rel(projects, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Sync validates and installs one generated HTTP-only vhost for each site in
// the fixed nginx/Apache service trees. It never creates a dashboard vhost.
func (m *Module) Sync(ctx context.Context, request agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error) {
	if err := ctx.Err(); err != nil {
		return agentproto.WebSyncResponse{}, err
	}
	targets := append([]agentproto.WebTarget(nil), request.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].SiteID < targets[j].SiteID })
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if err := m.ValidateTarget(target); err != nil {
			return agentproto.WebSyncResponse{}, err
		}
		if _, ok := seen[target.SiteID]; ok {
			return agentproto.WebSyncResponse{}, fmt.Errorf("agentweb: duplicate site ID %q", target.SiteID)
		}
		seen[target.SiteID] = struct{}{}
	}
	changed := 0
	for _, server := range []string{"nginx", "apache"} {
		if err := m.ensurePrivateBase(server); err != nil {
			return agentproto.WebSyncResponse{}, err
		}
		for _, target := range targets {
			body, err := m.RenderedVhost(server, target)
			if err != nil {
				return agentproto.WebSyncResponse{}, err
			}
			dest := filepath.Join(m.root, "services", server, "config", "conf.d", "generated", target.SiteID+".conf")
			if err := writeAtomic(dest, body, 0o644); err != nil {
				return agentproto.WebSyncResponse{}, fmt.Errorf("agentweb: write %s vhost for %s: %w", server, target.SiteID, err)
			}
			changed++
		}
	}
	m.mu.Lock()
	m.targets = targets
	m.mu.Unlock()
	return agentproto.WebSyncResponse{Changed: changed}, nil
}

func (m *Module) ensurePrivateBase(server string) error {
	webServer := dockerweb.Server(server)
	base, err := dockerweb.RenderBase(webServer, PrivateHTTPPort, PrivateHTTPPort+1)
	if err != nil {
		return fmt.Errorf("agentweb: render %s base config: %w", server, err)
	}
	name := "nginx.conf"
	if server == "apache" {
		name = "httpd.conf"
		base = []byte(strings.ReplaceAll(string(base), fmt.Sprintf("Listen %d\n", PrivateHTTPPort+1), ""))
	}
	configRoot := filepath.Join(m.root, "services", server, "config")
	for _, dir := range []string{
		filepath.Join(configRoot, "conf.d", "generated"),
		filepath.Join(configRoot, "conf.d", "custom"),
		filepath.Join(m.root, "services", server, "logs"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agentweb: create %s base directory: %w", server, err)
		}
	}
	return writeIfAbsent(filepath.Join(configRoot, name), base, 0o644)
}

func writeIfAbsent(name string, data []byte, mode os.FileMode) error {
	info, err := os.Lstat(name)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("agentweb: refusing to replace non-regular base config %s", name)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agentweb: inspect base config %s: %w", name, err)
	}
	return writeAtomic(name, data, mode)
}

// RenderedVhost returns one private HTTP vhost for focused tests and config
// adapters. The public dashboard is deliberately not part of this output.
func (m *Module) RenderedVhost(server string, target agentproto.WebTarget) ([]byte, error) {
	if err := m.ValidateTarget(target); err != nil {
		return nil, err
	}
	var b strings.Builder
	switch server {
	case "nginx":
		b.WriteString("server {\n    listen 18080;\n")
		fmt.Fprintf(&b, "    server_name %s;\n", target.Domain)
		switch target.Kind {
		case "node":
			fmt.Fprintf(&b, "    location / { proxy_pass http://127.0.0.1:%d; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto $scheme; }\n", target.Port)
		case "php":
			fmt.Fprintf(&b, "    root %s;\n    location ~ \\.php$ { fastcgi_pass 127.0.0.1:%d; fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name; include fastcgi_params; }\n", nginxProjectRoot(m.projectsRoot, target.Address), target.Port)
		case "static":
			fmt.Fprintf(&b, "    root %s;\n    location / { try_files $uri $uri/ =404; }\n", nginxProjectRoot(m.projectsRoot, target.Address))
		}
		b.WriteString("}\n")
	case "apache":
		b.WriteString("<VirtualHost *:18080>\n")
		fmt.Fprintf(&b, "    ServerName %s\n", target.Domain)
		switch target.Kind {
		case "node":
			fmt.Fprintf(&b, "    ProxyPass / http://127.0.0.1:%d/ upgrade=websocket\n    ProxyPassReverse / http://127.0.0.1:%d/\n", target.Port, target.Port)
		case "php":
			fmt.Fprintf(&b, "    DocumentRoot %q\n    <FilesMatch \"(?i)\\.php$\">\n        SetHandler \"proxy:fcgi://127.0.0.1:%d\"\n    </FilesMatch>\n", target.Address, target.Port)
		case "static":
			fmt.Fprintf(&b, "    DocumentRoot %q\n", target.Address)
		}
		b.WriteString("</VirtualHost>\n")
	default:
		return nil, fmt.Errorf("agentweb: unsupported web server %q", server)
	}
	return []byte(b.String()), nil
}

func nginxProjectRoot(projectsRoot, root string) string {
	rel, _ := filepath.Rel(projectsRoot, root)
	return filepath.ToSlash(filepath.Join("/sites", rel))
}

type certificateCopyRequest struct {
	Owner   string `json:"owner"`
	CertPEM []byte `json:"cert_pem"`
	KeyPEM  []byte `json:"key_pem"`
}

// CopyLeafCertificate installs only a leaf certificate/key pair under the
// private WSL TLS directory. CA certificates and mismatched keys are rejected.
func (m *Module) CopyLeafCertificate(ctx context.Context, owner string, certPEM, keyPEM []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ownerPattern.MatchString(owner) {
		return errors.New("agentweb: invalid certificate owner")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("agentweb: invalid leaf certificate pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return errors.New("agentweb: certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("agentweb: parse leaf certificate: %w", err)
	}
	if leaf.IsCA {
		return errors.New("agentweb: CA certificates and private keys never enter WSL")
	}
	dir := filepath.Join(m.root, "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agentweb: create TLS directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("agentweb: set TLS directory mode: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, owner+".crt"), certPEM, 0o600); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, owner+".key"), keyPEM, 0o600); err != nil {
		return err
	}
	return nil
}

func (m *Module) handleTargets(w http.ResponseWriter, r *http.Request) {
	var request agentproto.WebTargetsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	for _, target := range request.Targets {
		if err := m.ValidateTarget(target); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	m.mu.Lock()
	m.targets = append([]agentproto.WebTarget(nil), request.Targets...)
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func (m *Module) handleSync(w http.ResponseWriter, r *http.Request) {
	var request agentproto.WebTargetsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	response, err := m.Sync(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (m *Module) handleCopyCertificate(w http.ResponseWriter, r *http.Request) {
	var request certificateCopyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := m.CopyLeafCertificate(r.Context(), request.Owner, request.CertPEM, request.KeyPEM); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("agentweb: trailing JSON data")
		}
		return fmt.Errorf("agentweb: trailing JSON data: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, agentproto.ErrorResponse{Code: agentproto.CodeInvalidRequest, Message: err.Error()})
}

func writeAtomic(name string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("agentweb: create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "web-*.tmp")
	if err != nil {
		return fmt.Errorf("agentweb: create temporary file: %w", err)
	}
	temp := tmp.Name()
	defer os.Remove(temp)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentweb: write temporary file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentweb: set file mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentweb: sync file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentweb: close file: %w", err)
	}
	if err := os.Rename(temp, name); err != nil {
		return fmt.Errorf("agentweb: publish file: %w", err)
	}
	return nil
}
