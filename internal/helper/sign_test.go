//go:build !windows

package helper

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestSignCertificateClassifiesInvalidRequest(t *testing.T) {
	a := newTestAuthority(t)
	s := &Server{authority: a}
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "blog.test")

	_, err := s.signCertificate(context.Background(), helperproto.SignRequest{
		CSR:     csr,
		Domains: []string{"blog.test", "www.blog.test"},
	})
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("SAN mismatch error = %v, want ErrInvalidRequest", err)
	}

	_, err = s.signCertificate(context.Background(), helperproto.SignRequest{
		CSR:     []byte("not a CSR"),
		Domains: []string{"blog.test"},
	})
	if !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("invalid CSR error = %v, want ErrInvalidRequest", err)
	}
}

func TestSignCertificateInternalErrorIsNotInvalidRequest(t *testing.T) {
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "blog.test")
	// A partially initialized authority simulates an internal signing failure
	// rather than a request validation failure.
	s := &Server{authority: &Authority{cert: &x509.Certificate{}}}

	_, err := s.signCertificate(context.Background(), helperproto.SignRequest{
		CSR:     csr,
		Domains: []string{"blog.test"},
	})
	if err == nil {
		t.Fatal("signCertificate succeeded with no signing key")
	}
	if errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("internal signing error mapped to INVALID_REQUEST: %v", err)
	}
}
