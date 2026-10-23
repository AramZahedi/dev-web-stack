package helper

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type serverRuntime struct {
	version       string
	reloadService string
}

func (m *VhostManager) detectServer(ctx context.Context, mode string) (serverRuntime, error) {
	switch mode {
	case vhostModeNginx:
		versionOut, err := m.runner.Run(ctx, "nginx", "-v")
		if err != nil {
			return serverRuntime{}, fmt.Errorf("%w: nginx is not installed or not runnable: %s", helperproto.ErrServingModeUnavailable, scrubVhostOutput(versionOut))
		}
		verboseOut, _ := m.runner.Run(ctx, "nginx", "-V")
		version := parseNginxVersion(string(versionOut) + "\n" + string(verboseOut))
		return serverRuntime{version: version, reloadService: "nginx"}, nil
	case vhostModeApache:
		command := ""
		var versionOut []byte
		for _, name := range []string{"apache2", "httpd", "apachectl"} {
			out, err := m.runner.Run(ctx, name, "-v")
			if err == nil {
				command = name
				versionOut = out
				break
			}
		}
		if command == "" {
			return serverRuntime{}, fmt.Errorf("%w: Apache is not installed or not runnable", helperproto.ErrServingModeUnavailable)
		}
		modulesOut, moduleErr := m.runner.Run(ctx, command, "-M")
		if moduleErr != nil && command != "apachectl" {
			modulesOut, moduleErr = m.runner.Run(ctx, "apachectl", "-M")
		}
		if moduleErr != nil {
			return serverRuntime{}, fmt.Errorf("%w: cannot list Apache modules: %s", helperproto.ErrServingModeUnavailable, scrubVhostOutput(modulesOut))
		}
		if missing := missingApacheModules(string(modulesOut)); len(missing) > 0 {
			return serverRuntime{}, fmt.Errorf(
				"%w: Apache is missing required modules %s; enable them with `a2enmod ssl proxy proxy_http proxy_wstunnel headers rewrite` (Debian/Ubuntu) or install the matching mod_ssl/mod_proxy/mod_headers/mod_rewrite packages (RHEL/Fedora); devstack never enables modules automatically",
				helperproto.ErrServingModeUnavailable, strings.Join(missing, ", "))
		}
		service := "apache2"
		if command == "httpd" {
			service = "httpd"
		}
		return serverRuntime{version: parseApacheVersion(string(versionOut)), reloadService: service}, nil
	default:
		return serverRuntime{}, fmt.Errorf("%w: unsupported serving mode %q", helperproto.ErrInvalidRequest, mode)
	}
}

