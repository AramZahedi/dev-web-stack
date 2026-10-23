package helper

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/testcert"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

var vhostTestLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

type vhostTestEnv struct {
	t         *testing.T
	root      string
	backend   string
	docroot   string
	statePath string
	authority *Authority
	runner    *fakeRunner
	manager   *VhostManager

	mu             sync.Mutex
	forwarderCalls []bool
}

func newVhostTestEnv(t *testing.T) *vhostTestEnv {
	t.Helper()
	root := t.TempDir()
	backend := filepath.Join(root, "backend")
	docroot := filepath.Join(backend, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	env := &vhostTestEnv{
		t:         t,
		root:      root,
		backend:   backend,
		docroot:   docroot,
		statePath: filepath.Join(root, "state", "vhosts.json"),
		authority: newTestAuthority(t),
		runner:    &fakeRunner{},
	}
	env.manager = NewVhostManager(root, env.statePath, env.authority, env.runner, vhostTestLogger)
	env.manager.NginxIncludeDir = filepath.Join(root, "includes", "nginx")
	env.manager.ApacheIncludeDir = filepath.Join(root, "includes", "apache")
	env.manager.Forwarders = func(_ context.Context, enable bool) error {
		env.mu.Lock()
		defer env.mu.Unlock()
		env.forwarderCalls = append(env.forwarderCalls, enable)
		return nil
	}
	return env
}

func (env *vhostTestEnv) forwarders() []bool {
	env.mu.Lock()
	defer env.mu.Unlock()
	return append([]bool(nil), env.forwarderCalls...)
}

func (env *vhostTestEnv) certPEM(domains ...string) ([]byte, []byte) {
	env.t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		env.t.Fatalf("generate key: %v", err)
	}
	csr, err := testcert.CreateCSR(key, domains)
	if err != nil {
		env.t.Fatalf("create CSR: %v", err)
	}
	cert, _, _, err := env.authority.SignCSR(csr, domains, time.Hour)
	if err != nil {
		env.t.Fatalf("sign certificate: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		env.t.Fatalf("marshal key: %v", err)
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func (env *vhostTestEnv) request(mode string) helperproto.VHostSyncRequest {
	env.t.Helper()
	cert, key := env.certPEM("blog.test", "www.blog.test")
	dashCert, dashKey := env.certPEM("devstack.test")
	return helperproto.VHostSyncRequest{
		Mode:         mode,
		MaxBodyBytes: 1024,
		Sites: []helperproto.VHostSite{{
			ID:           "s-1",
			Name:         "blog",
			Domain:       "blog.test",
			BackendPath:  env.backend,
			WWW:          true,
			Kind:         string(model.SiteStatic),
			DocumentRoot: env.docroot,
			TLS:          true,
			DesiredState: string(model.DesiredRunning),
			CertPEM:      cert,
			KeyPEM:       key,
		}},
		Dashboard: helperproto.VHostDashboard{
			Domain:      "devstack.test",
			BackendPort: 7070,
			CertPEM:     dashCert,
			KeyPEM:      dashKey,
		},
		RequestedAt: time.Now().UTC(),
	}
}

func (env *vhostTestEnv) addNodeSite(req *helperproto.VHostSyncRequest, id, name, domain string, port int, docroot string) {
	env.t.Helper()
	cert, key := env.certPEM(domain)
	req.Sites = append(req.Sites, helperproto.VHostSite{
		ID:           id,
		Name:         name,
		Domain:       domain,
		BackendPath:  env.backend,
		Kind:         string(model.SiteNode),
		DocumentRoot: docroot,
		BackendPort:  port,
		TLS:          true,
		DesiredState: string(model.DesiredRunning),
		CertPEM:      cert,
		KeyPEM:       key,
	})
}

func (env *vhostTestEnv) currentTarget(server string) string {
	env.t.Helper()
	target, err := os.Readlink(filepath.Join(env.root, server, "current"))
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(env.root, server, target)
	}
	return target
}

func (env *vhostTestEnv) releases(server string) []string {
	env.t.Helper()
	entries, err := os.ReadDir(filepath.Join(env.root, server, "releases"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		env.t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (env *vhostTestEnv) bridge(server string) string {
	if server == "nginx" {
		return filepath.Join(env.manager.NginxIncludeDir, bridgeFilename)
	}
	return filepath.Join(env.manager.ApacheIncludeDir, bridgeFilename)
}

// TestSyncWritesReleaseAndBridge is the primary happy-path contract.
func TestSyncWritesReleaseAndBridge(t *testing.T) {
	env := newVhostTestEnv(t)
	st, err := env.manager.Sync(context.Background(), env.request("nginx"))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !st.ConfigOK || st.CurrentRelease == "" {
		t.Fatalf("status = %+v", st)
	}
	if st.Mode != "nginx" || st.Server != "nginx" {
		t.Fatalf("status mode/server = %q/%q", st.Mode, st.Server)
	}
	if !filepath.IsAbs(st.CurrentRelease) {
		t.Fatalf("current release %q is not absolute", st.CurrentRelease)
	}
	if !st.IncludePresent {
		t.Fatalf("status does not report the bridge include: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(env.root, "nginx", "current", "blog-s-1.conf")); err != nil {
		t.Fatalf("rendered site config: %v", err)
	}
	bridgeData, err := os.ReadFile(env.bridge("nginx"))
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if !bytes.HasPrefix(bridgeData, []byte(vhostGeneratedHeader+"\n")) {
		t.Fatalf("bridge is missing the generated header:\n%s", bridgeData)
	}
	wantInclude := "include " + filepath.Join(env.root, "nginx", "current") + "/*.conf;"
	if !bytes.Contains(bridgeData, []byte(wantInclude)) {
		t.Fatalf("bridge does not include %q:\n%s", wantInclude, bridgeData)
	}
	if !env.runner.called("nginx", "-t") {
		t.Fatal("nginx config test was not run")
	}
	if !env.runner.called("systemctl", "reload", "nginx") {
		t.Fatal("nginx reload was not run")
	}

	info, err := os.Stat(env.statePath)
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestSyncWritesApacheReleaseAndBridge(t *testing.T) {
	env := newVhostTestEnv(t)
	env.runner.fn = apacheRunner(t, true)
	st, err := env.manager.Sync(context.Background(), env.request("apache"))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if st.Server != "apache" || !st.ConfigOK {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(env.root, "apache", "current", "blog-s-1.conf")); err != nil {
		t.Fatalf("rendered Apache site config: %v", err)
	}
	bridgeData, err := os.ReadFile(env.bridge("apache"))
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	wantInclude := "IncludeOptional " + filepath.Join(env.root, "apache", "current") + "/*.conf"
	if !bytes.Contains(bridgeData, []byte(wantInclude)) {
		t.Fatalf("bridge does not include %q:\n%s", wantInclude, bridgeData)
	}
	if !env.runner.called("apachectl", "configtest") {
		t.Fatal("apachectl configtest was not run")
	}
	if !env.runner.called("systemctl", "reload", "apache2") {
		t.Fatal("apache reload was not run")
	}
}

func apacheRunner(t *testing.T, modules bool) func(string, []string) ([]byte, error) {
	t.Helper()
	return func(name string, args []string) ([]byte, error) {
		switch {
		case name == "apache2" && len(args) == 1 && args[0] == "-v":
			return []byte("Server version: Apache/2.4.58 (Unix)\nServer built: test\n"), nil
		case name == "apache2" && len(args) == 1 && args[0] == "-M":
			if !modules {
				return []byte("Loaded Modules:\n core_module (static)\n ssl_module (shared)\n proxy_module (shared)\n proxy_http_module (shared)\n proxy_wstunnel_module (shared)\n headers_module (shared)\n"), nil
			}
			return []byte("Loaded Modules:\n core_module (static)\n ssl_module (shared)\n proxy_module (shared)\n proxy_http_module (shared)\n proxy_wstunnel_module (shared)\n headers_module (shared)\n rewrite_module (shared)\n"), nil
		}
		return nil, nil
	}
}

func TestSyncCustomTLSDir(t *testing.T) {
	env := newVhostTestEnv(t)
	custom := filepath.Join(env.root, "custom-tls")
	env.manager.TLSDir = custom

	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	certPath := filepath.Join(custom, "s-1.crt")
	keyPath := filepath.Join(custom, "s-1.key")
	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("custom cert: %v", err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode = %04o, want 0644", certInfo.Mode().Perm())
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("custom key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", keyInfo.Mode().Perm())
	}
	rendered, err := os.ReadFile(filepath.Join(env.root, "nginx", "current", "blog-s-1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rendered, []byte(certPath)) || !bytes.Contains(rendered, []byte(keyPath)) {
		t.Fatalf("config does not reference the custom TLS directory:\n%s", rendered)
	}
	if _, err := os.Stat(filepath.Join(custom, "__dashboard__.crt")); err != nil {
		t.Fatalf("dashboard cert: %v", err)
	}
}

func TestSyncFixesLooseKeyMode(t *testing.T) {
	env := newVhostTestEnv(t)
	tlsDir := filepath.Join(env.root, "tls")
	if err := os.MkdirAll(tlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(tlsDir, "s-1.key")
	if err := os.WriteFile(loose, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	info, err := os.Stat(loose)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestSyncRollsBackOnConfigFailure(t *testing.T) {
	env := newVhostTestEnv(t)
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	first := env.currentTarget("nginx")
	tests := 0
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-t" {
			tests++
			if tests == 1 {
				return []byte("nginx: [emerg] invalid parameter\n"), errors.New("exit status 1")
			}
		}
		return nil, nil
	}
	req := env.request("nginx")
	env.addNodeSite(&req, "s-2", "shop", "shop.test", 41000, "")

	_, err := env.manager.Sync(context.Background(), req)
	if !errors.Is(err, helperproto.ErrVhostConfigInvalid) {
		t.Fatalf("error = %v, want VHOST_CONFIG_INVALID", err)
	}
	if got := env.currentTarget("nginx"); got != first {
		t.Fatalf("current changed to %q after failure, want %q", got, first)
	}
	if got := env.releases("nginx"); len(got) != 1 {
		t.Fatalf("releases after failed sync = %v, want the first release only", got)
	}
	st, err := env.manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.CurrentRelease != first {
		t.Fatalf("status current = %q, want %q", st.CurrentRelease, first)
	}
	if !st.ConfigOK {
		t.Fatal("status reports config failure after the previous release was restored and re-tested")
	}
	if st.LastError == "" {
		t.Fatal("status does not report the last error")
	}
}

func TestSyncRollsBackOnReloadFailure(t *testing.T) {
	env := newVhostTestEnv(t)
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	first := env.currentTarget("nginx")
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "systemctl" && len(args) == 2 && args[0] == "reload" && args[1] == "nginx" {
			return []byte("Job for nginx.service failed\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	req := env.request("nginx")
	env.addNodeSite(&req, "s-2", "shop", "shop.test", 41000, "")

	_, err := env.manager.Sync(context.Background(), req)
	if !errors.Is(err, helperproto.ErrServerReloadFailed) {
		t.Fatalf("error = %v, want SERVER_RELOAD_FAILED", err)
	}
	if got := env.currentTarget("nginx"); got != first {
		t.Fatalf("current changed to %q after reload failure, want %q", got, first)
	}
}

func TestFirstSyncFailureCleansUp(t *testing.T) {
	env := newVhostTestEnv(t)
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-t" {
			return []byte("nginx: [emerg] bad include\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); !errors.Is(err, helperproto.ErrVhostConfigInvalid) {
		t.Fatalf("error = %v, want VHOST_CONFIG_INVALID", err)
	}
	if _, err := os.Lstat(filepath.Join(env.root, "nginx", "current")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current exists after first-sync failure: %v", err)
	}
	if got := env.releases("nginx"); len(got) != 0 {
		t.Fatalf("releases after first-sync failure = %v", got)
	}
	if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge exists after first-sync failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "s-1.crt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site cert exists after first-sync failure: %v", err)
	}
}

func TestSyncPrunesToTwoReleases(t *testing.T) {
	env := newVhostTestEnv(t)
	for i := 0; i < 3; i++ {
		if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	if got := env.releases("nginx"); len(got) != 2 {
		t.Fatalf("releases = %v, want the last two", got)
	}
	if current := env.currentTarget("nginx"); current == "" {
		t.Fatal("current symlink is missing")
	}
}

func TestSyncPersistsStateAndStatusUsesNoCommands(t *testing.T) {
	env := newVhostTestEnv(t)
	first, err := env.manager.Sync(context.Background(), env.request("nginx"))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(env.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted vhostState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if persisted.Mode != "nginx" || persisted.Server != "nginx" || persisted.CurrentRelease != first.CurrentRelease {
		t.Fatalf("persisted state = %+v", persisted)
	}
	if !persisted.ConfigOK || persisted.LastSync.IsZero() || persisted.BridgePath == "" || persisted.BridgeHash == "" {
		t.Fatalf("persisted state = %+v", persisted)
	}
	owners := map[string]bool{}
	for _, owner := range persisted.TLSOwners {
		owners[owner] = true
	}
	if !owners["s-1"] || !owners["__dashboard__"] {
		t.Fatalf("persisted TLS owners = %v", persisted.TLSOwners)
	}

	before := env.runner.count("nginx") + env.runner.count("systemctl") + env.runner.count("apachectl")
	st, err := env.manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := env.runner.count("nginx") + env.runner.count("systemctl") + env.runner.count("apachectl")
	if before != after {
		t.Fatal("Status invoked a command")
	}
	if st.Mode != "nginx" || st.CurrentRelease != first.CurrentRelease || !st.IncludePresent || !st.ConfigOK {
		t.Fatalf("status = %+v", st)
	}
}

func TestStatusMissingStateIsBuiltin(t *testing.T) {
	env := newVhostTestEnv(t)
	st, err := env.manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != "builtin" || !st.ConfigOK || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if env.runner.count("nginx") != 0 || env.runner.count("systemctl") != 0 {
		t.Fatal("Status invoked a command")
	}
}

func TestSyncRejectsBadCertificates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*vhostTestEnv, *helperproto.VHostSyncRequest)
	}{
		{
			name: "wrong SAN",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				cert, key := env.certPEM("other.test")
				req.Sites[0].CertPEM = cert
				req.Sites[0].KeyPEM = key
			},
		},
		{
			name: "mismatched key",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				_, key := env.certPEM("blog.test", "www.blog.test")
				req.Sites[0].KeyPEM = key
			},
		},
		{
			name: "expired leaf",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				base := time.Now().Add(-3 * time.Hour)
				env.authority.now = func() time.Time { return base }
				cert, key := env.certPEM("blog.test", "www.blog.test")
				dashCert, dashKey := env.certPEM("devstack.test")
				env.authority.now = func() time.Time { return base.Add(2 * time.Hour) }
				req.Sites[0].CertPEM = cert
				req.Sites[0].KeyPEM = key
				req.Dashboard.CertPEM = dashCert
				req.Dashboard.KeyPEM = dashKey
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newVhostTestEnv(t)
			req := env.request("nginx")
			tc.mutate(env, &req)
			if _, err := env.manager.Sync(context.Background(), req); !errors.Is(err, helperproto.ErrInvalidRequest) {
				t.Fatalf("error = %v, want INVALID_REQUEST", err)
			}
			if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bridge was written for an invalid certificate: %v", err)
			}
			if _, err := os.Stat(filepath.Join(env.root, "tls")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("TLS material was written for an invalid certificate: %v", err)
			}
		})
	}
}

func TestSyncRejectsBackendContainmentViolations(t *testing.T) {
	outside := func(env *vhostTestEnv) string {
		dir := filepath.Join(env.root, "outside", "public")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	tests := []struct {
		name   string
		mutate func(*vhostTestEnv, *helperproto.VHostSyncRequest)
	}{
		{
			name: "document root outside backend",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].DocumentRoot = outside(env)
			},
		},
		{
			name: "document root symlink escape",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				link := filepath.Join(env.backend, "escape")
				if err := os.Symlink(outside(env), link); err != nil {
					t.Fatal(err)
				}
				req.Sites[0].DocumentRoot = link
			},
		},
		{
			name: "missing backend path",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].BackendPath = filepath.Join(env.root, "missing")
			},
		},
		{
			name: "empty backend path",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].BackendPath = ""
			},
		},
		{
			name: "backend path traversal",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].BackendPath = "/tmp/../etc"
			},
		},
		{
			name: "missing document root",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].DocumentRoot = filepath.Join(env.backend, "missing")
			},
		},
		{
			name: "document root is a file",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				file := filepath.Join(env.backend, "index.html")
				if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				req.Sites[0].DocumentRoot = file
			},
		},
		{
			name: "relative document root",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].DocumentRoot = "public"
			},
		},
		{
			name: "node document root outside backend",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].Kind = string(model.SiteNode)
				req.Sites[0].BackendPort = 41000
				req.Sites[0].DocumentRoot = outside(env)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newVhostTestEnv(t)
			req := env.request("nginx")
			tc.mutate(env, &req)
			if _, err := env.manager.Sync(context.Background(), req); !errors.Is(err, helperproto.ErrInvalidRequest) {
				t.Fatalf("error = %v, want INVALID_REQUEST", err)
			}
			if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bridge was written for an invalid document root: %v", err)
			}
		})
	}
}

