//go:build windows

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/securestore"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

func TestWindowsBootstrapRunsHelperAgentProxyThenReconcile(t *testing.T) {
	var order []string
	steps := windowsBootstrapSteps{
		Helper:    func(context.Context) error { order = append(order, "helper"); return nil },
		Agent:     func(context.Context) error { order = append(order, "agent"); return nil },
		Proxy:     func(context.Context) error { order = append(order, "proxy"); return nil },
		Reconcile: func(context.Context) error { order = append(order, "reconcile"); return nil },
	}
	if err := bootstrapWindows(context.Background(), steps); err != nil {
		t.Fatalf("bootstrapWindows: %v", err)
	}
	if want := []string{"helper", "agent", "proxy", "reconcile"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("startup order = %v, want %v", order, want)
	}
}

func TestWindowsBootstrapCanRecoverAfterHelperAndAgentRestart(t *testing.T) {
	helperAvailable, agentAvailable := false, false
	steps := windowsBootstrapSteps{
		Helper: func(context.Context) error {
			if !helperAvailable {
				return errors.New("helper service restarting")
			}
			return nil
		},
		Agent: func(context.Context) error {
			if !agentAvailable {
				return errors.New("agent restarting")
			}
			return nil
		},
		Proxy:     func(context.Context) error { return nil },
		Reconcile: func(context.Context) error { return nil },
	}
	if err := bootstrapWindows(context.Background(), steps); err == nil {
		t.Fatal("bootstrap succeeded while helper was unavailable")
	}
	helperAvailable = true
	if err := bootstrapWindows(context.Background(), steps); err == nil {
		t.Fatal("bootstrap succeeded while agent was unavailable")
	}
	agentAvailable = true
	if err := bootstrapWindows(context.Background(), steps); err != nil {
		t.Fatalf("bootstrap after helper/agent recovery: %v", err)
	}
}

func TestLoadWindowsAgentClientUsesDPAPIStoredTokenAndPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)
	if err := securestore.Save("agent-token", []byte("fake-token")); err != nil {
		t.Fatal(err)
	}
	if err := securestore.Save("agent-pin", []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	client, err := loadWindowsAgentClient(home, "https://127.0.0.1:39873")
	if err != nil {
		t.Fatalf("loadWindowsAgentClient: %v", err)
	}
	if client == nil {
		t.Fatal("loadWindowsAgentClient returned nil client")
	}
	_ = client.Close()
}

func TestLoadWindowsAgentClientRefusesSecretsFromAnotherHome(t *testing.T) {
	wrongHome := t.TempDir()
	t.Setenv("DEVSTACK_HOME", wrongHome)
	if err := securestore.Save("agent-token", []byte("wrong-home-token")); err != nil {
		t.Fatal(err)
	}
	if err := securestore.Save("agent-pin", []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWindowsAgentClient(t.TempDir(), "https://127.0.0.1:39873"); err == nil {
		t.Fatal("loaded agent credentials from a different DEVSTACK_HOME")
	}
}

func TestWindowsBootstrapMapsAgentAuthenticationAndPinFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"auth", agentproto.ErrUnauthenticated, "AGENT_UNAUTHENTICATED"},
		{"pin", agentproto.ErrCertPinMismatch, "AGENT_CERT_UNTRUSTED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := bootstrapWindows(context.Background(), windowsBootstrapSteps{
				Helper: func(context.Context) error { return nil },
				Agent:  func(context.Context) error { return test.err },
			})
			var bootstrapErr *WindowsBootstrapError
			if !errors.As(err, &bootstrapErr) || bootstrapErr.Code != test.code {
				t.Fatalf("bootstrap error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestWindowsLocalhostForwardingFailureHasRemediationCode(t *testing.T) {
	runner := bootstrapWSLRunner{err: errors.New("connection refused")}
	err := probeWindowsLocalhostForwarding(context.Background(), runner, "devstack", 8080)
	var bootstrapErr *WindowsBootstrapError
	if !errors.As(err, &bootstrapErr) || bootstrapErr.Code != "WSL_LOCALHOST_FORWARDING_UNAVAILABLE" || !errors.Is(err, wsl.ErrLocalhostForwarding) {
		t.Fatalf("forwarding error = %v, want typed WSL localhost forwarding diagnostic", err)
	}
}

type bootstrapWSLRunner struct{ err error }

func (r bootstrapWSLRunner) Run(context.Context, ...string) ([]byte, error) {
	return nil, r.err
}
