//go:build !windows

package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

type fakeToolStatusProvider struct {
	status helperproto.ToolStatusResponse
	err    error
}

func (f fakeToolStatusProvider) ToolStatus(context.Context) (helperproto.ToolStatusResponse, error) {
	return f.status, f.err
}

func TestResolveToolBinDirUsesHelperWrapperPath(t *testing.T) {
	dir := resolveToolBinDir(context.Background(), fakeToolStatusProvider{
		status: helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{
			{ID: helperproto.ToolComposer, WrapperPath: "/opt/bin/composer"},
			// Installed/Managed remain false: the configured bin directory is
			// still known before the wrapper exists.
			{ID: helperproto.ToolPHPDefault, WrapperPath: "/opt/bin/php"},
		}},
	})
	if dir != "/opt/bin" {
		t.Fatalf("dir = %q, want /opt/bin", dir)
	}
}

func TestResolveToolBinDirDefaultsWhenUnavailable(t *testing.T) {
	for _, provider := range []fakeToolStatusProvider{
		{err: errors.New("helper unavailable")},
		{status: helperproto.ToolStatusResponse{}},
		{status: helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{{ID: helperproto.ToolComposer}}}},
	} {
		if got := resolveToolBinDir(context.Background(), provider); got != "/usr/local/bin" {
			t.Fatalf("dir = %q, want default /usr/local/bin", got)
		}
	}
}