func TestSyncAllowsContainedSymlinkAndNodeWithoutRoot(t *testing.T) {
	env := newVhostTestEnv(t)
	realRoot := filepath.Join(env.backend, "real", "public")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(env.backend, "linked")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	req := env.request("nginx")
	req.Sites[0].DocumentRoot = link
	if _, err := env.manager.Sync(context.Background(), req); err != nil {
		t.Fatalf("contained symlink rejected: %v", err)
	}
	rendered, err := os.ReadFile(filepath.Join(env.root, "nginx", "current", "blog-s-1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rendered, []byte(realRoot)) {
		t.Fatalf("rendered config does not use the resolved document root:\n%s", rendered)
	}

	nodeReq := env.request("nginx")
	nodeReq.Sites[0].Kind = string(model.SiteNode)
	nodeReq.Sites[0].DocumentRoot = ""
	nodeReq.Sites[0].BackendPort = 41000
	if _, err := env.manager.Sync(context.Background(), nodeReq); err != nil {
		t.Fatalf("node site without document root rejected: %v", err)
	}
}

func TestSyncValidatesPortsKindsAndDuplicates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*vhostTestEnv, *helperproto.VHostSyncRequest)
	}{
		{
			name: "static with backend port",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].BackendPort = 41000
			},
		},
		{
			name: "node port too low",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].Kind = string(model.SiteNode)
				req.Sites[0].DocumentRoot = ""
				req.Sites[0].BackendPort = 40999
			},
		},
		{
			name: "node port too high",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].Kind = string(model.SiteNode)
				req.Sites[0].DocumentRoot = ""
				req.Sites[0].BackendPort = 43000
			},
		},
		{
			name: "php unsupported",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].Kind = string(model.SitePHP)
			},
		},
		{
			name: "unknown kind",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].Kind = "python"
			},
		},
		{
			name: "bad desired state",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Sites[0].DesiredState = "crashed"
			},
		},
		{
			name: "dashboard port",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Dashboard.BackendPort = 7000
			},
		},
		{
			name: "duplicate ID",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				env.addNodeSite(req, "s-1", "shop", "shop.test", 41000, "")
			},
		},
		{
			name: "duplicate domain",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				env.addNodeSite(req, "s-2", "shop", "blog.test", 41000, "")
			},
		},
		{
			name: "www collision",
			mutate: func(env *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				env.addNodeSite(req, "s-2", "shop", "www.blog.test", 41000, "")
			},
		},
		{
			name: "invalid mode",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.Mode = "caddy"
			},
		},
		{
			name: "negative body limit",
			mutate: func(_ *vhostTestEnv, req *helperproto.VHostSyncRequest) {
				req.MaxBodyBytes = -1
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newVhostTestEnv(t)
			req := env.request("nginx")
			tc.mutate(env, &req)
			if _, err := env.manager.Sync(context.Background(), req); !errors.Is(err, helperproto.ErrInvalidRequest) {
				t.Fatalf("error = %v, want INVALID_REQUEST", err)
			}
			if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bridge was written for an invalid request: %v", err)
			}
		})
	}
}

