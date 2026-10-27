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

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
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
