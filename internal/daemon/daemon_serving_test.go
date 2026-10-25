package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// vhostTestRunner answers the fixed nginx commands the helper runs. It never
// touches a system nginx installation.
type vhostTestRunner struct{}

func (vhostTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "nginx":
		return []byte("nginx version: nginx/1.26.0"), nil
	case "systemctl":
		if len(args) == 2 && args[0] == "is-active" {
			return []byte("active\n"), nil
		}
		return []byte("ok"), nil
	}
	return nil, fmt.Errorf("unexpected command %s %v", name, args)
}

// vhostTestHelperConfig returns the helper configuration used by the vhost
// daemon tests. Tests may adjust paths before starting the helper.
func vhostTestHelperConfig(t *testing.T) helper.Config {
	t.Helper()
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowHome := filepath.Join(dir, "allowed-home")
	if err := os.MkdirAll(allowHome, 0o755); err != nil {
		t.Fatal(err)
	}
	return helper.Config{
		SocketPath:       filepath.Join(dir, "run", "helper.sock"),
		HostsPath:        hosts,
		AllowUID:         os.Getuid(),
		AllowHome:        allowHome,
		CADir:            filepath.Join(dir, "pki"),
		TrustDir:         filepath.Join(dir, "trust"),
		ForwardHTTPPort:  freePort(t),
		ForwardHTTPSPort: freePort(t),
		ProxyHTTPPort:    freePort(t),
		ProxyHTTPSPort:   freePort(t),
		VhostRoot:        filepath.Join(dir, "vhost-root"),
		NginxIncludeDir:  filepath.Join(dir, "nginx-includes"),
		Runner:           vhostTestRunner{},
	}
}

// startVhostTestHelper runs a real helper with the vhost manager enabled, a
// temporary release/include tree, and a fake command runner.
func startVhostTestHelper(t *testing.T) string {
	t.Helper()
	return startVhostTestHelperWithConfig(t, vhostTestHelperConfig(t))
}

func startVhostTestHelperWithConfig(t *testing.T, cfg helper.Config) string {
	t.Helper()
	srv, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatalf("new vhost helper: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start vhost helper: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	return cfg.SocketPath
}

func setConfigServingMode(t *testing.T, home, mode string) {
	t.Helper()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServingMode = mode
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

// requireDefaultUIPort skips external-mode tests when the fixed dashboard port
// is already in use. The helper only accepts a 7070 dashboard backend.
func requireDefaultUIPort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:7070")
	if err != nil {
		t.Skipf("dashboard port 7070 is unavailable: %v", err)
	}
	_ = listener.Close()
}

func vhostStatusRequest(t *testing.T, client *http.Client) helperproto.VhostStatusResponse {
	t.Helper()
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/vhosts/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vhost status = %d %s", resp.StatusCode, data)
	}
	var status helperproto.VhostStatusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestDaemonBuiltinStartsProxy(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)
	status := vhostStatusRequest(t, client)
	if status.Mode != "builtin" || !status.ConfigOK {
		t.Fatalf("status = %+v", status)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", opts.HTTPPort), time.Second)
	if err != nil {
		t.Fatalf("builtin proxy HTTP listener is not running: %v", err)
	}
	_ = conn.Close()
}

func TestDaemonExternalSkipsProxyAndReconciles(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	setConfigServingMode(t, home, config.ServingModeNginx)
	opts := testOptions(t, home, startVhostTestHelper(t))
	requireDefaultUIPort(t)
	opts.UIPort = 7070
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)
	status := vhostStatusRequest(t, client)
	if status.Mode != "nginx" || !status.ConfigOK || status.LastSync.IsZero() {
		t.Fatalf("status = %+v", status)
	}

	for _, port := range []int{opts.HTTPPort, opts.HTTPSPort} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("built-in proxy port %d is listening in external mode", port)
		}
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := createStaticSite(t, client, "blog", project)
	if site.HTTPPort != 0 || site.Status != model.StatusRunning {
		t.Fatalf("external static site = %+v", site)
	}

	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings = %d %s", resp.StatusCode, data)
	}
	var settings struct {
		ServingMode string `json:"serving_mode"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.ServingMode != config.ServingModeNginx {
		t.Fatalf("persisted serving_mode = %q", settings.ServingMode)
	}
}

func TestDaemonModeSwitchKeepsProxyAndSettingsFresh(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	setConfigServingMode(t, home, config.ServingModeNginx)
	opts := testOptions(t, home, startVhostTestHelper(t))
	requireDefaultUIPort(t)
	opts.UIPort = 7070
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	resp, data := apiRequest(t, client, http.MethodPost, "/api/v1/vhosts/mode", map[string]string{"mode": "builtin"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to builtin = %d %s", resp.StatusCode, data)
	}
	var status helperproto.VhostStatusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status.Mode != "builtin" {
		t.Fatalf("mode status = %+v", status)
	}

	resp, data = apiRequest(t, client, http.MethodGet, "/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings = %d %s", resp.StatusCode, data)
	}
	var settings struct {
		ServingMode string `json:"serving_mode"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.ServingMode != "builtin" {
		t.Fatalf("settings serving_mode = %q, want builtin", settings.ServingMode)
	}

	// The request context that triggered the switch is canceled once the
	// response is consumed. The built-in proxy must outlive it.
	time.Sleep(300 * time.Millisecond)
	for _, port := range []int{opts.HTTPPort, opts.HTTPSPort} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			t.Fatalf("built-in proxy port %d stopped after the mode switch: %v", port, err)
		}
		_ = conn.Close()
	}
}