func TestSyncRejectsInjection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*helperproto.VHostSyncRequest)
	}{
		{"newline ID", func(req *helperproto.VHostSyncRequest) { req.Sites[0].ID = "s-1\nserver" }},
		{"newline name", func(req *helperproto.VHostSyncRequest) { req.Sites[0].Name = "blog\nserver" }},
		{"newline domain", func(req *helperproto.VHostSyncRequest) { req.Sites[0].Domain = "blog.test\nserver" }},
		{"CR domain", func(req *helperproto.VHostSyncRequest) { req.Sites[0].Domain = "blog.test\rserver" }},
		{"NUL backend", func(req *helperproto.VHostSyncRequest) { req.Sites[0].BackendPath = "/tmp\x00/backend" }},
		{"newline backend", func(req *helperproto.VHostSyncRequest) { req.Sites[0].BackendPath = "/tmp\n/backend" }},
		{"NUL root", func(req *helperproto.VHostSyncRequest) { req.Sites[0].DocumentRoot = "/tmp\x00" }},
		{"CR root", func(req *helperproto.VHostSyncRequest) { req.Sites[0].DocumentRoot = "/tmp\r/blog" }},
		{"dashboard domain", func(req *helperproto.VHostSyncRequest) { req.Dashboard.Domain = "evil.test" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newVhostTestEnv(t)
			req := env.request("nginx")
			tc.mutate(&req)
			if _, err := env.manager.Sync(context.Background(), req); !errors.Is(err, helperproto.ErrInvalidRequest) {
				t.Fatalf("error = %v, want INVALID_REQUEST", err)
			}
			if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bridge was written for an injection request: %v", err)
			}
		})
	}
}

