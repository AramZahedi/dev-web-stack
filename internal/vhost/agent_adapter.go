package vhost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"regexp"
)

var leafOwnerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// LeafCertificateCopier is the daemon's authenticated, pinned-HTTPS client
// capability for copying leaf material into the WSL agent.
type LeafCertificateCopier interface {
	CopyLeafCertificate(ctx context.Context, owner string, certPEM, keyPEM []byte) error
}

// AgentCertificateAdapter validates local certificate material before handing
// it to the authenticated agent for its private WSL copy.
type AgentCertificateAdapter struct{ copier LeafCertificateCopier }

func NewAgentCertificateAdapter(copier LeafCertificateCopier) (*AgentCertificateAdapter, error) {
	if copier == nil {
		return nil, errors.New("vhost: agent leaf certificate copier is required")
	}
	return &AgentCertificateAdapter{copier: copier}, nil
}

// CopyLeafCertificate accepts a matching non-CA leaf pair only. The CA private
// key is never forwarded to WSL or a Docker container.
func (a *AgentCertificateAdapter) CopyLeafCertificate(ctx context.Context, owner string, certPEM, keyPEM []byte) error {
	if !leafOwnerPattern.MatchString(owner) {
		return errors.New("vhost: invalid leaf certificate owner")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("vhost: invalid leaf certificate pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return errors.New("vhost: leaf certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("vhost: parse leaf certificate: %w", err)
	}
	if leaf.IsCA {
		return errors.New("vhost: CA certificates and private keys cannot be copied to WSL")
	}
	if err := a.copier.CopyLeafCertificate(ctx, owner, certPEM, keyPEM); err != nil {
		return fmt.Errorf("vhost: copy leaf certificate to WSL: %w", err)
	}
	return nil
}
