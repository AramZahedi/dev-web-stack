// Package dockerweb renders nginx and Apache configuration for sites served by
// devstack-managed web-server containers. Container paths use the /sites and
// /certs bind mounts while PHP FastCGI parameters keep the native host paths
// that PHP-FPM expects.
package dockerweb

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const (
	defaultCertsDir        = "/certs"
	defaultMaxBodyBytes    = int64(1 << 30)
	dashboardDomain        = "devstack.test"
	dashboardOwner         = "__dashboard__"
	defaultFrontController = "index.php"
	defaultSPAFallback     = "index.html"
	maxSlugLen             = 63
)

var (
	slugRegexp   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	siteIDRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// SiteSpec is one site vhost request. HostDocumentRoot is the absolute host
// path; the container path is derived by making it relative to Projects.
type SiteSpec struct {
	ID   string
	Name string
	// Domain is the primary host name. WWW adds a www. alias.
	Domain string
	WWW    bool

	Kind             model.SiteKind
	HostDocumentRoot string
	BackendPort      int

	TLS        bool
	ForceHTTPS bool

	FrontController     bool
	FrontControllerFile string
	FrontControllerMode string // auto | generated | htaccess
	ApacheAllowOverride string // all | fileinfo | none

	SPAFallback     bool
	SPAFallbackFile string

	DesiredState model.DesiredState
	// CertOwner selects the certificate file stem under CertsDir. It defaults
	// to the site ID and may be the dashboard owner for non-TLS sites whose
	// certificate could not be issued.
	CertOwner string
}

// DashboardSpec describes the mandatory devstack.test vhost.
type DashboardSpec struct {
	Domain string
	UIPort int
}

// Set is a complete render request.
type Set struct {
	Projects        string
	CertsDir        string
	Sites           []SiteSpec
	Dashboard       DashboardSpec
	MaxBodyBytes    int64
	HTTPListenPort  int
	HTTPSListenPort int
}

type listenPorts struct {
	http  int
	https int
}

// Render renders every vhost for one server. Keys are base names: one file per
// site, _devstack.test.conf, and _default.conf. Manager writes site and
// dashboard files under conf.d/generated and seeds _default.conf as a base file.
func Render(server Server, set Set) (map[string][]byte, error) {
	if err := server.Validate(); err != nil {
		return nil, err
	}
	projects, err := cleanAbsolutePath("projects root", set.Projects)
	if err != nil {
		return nil, err
	}
	certsDir := set.CertsDir
	if strings.TrimSpace(certsDir) == "" {
		certsDir = defaultCertsDir
	}
	certsDir, err = cleanAbsolutePath("certificate directory", certsDir)
	if err != nil {
		return nil, err
	}
	maxBody := set.MaxBodyBytes
	if maxBody < 0 {
		return nil, errors.New("dockerweb: max body bytes must not be negative")
	}
	if maxBody == 0 {
		maxBody = defaultMaxBodyBytes
	}
	ports, err := resolvePorts(set.HTTPListenPort, set.HTTPSListenPort)
	if err != nil {
		return nil, err
	}
	if err := validateDashboard(set.Dashboard); err != nil {
		return nil, err
	}

	files := make(map[string][]byte, len(set.Sites)+2)
	seen := make(map[string]bool, len(set.Sites))
	for _, site := range set.Sites {
		if err := validateSite(site, projects); err != nil {
			return nil, err
		}
		if seen[site.ID] {
			return nil, fmt.Errorf("dockerweb: duplicate site ID %q", site.ID)
		}
		seen[site.ID] = true
		containerRoot, err := containerRoot(projects, site)
		if err != nil {
			return nil, err
		}
		body, err := renderSite(server, site, containerRoot, certsDir, maxBody, ports)
		if err != nil {
			return nil, err
		}
		files[site.ID+".conf"] = generatedFile(server, site.ID, body)
	}

	dashboardBody := renderDashboard(server, set.Dashboard, certsDir, maxBody, ports)
	files["_devstack.test.conf"] = generatedFile(server, "dashboard", dashboardBody)
	defaultBody := renderDefault404(server, certsDir, ports)
	files["_default.conf"] = generatedFile(server, "default", defaultBody)
	return files, nil
}

func generatedFile(server Server, siteID string, body []byte) []byte {
	sum := sha256.Sum256(body)
	header := fmt.Sprintf("# devstack dockerweb server=%s site=%s sha256=%x\n\n", server, siteID, sum)
	return append([]byte(header), body...)
}

func resolvePorts(httpPort, httpsPort int) (listenPorts, error) {
	if httpPort == 0 {
		httpPort = defaultHTTPPort
	}
	if httpsPort == 0 {
		httpsPort = defaultHTTPSPort
	}
	if err := validateListenPorts(httpPort, httpsPort); err != nil {
		return listenPorts{}, err
	}
	return listenPorts{http: httpPort, https: httpsPort}, nil
}

func validateDashboard(d DashboardSpec) error {
	if strings.TrimSpace(d.Domain) == "" {
		return errors.New("dockerweb: dashboard domain is empty")
	}
	if err := domain.ValidateDomain(d.Domain); err != nil {
		return fmt.Errorf("dockerweb: dashboard: %w", err)
	}
	if d.Domain != dashboardDomain {
		return fmt.Errorf("dockerweb: dashboard domain %q must be %q", d.Domain, dashboardDomain)
	}
	if d.UIPort < 1 || d.UIPort > 65535 {
		return fmt.Errorf("dockerweb: dashboard UI port %d is out of range", d.UIPort)
	}
	return nil
}

func validateSite(s SiteSpec, projects string) error {
	if !siteIDRegexp.MatchString(s.ID) || len(s.ID) > maxSlugLen {
		return fmt.Errorf("dockerweb: invalid site ID %q", s.ID)
	}
	if !slugRegexp.MatchString(s.Name) || len(s.Name) > maxSlugLen {
		return fmt.Errorf("dockerweb: invalid site name %q", s.Name)
	}
	if err := domain.ValidateSiteDomain(s.Domain); err != nil {
		return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}
	switch s.DesiredState {
	case model.DesiredRunning, model.DesiredStopped:
	default:
		return fmt.Errorf("dockerweb: site %q: invalid desired state %q", s.ID, s.DesiredState)
	}
	switch s.Kind {
	case model.SiteStatic, model.SitePHP, model.SiteNode:
	default:
		return fmt.Errorf("dockerweb: site %q: unsupported kind %q", s.ID, s.Kind)
	}
	if s.FrontController && s.Kind != model.SitePHP {
		return fmt.Errorf("dockerweb: site %q: front controller is only supported for PHP sites", s.ID)
	}
	if s.SPAFallback && s.Kind != model.SiteStatic {
		return fmt.Errorf("dockerweb: site %q: SPA fallback is only supported for static sites", s.ID)
	}

	mode, err := frontControllerMode(s.FrontControllerMode)
	if err != nil {
		return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}
	if mode == "htaccess" && s.ApacheAllowOverride == "none" {
		return fmt.Errorf("dockerweb: site %q: front_controller_mode=htaccess requires an Apache override policy other than none", s.ID)
	}
	if _, err := allowOverrideValue(s.ApacheAllowOverride); err != nil {
		return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}

	if s.Kind == model.SiteNode {
		if s.DesiredState == model.DesiredStopped && s.BackendPort == 0 {
			return nil
		}
		if s.BackendPort < 1 || s.BackendPort > 65535 {
			return fmt.Errorf("dockerweb: site %q: node backend port %d is out of range", s.ID, s.BackendPort)
		}
		return nil
	}

	if strings.TrimSpace(s.HostDocumentRoot) == "" {
		if s.DesiredState == model.DesiredStopped && s.Kind == model.SiteNode {
			return nil
		}
		return fmt.Errorf("dockerweb: site %q: document root is empty", s.ID)
	}
	root, err := cleanAbsolutePath("document root", s.HostDocumentRoot)
	if err != nil {
		return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}
	if _, err := containerRoot(projects, s); err != nil {
		return err
	}
	if s.DesiredState == model.DesiredStopped {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("dockerweb: site %q: document root: %w", s.ID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dockerweb: site %q: document root %q is not a directory", s.ID, root)
	}

	if s.Kind == model.SitePHP {
		if s.BackendPort < 1 || s.BackendPort > 65535 {
			return fmt.Errorf("dockerweb: site %q: PHP backend port %d is out of range", s.ID, s.BackendPort)
		}
		if s.FrontController {
			file, err := s.frontControllerFile()
			if err != nil {
				return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
			}
			if _, err := checkedFileInRoot(root, file, ".php", true); err != nil {
				return fmt.Errorf("dockerweb: site %q: front controller: %w", s.ID, err)
			}
		}
		if mode == "htaccess" {
			if _, err := checkedFileInRoot(root, ".htaccess", "", false); err != nil {
				return fmt.Errorf("dockerweb: site %q: front_controller_mode=htaccess requires a project .htaccess: %w", s.ID, err)
			}
		}
	}
	if s.Kind == model.SiteStatic && s.SPAFallback {
		file, err := s.spaFallbackFile()
		if err != nil {
			return fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
		}
		if _, err := checkedFileInRoot(root, file, "", true); err != nil {
			return fmt.Errorf("dockerweb: site %q: SPA fallback: %w", s.ID, err)
		}
	}
	return nil
}

func (s SiteSpec) frontControllerFile() (string, error) {
	file := strings.TrimSpace(s.FrontControllerFile)
	if file == "" {
		file = defaultFrontController
	}
	return cleanRelativeFile("front controller file", file)
}

func (s SiteSpec) spaFallbackFile() (string, error) {
	file := strings.TrimSpace(s.SPAFallbackFile)
	if file == "" {
		file = defaultSPAFallback
	}
	return cleanRelativeFile("SPA fallback file", file)
}

func (s SiteSpec) certOwner() string {
	if strings.TrimSpace(s.CertOwner) != "" {
		return s.CertOwner
	}
	return s.ID
}

func (s SiteSpec) certificatePaths(certsDir string) (string, string) {
	owner := s.certOwner()
	return filepath.Join(certsDir, owner+".crt"), filepath.Join(certsDir, owner+".key")
}

func frontControllerMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", nil
	case "generated":
		return "generated", nil
	case "htaccess":
		return "htaccess", nil
	default:
		return "", fmt.Errorf("invalid front controller mode %q", value)
	}
}