func TestSyncRefusesForeignBridge(t *testing.T) {
	env := newVhostTestEnv(t)
	bridge := env.bridge("nginx")
	if err := os.MkdirAll(filepath.Dir(bridge), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("# user-managed nginx config\nserver { listen 80; }\n")
	if err := os.WriteFile(bridge, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
		t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
	}
	data, err := os.ReadFile(bridge)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, foreign) {
		t.Fatalf("foreign bridge was overwritten:\n%s", data)
	}
	if got := env.releases("nginx"); len(got) != 0 {
		t.Fatalf("release was left behind after bridge refusal: %v", got)
	}
}

func TestSyncRefusesEditedOwnedBridge(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.Sync(ctx, env.request("nginx")); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	bridge := env.bridge("nginx")
	edited := []byte(vhostGeneratedHeader + "\n\ninclude /tmp/evil/*.conf;\n")
	if err := os.WriteFile(bridge, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Sync(ctx, env.request("nginx")); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
		t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
	}
	data, err := os.ReadFile(bridge)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, edited) {
		t.Fatalf("edited bridge was overwritten:\n%s", data)
	}
}

func TestRemoveRefusesEditedOwnedBridge(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	bridge := env.bridge("nginx")
	edited := []byte(vhostGeneratedHeader + "\n\ninclude /tmp/evil/*.conf;\n")
	if err := os.WriteFile(bridge, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.Remove(ctx); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
		t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
	}
	if _, err := os.Stat(bridge); err != nil {
		t.Fatalf("edited bridge was removed: %v", err)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
}

func TestSyncBridgeMigrationOwnership(t *testing.T) {
	t.Run("matching bridge is adopted", func(t *testing.T) {
		env := newVhostTestEnv(t)
		bridge := env.bridge("nginx")
		if err := os.MkdirAll(filepath.Dir(bridge), 0o755); err != nil {
			t.Fatal(err)
		}
		content, err := env.manager.bridgeContent(vhost.ServerNginx)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bridge, content, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		state, err := env.manager.readState()
		if err != nil {
			t.Fatal(err)
		}
		if state.BridgeHash == "" {
			t.Fatal("matching pre-existing bridge was not recorded as owned")
		}
	})

	t.Run("edited header bridge without state is refused", func(t *testing.T) {
		env := newVhostTestEnv(t)
		bridge := env.bridge("nginx")
		if err := os.MkdirAll(filepath.Dir(bridge), 0o755); err != nil {
			t.Fatal(err)
		}
		edited := []byte(vhostGeneratedHeader + "\n\ninclude /tmp/evil/*.conf;\n")
		if err := os.WriteFile(bridge, edited, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := env.manager.Sync(context.Background(), env.request("nginx")); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
			t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
		}
	})
}

func TestSyncAutoDetectsNginxInclude(t *testing.T) {
	env := newVhostTestEnv(t)
	env.manager.NginxIncludeDir = ""
	prefix := t.TempDir()
	confDir := filepath.Join(prefix, "conf")
	includeDir := filepath.Join(confDir, "conf.d")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainConfig := filepath.Join(confDir, "nginx.conf")
	if err := os.WriteFile(mainConfig, []byte("events {}\nhttp {\n    include conf.d/*.conf;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-V" {
			return []byte("nginx version: nginx/1.24.0\nconfigure arguments: --prefix=" + prefix + "\n"), nil
		}
		if name == "nginx" && len(args) == 1 && args[0] == "-t" {
			return nil, nil
		}
		return nil, nil
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(includeDir, bridgeFilename)); err != nil {
		t.Fatalf("auto-detected bridge: %v", err)
	}
}

func TestSyncAutoDetectsDebianNginxInclude(t *testing.T) {
	env := newVhostTestEnv(t)
	env.manager.NginxIncludeDir = ""
	base := t.TempDir()
	confDir := filepath.Join(base, "etc", "nginx")
	includeDir := filepath.Join(confDir, "conf.d")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(confDir, "nginx.conf")
	if err := os.WriteFile(confPath, []byte("events {}\nhttp {\n    include conf.d/*.conf;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-V" {
			return []byte("nginx version: nginx/1.24.0\nconfigure arguments: --prefix=/usr/share/nginx --conf-path=" + confPath + "\n"), nil
		}
		if name == "nginx" && len(args) == 1 && args[0] == "-t" {
			return nil, nil
		}
		return nil, nil
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(includeDir, bridgeFilename)); err != nil {
		t.Fatalf("Debian-layout bridge: %v", err)
	}
}

func TestSyncAutoDetectIncludeMissing(t *testing.T) {
	env := newVhostTestEnv(t)
	env.manager.NginxIncludeDir = ""
	prefix := t.TempDir()
	env.manager.nginxCandidates = func(prefix, _ string) []includeCandidate {
		return []includeCandidate{{
			dir:    filepath.Join(prefix, "conf", "conf.d"),
			config: filepath.Join(prefix, "conf", "nginx.conf"),
		}}
	}
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-V" {
			return []byte("nginx version: nginx/1.24.0\nconfigure arguments: --prefix=" + prefix + "\n"), nil
		}
		return nil, nil
	}
	if _, err := env.manager.Sync(context.Background(), env.request("nginx")); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
		t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
	}
}