func parseNginxVersion(output string) string {
	if index := strings.Index(output, "nginx/"); index >= 0 {
		fields := strings.Fields(output[index+len("nginx/"):])
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func parseApacheVersion(output string) string {
	if index := strings.Index(output, "Apache/"); index >= 0 {
		fields := strings.Fields(output[index:])
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func missingApacheModules(output string) []string {
	required := []string{"ssl_module", "proxy_module", "proxy_http_module", "proxy_wstunnel_module", "headers_module", "rewrite_module"}
	var missing []string
	for _, module := range required {
		if !strings.Contains(output, module) {
			missing = append(missing, module)
		}
	}
	return missing
}

func (m *VhostManager) resolveIncludeDir(ctx context.Context, mode string, runtime serverRuntime) (string, error) {
	var explicit string
	switch mode {
	case vhostModeNginx:
		explicit = m.NginxIncludeDir
	case vhostModeApache:
		explicit = m.ApacheIncludeDir
	}
	if explicit != "" {
		if err := validateAbsolutePath("include directory", explicit); err != nil {
			return "", fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
		}
		return explicit, nil
	}
	if mode == vhostModeNginx {
		dir, err := detectNginxIncludeDir(ctx, m.runner, m.nginxCandidates)
		if err != nil {
			return "", err
		}
		return dir, nil
	}
	dir, err := detectApacheIncludeDir(ctx, m.runner)
	if err != nil {
		return "", err
	}
	return dir, nil
}

type includeCandidate struct {
	dir    string
	config string
}

func detectNginxIncludeDir(ctx context.Context, runner CommandRunner, candidatesFn func(prefix, confPath string) []includeCandidate) (string, error) {
	var prefix, confPath string
	if out, err := runner.Run(ctx, "nginx", "-V"); err == nil {
		verbose := string(out)
		prefix = parseConfigurePrefix(verbose)
		confPath = parseConfigureConfPath(verbose)
	}
	if candidatesFn == nil {
		candidatesFn = defaultNginxIncludeCandidates
	}
	for _, candidate := range candidatesFn(prefix, confPath) {
		if candidate.config == "" || candidate.dir == "" {
			continue
		}
		if configIncludesDir("nginx", candidate.config, "", candidate.dir) {
			return candidate.dir, nil
		}
	}
	return "", fmt.Errorf(
		"%w: nginx does not include a supported conf.d directory; add `include %s/*.conf;` to the http block of nginx.conf (or pass --nginx-include-dir)",
		helperproto.ErrVhostIncludeMissing, "/etc/nginx/conf.d")
}

func defaultNginxIncludeCandidates(prefix, confPath string) []includeCandidate {
	var candidates []includeCandidate
	seen := map[string]struct{}{}
	add := func(dir, config string) {
		if dir == "" || config == "" {
			return
		}
		key := dir + "\x00" + config
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, includeCandidate{dir: dir, config: config})
	}
	if confPath != "" {
		confDir := filepath.Dir(confPath)
		add(filepath.Join(confDir, "conf.d"), confPath)
	}
	if prefix != "" {
		defaultConfig := filepath.Join(prefix, "conf", "nginx.conf")
		add(filepath.Join(prefix, "conf", "conf.d"), defaultConfig)
		add(filepath.Join(prefix, "conf.d"), defaultConfig)
	}
	add("/etc/nginx/conf.d", "/etc/nginx/nginx.conf")
	add("/usr/local/nginx/conf/conf.d", "/usr/local/nginx/conf/nginx.conf")
	add("/usr/local/etc/nginx/conf.d", "/usr/local/etc/nginx/nginx.conf")
	return candidates
}

func detectApacheIncludeDir(ctx context.Context, runner CommandRunner) (string, error) {
	var root, config string
	for _, name := range []string{"apache2", "httpd", "apachectl"} {
		out, err := runner.Run(ctx, name, "-V")
		if err != nil {
			continue
		}
		root = parseApacheRoot(string(out))
		config = parseApacheConfigFile(string(out))
		break
	}
	var candidates []includeCandidate
	if root != "" {
		configPath := config
		if configPath != "" && !filepath.IsAbs(configPath) {
			configPath = filepath.Join(root, configPath)
		}
		candidates = append(candidates,
			includeCandidate{dir: filepath.Join(root, "conf.d"), config: configPath},
			includeCandidate{dir: filepath.Join(root, "conf-enabled"), config: configPath},
		)
	}
	candidates = append(candidates,
		includeCandidate{dir: "/etc/apache2/conf-enabled", config: "/etc/apache2/apache2.conf"},
		includeCandidate{dir: "/etc/httpd/conf.d", config: "/etc/httpd/conf/httpd.conf"},
		includeCandidate{dir: "/etc/apache2/conf.d", config: "/etc/apache2/apache2.conf"},
	)
	for _, candidate := range candidates {
		if candidate.config == "" {
			continue
		}
		if configIncludesDir("apache", candidate.config, root, candidate.dir) {
			return candidate.dir, nil
		}
	}
	return "", fmt.Errorf(
		"%w: Apache does not include a supported conf.d/conf-enabled directory; add `IncludeOptional %s/*.conf` to httpd.conf (or pass --apache-include-dir)",
		helperproto.ErrVhostIncludeMissing, "/etc/apache2/conf-enabled")
}

func parseConfigurePrefix(output string) string {
	return parseConfigureArg(output, "--prefix=")
}

func parseConfigureConfPath(output string) string {
	return parseConfigureArg(output, "--conf-path=")
}

func parseConfigureArg(output, marker string) string {
	index := strings.Index(output, marker)
	if index < 0 {
		return ""
	}
	fields := strings.Fields(output[index+len(marker):])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], `"'`)
}

func parseApacheRoot(output string) string {
	const marker = `HTTPD_ROOT="`
	index := strings.Index(output, marker)
	if index < 0 {
		return ""
	}
	value := output[index+len(marker):]
	if end := strings.IndexByte(value, '"'); end >= 0 {
		value = value[:end]
	}
	return value
}

func parseApacheConfigFile(output string) string {
	const marker = `SERVER_CONFIG_FILE="`
	index := strings.Index(output, marker)
	if index < 0 {
		return ""
	}
	value := output[index+len(marker):]
	if end := strings.IndexByte(value, '"'); end >= 0 {
		value = value[:end]
	}
	return value
}

// configIncludesDir reports whether configPath contains an include directive
// covering dir. It reads only fixed, helper-chosen paths.
func configIncludesDir(server, configPath, serverRoot, dir string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var value string
		switch server {
		case "nginx":
			if !strings.HasPrefix(line, "include ") {
				continue
			}
			value = strings.TrimSpace(strings.TrimPrefix(line, "include "))
			value = strings.TrimSuffix(value, ";")
		case "apache":
			for _, directive := range []string{"IncludeOptional ", "Include "} {
				if strings.HasPrefix(line, directive) {
					value = strings.TrimSpace(strings.TrimPrefix(line, directive))
					break
				}
			}
			if value == "" {
				continue
			}
		}
		value = strings.Trim(value, `"'`)
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			if serverRoot != "" {
				if configIncludesPath(filepath.Join(serverRoot, value), dir) {
					return true
				}
			}
			if server == "apache" {
				// ServerRoot is commonly the parent of the directory holding
				// the main config, even when -V output is unavailable.
				serverRootFallback := filepath.Dir(filepath.Dir(configPath))
				if configIncludesPath(filepath.Join(serverRootFallback, value), dir) {
					return true
				}
			}
			value = filepath.Join(filepath.Dir(configPath), value)
		}
		if configIncludesPath(value, dir) {
			return true
		}
	}
	return false
}

