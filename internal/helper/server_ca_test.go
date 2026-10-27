//go:build !windows

package helper_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/testcert"
)

type serverFakeRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *serverFakeRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
	return nil, nil
}

func (r *serverFakeRunner) called(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if call == name {
			return true
		}
	}
	return false
}

func trustTargetsByName(targets []helper.Target) map[string]helper.Target {
	out := make(map[string]helper.Target, len(targets))
	for _, target := range targets {
		out[target.Name] = target
	}
	return out
}

func TestHelperCARoutes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	anchorDir := t.TempDir()
	caDir := filepath.Join(t.TempDir(), "ca")
	runner := &serverFakeRunner{}
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:  os.Getuid(),
		AllowHome: home,
		CADir:     caDir,
		TrustDir:  anchorDir,
		Runner:    runner,
	})
	c := dialClient(t, socketPath)
	ctx := context.Background()

	ca, err := c.EnsureCA(ctx)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	if len(ca.RootPEM) == 0 || ca.Fingerprint == "" {
		t.Fatalf("EnsureCA response = %+v", ca)
	}
	if string(c.RootPEM()) != string(ca.RootPEM) {
		t.Fatal("client did not cache the root PEM")
	}
	status, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.CAFingerprint != ca.Fingerprint {
		t.Fatalf("status fingerprint = %q, want %q", status.CAFingerprint, ca.Fingerprint)
	}
	if _, err := os.Stat(filepath.Join(caDir, "root.key")); err != nil {
		t.Fatalf("CA key not created: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	domains := []string{"blog.test", "www.blog.test"}
	csr, err := testcert.CreateCSR(key, domains)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := c.SignCertificate(ctx, csr, domains)
	if err != nil {
		t.Fatalf("SignCertificate: %v", err)
	}
	if signed.Serial == "" || signed.NotAfter.IsZero() {
		t.Fatalf("SignCertificate response = %+v", signed)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.RootPEM) {
		t.Fatal("root PEM did not parse")
	}
	leaf, err := testcert.ParseCertificate(signed.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "blog.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify leaf: %v", err)
	}

	targets, err := c.InstallTrust(ctx, home)
	if err != nil {
		t.Fatalf("InstallTrust: %v", err)
	}
	anchorPath := filepath.Join(anchorDir, "devstack-root.crt")
	if _, err := os.Stat(anchorPath); err != nil {
		t.Fatalf("anchor not written: %v", err)
	}
	if !runner.called("update-ca-certificates") {
		t.Fatalf("update command not called; calls = %v", runner.calls)
	}
	if system, ok := trustTargetsByName(targets)["OS"]; !ok || !system.OK {
		t.Fatalf("System install target = %+v (found %v)", system, ok)
	}

	targets, err = c.TrustStatus(ctx, home)
	if err != nil {
		t.Fatalf("TrustStatus: %v", err)
	}
	if system, ok := trustTargetsByName(targets)["OS"]; !ok || !system.OK {
		t.Fatalf("System status target = %+v (found %v)", system, ok)
	}

	if _, err := c.RemoveTrust(ctx, home); err != nil {
		t.Fatalf("RemoveTrust: %v", err)
	}
	if _, err := os.Stat(anchorPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchor still present after RemoveTrust: %v", err)
	}
}

func TestHelperTrustRejectsForeignHome(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	otherHome := t.TempDir()
	anchorDir := t.TempDir()
	runner := &serverFakeRunner{}
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:  os.Getuid(),
		AllowHome: home,
		CADir:     filepath.Join(t.TempDir(), "ca"),
		TrustDir:  anchorDir,
		Runner:    runner,
	})
	c := dialClient(t, socketPath)
	ctx := context.Background()

	checks := []struct {
		name string
		call func() error
	}{
		{
			"install",
			func() error {
				_, err := c.InstallTrust(ctx, otherHome)
				return err
			},
		},
		{
			"remove",
			func() error {
				_, err := c.RemoveTrust(ctx, otherHome)
				return err
			},
		},
		{
			"status",
			func() error {
				_, err := c.TrustStatus(ctx, otherHome)
				return err
			},
		},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !errors.Is(err, helperclient.ErrUnauthorized) {
				t.Fatalf("%s error = %v, want ErrUnauthorized", tt.name, err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(anchorDir, "devstack-root.crt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchor written for a foreign home: %v", err)
	}
	if runner.called("update-ca-certificates") {
		t.Fatal("update command ran for a foreign home")
	}

	if _, err := c.InstallTrust(ctx, "relative/home"); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("relative home error = %v, want ErrInvalidRequest", err)
	}
	if _, err := c.InstallTrust(ctx, ""); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("empty home error = %v, want ErrInvalidRequest", err)
	}
}

func TestHelperSignCertificateRejectsNonTestDomain(t *testing.T) {
	home := t.TempDir()
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:  os.Getuid(),
		AllowHome: home,
		CADir:     filepath.Join(t.TempDir(), "ca"),
	})
	c := dialClient(t, socketPath)
	ctx := context.Background()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := testcert.CreateCSR(key, []string{"evil.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SignCertificate(ctx, csr, []string{"evil.com"})
	if err == nil {
		t.Fatal("SignCertificate accepted a non-.test domain")
	}
	if !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "must end in .test") {
		t.Fatalf("error = %v, want an explanation of the rejected domain", err)
	}
}