func TestSyncApacheMissingModules(t *testing.T) {
	env := newVhostTestEnv(t)
	env.runner.fn = apacheRunner(t, false)
	_, err := env.manager.Sync(context.Background(), env.request("apache"))
	if !errors.Is(err, helperproto.ErrServingModeUnavailable) {
		t.Fatalf("error = %v, want SERVING_MODE_UNAVAILABLE", err)
	}
	if !strings.Contains(err.Error(), "rewrite_module") {
		t.Fatalf("error does not name the missing module: %v", err)
	}
	if !strings.Contains(err.Error(), "a2enmod") {
		t.Fatalf("error does not carry remediation: %v", err)
	}
}

func TestSyncBusy(t *testing.T) {
	env := newVhostTestEnv(t)
	env.manager.syncMu.Lock()
	_, err := env.manager.Sync(context.Background(), env.request("nginx"))
	env.manager.syncMu.Unlock()
	if !errors.Is(err, helperproto.ErrVhostSyncBusy) {
		t.Fatalf("error = %v, want VHOST_SYNC_BUSY", err)
	}
}

func TestSetModeForwarders(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	req := env.request("nginx")

	st, err := env.manager.SetMode(ctx, "nginx", req)
	if err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	if st.Mode != "nginx" {
		t.Fatalf("mode = %q", st.Mode)
	}
	if got := env.forwarders(); len(got) != 1 || got[0] != false {
		t.Fatalf("forwarder calls after enabling nginx = %v, want [false]", got)
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("bridge after SetMode nginx: %v", err)
	}

	if _, err := env.manager.SetMode(ctx, "builtin", helperproto.VHostSyncRequest{}); err != nil {
		t.Fatalf("SetMode builtin: %v", err)
	}
	if got := env.forwarders(); len(got) != 2 || got[1] != true {
		t.Fatalf("forwarder calls after returning to builtin = %v, want [false true]", got)
	}
	if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge survived builtin mode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "s-1.crt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("site TLS survived builtin mode: %v", err)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "builtin" || status.CurrentRelease != "" {
		t.Fatalf("status after builtin = %+v", status)
	}
}