func configIncludesPath(pattern, dir string) bool {
	cleaned := filepath.Clean(pattern)
	dir = filepath.Clean(dir)
	if cleaned == dir {
		return true
	}
	if strings.HasPrefix(cleaned, dir+string(filepath.Separator)) {
		return true
	}
	patternDir := filepath.Dir(cleaned)
	return patternDir == dir
}

func (m *VhostManager) runConfigTest(ctx context.Context, mode string) ([]byte, error) {
	switch mode {
	case vhostModeNginx:
		return m.runner.Run(ctx, "nginx", "-t")
	case vhostModeApache:
		return m.runner.Run(ctx, "apachectl", "configtest")
	default:
		return nil, fmt.Errorf("unsupported serving mode %q", mode)
	}
}

func (m *VhostManager) reloadServerOutput(ctx context.Context, mode, service string) ([]byte, error) {
	if service == "" {
		switch mode {
		case vhostModeNginx:
			service = "nginx"
		case vhostModeApache:
			service = "apache2"
		default:
			return nil, fmt.Errorf("unsupported serving mode %q", mode)
		}
	}
	return m.runner.Run(ctx, "systemctl", "reload", service)
}

func (m *VhostManager) reloadServer(ctx context.Context, server, service string) error {
	mode := server
	if mode == "" {
		mode = service
	}
	output, err := m.reloadServerOutput(ctx, mode, service)
	if err != nil {
		return fmt.Errorf("%w: reload %s: %s", helperproto.ErrServerReloadFailed, service, scrubVhostOutput(output))
	}
	return nil
}

