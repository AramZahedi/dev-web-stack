package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

type fakeWindowsDiagnostics struct {
	wslErr         error
	agentErr       error
	dockerErr      error
	forwardingErr  error
	portsErr       error
	forwardingPort int
	distro         string
}

func (f *fakeWindowsDiagnostics) CheckWSL(_ context.Context, distro string) error {
	f.distro = distro
	return f.wslErr
}
func (f *fakeWindowsDiagnostics) CheckAgent(_ context.Context, distro string) error {
	f.distro = distro
	return f.agentErr
}
func (f *fakeWindowsDiagnostics) CheckDocker(_ context.Context, distro string) error {
	f.distro = distro
	return f.dockerErr
}
func (f *fakeWindowsDiagnostics) CheckLocalhostForwarding(_ context.Context, distro string, port int) error {
	f.distro = distro
	f.forwardingPort = port
	return f.forwardingErr
}
func (f *fakeWindowsDiagnostics) CheckPorts(_ context.Context, distro string) error {
	f.distro = distro
	return f.portsErr
}

func TestWindowsChecksReportAgentDockerForwardingAndWSLPorts(t *testing.T) {
	diagnostics := &fakeWindowsDiagnostics{}
	svc := NewService(Deps{
		Platform: "windows",
		Config:   config.Config{WSLDistro: "devstack"},
		Windows:  diagnostics,
	})
	checks := svc.windowsChecks(context.Background())
	for _, id := range []string{"wsl", "agent", "docker", "wsl-localhost-forwarding", "wsl-ports"} {
		check, ok := checkByIDIn(checks, id)
		if !ok || check.Status != StatusOK {
			t.Fatalf("%s check = %+v, present=%t", id, check, ok)
		}
	}
	if diagnostics.distro != "devstack" || diagnostics.forwardingPort != 8080 {
		t.Fatalf("diagnostic target distro=%q forwarding port=%d", diagnostics.distro, diagnostics.forwardingPort)
	}
}

func TestWindowsForwardingFailureHasTypedRemediation(t *testing.T) {
	diagnostics := &fakeWindowsDiagnostics{forwardingErr: errors.New("host gateway timed out")}
	svc := NewService(Deps{Platform: "windows", Config: config.Config{WSLDistro: "devstack"}, Windows: diagnostics})
	check, ok := checkByIDIn(svc.windowsChecks(context.Background()), "wsl-localhost-forwarding")
	if !ok || check.Status != StatusFail {
		t.Fatalf("forwarding check = %+v, present=%t", check, ok)
	}
	if !containsAll(check.Detail, "WSL_LOCALHOST_FORWARDING_UNAVAILABLE", "timed out") || check.Remediation == "" {
		t.Fatalf("forwarding result lacks typed failure/remediation: %+v", check)
	}
}

func checkByIDIn(checks []Check, id string) (Check, bool) {
	for _, check := range checks {
		if check.ID == id {
			return check, true
		}
	}
	return Check{}, false
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
