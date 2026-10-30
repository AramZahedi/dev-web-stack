//go:build windows

package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

func TestWindowsRunOpensSQLiteWritesLogAndCleansReadiness(t *testing.T) {
	home := t.TempDir()
	httpPort, httpsPort, uiPort := freeTCPPort(t), freeTCPPort(t), freeTCPPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Home: home, HTTPPort: httpPort, HTTPSPort: httpsPort, UIPort: uiPort})
	}()
	ready := filepath.Join(home, "run", "daemon.ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before readiness: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("daemon did not publish readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
	for _, path := range []string{filepath.Join(home, "state.db"), filepath.Join(home, "logs", "daemon.log")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("required Windows daemon file %q: %v", path, err)
		}
	}
	if _, err := os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("readiness marker after shutdown error = %v, want not-exist", err)
	}
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != home && strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("Windows daemon created dot-prefixed name %q", entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRunPersistsAppliedPortsAndClearsRestartRequired(t *testing.T) {
	home := t.TempDir()
	seed, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := seed.SetSetting(ctx, "restart_required", "true"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SetSetting(ctx, "ui_port", "7070"); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	httpPort, httpsPort, uiPort := freeTCPPort(t), freeTCPPort(t), freeTCPPort(t)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Options{Home: home, HTTPPort: httpPort, HTTPSPort: httpsPort, UIPort: uiPort})
	}()
	ready := filepath.Join(home, "run", "daemon.ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before readiness: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("daemon did not publish readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/settings", uiPort))
	if err != nil {
		cancel()
		<-done
		t.Fatalf("GET settings: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		<-done
		t.Fatalf("GET settings status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var settings struct {
		HTTPPort        int  `json:"http_port"`
		HTTPSPort       int  `json:"https_port"`
		UIPort          int  `json:"ui_port"`
		RestartRequired bool `json:"restart_required"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&settings)
	_ = resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}
	if decodeErr != nil {
		t.Fatalf("decode settings: %v", decodeErr)
	}
	if settings.HTTPPort != httpPort {
		t.Errorf("settings http_port = %d, want running port %d", settings.HTTPPort, httpPort)
	}
	if settings.HTTPSPort != httpsPort {
		t.Errorf("settings https_port = %d, want running port %d", settings.HTTPSPort, httpsPort)
	}
	if settings.UIPort != uiPort {
		t.Errorf("settings ui_port = %d, want running port %d", settings.UIPort, uiPort)
	}
	if settings.RestartRequired {
		t.Error("settings restart_required = true after startup with the saved ports applied")
	}
}

func TestWindowsSettingsArePersistedBeforeDashboardServes(t *testing.T) {
	home := t.TempDir()
	seed, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := seed.SetSetting(ctx, "restart_required", "true"); err != nil {
		t.Fatal(err)
	}
	if err := seed.SetSetting(ctx, "ui_port", "7070"); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	pipePath := fmt.Sprintf(`\\.\pipe\devstack-settings-%d`, time.Now().UnixNano())
	pipe, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := pipe.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	httpPort, httpsPort, uiPort := freeTCPPort(t), freeTCPPort(t), freeTCPPort(t)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Options{
			Home:         home,
			HelperSocket: pipePath,
			HTTPPort:     httpPort,
			HTTPSPort:    httpsPort,
			UIPort:       uiPort,
		})
	}()

	var helperConn net.Conn
	select {
	case helperConn = <-accepted:
		defer helperConn.Close()
	case err := <-done:
		t.Fatalf("Run returned before dialing the helper: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("daemon did not dial the helper pipe")
	}

	// The helper call hangs, so the daemon is still in bootstrap. The dashboard
	// must already expose the applied ports and cleared restart_required; a
	// post-bootstrap mirror write would instead return the stale seeded values
	// here and could overwrite a concurrent settings save.
	client := &http.Client{Timeout: time.Second}
	var settings struct {
		UIPort          int  `json:"ui_port"`
		RestartRequired bool `json:"restart_required"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/settings", uiPort))
		if err == nil {
			decodeErr := json.NewDecoder(resp.Body).Decode(&settings)
			_ = resp.Body.Close()
			if decodeErr != nil {
				cancel()
				<-done
				t.Fatalf("decode settings: %v", decodeErr)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run returned before the dashboard served: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("dashboard did not serve during the blocked helper bootstrap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run after cancellation: %v", err)
	}
	if settings.UIPort != uiPort {
		t.Errorf("settings ui_port = %d, want running port %d while bootstrap is blocked", settings.UIPort, uiPort)
	}
	if settings.RestartRequired {
		t.Error("settings restart_required = true while the dashboard is already serving")
	}
}

func TestWindowsSQLiteHomeRequiresNTFS(t *testing.T) {
	original := windowsFileSystemForPath
	windowsFileSystemForPath = func(string) (string, error) { return "exFAT", nil }
	t.Cleanup(func() { windowsFileSystemForPath = original })
	if err := validateWindowsHome(t.TempDir()); err == nil || !strings.Contains(err.Error(), "NTFS") {
		t.Fatalf("validateWindowsHome error = %v, want NTFS requirement", err)
	}
}

func TestWindowsEnsureArgsPreserveRunOverrides(t *testing.T) {
	got := windowsDaemonArgs(Options{
		Home: `C:\devstack-test`, HelperSocket: `\\.\pipe\devstack-helper-test`,
		HostsPath: `C:\temp\hosts`, HTTPPort: 18080, HTTPSPort: 18443, UIPort: 17070, LogLevel: "debug",
	})
	want := []string{"daemon", "run", "--home", `C:\devstack-test`, "--helper-socket", `\\.\pipe\devstack-helper-test`, "--hosts", `C:\temp\hosts`, "--http-port", "18080", "--https-port", "18443", "--ui-port", "17070", "--log-level", "debug"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows daemon args = %#v, want %#v", got, want)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

type testSigner struct {
	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate
	caPEM  []byte
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "devstack test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testSigner{
		caKey:  key,
		caCert: cert,
		caPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

func (s *testSigner) RootPEM() []byte {
	return append([]byte(nil), s.caPEM...)
}

func (s *testSigner) SignCertificate(_ context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return helperproto.SignResponse{}, errors.New("test signer: CSR is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	if err := csr.CheckSignature(); err != nil {
		return helperproto.SignResponse{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0]},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              domains,
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, tmpl, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	return helperproto.SignResponse{
		CertPEM:  leafPEM,
		Serial:   serial.Text(16),
		NotAfter: tmpl.NotAfter,
	}, nil
}

func TestWindowsWireDashboardTLS(t *testing.T) {
	home := t.TempDir()
	pkiDir := filepath.Join(home, "pki")
	if err := os.MkdirAll(pkiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	signer := newTestSigner(t)

	dbPath := filepath.Join(home, "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	certManager := certs.NewManager(st, signer, pkiDir, nil)
	router := proxy.NewRouter()
	cfg := config.Default()
	cfg.UIPort = freeTCPPort(t)

	// Before wiring, router has no certificate
	if _, err := router.Certificate(&tls.ClientHelloInfo{ServerName: "devstack.test"}); err == nil {
		t.Fatal("router should not have certificate before wiring")
	}

	// Wire dashboard TLS
	if err := wireDashboardTLS(context.Background(), router, certManager, cfg, nil); err != nil {
		t.Fatalf("wireDashboardTLS: %v", err)
	}

	// Now router must have certificate for devstack.test and as default
	cert, err := router.Certificate(&tls.ClientHelloInfo{ServerName: "devstack.test"})
	if err != nil {
		t.Fatalf("router.Certificate(devstack.test): %v", err)
	}
	if cert == nil {
		t.Fatal("router returned nil certificate for devstack.test")
	}

	defaultCert, err := router.Certificate(&tls.ClientHelloInfo{ServerName: "unknown.test"})
	if err != nil {
		t.Fatalf("router.Certificate(unknown.test): %v", err)
	}
	if defaultCert == nil {
		t.Fatal("router returned nil default certificate")
	}

	// Start proxy with this router on a free HTTPS port
	httpsPort := freeTCPPort(t)
	httpsAddr, err := proxy.WindowsPublicListenAddr(httpsPort)
	if err != nil {
		t.Fatal(err)
	}
	srv := proxy.NewServer(proxy.ServerConfig{
		HTTPSAddr: httpsAddr,
		Router:    router,
	})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())

	// Connect with TLS client and verify TLS handshake succeeds with leaf cert
	rootPool := x509.NewCertPool()
	rootPool.AppendCertsFromPEM(signer.RootPEM())
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    rootPool,
				ServerName: "devstack.test",
			},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := tlsClient.Get(fmt.Sprintf("https://127.0.0.1:%d/", httpsPort))
	if err != nil {
		t.Fatalf("TLS connection to dashboard port failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		t.Fatal("TLS handshake succeeded but PeerCertificates is empty")
	}
	if resp.TLS.PeerCertificates[0].Subject.CommonName != "devstack.test" {
		t.Fatalf("CommonName = %q, want devstack.test", resp.TLS.PeerCertificates[0].Subject.CommonName)
	}
}

type fakeDiagnosticWSLRunner struct {
	output []byte
	err    error
}

func (r *fakeDiagnosticWSLRunner) Run(context.Context, ...string) ([]byte, error) {
	return r.output, r.err
}

func TestWindowsDiagnosticsAndDoctorServices(t *testing.T) {
	runner := &fakeDiagnosticWSLRunner{output: []byte("ok\n")}
	diag := &windowsDiagnostics{
		runner: runner,
	}
	ctx := context.Background()
	if err := diag.CheckWSL(ctx, "devstack"); err != nil {
		t.Fatalf("CheckWSL: %v", err)
	}
	if err := diag.CheckDocker(ctx, "devstack"); err != nil {
		t.Fatalf("CheckDocker: %v", err)
	}
	if err := diag.CheckLocalhostForwarding(ctx, "devstack", 8080); err != nil {
		t.Fatalf("CheckLocalhostForwarding: %v", err)
	}

	disk := windowsDiskChecker{}
	free, err := disk.FreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free == 0 {
		t.Fatal("FreeBytes returned 0 for tempdir")
	}

	probe := portProbe{}
	port := freeTCPPort(t)
	if err := probe.Listenable(net.JoinHostPort("127.0.0.1", fmt.Sprint(port))); err != nil {
		t.Fatalf("Listenable: %v", err)
	}
}

func TestWindowsHelperAdapterRejectsSilentHostsSyncWithoutHelper(t *testing.T) {
	adapter := &helperAdapter{}
	err := adapter.SyncHosts(context.Background(), []string{"blog.test"})
	if err == nil {
		t.Fatal("SyncHosts reported success without a helper; the hosts file was not updated")
	}
	if !errors.Is(err, servicecontrol.ErrHelperUnavailable) {
		t.Fatalf("SyncHosts error = %v, want ErrHelperUnavailable", err)
	}
}

func TestWindowsHelperAdapterTruthfulness(t *testing.T) {
	ctx := context.Background()
	req := helperproto.VHostSyncRequest{Mode: "nginx"}

	t.Run("nil client returns ErrNotImplemented and does not fabricate success", func(t *testing.T) {
		adapter := &helperAdapter{}

		// VhostStatus must return an error and NOT claim success
		status, err := adapter.VhostStatus(ctx)
		if err == nil {
			t.Fatalf("expected error from VhostStatus, got %+v", status)
		}
		if !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("expected ErrNotImplemented, got: %v", err)
		}
		if status.ConfigOK {
			t.Fatal("VhostStatus fabricated ConfigOK: true")
		}

		// SyncVhosts must return an error
		if err := adapter.SyncVhosts(ctx, req); err == nil {
			t.Fatal("expected error from SyncVhosts, got nil")
		}

		// SetServingMode with external mode must return an error and NOT claim success
		status, err = adapter.SetServingMode(ctx, "nginx", req)
		if err == nil {
			t.Fatalf("expected error from SetServingMode(nginx), got %+v", status)
		}
		if !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("expected ErrNotImplemented, got: %v", err)
		}
		if status.ConfigOK {
			t.Fatal("SetServingMode(nginx) fabricated ConfigOK: true")
		}

		// SetServingMode with builtin succeeds
		status, err = adapter.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{})
		if err != nil {
			t.Fatalf("SetServingMode(builtin) error = %v", err)
		}
		if status.Mode != config.ServingModeBuiltin || !status.ConfigOK {
			t.Fatalf("status = %+v, want builtin with ConfigOK: true", status)
		}
	})

	t.Run("client without vhost endpoints returns ErrNotImplemented and does not fabricate success", func(t *testing.T) {
		pipe := fmt.Sprintf(`\\.\pipe\test-vhost-truth-%d-%d`, os.Getpid(), freeTCPPort(t))
		ln, err := ipc.Listen(ipc.NamedPipeEndpoint(pipe))
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		srv := &http.Server{Handler: http.NotFoundHandler()}
		go func() { _ = srv.Serve(ln) }()
		defer srv.Close()

		client, err := helperclient.Dial(pipe)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		adapter := &helperAdapter{}
		adapter.set(client)

		// VhostStatus
		status, err := adapter.VhostStatus(ctx)
		if err == nil {
			t.Fatalf("expected error from VhostStatus, got %+v", status)
		}
		if !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("expected ErrNotImplemented, got: %v", err)
		}
		if status.ConfigOK {
			t.Fatal("VhostStatus fabricated ConfigOK: true")
		}

		// SyncVhosts
		if err := adapter.SyncVhosts(ctx, req); err == nil {
			t.Fatal("expected error from SyncVhosts, got nil")
		}

		// SetServingMode(nginx)
		status, err = adapter.SetServingMode(ctx, "nginx", req)
		if err == nil {
			t.Fatalf("expected error from SetServingMode(nginx), got %+v", status)
		}
		if !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("expected ErrNotImplemented, got: %v", err)
		}
		if status.ConfigOK {
			t.Fatal("SetServingMode(nginx) fabricated ConfigOK: true")
		}

		// SetServingMode(builtin)
		status, err = adapter.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{})
		if err != nil {
			t.Fatalf("SetServingMode(builtin) error = %v", err)
		}
		if status.Mode != config.ServingModeBuiltin || !status.ConfigOK {
			t.Fatalf("status = %+v, want builtin with ConfigOK: true", status)
		}
	})
}

func TestWaitWindowsReadyFailsFastOnChildExit(t *testing.T) {
	home := t.TempDir()
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "daemon.log")
	if err := os.WriteFile(logFile, []byte("fatal error: bind address already in use: 7070\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitCh := make(chan error, 1)
	waitCh <- errors.New("exit status 1")

	start := time.Now()
	err := waitWindowsReady(context.Background(), home, 30*time.Second, waitCh)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from waitWindowsReady when child exits, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waitWindowsReady took %v, want fail-fast in < 2s", elapsed)
	}
	if !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Fatalf("error = %q, want 'exited before becoming ready'", err.Error())
	}
	if !strings.Contains(err.Error(), "bind address already in use") {
		t.Fatalf("error = %q, want tail log containing bind address error", err.Error())
	}
}

func TestHelperAdapterLazyReconnect(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\test-lazy-helper-%d-%d`, os.Getpid(), freeTCPPort(t))
	adapter := &helperAdapter{socketPath: pipe}

	// Before listener is up, get() should return nil
	if client := adapter.get(); client != nil {
		// helperclient.Dial creates the http.Client structure without connecting
		t.Logf("get() returned initial client struct: %v", client)
	}

	ln, err := ipc.Listen(ipc.NamedPipeEndpoint(pipe))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"1.0","hosts_synced":true}` + "\n"))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	client := adapter.get()
	if client == nil {
		t.Fatal("adapter.get() returned nil after listener started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("client.Status: %v", err)
	}
	if status.Version != "1.0" {
		t.Fatalf("status.Version = %q, want 1.0", status.Version)
	}
}

func TestWindowsLogTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	// Missing log
	if got := tailWindowsLog(logPath); !strings.Contains(got, "unavailable") {
		t.Errorf("missing log tail = %q, want 'unavailable'", got)
	}

	// Empty log
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tailWindowsLog(logPath); got != "(daemon log is empty)" {
		t.Errorf("empty log tail = %q, want '(daemon log is empty)'", got)
	}

	// Normal content
	content := "line 1\nline 2\nline 3"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tailWindowsLog(logPath); got != content {
		t.Errorf("log tail = %q, want %q", got, content)
	}
}