// staticRootWorkerError verifies that the distribution's nginx/Apache worker
// can traverse to a running static document root and read it. The helper runs
// as root, so os.Stat alone would approve paths the unprivileged worker cannot
// enter (for example a 0700 home directory). The worker account differs by
// distribution, so this checks world traversal/read bits and treats a POSIX ACL
// grant to any group or named user as sufficient.
//
// Callers enforce this only when os.Geteuid()==0 so unprivileged tests and E2E
// runs, where the helper cannot know the server worker identity, are unaffected.
func staticRootWorkerError(root string) error {
	if _, err := os.Stat(root); err != nil {
		return err
	}
	for dir := root; ; {
		read := dir == root
		if err := workerDirectoryAccessError(dir, read); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func workerDirectoryAccessError(dir string, read bool) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	want := os.FileMode(0o001)
	action := "traverse"
	if read {
		want |= 0o004
		action = "read"
	}
	if info.Mode().Perm()&want == want {
		return nil
	}
	if posixACLGrants(dir, read) {
		return nil
	}
	remediation := "grant world execute (for example `sudo chmod o+x " + dir + "`)"
	if read {
		remediation = "grant world read and execute (for example `sudo chmod o+rx " + dir + "`)"
	}
	return fmt.Errorf(
		"the nginx/Apache worker cannot %s directory %q (mode %04o); %s, or run the server worker as the site owner, then run `devstack vhost sync` again",
		action, dir, info.Mode().Perm(), remediation)
}

const posixACLAccessXattr = "system.posix_acl_access"

// posixACLGrants reports whether an access ACL grants the requested access to
// a group or named user. The ACL mask is applied. This avoids rejecting an
// ACL-based deployment that intentionally withholds world permissions.
func posixACLGrants(path string, read bool) bool {
	size, err := syscall.Getxattr(path, posixACLAccessXattr, nil)
	if err != nil || size < 4 {
		return false
	}
	buf := make([]byte, size)
	n, err := syscall.Getxattr(path, posixACLAccessXattr, buf)
	if err != nil || n < 4 {
		return false
	}
	buf = buf[:n]

	type aclEntry struct {
		tag  uint16
		perm uint16
	}
	mask := uint16(0o7)
	entries := make([]aclEntry, 0, (len(buf)-4)/8)
	for offset := 4; offset+8 <= len(buf); offset += 8 {
		entry := aclEntry{
			tag:  uint16(buf[offset]) | uint16(buf[offset+1])<<8,
			perm: uint16(buf[offset+2]) | uint16(buf[offset+3])<<8,
		}
		entries = append(entries, entry)
		if entry.tag == 0x10 {
			mask = entry.perm
		}
	}
	want := uint16(0o1)
	if read {
		want |= 0o4
	}
	for _, entry := range entries {
		switch entry.tag {
		case 0x02, 0x04, 0x08, 0x20: // named user, owning group, named group, other
			if entry.perm&mask&want == want {
				return true
			}
		}
	}
	return false
}

// detectDefaultServerConflict reports whether an existing nginx configuration
// already declares default_server on one of the listen ports devstack renders.
// It prefers `nginx -T`, which labels each file, and falls back to scanning
// fixed configuration locations when the server refuses to dump its config.
// Lines from devstack's own vhost tree are ignored.
func (m *VhostManager) detectDefaultServerConflict(ctx context.Context, mode string) bool {
	if mode != vhostModeNginx {
		return false
	}
	wantPorts := map[int]struct{}{
		resolveVhostPort(m.HTTPListenPort, 80):   {},
		resolveVhostPort(m.HTTPSListenPort, 443): {},
	}
	ownTree := m.serverTree(vhostModeNginx)

	if out, err := m.runner.Run(ctx, "nginx", "-T"); err == nil {
		return nginxDumpHasForeignDefault(string(out), ownTree, wantPorts)
	}
	return scanNginxConfigsForDefault(ownTree, wantPorts, m.NginxIncludeDir)
}

func resolveVhostPort(port, fallback int) int {
	if port == 0 {
		return fallback
	}
	return port
}

func nginxDumpHasForeignDefault(dump, ownTree string, wantPorts map[int]struct{}) bool {
	currentFile := ""
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "# configuration file ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			currentFile = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "# configuration file ")), ":")
			continue
		}
		if currentFile == "" || pathInside(currentFile, ownTree) {
			continue
		}
		if port, ok := nginxDefaultServerPort(line); ok {
			if _, wanted := wantPorts[port]; wanted {
				return true
			}
		}
	}
	return false
}

func scanNginxConfigsForDefault(ownTree string, wantPorts map[int]struct{}, extraDirs ...string) bool {
	roots := []string{
		"/etc/nginx",
		"/usr/local/etc/nginx",
		"/usr/local/nginx/conf",
	}
	for _, dir := range extraDirs {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, dir)
		}
	}
	seen := map[string]struct{}{}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		found := false
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || found {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".conf") || pathInside(path, ownTree) {
				return nil
			}
			if _, ok := seen[path]; ok {
				return nil
			}
			seen[path] = struct{}{}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, line := range strings.Split(string(data), "\n") {
				if port, ok := nginxDefaultServerPort(line); ok {
					if _, wanted := wantPorts[port]; wanted {
						found = true
						return fs.SkipAll
					}
				}
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

func nginxDefaultServerPort(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return 0, false
	}
	fields := strings.Fields(line)
	listenAt := -1
	for i, field := range fields {
		if field == "listen" {
			listenAt = i
			break
		}
	}
	if listenAt < 0 || listenAt+1 >= len(fields) {
		return 0, false
	}
	hasDefault := false
	for _, field := range fields[listenAt+1:] {
		if strings.TrimSuffix(field, ";") == "default_server" {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		return 0, false
	}
	address := strings.TrimSuffix(fields[listenAt+1], ";")
	if index := strings.LastIndex(address, ":"); index >= 0 {
		address = address[index+1:]
	}
	port, err := strconv.Atoi(address)
	if err != nil {
		return 0, false
	}
	return port, true
}
