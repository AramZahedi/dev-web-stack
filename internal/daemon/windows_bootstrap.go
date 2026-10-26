//go:build windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/securestore"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

// WindowsBootstrapError carries the documented diagnostic code while
// preserving the underlying typed transport/WSL error for errors.Is/As.
type WindowsBootstrapError struct {
	Code string
	Err  error
}

func (e *WindowsBootstrapError) Error() string {
	if e == nil {
		return "daemon: Windows bootstrap failed"
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *WindowsBootstrapError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type windowsBootstrapSteps struct {
	Helper    func(context.Context) error
	Agent     func(context.Context) error
	Proxy     func(context.Context) error
	Reconcile func(context.Context) error
}

// bootstrapWindows enforces the startup dependency order. Callbacks are
// restart-safe checks/starts so rerunning after a helper or WSL agent restart
// converges through the same path.
func bootstrapWindows(ctx context.Context, steps windowsBootstrapSteps) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ordered := []struct {
		name string
		code string
		fn   func(context.Context) error
	}{
		{"helper", "HELPER_UNAVAILABLE", steps.Helper},
		{"WSL agent", "WSL_UNAVAILABLE", steps.Agent},
		{"public proxy", "PROXY_UNAVAILABLE", steps.Proxy},
		{"serving reconciliation", "SERVING_RECONCILE_FAILED", steps.Reconcile},
	}
	for _, step := range ordered {
		if step.fn == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("daemon: Windows bootstrap canceled before %s: %w", step.name, err)
		}
		if err := step.fn(ctx); err != nil {
			return classifyWindowsBootstrapError(step.code, err)
		}
	}
	return nil
}

func classifyWindowsBootstrapError(fallback string, err error) error {
	code := fallback
	switch {
	case errors.Is(err, agentproto.ErrUnauthenticated):
		code = "AGENT_UNAUTHENTICATED"
	case errors.Is(err, agentproto.ErrCertPinMismatch):
		code = "AGENT_CERT_UNTRUSTED"
	case errors.Is(err, wsl.ErrLocalhostForwarding):
		code = "WSL_LOCALHOST_FORWARDING_UNAVAILABLE"
	}
	return &WindowsBootstrapError{Code: code, Err: err}
}

// loadWindowsAgentClient decrypts the DPAPI-protected bearer token and SHA-256
// certificate fingerprint from the current user's securestore, then constructs
// a strictly HTTPS pinned client. The caller supplies the discovered endpoint;
// the frozen config/WSL seams do not yet define how that endpoint is published.
func loadWindowsAgentClient(home string, baseURL string) (*agentclient.Client, error) {
	configuredHome := strings.TrimSpace(os.Getenv("DEVSTACK_HOME"))
	wantHome, wantErr := filepath.Abs(strings.TrimSpace(home))
	gotHome, gotErr := filepath.Abs(configuredHome)
	if wantErr != nil || gotErr != nil || configuredHome == "" || !strings.EqualFold(filepath.Clean(wantHome), filepath.Clean(gotHome)) {
		return nil, fmt.Errorf("daemon: securestore home does not match Windows daemon home %q", home)
	}
	token, err := securestore.Load("agent-token")
	if err != nil {
		return nil, fmt.Errorf("daemon: load protected WSL agent token: %w", err)
	}
	pin, err := securestore.Load("agent-pin")
	if err != nil {
		return nil, fmt.Errorf("daemon: load protected WSL agent certificate pin: %w", err)
	}
	defer clear(token)
	defer clear(pin)
	return agentclient.New(agentclient.Config{
		BaseURL: strings.TrimSpace(baseURL), Token: strings.TrimSpace(string(token)),
		PinSHA256: strings.TrimSpace(string(pin)),
	})
}

func probeWindowsLocalhostForwarding(ctx context.Context, runner wsl.Runner, distro string, port int) error {
	if err := wsl.ProbeLocalhostForwarding(ctx, runner, distro, port); err != nil {
		return classifyWindowsBootstrapError("WSL_UNAVAILABLE", err)
	}
	return nil
}
