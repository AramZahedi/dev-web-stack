package wsl

import (
	"strings"
	"testing"
)

func TestHostsGenerationConfigDisablesWSLGeneratedHosts(t *testing.T) {
	got := DisableGeneratedHosts(`[boot]
systemd=true

[network]
generateHosts=true
generateResolvConf=false

[automount]
enabled=true
`)
	if !strings.Contains(got, "[network]\ngenerateHosts=false\ngenerateResolvConf=false") {
		t.Fatalf("generated WSL config =\n%s", got)
	}
	if strings.Count(got, "generateHosts=") != 1 {
		t.Fatalf("generateHosts setting is duplicated:\n%s", got)
	}
	if again := DisableGeneratedHosts(got); again != got {
		t.Fatalf("disable operation is not idempotent:\n%s", again)
	}
}