func allowOverrideValue(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "All", nil
	case "fileinfo":
		return "FileInfo", nil
	case "none":
		return "None", nil
	default:
		return "", fmt.Errorf("invalid Apache allow override policy %q", value)
	}
}

func cleanAbsolutePath(name, value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("dockerweb: %s contains NUL", name)
	}
	if strings.ContainsAny(value, "\n\r") {
		return "", fmt.Errorf("dockerweb: %s contains a newline", name)
	}
	if strings.ContainsRune(value, '$') {
		return "", fmt.Errorf("dockerweb: %s contains '$', which is a variable marker", name)
	}
	if strings.ContainsRune(value, '#') {
		return "", fmt.Errorf("dockerweb: %s contains '#', which cannot be represented in an Apache regex", name)
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("dockerweb: %s %q must be absolute", name, value)
	}
	clean := filepath.Clean(value)
	if clean != value {
		return "", fmt.Errorf("dockerweb: %s %q must be a clean path", name, value)
	}
	return clean, nil
}

func cleanRelativeFile(name, value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("dockerweb: %s contains NUL", name)
	}
	if strings.ContainsAny(value, "\n\r") {
		return "", fmt.Errorf("dockerweb: %s contains a newline", name)
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("dockerweb: %s %q must be relative to the document root", name, value)
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("dockerweb: %s %q escapes the document root", name, value)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("dockerweb: %s %q is not a clean relative path", name, value)
		}
		if strings.HasPrefix(part, ".") && part != ".htaccess" {
			return "", fmt.Errorf("dockerweb: %s %q must not be a dotfile", name, value)
		}
	}
	if strings.Contains(clean, `\`) {
		return "", fmt.Errorf("dockerweb: %s %q must use forward slashes", name, value)
	}
	return clean, nil
}

// checkedFileInRoot verifies that rel is a regular file under root and that no
// symlink component escapes root. requireSuffix, when non-empty, must match the
// file name's extension.
func checkedFileInRoot(root, rel, requireSuffix string, executableStatic bool) (string, error) {
	_ = executableStatic
	if requireSuffix != "" && !strings.HasSuffix(strings.ToLower(rel), requireSuffix) {
		return "", fmt.Errorf("%q must end in %s", rel, requireSuffix)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", rel)
	}
	inside, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", err
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q escapes the document root through a symlink", rel)
	}
	return resolved, nil
}

// containerRoot returns /sites/<relative-to-projects> for a site. Sites outside
// the managed projects root are a documented Wave 2 limitation.
func containerRoot(projects string, s SiteSpec) (string, error) {
	if s.Kind == model.SiteNode {
		return "", nil
	}
	root, err := cleanAbsolutePath("document root", s.HostDocumentRoot)
	if err != nil {
		return "", err
	}
	resolvedProjects := projects
	if resolved, err := filepath.EvalSymlinks(projects); err == nil {
		resolvedProjects = resolved
	}
	resolvedRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = resolved
	}
	rel, err := filepath.Rel(resolvedProjects, resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}
	if rel == "." {
		return "/sites", nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dockerweb: site %q: document root %q is outside the managed projects root %q; sites outside projects require explicit bind mounts and are a Wave 2 limitation", s.ID, resolvedRoot, resolvedProjects)
	}
	return "/sites/" + filepath.ToSlash(rel), nil
}

func renderSite(server Server, s SiteSpec, containerRoot, certsDir string, maxBody int64, ports listenPorts) ([]byte, error) {
	mode, err := frontControllerMode(s.FrontControllerMode)
	if err != nil {
		return nil, fmt.Errorf("dockerweb: site %q: %w", s.ID, err)
	}
	if mode == "htaccess" && server != ServerApache {
		return nil, fmt.Errorf("dockerweb: site %q: front_controller_mode=htaccess is only valid for Apache", s.ID)
	}
	switch server {
	case ServerNginx:
		return renderNginxSite(s, containerRoot, certsDir, maxBody, ports), nil
	case ServerApache:
		return renderApacheSite(s, containerRoot, certsDir, maxBody, ports)
	default:
		return nil, fmt.Errorf("dockerweb: unsupported server %q", server)
	}
}

// ---------------------------------------------------------------------------
// nginx templates
// ---------------------------------------------------------------------------

func renderNginxSite(s SiteSpec, containerRoot, certsDir string, maxBody int64, ports listenPorts) []byte {
	var b strings.Builder
	if s.DesiredState == model.DesiredStopped {
		writeNginxStoppedServer(&b, s, certsDir, ports, false)
		b.WriteString("\n")
		writeNginxStoppedServer(&b, s, certsDir, ports, true)
		return []byte(b.String())
	}

	switch {
	case s.TLS && s.ForceHTTPS:
		writeNginxRedirectServer(&b, s, certsDir, ports, false, "https")
		b.WriteString("\n")
		writeNginxServingServer(&b, s, containerRoot, certsDir, maxBody, ports, true)
	case s.TLS:
		writeNginxServingServer(&b, s, containerRoot, certsDir, maxBody, ports, false)
		b.WriteString("\n")
		writeNginxServingServer(&b, s, containerRoot, certsDir, maxBody, ports, true)
	default:
		writeNginxServingServer(&b, s, containerRoot, certsDir, maxBody, ports, false)
		b.WriteString("\n")
		writeNginxRedirectServer(&b, s, certsDir, ports, true, "http")
	}
	return []byte(b.String())
}

func writeNginxOpen(b *strings.Builder, s SiteSpec, certsDir string, ports listenPorts, tls bool) {
	port := ports.http
	if tls {
		port = ports.https
	}
	b.WriteString("server {\n")
	if tls {
		fmt.Fprintf(b, "    listen %d ssl;\n", port)
		b.WriteString("    http2 on;\n")
	} else {
		fmt.Fprintf(b, "    listen %d;\n", port)
	}
	names := s.Domain
	if s.WWW {
		names += " www." + s.Domain
	}
	fmt.Fprintf(b, "    server_name %s;\n", names)
	if tls {
		cert, key := s.certificatePaths(certsDir)
		fmt.Fprintf(b, "    ssl_certificate     %s;\n", nginxValue(cert))
		fmt.Fprintf(b, "    ssl_certificate_key %s;\n", nginxValue(key))
		b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
	}
}

func writeNginxRedirectServer(b *strings.Builder, s SiteSpec, certsDir string, ports listenPorts, tls bool, scheme string) {
	writeNginxOpen(b, s, certsDir, ports, tls)
	targetPort := ports.http
	if scheme == "https" {
		targetPort = ports.https
	}
	authority := "$host"
	defaultPort := defaultHTTPPort
	if scheme == "https" {
		defaultPort = defaultHTTPSPort
	}
	if targetPort != defaultPort {
		authority = fmt.Sprintf("$host:%d", targetPort)
	}
	fmt.Fprintf(b, "\n    return 308 %s://%s$request_uri;\n", scheme, authority)
	b.WriteString("}\n")
}

func writeNginxStoppedServer(b *strings.Builder, s SiteSpec, certsDir string, ports listenPorts, tls bool) {
	writeNginxOpen(b, s, certsDir, ports, tls)
	b.WriteString("    default_type text/html;\n\n")
	fmt.Fprintf(b, "    return 503 %s;\n", nginxQuotedValue(maintenanceHTML))
	b.WriteString("}\n")
}

func writeNginxServingServer(b *strings.Builder, s SiteSpec, containerRoot, certsDir string, maxBody int64, ports listenPorts, tls bool) {
	writeNginxOpen(b, s, certsDir, ports, tls)
	b.WriteString("\n")
	fmt.Fprintf(b, "    client_max_body_size %d;\n\n", maxBody)
	switch s.Kind {
	case model.SiteNode:
		writeNginxNodeBody(b, s)
	case model.SitePHP:
		writeNginxPHPBody(b, s, containerRoot)
	default:
		writeNginxStaticBody(b, s, containerRoot)
	}
	b.WriteString("}\n")
}

func writeNginxStaticBody(b *strings.Builder, s SiteSpec, containerRoot string) {
	fmt.Fprintf(b, "    root %s;\n", nginxValue(containerRoot))
	b.WriteString("    index index.html;\n")
	b.WriteString("    autoindex off;\n")
	b.WriteString("    add_header Cache-Control \"no-store\" always;\n\n")
	writeNginxDotfileDeny(b)
	b.WriteString("\n")
	writeNginxSourceBackupDeny(b)
	b.WriteString("\n")
	writeNginxDevstackDeny(b)
	b.WriteString("\n")
	b.WriteString("    location ~* \"\\.[A-Za-z0-9]{1,10}$\" {\n")
	b.WriteString("        try_files $uri =404;\n")
	b.WriteString("    }\n\n")
	b.WriteString("    location / {\n")
	if s.SPAFallback {
		fallback, _ := s.spaFallbackFile()
		fmt.Fprintf(b, "        try_files $uri $uri/ /%s;\n", filepath.ToSlash(fallback))
	} else {
		b.WriteString("        try_files $uri $uri/ =404;\n")
	}
	b.WriteString("    }\n")
}

func writeNginxNodeBody(b *strings.Builder, s SiteSpec) {
	b.WriteString("    error_page 502 503 504 =503 /_devstack/503.html;\n\n")
	b.WriteString("    location / {\n")
	fmt.Fprintf(b, "        proxy_pass http://127.0.0.1:%d;\n", s.BackendPort)
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
	b.WriteString("        proxy_set_header Connection \"upgrade\";\n")
	b.WriteString("        proxy_set_header Host $host;\n")
	b.WriteString("        proxy_set_header X-Forwarded-For $remote_addr;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Host $host;\n")
	b.WriteString("        proxy_hide_header Strict-Transport-Security;\n")
	b.WriteString("        proxy_read_timeout 120s;\n")
	b.WriteString("        proxy_intercept_errors on;\n")
	b.WriteString("    }\n\n")
	b.WriteString("    location = /_devstack/503.html {\n")
	b.WriteString("        internal;\n")
	b.WriteString("        default_type text/html;\n")
	fmt.Fprintf(b, "        return 200 %s;\n", nginxQuotedValue(maintenanceHTML))
	b.WriteString("    }\n\n")
	writeNginxSourceBackupDeny(b)
	b.WriteString("\n")
	writeNginxDenyLocations(b)
}

func writeNginxPHPBody(b *strings.Builder, s SiteSpec, containerRoot string) {
	frontFile, _ := s.frontControllerFile()
	hostRoot := filepath.Clean(s.HostDocumentRoot)
	fmt.Fprintf(b, "    root %s;\n", nginxValue(containerRoot))
	b.WriteString("    index index.php index.html;\n")
	b.WriteString("    autoindex off;\n\n")
	writeNginxDotfileDeny(b)
	b.WriteString("\n")
	writeNginxSourceBackupDeny(b)
	b.WriteString("\n")
	writeNginxDevstackDeny(b)
	b.WriteString("\n")
	b.WriteString("    location / {\n")
	if s.FrontController {
		b.WriteString("        try_files $uri $uri/ @devstack_front_controller;\n")
	} else {
		b.WriteString("        try_files $uri $uri/ =404;\n")
	}
	b.WriteString("    }\n\n")
	b.WriteString("    location ~* ^(.+\\.php)(/.*)?$ {\n")
	b.WriteString("        fastcgi_split_path_info ^(.+\\.php)(/.*)$;\n")
	b.WriteString("        set $devstack_path_info $fastcgi_path_info;\n")
	b.WriteString("        try_files $fastcgi_script_name =404;\n")
	fmt.Fprintf(b, "        fastcgi_pass 127.0.0.1:%d;\n", s.BackendPort)
	b.WriteString("        fastcgi_index index.php;\n")
	b.WriteString("        fastcgi_read_timeout 120s;\n")
	b.WriteString("        fastcgi_buffering off;\n")
	b.WriteString("        fastcgi_pass_request_headers on;\n")
	b.WriteString("        fastcgi_hide_header Strict-Transport-Security;\n")
	writeNginxFastCGIParams(b,
		nginxQuotedValue(hostRoot+"$fastcgi_script_name"),
		"$fastcgi_script_name",
		"$devstack_path_info",
		nginxQuotedValue(hostRoot),
	)
	b.WriteString("    }\n")
	if s.FrontController {
		b.WriteString("\n")
		b.WriteString("    location @devstack_front_controller {\n")
		fmt.Fprintf(b, "        fastcgi_pass 127.0.0.1:%d;\n", s.BackendPort)
		b.WriteString("        fastcgi_index index.php;\n")
		b.WriteString("        fastcgi_read_timeout 120s;\n")
		b.WriteString("        fastcgi_buffering off;\n")
		b.WriteString("        fastcgi_pass_request_headers on;\n")
		b.WriteString("        fastcgi_hide_header Strict-Transport-Security;\n")
		writeNginxFastCGIParams(b,
			nginxQuotedValue(filepath.Join(hostRoot, filepath.FromSlash(frontFile))),
			nginxQuotedValue("/"+filepath.ToSlash(frontFile)),
			"$uri",
			nginxQuotedValue(hostRoot),
		)
		b.WriteString("    }\n")
	}
}

func writeNginxFastCGIParams(b *strings.Builder, scriptFilename, scriptName, pathInfo, documentRoot string) {
	fmt.Fprintf(b, "        fastcgi_param SCRIPT_FILENAME %s;\n", scriptFilename)
	fmt.Fprintf(b, "        fastcgi_param SCRIPT_NAME %s;\n", scriptName)
	fmt.Fprintf(b, "        fastcgi_param PATH_INFO %s;\n", pathInfo)
	fmt.Fprintf(b, "        fastcgi_param DOCUMENT_ROOT %s;\n", documentRoot)
	b.WriteString("        fastcgi_param QUERY_STRING $query_string;\n")
	b.WriteString("        fastcgi_param REQUEST_METHOD $request_method;\n")
	b.WriteString("        fastcgi_param REQUEST_URI $request_uri;\n")
	b.WriteString("        fastcgi_param SERVER_PROTOCOL $server_protocol;\n")
	b.WriteString("        fastcgi_param GATEWAY_INTERFACE CGI/1.1;\n")
	b.WriteString("        fastcgi_param HTTPS $https if_not_empty;\n")
	b.WriteString("        fastcgi_param SERVER_SOFTWARE nginx/$nginx_version;\n")
	b.WriteString("        fastcgi_param SERVER_NAME $server_name;\n")
	b.WriteString("        fastcgi_param SERVER_ADDR $server_addr;\n")
	b.WriteString("        fastcgi_param SERVER_PORT $server_port;\n")
	b.WriteString("        fastcgi_param REMOTE_ADDR $remote_addr;\n")
	b.WriteString("        fastcgi_param REMOTE_PORT $remote_port;\n")
	b.WriteString("        fastcgi_param HTTP_X_FORWARDED_FOR $remote_addr;\n")
	b.WriteString("        fastcgi_param HTTP_X_FORWARDED_PROTO $scheme;\n")
	b.WriteString("        fastcgi_param HTTP_X_FORWARDED_HOST $host;\n")
	b.WriteString("        fastcgi_param HTTP_COOKIE $http_cookie;\n")
	b.WriteString("        fastcgi_param HTTP_AUTHORIZATION $http_authorization;\n")
	b.WriteString("        fastcgi_param HTTP_HOST $http_host;\n")
	b.WriteString("        fastcgi_param HTTP_USER_AGENT $http_user_agent;\n")
	b.WriteString("        fastcgi_param HTTP_ACCEPT $http_accept;\n")
	b.WriteString("        fastcgi_param CONTENT_TYPE $content_type;\n")
	b.WriteString("        fastcgi_param CONTENT_LENGTH $content_length;\n")
	b.WriteString("        fastcgi_param REDIRECT_STATUS 200;\n")
}

func writeNginxDenyLocations(b *strings.Builder) {
	writeNginxDotfileDeny(b)
	b.WriteString("\n")
	writeNginxDevstackDeny(b)
}

func writeNginxDotfileDeny(b *strings.Builder) {
	b.WriteString("    location ~ /\\. {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxSourceBackupDeny(b *strings.Builder) {
	fmt.Fprintf(b, "    location ~* \\.php(?:%s)$ {\n", phpSourceBackupPattern())
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func writeNginxDevstackDeny(b *strings.Builder) {
	b.WriteString("    location ^~ /_devstack {\n")
	b.WriteString("        deny all;\n")
	b.WriteString("    }\n")
}

func nginxValue(value string) string {
	if nginxBareSafe(value) {
		return value
	}
	return nginxQuotedValue(value)
}

func nginxQuotedValue(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

func nginxBareSafe(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("/._-:~+", rune(c)):
		default:
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Apache templates
// ---------------------------------------------------------------------------

func renderApacheSite(s SiteSpec, containerRoot, certsDir string, maxBody int64, ports listenPorts) ([]byte, error) {
	mode, err := frontControllerMode(s.FrontControllerMode)
	if err != nil {
		return nil, err
	}
	override, err := allowOverrideValue(s.ApacheAllowOverride)
	if err != nil {
		return nil, err
	}
	generatedFallback := s.FrontController && mode != "htaccess"
	if mode == "auto" && s.ApacheAllowOverride != "none" {
		if _, err := checkedFileInRoot(s.HostDocumentRoot, ".htaccess", "", false); err == nil {
			generatedFallback = false
		}
	}

	var b strings.Builder
	if s.DesiredState == model.DesiredStopped {
		writeApacheStoppedVHost(&b, s, false, certsDir, ports)
		b.WriteString("\n")
		writeApacheStoppedVHost(&b, s, true, certsDir, ports)
		return []byte(b.String()), nil
	}
	switch {
	case s.TLS && s.ForceHTTPS:
		writeApacheRedirectVHost(&b, s, false, "https", certsDir, ports)
		b.WriteString("\n")
		writeApacheServingVHost(&b, s, containerRoot, certsDir, maxBody, ports, true, override, generatedFallback)
	case s.TLS:
		writeApacheServingVHost(&b, s, containerRoot, certsDir, maxBody, ports, false, override, generatedFallback)
		b.WriteString("\n")
		writeApacheServingVHost(&b, s, containerRoot, certsDir, maxBody, ports, true, override, generatedFallback)
	default:
		writeApacheServingVHost(&b, s, containerRoot, certsDir, maxBody, ports, false, override, generatedFallback)
		b.WriteString("\n")
		writeApacheRedirectVHost(&b, s, true, "http", certsDir, ports)
	}
	return []byte(b.String()), nil
}

func apacheVHostPort(ports listenPorts, tls bool) int {
	if tls {
		return ports.https
	}
	return ports.http
}

func writeApacheVHostOpen(b *strings.Builder, s SiteSpec, ports listenPorts, tls bool) {
	fmt.Fprintf(b, "<VirtualHost *:%d>\n", apacheVHostPort(ports, tls))
	fmt.Fprintf(b, "    ServerName %s\n", s.Domain)
	if s.WWW {
		fmt.Fprintf(b, "    ServerAlias www.%s\n", s.Domain)
	}
}

func writeApacheTLS(b *strings.Builder, s SiteSpec, certsDir string) {
	cert, key := s.certificatePaths(certsDir)
	b.WriteString("    SSLEngine on\n")
	fmt.Fprintf(b, "    SSLCertificateFile %s\n", apacheQuote(cert))
	fmt.Fprintf(b, "    SSLCertificateKeyFile %s\n", apacheQuote(key))
	b.WriteString("    SSLProtocol -all +TLSv1.2 +TLSv1.3\n")
}

func writeApacheServingVHost(b *strings.Builder, s SiteSpec, containerRoot, certsDir string, maxBody int64, ports listenPorts, tls bool, override string, generatedFallback bool) {
	writeApacheVHostOpen(b, s, ports, tls)
	if tls {
		writeApacheTLS(b, s, certsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    LimitRequestBody %d\n\n", maxBody)
	switch s.Kind {
	case model.SiteNode:
		scheme := "http"
		if tls {
			scheme = "https"
		}
		writeApacheProxyConfig(b, s.BackendPort, scheme)
		b.WriteString("\n")
		fmt.Fprintf(b, "    ErrorDocument 503 %s\n\n", apacheQuote(maintenanceHTML))
		writeApacheFileDenies(b)
	case model.SitePHP:
		scheme := "http"
		if tls {
			scheme = "https"
		}
		writeApachePHPBody(b, s, containerRoot, scheme, override, generatedFallback)
	default:
		writeApacheStaticBody(b, s, containerRoot, override)
	}
	b.WriteString("</VirtualHost>\n")
}

func writeApacheStaticBody(b *strings.Builder, s SiteSpec, containerRoot, override string) {
	fmt.Fprintf(b, "    DocumentRoot %s\n", apacheQuote(containerRoot))
	fmt.Fprintf(b, "    <Directory %s>\n", apacheQuote(containerRoot))
	b.WriteString("        Options -Indexes\n")
	fmt.Fprintf(b, "        AllowOverride %s\n", override)
	b.WriteString("        Require all granted\n")
	if s.SPAFallback {
		fallback, _ := s.spaFallbackFile()
		b.WriteString("\n        RewriteEngine On\n")
		b.WriteString("        RewriteCond %{REQUEST_FILENAME} !-f\n")
		b.WriteString("        RewriteCond %{REQUEST_FILENAME} !-d\n")
		b.WriteString("        RewriteCond %{REQUEST_URI} !\\.[A-Za-z0-9]{1,10}$\n")
		fmt.Fprintf(b, "        RewriteRule ^ /%s [L]\n", filepath.ToSlash(fallback))
	}
	b.WriteString("    </Directory>\n\n")
	writeApacheFileDenies(b)
	b.WriteString("\n    Header always set Cache-Control \"no-store\"\n")
}

func writeApachePHPBody(b *strings.Builder, s SiteSpec, containerRoot, scheme, override string, generatedFallback bool) {
	hostRoot := filepath.Clean(s.HostDocumentRoot)
	frontFile, _ := s.frontControllerFile()
	fmt.Fprintf(b, "    DocumentRoot %s\n", apacheQuote(containerRoot))
	fmt.Fprintf(b, "    <Directory %s>\n", apacheQuote(containerRoot))
	b.WriteString("        Options -Indexes\n")
	fmt.Fprintf(b, "        AllowOverride %s\n", override)
	b.WriteString("        Require all granted\n")
	b.WriteString("        AcceptPathInfo On\n")
	if generatedFallback {
		fmt.Fprintf(b, "        FallbackResource /%s\n", filepath.ToSlash(frontFile))
	}
	b.WriteString("    </Directory>\n\n")
	b.WriteString("    DirectoryIndex index.php index.html\n")
	b.WriteString("    Header always unset Strict-Transport-Security\n\n")
	writeApacheForwardedHeaders(b, scheme)
	b.WriteString("\n")
	fmt.Fprintf(b, "    <FilesMatch \"(?i)\\.php(?:%s)$\">\n", phpSourceBackupPattern())
	b.WriteString("        Require all denied\n")
	b.WriteString("    </FilesMatch>\n\n")
	b.WriteString("    <FilesMatch \"(?i)\\.php$\">\n")
	fmt.Fprintf(b, "        SetHandler \"proxy:fcgi://127.0.0.1:%d\"\n", s.BackendPort)
	b.WriteString("    </FilesMatch>\n\n")
	writeApacheFileDenies(b)
	b.WriteString("\n")
	quotedRoot := regexp.QuoteMeta(containerRoot)
	hostValue := apacheQuote(hostRoot + "$1")
	// PATH_INFO must be captured before SCRIPT_FILENAME is rewritten. Apache
	// 2.4.69 prefixes SCRIPT_FILENAME with proxy:fcgi://host:port/.
	fmt.Fprintf(b, "    ProxyFCGISetEnvIf \"reqenv('SCRIPT_FILENAME') =~ m#^proxy:fcgi://[^/]+%s(/.*?\\.php)(/.*)$#\" PATH_INFO \"$2\"\n", quotedRoot)
	fmt.Fprintf(b, "    ProxyFCGISetEnvIf \"reqenv('SCRIPT_FILENAME') =~ m#^proxy:fcgi://[^/]+%s(/.*?\\.php)(?:/.*)?$#\" SCRIPT_FILENAME %s\n", quotedRoot, hostValue)
	fmt.Fprintf(b, "    ProxyFCGISetEnvIf \"reqenv('SCRIPT_FILENAME') =~ m#^%s(/.*?\\.php)(/.*)$#\" PATH_INFO \"$2\"\n", quotedRoot)
	fmt.Fprintf(b, "    ProxyFCGISetEnvIf \"reqenv('SCRIPT_FILENAME') =~ m#^%s(/.*?\\.php)(?:/.*)?$#\" SCRIPT_FILENAME %s\n", quotedRoot, hostValue)
}

func writeApacheForwardedHeaders(b *strings.Builder, scheme string) {
	b.WriteString("    RewriteEngine On\n")
	b.WriteString("    RewriteCond %{HTTP_HOST} ^(.+)$\n")
	b.WriteString("    RewriteRule ^ - [E=DEVSTACK_HOST:%1]\n")
	b.WriteString("    RewriteCond %{REMOTE_ADDR} ^(.+)$\n")
	b.WriteString("    RewriteRule ^ - [E=DEVSTACK_REMOTE:%1]\n")
	b.WriteString("    RequestHeader set X-Forwarded-For \"%{DEVSTACK_REMOTE}e\"\n")
	fmt.Fprintf(b, "    RequestHeader set X-Forwarded-Proto %s\n", apacheQuote(scheme))
	b.WriteString("    RequestHeader set X-Forwarded-Host \"%{DEVSTACK_HOST}e\"\n")
}

func writeApacheFileDenies(b *strings.Builder) {
	fmt.Fprintf(b, "    <FilesMatch \"(?i)\\.php(?:%s)$\">\n", phpSourceBackupPattern())
	b.WriteString("        Require all denied\n")
	b.WriteString("    </FilesMatch>\n\n")
	b.WriteString("    <FilesMatch \"^\\.\">\n")
	b.WriteString("        Require all denied\n")
	b.WriteString("    </FilesMatch>\n\n")
	b.WriteString("    <LocationMatch \"(^|/)\\.\">\n")
	b.WriteString("        Require all denied\n")
	b.WriteString("    </LocationMatch>\n\n")
	writeApacheDenyLocations(b)
}

func writeApacheDenyLocations(b *strings.Builder) {
	b.WriteString("    <LocationMatch \"^/_devstack(/|$)\">\n")
	b.WriteString("        Require all denied\n")
	b.WriteString("    </LocationMatch>\n")
}

func writeApacheProxyConfig(b *strings.Builder, port int, scheme string) {
	upstream := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	quoted := apacheQuote(upstream)
	b.WriteString("    ProxyPreserveHost On\n")
	b.WriteString("    ProxyAddHeaders Off\n")
	b.WriteString("    Header always unset Strict-Transport-Security\n")
	fmt.Fprintf(b, "    ProxyPass \"/\" %s upgrade=websocket connectiontimeout=5 timeout=120\n", quoted)
	fmt.Fprintf(b, "    ProxyPassReverse \"/\" %s\n\n", quoted)
	writeApacheForwardedHeaders(b, scheme)
}

func writeApacheStoppedVHost(b *strings.Builder, s SiteSpec, tls bool, certsDir string, ports listenPorts) {
	writeApacheVHostOpen(b, s, ports, tls)
	if tls {
		writeApacheTLS(b, s, certsDir)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    ErrorDocument 503 %s\n", apacheQuote(maintenanceHTML))
	b.WriteString("    RewriteEngine On\n")
	b.WriteString("    RewriteRule ^ - [R=503,L]\n")
	b.WriteString("</VirtualHost>\n")
}

func writeApacheRedirectVHost(b *strings.Builder, s SiteSpec, tls bool, scheme string, certsDir string, ports listenPorts) {
	writeApacheVHostOpen(b, s, ports, tls)
	if tls {
		writeApacheTLS(b, s, certsDir)
	}
	b.WriteString("\n    RewriteEngine On\n")
	sourcePort := apacheVHostPort(ports, tls)
	targetPort := apacheVHostPort(ports, scheme == "https")
	targetDefault := defaultHTTPPort
	if scheme == "https" {
		targetDefault = defaultHTTPSPort
	}
	sourceDefault := defaultHTTPPort
	if tls {
		sourceDefault = defaultHTTPSPort
	}
	if sourcePort == sourceDefault && targetPort == targetDefault {
		fmt.Fprintf(b, "    RewriteRule ^ %s://%%{HTTP_HOST}%%{REQUEST_URI} [R=308,L]\n", scheme)
	} else {
		b.WriteString("    RewriteCond %{HTTP_HOST} ^([^:]+)\n")
		if targetPort == targetDefault {
			fmt.Fprintf(b, "    RewriteRule ^ %s://%%1%%{REQUEST_URI} [R=308,L]\n", scheme)
		} else {
			fmt.Fprintf(b, "    RewriteRule ^ %s://%%1:%d%%{REQUEST_URI} [R=308,L]\n", scheme, targetPort)
		}
	}
	b.WriteString("</VirtualHost>\n")
}

// ---------------------------------------------------------------------------
// dashboard and default vhosts
// ---------------------------------------------------------------------------

func renderDashboard(server Server, d DashboardSpec, certsDir string, maxBody int64, ports listenPorts) []byte {
	switch server {
	case ServerNginx:
		return renderNginxDashboard(d, certsDir, maxBody, ports)
	case ServerApache:
		return renderApacheDashboard(d, certsDir, maxBody, ports)
	default:
		return nil
	}
}

func renderNginxDashboard(d DashboardSpec, certsDir string, maxBody int64, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString("server {\n")
	fmt.Fprintf(&b, "    listen %d;\n", ports.http)
	fmt.Fprintf(&b, "    server_name %s;\n", d.Domain)
	authority := "$host"
	if ports.https != defaultHTTPSPort {
		authority = fmt.Sprintf("$host:%d", ports.https)
	}
	fmt.Fprintf(&b, "\n    return 308 https://%s$request_uri;\n}\n\n", authority)
	b.WriteString("server {\n")
	fmt.Fprintf(&b, "    listen %d ssl;\n", ports.https)
	b.WriteString("    http2 on;\n")
	fmt.Fprintf(&b, "    server_name %s;\n", d.Domain)
	cert, key := filepath.Join(certsDir, dashboardOwner+".crt"), filepath.Join(certsDir, dashboardOwner+".key")
	fmt.Fprintf(&b, "    ssl_certificate     %s;\n", nginxValue(cert))
	fmt.Fprintf(&b, "    ssl_certificate_key %s;\n", nginxValue(key))
	b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n\n")
	fmt.Fprintf(&b, "    client_max_body_size %d;\n\n", maxBody)
	b.WriteString("    location / {\n")
	fmt.Fprintf(&b, "        proxy_pass http://127.0.0.1:%d;\n", d.UIPort)
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
	b.WriteString("        proxy_set_header Connection \"upgrade\";\n")
	b.WriteString("        proxy_set_header Host $host;\n")
	b.WriteString("        proxy_set_header X-Forwarded-For $remote_addr;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Host $host;\n")
	b.WriteString("        proxy_hide_header Strict-Transport-Security;\n")
	b.WriteString("    }\n}\n")
	return []byte(b.String())
}

func renderApacheDashboard(d DashboardSpec, certsDir string, maxBody int64, ports listenPorts) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<VirtualHost *:%d>\n", ports.http)
	fmt.Fprintf(&b, "    ServerName %s\n", d.Domain)
	b.WriteString("\n    RewriteEngine On\n")
	if ports.https != defaultHTTPSPort {
		b.WriteString("    RewriteCond %{HTTP_HOST} ^([^:]+)\n")
		fmt.Fprintf(&b, "    RewriteRule ^ https://%%1:%d%%{REQUEST_URI} [R=308,L]\n", ports.https)
	} else {
		b.WriteString("    RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]\n")
	}
	b.WriteString("</VirtualHost>\n\n")
	fmt.Fprintf(&b, "<VirtualHost *:%d>\n", ports.https)
	fmt.Fprintf(&b, "    ServerName %s\n", d.Domain)
	cert, key := filepath.Join(certsDir, dashboardOwner+".crt"), filepath.Join(certsDir, dashboardOwner+".key")
	b.WriteString("    SSLEngine on\n")
	fmt.Fprintf(&b, "    SSLCertificateFile %s\n", apacheQuote(cert))
	fmt.Fprintf(&b, "    SSLCertificateKeyFile %s\n", apacheQuote(key))
	b.WriteString("    SSLProtocol -all +TLSv1.2 +TLSv1.3\n\n")
	fmt.Fprintf(&b, "    LimitRequestBody %d\n\n", maxBody)
	writeApacheProxyConfig(&b, d.UIPort, "https")
	b.WriteString("</VirtualHost>\n")
	return []byte(b.String())
}

func renderDefault404(server Server, certsDir string, ports listenPorts) []byte {
	switch server {
	case ServerNginx:
		return renderNginxDefault(certsDir, ports)
	case ServerApache:
		return renderApacheDefault(certsDir, ports)
	default:
		return nil
	}
}

func renderNginxDefault(certsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString("server {\n")
	fmt.Fprintf(&b, "    listen %d default_server;\n", ports.http)
	b.WriteString("    server_name _;\n")
	b.WriteString("    default_type text/html;\n\n")
	fmt.Fprintf(&b, "    return 404 %s;\n", nginxQuotedValue(notFoundHTML))
	b.WriteString("}\n\n")
	b.WriteString("server {\n")
	fmt.Fprintf(&b, "    listen %d ssl default_server;\n", ports.https)
	b.WriteString("    http2 on;\n")
	b.WriteString("    server_name _;\n")
	cert, key := filepath.Join(certsDir, dashboardOwner+".crt"), filepath.Join(certsDir, dashboardOwner+".key")
	fmt.Fprintf(&b, "    ssl_certificate     %s;\n", nginxValue(cert))
	fmt.Fprintf(&b, "    ssl_certificate_key %s;\n", nginxValue(key))
	b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
	b.WriteString("    default_type text/html;\n\n")
	fmt.Fprintf(&b, "    return 404 %s;\n", nginxQuotedValue(notFoundHTML))
	b.WriteString("}\n")
	return []byte(b.String())
}

func renderApacheDefault(certsDir string, ports listenPorts) []byte {
	var b strings.Builder
	b.WriteString("# This file sorts first; Apache uses the first vhost for each port as the default.\n")
	for _, tls := range []bool{false, true} {
		fmt.Fprintf(&b, "\n<VirtualHost *:%d>\n", apacheVHostPort(ports, tls))
		b.WriteString("    ServerName devstack.invalid\n")
		if tls {
			cert, key := filepath.Join(certsDir, dashboardOwner+".crt"), filepath.Join(certsDir, dashboardOwner+".key")
			b.WriteString("    SSLEngine on\n")
			fmt.Fprintf(&b, "    SSLCertificateFile %s\n", apacheQuote(cert))
			fmt.Fprintf(&b, "    SSLCertificateKeyFile %s\n", apacheQuote(key))
			b.WriteString("    SSLProtocol -all +TLSv1.2 +TLSv1.3\n")
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "    ErrorDocument 404 %s\n", apacheQuote(notFoundHTML))
		b.WriteString("    RewriteEngine On\n")
		b.WriteString("    RewriteRule ^ - [R=404,L]\n")
		b.WriteString("</VirtualHost>\n")
	}
	return []byte(b.String())
}

// apacheQuote renders one Apache configuration argument.
func apacheQuote(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------------------
// static assets
// ---------------------------------------------------------------------------

func phpSourceBackupSuffixes() []string {
	return []string{"~", ".bak", ".orig", ".save", ".old", ".swp", ".swo", ".tmp"}
}

func phpSourceBackupPattern() string {
	var tilde bool
	var dotted []string
	for _, suffix := range phpSourceBackupSuffixes() {
		if suffix == "~" {
			tilde = true
			continue
		}
		dotted = append(dotted, strings.TrimPrefix(suffix, "."))
	}
	pattern := ""
	if tilde {
		pattern = "~"
	}
	if len(dotted) > 0 {
		if pattern != "" {
			pattern += "|"
		}
		pattern += `\.(?:` + strings.Join(dotted, "|") + `)`
	}
	return pattern
}

const pageStyle = `body{margin:0;min-height:100vh;display:grid;place-items:center;background:rgb(9,12,24);color:rgb(232,236,248);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}main{max-width:32rem;padding:2rem}h1{font-size:1.5rem;margin:0 0 .75rem}p{margin:0 0 1rem;color:rgb(169,180,208)}.brand{font:600 .75rem/1 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.18em;text-transform:uppercase;color:rgb(157,123,255);margin-bottom:.75rem}a{color:rgb(185,162,255)}`

const maintenanceHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Site unavailable - devstack</title><style>` + pageStyle + `</style><main><p class="brand">devstack</p><h1>Site unavailable</h1><p>This site is stopped. Start it from the devstack dashboard or CLI, then reload the page.</p><p><a href="https://devstack.test/">Open devstack</a></p></main></html>`

const notFoundHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Site not found - devstack</title><style>` + pageStyle + `</style><main><p class="brand">devstack</p><h1>Site not found</h1><p>No devstack site is registered for this host. Check the domain and try again.</p><p><a href="https://devstack.test/">Open devstack</a></p></main></html>`
