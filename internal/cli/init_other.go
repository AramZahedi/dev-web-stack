//go:build !windows

package cli

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type platformInitContext struct{}

func initPlatformPreDaemon(_ context.Context, _ string, _ config.Config, _ helperproto.EnsureCAResponse) (*platformInitContext, error) {
	return nil, nil
}

func initPlatformSyncHosts(_ context.Context, _ *platformInitContext, _ []string) error {
	return nil
}

func initPlatformPostDaemon(_ context.Context, _ *platformInitContext, _ config.Config) error {
	return nil
}

func setupFakeWSLForCLITests(_ testing.TB) {}