func TestSetModeRestoresOnFailure(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "nginx" && len(args) == 1 && args[0] == "-t" {
			return []byte("nginx: [emerg] boom\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); !errors.Is(err, helperproto.ErrVhostConfigInvalid) {
		t.Fatalf("error = %v, want VHOST_CONFIG_INVALID", err)
	}
	if got := env.forwarders(); len(got) != 2 || got[0] != false || got[1] != true {
		t.Fatalf("forwarder calls = %v, want [false true]", got)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "builtin" {
		t.Fatalf("mode after failed switch = %q, want builtin", status.Mode)
	}
	if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge left behind after failed switch: %v", err)
	}
}

func TestSetModeBuiltinReloadFailureKeepsExternalMode(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "systemctl" && len(args) == 2 && args[0] == "reload" && args[1] == "nginx" {
			return []byte("reload failed\n"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if _, err := env.manager.SetMode(ctx, "builtin", helperproto.VHostSyncRequest{}); !errors.Is(err, helperproto.ErrServerReloadFailed) {
		t.Fatalf("error = %v, want SERVER_RELOAD_FAILED", err)
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("bridge was not restored after reload failure: %v", err)
	}
	if got := env.forwarders(); len(got) != 1 {
		t.Fatalf("forwarders were re-enabled after failed removal: %v", got)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
}

func TestSetModeBetweenExternalServers(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	env.runner.fn = apacheRunner(t, true)
	st, err := env.manager.SetMode(ctx, "apache", env.request("apache"))
	if err != nil {
		t.Fatalf("SetMode apache: %v", err)
	}
	if st.Mode != "apache" {
		t.Fatalf("mode = %q, want apache", st.Mode)
	}
	if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old nginx bridge survived: %v", err)
	}
	if _, err := os.Stat(env.bridge("apache")); err != nil {
		t.Fatalf("apache bridge: %v", err)
	}
	if got := env.forwarders(); len(got) != 1 || got[0] != false {
		t.Fatalf("forwarder calls = %v, want [false]", got)
	}
	if err := env.manager.Remove(ctx); err != nil {
		t.Fatalf("Remove after switch: %v", err)
	}
	for _, server := range []string{"nginx", "apache"} {
		if _, err := os.Lstat(filepath.Join(env.root, server, "current")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s current survived Remove after a mode switch: %v", server, err)
		}
	}
}

func TestSetModeExternalRestoresOldBridgeOnNewFailure(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	env.runner.fn = func(name string, args []string) ([]byte, error) {
		if name == "apachectl" && len(args) == 1 && args[0] == "configtest" {
			return []byte("Syntax error\n"), errors.New("exit status 1")
		}
		if name == "apache2" && len(args) == 1 {
			return apacheRunner(t, true)(name, args)
		}
		return nil, nil
	}
	if _, err := env.manager.SetMode(ctx, "apache", env.request("apache")); !errors.Is(err, helperproto.ErrVhostConfigInvalid) {
		t.Fatalf("error = %v, want VHOST_CONFIG_INVALID", err)
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("old nginx bridge was not restored: %v", err)
	}
	if _, err := os.Stat(env.bridge("apache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed apache bridge remains: %v", err)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
	for _, name := range []string{"s-1.crt", "s-1.key", "__dashboard__.crt", "__dashboard__.key"} {
		if _, err := os.Stat(filepath.Join(env.root, "tls", name)); err != nil {
			t.Fatalf("shared TLS file %s was removed by the failed switch: %v", name, err)
		}
	}
	reloads := 0
	for _, call := range env.runner.invocations("systemctl") {
		if len(call.args) == 2 && call.args[0] == "reload" && call.args[1] == "nginx" {
			reloads++
		}
	}
	if reloads < 2 {
		t.Fatalf("old nginx was not reloaded during rollback (reloads = %d)", reloads)
	}
}

func TestSetModeBuiltinForwarderFailureRestoresExternal(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	env.manager.Forwarders = func(_ context.Context, enable bool) error {
		env.mu.Lock()
		env.forwarderCalls = append(env.forwarderCalls, enable)
		env.mu.Unlock()
		if enable {
			return errors.New("ports 80/443 are unavailable")
		}
		return nil
	}
	if _, err := env.manager.SetMode(ctx, "builtin", helperproto.VHostSyncRequest{}); !errors.Is(err, helperproto.ErrVhostSyncFailed) {
		t.Fatalf("error = %v, want VHOST_SYNC_FAILED", err)
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("bridge was not restored: %v", err)
	}
	if env.currentTarget("nginx") == "" {
		t.Fatal("release was not restored")
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "s-1.crt")); err != nil {
		t.Fatalf("TLS was not restored: %v", err)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
}

func TestSetModeBuiltinRemovalFailureRestoresExternal(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	releasesDir := filepath.Join(env.root, "nginx", "releases")
	if err := os.Chmod(releasesDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(releasesDir, 0o755) })
	if _, err := env.manager.SetMode(ctx, "builtin", helperproto.VHostSyncRequest{}); err == nil {
		t.Fatal("SetMode builtin succeeded despite an unremovable release")
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("bridge was not restored: %v", err)
	}
	if env.currentTarget("nginx") == "" {
		t.Fatal("current release was not restored")
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
	if got := env.forwarders(); len(got) != 1 || got[0] != false {
		t.Fatalf("forwarders after failed teardown = %v, want [false]", got)
	}
}

func TestRemoveRemovalFailureKeepsExternalState(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	releasesDir := filepath.Join(env.root, "nginx", "releases")
	if err := os.Chmod(releasesDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(releasesDir, 0o755) })
	if err := env.manager.Remove(ctx); err == nil {
		t.Fatal("Remove succeeded despite an unremovable release")
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("bridge was not restored: %v", err)
	}
	if env.currentTarget("nginx") == "" {
		t.Fatal("current release was not restored")
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "s-1.crt")); err != nil {
		t.Fatalf("TLS was removed after a failed teardown: %v", err)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
	if got := env.forwarders(); len(got) != 1 || got[0] != false {
		t.Fatalf("forwarders after failed Remove = %v, want [false]", got)
	}
}

func TestRemoveOnlyOurs(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	unrelated := filepath.Join(env.root, "nginx", "keep.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	unknownRelease := filepath.Join(env.root, "nginx", "releases", "user-release")
	if err := os.MkdirAll(unknownRelease, 0o755); err != nil {
		t.Fatal(err)
	}
	otherTLS := filepath.Join(env.root, "tls", "other.crt")
	if err := os.WriteFile(otherTLS, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.Remove(ctx); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(env.bridge("nginx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge survived Remove: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(env.root, "nginx", "current")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current survived Remove: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	if _, err := os.Stat(unknownRelease); err != nil {
		t.Fatalf("unknown release directory was removed: %v", err)
	}
	if _, err := os.Stat(otherTLS); err != nil {
		t.Fatalf("unrelated TLS file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "s-1.crt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed site cert survived Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "tls", "__dashboard__.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed dashboard key survived Remove: %v", err)
	}
	if got := env.forwarders(); len(got) != 2 || got[1] != true {
		t.Fatalf("forwarder calls = %v, want the restore enable", got)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "builtin" || status.CurrentRelease != "" || status.BridgePath != "" {
		t.Fatalf("status after Remove = %+v", status)
	}
}

func TestRemoveRefusesForeignBridgeAndKeepsMode(t *testing.T) {
	env := newVhostTestEnv(t)
	ctx := context.Background()
	if _, err := env.manager.SetMode(ctx, "nginx", env.request("nginx")); err != nil {
		t.Fatalf("SetMode nginx: %v", err)
	}
	if err := os.WriteFile(env.bridge("nginx"), []byte("user owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.manager.Remove(ctx); !errors.Is(err, helperproto.ErrVhostIncludeMissing) {
		t.Fatalf("error = %v, want VHOST_INCLUDE_MISSING", err)
	}
	if _, err := os.Stat(env.bridge("nginx")); err != nil {
		t.Fatalf("foreign bridge was removed: %v", err)
	}
	if got := env.forwarders(); len(got) != 1 {
		t.Fatalf("forwarders were restored despite refusal: %v", got)
	}
	status, err := env.manager.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "nginx" {
		t.Fatalf("mode = %q, want nginx", status.Mode)
	}
}

func TestServerAutoForwardSuppressedInExternalMode(t *testing.T) {
	parent := t.TempDir()
	statePath := filepath.Join(parent, "state", "vhosts.json")
	m := NewVhostManager(filepath.Join(parent, "vhosts"), statePath, nil, &fakeRunner{}, vhostTestLogger)
	if err := m.writeState(vhostState{Mode: "nginx", Server: "nginx", ConfigOK: true}); err != nil {
		t.Fatalf("write state: %v", err)
	}
	srv, err := NewServer(Config{
		SocketPath:       filepath.Join(parent, "run", "helper.sock"),
		AllowUID:         os.Getuid(),
		VhostRoot:        parent,
		VhostStatePath:   statePath,
		ForwardHTTPPort:  18080,
		ForwardHTTPSPort: 18443,
		ProxyHTTPPort:    19080,
		ProxyHTTPSPort:   19443,
		AutoForward:      true,
		Logger:           vhostTestLogger,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Close() }()
	srv.forwardersMu.Lock()
	started := srv.forwarders != nil
	srv.forwardersMu.Unlock()
	if started {
		t.Fatal("Start enabled forwarders while persisted mode was nginx")
	}
}

func TestRealServerDetectionSkipsWhenUnavailable(t *testing.T) {
	manager := NewVhostManager(t.TempDir(), filepath.Join(t.TempDir(), "vhosts.json"), nil, execRunner{}, vhostTestLogger)
	for _, mode := range []string{vhostModeNginx, vhostModeApache} {
		t.Run(mode, func(t *testing.T) {
			if _, err := manager.detectServer(context.Background(), mode); err != nil {
				t.Skipf("%s is unavailable: %v", mode, err)
			}
		})
	}
}

func TestVhostStateRoundTrip(t *testing.T) {
	env := newVhostTestEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	want := vhostState{
		Mode:           "apache",
		Server:         "apache",
		ServerVersion:  "Apache/2.4",
		BridgePath:     "/etc/apache2/conf-enabled/devstack-vhosts.conf",
		CurrentRelease: "/etc/devstack/vhosts/apache/releases/x",
		ConfigOK:       true,
		LastSync:       time.Now().UTC().Truncate(time.Second),
		TLSOwners:      []string{"s-1", "__dashboard__"},
	}
	if err := env.manager.writeState(want); err != nil {
		t.Fatal(err)
	}
	got, err := env.manager.readState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.CurrentRelease != want.CurrentRelease || len(got.TLSOwners) != 2 {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}