func TestDaemonStartupWithBrokenStaticRoot(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	setConfigServingMode(t, home, config.ServingModeNginx)
	opts := testOptions(t, home, startVhostTestHelper(t))
	requireDefaultUIPort(t)
	opts.UIPort = 7070

	run := startDaemon(t, opts)
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := createStaticSite(t, client, "blog", project)
	run.stop(t)
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}

	restarted := startDaemon(t, opts)
	defer restarted.stop(t)
	client = unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)
	if status := vhostStatusRequest(t, client); status.Mode != "nginx" {
		t.Fatalf("status after restart = %+v", status)
	}
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/sites/"+site.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get broken site = %d %s", resp.StatusCode, data)
	}
	var stored model.Site
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.StatusError {
		t.Fatalf("broken site status = %q, want error", stored.Status)
	}
}

func TestDaemonStartsAndRecoversWhenServingReconcileFails(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	setConfigServingMode(t, home, config.ServingModeNginx)
	helperCfg := vhostTestHelperConfig(t)
	// A user-managed bridge without the devstack header makes the initial
	// external sync fail. The daemon must still publish its API so the user can
	// recover with `vhost use builtin`.
	if err := os.MkdirAll(helperCfg.NginxIncludeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperCfg.NginxIncludeDir, "devstack-vhosts.conf"),
		[]byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t, home, startVhostTestHelperWithConfig(t, helperCfg))
	requireDefaultUIPort(t)
	opts.UIPort = 7070

	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)
	if status := vhostStatusRequest(t, client); status.Mode != config.ServingModeBuiltin {
		t.Fatalf("helper mode after failed reconcile = %+v", status)
	}

	// Doctor and the mode switch must remain usable.
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/doctor", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doctor after failed reconcile = %d %s", resp.StatusCode, data)
	}
	resp, data = apiRequest(t, client, http.MethodPost, "/api/v1/vhosts/mode", map[string]string{"mode": config.ServingModeBuiltin})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recover to builtin = %d %s", resp.StatusCode, data)
	}
	var status helperproto.VhostStatusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status.Mode != config.ServingModeBuiltin {
		t.Fatalf("recovered mode = %+v", status)
	}
}

func TestRenewExternalSyncsVhosts(t *testing.T) {
	useBuiltBinary(t)
	previous := renewInterval
	renewInterval = 100 * time.Millisecond
	t.Cleanup(func() { renewInterval = previous })

	home := t.TempDir()
	setConfigServingMode(t, home, config.ServingModeNginx)
	opts := testOptions(t, home, startVhostTestHelper(t))
	requireDefaultUIPort(t)
	opts.UIPort = 7070
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := createStaticSite(t, client, "blog", project)
	before := vhostStatusRequest(t, client).LastSync

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rec, err := st.GetCertBySite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.NotAfter = time.Now().Add(time.Hour).UTC()
	if err := st.SaveCert(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var renewed model.CertRecord
	for {
		status := vhostStatusRequest(t, client)
		renewed, err = st.GetCertBySite(context.Background(), site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.LastSync.After(before) && renewed.NotAfter.After(rec.NotAfter.Add(24*time.Hour)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("certificate renewal did not complete; last sync %v, before %v, not-after %v", status.LastSync, before, renewed.NotAfter)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestDaemonDoctorReflectsRuntimeServingMode(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startVhostTestHelper(t))
	requireDefaultUIPort(t)
	opts.UIPort = 7070
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)

	assertDoctorServingMode(t, client, config.ServingModeBuiltin, true)

	resp, data := apiRequest(t, client, http.MethodPost, "/api/v1/vhosts/mode", map[string]string{"mode": config.ServingModeNginx})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to nginx = %d %s", resp.StatusCode, data)
	}
	assertDoctorServingMode(t, client, config.ServingModeNginx, false)

	resp, data = apiRequest(t, client, http.MethodPost, "/api/v1/vhosts/mode", map[string]string{"mode": config.ServingModeBuiltin})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to builtin = %d %s", resp.StatusCode, data)
	}
	assertDoctorServingMode(t, client, config.ServingModeBuiltin, true)
}

func assertDoctorServingMode(t *testing.T, client *http.Client, want string, vhostServerSkipped bool) {
	t.Helper()
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/doctor", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doctor = %d %s", resp.StatusCode, data)
	}
	var report doctor.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}

	serving := findDoctorCheck(t, report, "serving-mode")
	if serving.Status != doctor.StatusOK {
		t.Fatalf("serving-mode = %+v, want ok for %s", serving, want)
	}
	if !strings.Contains(serving.Detail, want) {
		t.Fatalf("serving-mode detail = %q, want it to mention %s", serving.Detail, want)
	}

	server := findDoctorCheck(t, report, "vhost-server")
	if skipped := server.Status == doctor.StatusSkipped; skipped != vhostServerSkipped {
		t.Fatalf("vhost-server = %+v, skipped = %v, want skipped = %v", server, skipped, vhostServerSkipped)
	}
	if !vhostServerSkipped {
		tlsCheck := findDoctorCheck(t, report, "vhost-tls")
		if tlsCheck.Status != doctor.StatusOK {
			t.Fatalf("vhost-tls = %+v, want ok from the helper metadata", tlsCheck)
		}
	}
}

func findDoctorCheck(t *testing.T, report doctor.Report, id string) doctor.Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("doctor report has no %s check: %+v", id, report.Checks)
	return doctor.Check{}
}