func TestWindowsAgentServiceAdapterFallbackOnUnreachable(t *testing.T) {
	adapter := &windowsAgentServiceAdapter{
		home:    t.TempDir(),
		baseURL: "https://127.0.0.1:9",
		distro:  "invalid-test-distro",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	resp, err := adapter.ServiceStatus(ctx, "nginx")
	if err != nil {
		t.Fatalf("ServiceStatus returned error: %v", err)
	}
	if resp.ID != "nginx" {
		t.Errorf("resp.ID = %q, want nginx", resp.ID)
	}
	if resp.State != string(services.StateNotInstalled) {
		t.Errorf("resp.State = %q, want %q", resp.State, services.StateNotInstalled)
	}
}

func TestIsConnectionRefused(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connectex: No connection could be made because the target machine actively refused it."), true},
		{errors.New("dial tcp 127.0.0.1:7443: connect: connection refused"), true},
		{errors.New("WSL agent is not connected"), true},
		{errors.New("file not found"), false},
	}
	for _, c := range cases {
		if got := isConnectionRefused(c.err); got != c.want {
			t.Errorf("isConnectionRefused(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestWindowsHelperAdapterToolLifecycle(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	adapter := &helperAdapter{home: home}
	ctx := context.Background()

	// Initial status when no shims exist
	resp, err := adapter.ToolStatus(ctx)
	if err != nil {
		t.Fatalf("ToolStatus failed: %v", err)
	}
	if len(resp.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(resp.Tools))
	}
	for _, tool := range resp.Tools {
		if tool.Installed || tool.Managed {
			t.Errorf("tool %s initially reported installed=%v managed=%v, want false", tool.ID, tool.Installed, tool.Managed)
		}
	}

	// Create shims: php.cmd and composer.cmd
	if err := os.WriteFile(filepath.Join(binDir, "php.cmd"), []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "composer.cmd"), []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp, err = adapter.ToolStatus(ctx)
	if err != nil {
		t.Fatalf("ToolStatus with shims failed: %v", err)
	}
	for _, tool := range resp.Tools {
		if !tool.Installed || !tool.Managed {
			t.Errorf("tool %s with shim reported installed=%v managed=%v, want true", tool.ID, tool.Installed, tool.Managed)
		}
		if tool.ID == helperproto.ToolPHPDefault && !strings.HasSuffix(tool.WrapperPath, "php.cmd") {
			t.Errorf("php-default wrapper path = %q, want ending in php.cmd", tool.WrapperPath)
		}
		if tool.ID == helperproto.ToolComposer && !strings.HasSuffix(tool.WrapperPath, "composer.cmd") {
			t.Errorf("composer wrapper path = %q, want ending in composer.cmd", tool.WrapperPath)
		}
	}

	// Remove composer
	remResp, err := adapter.ToolRemove(ctx, helperproto.ToolComposer)
	if err != nil {
		t.Fatalf("ToolRemove(composer) failed: %v", err)
	}
	if remResp.Installed || remResp.Managed {
		t.Errorf("ToolRemove(composer) returned installed=%v managed=%v, want false", remResp.Installed, remResp.Managed)
	}
	if _, err := os.Stat(filepath.Join(binDir, "composer.cmd")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("composer.cmd still exists after remove")
	}

	// Remove php-default
	remPHP, err := adapter.ToolRemove(ctx, helperproto.ToolPHPDefault)
	if err != nil {
		t.Fatalf("ToolRemove(php-default) failed: %v", err)
	}
	if remPHP.Installed || remPHP.Managed {
		t.Errorf("ToolRemove(php-default) returned installed=%v managed=%v, want false", remPHP.Installed, remPHP.Managed)
	}
	if _, err := os.Stat(filepath.Join(binDir, "php.cmd")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("php.cmd still exists after remove")
	}
}

func TestWindowsHelperAdapterUsesNodeBuildPathMetadata(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	const (
		legacyPath = "/data/devstack/runtimes/node@22.11.0"
		modernPath = "/data/devstack/runtimes/node/22.11.0-linux-x64"
	)
	if err := st.UpsertRuntime(ctx, model.RuntimeBuild{
		ID:          "node@22.11.0",
		Kind:        "node",
		Version:     "22.11.0",
		Backend:     model.BackendWSL,
		Path:        legacyPath,
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	paths := config.NewPaths(home)
	if err := os.MkdirAll(tooling.NodeDefaultToolDir(paths), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tooling.NodeDefaultVersionPath(paths), []byte("node@22.11.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tooling.NodeDefaultBuildPathPath(paths), []byte(modernPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapter := &helperAdapter{
		home:      home,
		nodeTools: newWindowsToolService(home, "devstack", st),
	}
	installed, err := adapter.ToolInstall(ctx, helperproto.ToolNodeDefault, false)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Target != modernPath+"/bin/node" {
		t.Fatalf("target = %q, want the BUILD_PATH target %q", installed.Target, modernPath+"/bin/node")
	}
	data, err := os.ReadFile(filepath.Join(home, "bin", "node.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), modernPath+"/bin/node") {
		t.Fatalf("node.cmd does not use the BUILD_PATH build:\n%s", data)
	}
	if strings.Contains(string(data), legacyPath+"/bin/node") {
		t.Fatalf("node.cmd fell back to the legacy build:\n%s", data)
	}
}

func TestWindowsHelperAdapterManagesNodeShims(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.UpsertRuntime(ctx, model.RuntimeBuild{
		ID:          "node@22.11.0",
		Kind:        "node",
		Version:     "22.11.0",
		Backend:     model.BackendWSL,
		Path:        "/data/devstack/runtimes/node@22.11.0",
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	adapter := &helperAdapter{
		home:      home,
		nodeTools: newWindowsToolService(home, "devstack", st),
	}
	installed, err := adapter.ToolInstall(ctx, helperproto.ToolNodeDefault, false)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Managed || installed.WrapperPath != filepath.Join(home, "bin", "node.cmd") {
		t.Fatalf("node install status = %+v", installed)
	}
	status, err := adapter.ToolStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Tools) != 3 {
		t.Fatalf("tools = %+v, want php, composer, and node", status.Tools)
	}
	if _, err := adapter.ToolRemove(ctx, helperproto.ToolNodeDefault); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if _, err := os.Stat(filepath.Join(home, "bin", name+".cmd")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s.cmd still exists after remove: %v", name, err)
		}
	}
}
