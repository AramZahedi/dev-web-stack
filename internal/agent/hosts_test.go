package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHostsManagerWritesManagedTestMappingsAndPreservesLocalhost(t *testing.T) {
	manager, hostsPath, configPath := newHostsTestManager(t, "127.0.0.1 localhost\n::1 localhost ip6-localhost\n192.0.2.20 unrelated.example\n", "[boot]\nsystemd=true\n")
	changed, err := manager.Sync([]string{"zeta.test", "alpha.test", "alpha.test"}, "172.20.0.1")
	if err != nil || !changed {
		t.Fatalf("Sync changed=%v err=%v", changed, err)
	}
	data, _ := os.ReadFile(hostsPath)
	hosts := string(data)
	for _, want := range []string{"127.0.0.1 localhost", "::1 localhost ip6-localhost", "192.0.2.20 unrelated.example", "127.0.0.1 alpha.test", "127.0.0.1 zeta.test", "172.20.0.1 devstack-windows.internal"} {
		if !strings.Contains(hosts, want) {
			t.Errorf("hosts file missing %q:\n%s", want, hosts)
		}
	}
	if strings.Count(hosts, "alpha.test") != 1 {
		t.Fatalf("duplicate domain mapping in hosts:\n%s", hosts)
	}
	config, _ := os.ReadFile(configPath)
	if !strings.Contains(string(config), "generateHosts=false") {
		t.Fatalf("wsl.conf does not disable generated hosts:\n%s", config)
	}
	changed, err = manager.Sync([]string{"alpha.test", "zeta.test"}, "172.20.0.1")
	if err != nil || changed {
		t.Fatalf("idempotent Sync changed=%v err=%v", changed, err)
	}
}

func TestHostsManagerPreservesUnrelatedLinesAndRemovesOnUninstall(t *testing.T) {
	manager, hostsPath, _ := newHostsTestManager(t, "127.0.0.1 localhost\n# user's comment\n10.0.0.9 keep.me\n", "")
	if _, err := manager.Sync([]string{"site.test"}, "172.19.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hostsPath)
	hosts := string(data)
	if strings.Contains(hosts, hostsBlockStart) || strings.Contains(hosts, "site.test") || strings.Contains(hosts, "devstack-windows.internal") {
		t.Fatalf("managed hosts entries remain after removal:\n%s", hosts)
	}
	for _, keep := range []string{"127.0.0.1 localhost", "# user's comment", "10.0.0.9 keep.me"} {
		if !strings.Contains(hosts, keep) {
			t.Errorf("unrelated hosts line %q was lost:\n%s", keep, hosts)
		}
	}
}

func TestHostsManagerRejectsUnprivilegedOrInvalidInputs(t *testing.T) {
	manager, _, _ := newHostsTestManager(t, "127.0.0.1 localhost\n", "")
	manager.isRoot = func() bool { return false }
	if _, err := manager.Sync([]string{"site.test"}, "172.16.0.1"); !errors.Is(err, ErrRootRequired) {
		t.Fatalf("unprivileged Sync error = %v", err)
	}
	manager.isRoot = func() bool { return true }
	for _, domains := range [][]string{{"evil.example"}, {"bad.test\n127.0.0.1 injected"}, {"localhost"}} {
		if _, err := manager.Sync(domains, "172.16.0.1"); err == nil {
			t.Errorf("Sync accepted invalid domains %#v", domains)
		}
	}
	if _, err := manager.Sync([]string{"ok.test"}, "8.8.8.8"); err == nil {
		t.Fatal("Sync accepted a non-private host gateway")
	}
}

func TestInstallCABundleWritesPublicCertAndEnvironment(t *testing.T) {
	root := t.TempDir()
	bundle := testCABundle(t)
	path, err := InstallCABundle(root, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "certs", "ca-bundle.crt") {
		t.Fatalf("CA path = %q", path)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(bundle) {
		t.Fatal("CA bundle bytes changed")
	}
	env, err := CAEnvironment(root, map[string]string{"PATH": "/usr/bin", "PHP_INI_SCAN_DIR": "/etc/php/conf.d"})
	if err != nil {
		t.Fatal(err)
	}
	if env["SSL_CERT_FILE"] != path || env["CURL_CA_BUNDLE"] != path || env["NODE_EXTRA_CA_CERTS"] != path || !strings.Contains(env["PHP_INI_SCAN_DIR"], "php-conf.d") || env["PATH"] != "/usr/bin" {
		t.Fatalf("CA environment = %#v", env)
	}
	phpIni, err := os.ReadFile(filepath.Join(root, "certs", "php-conf.d", "99-devstack-ca.ini"))
	if err != nil || !strings.Contains(string(phpIni), "openssl.cafile="+path) || !strings.Contains(string(phpIni), "curl.cainfo="+path) {
		t.Fatalf("PHP CA ini = %q, %v", phpIni, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("public CA bundle mode = %04o", info.Mode().Perm())
		}
	}
}

func TestInstallCABundleRejectsPrivateKeyAndNonCA(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallCABundle(root, []byte("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n")); err == nil {
		t.Fatal("InstallCABundle accepted private key input")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "leaf"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCABundle(root, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err == nil {
		t.Fatal("InstallCABundle accepted a non-CA certificate")
	}
}

func newHostsTestManager(t *testing.T, hosts, config string) (*HostsManager, string, string) {
	t.Helper()
	root := t.TempDir()
	hostsPath := filepath.Join(root, "hosts")
	configPath := filepath.Join(root, "wsl.conf")
	if err := os.WriteFile(hostsPath, []byte(hosts), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewHostsManager(hostsPath, configPath)
	manager.isRoot = func() bool { return true }
	return manager, hostsPath, configPath
}

func testCABundle(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(8), Subject: pkix.Name{CommonName: "devstack-test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
