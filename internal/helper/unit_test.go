package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelperUnitWritableVhostBridges guards the systemd sandbox. Under
// ProtectSystem=strict the helper can only write ReadWritePaths, so external
// mode fails at the bridge write unless the nginx and Apache include
// directories are listed. The leading '-' keeps the unit valid on machines
// that only have one server installed.
func TestHelperUnitWritableVhostBridges(t *testing.T) {
	path := filepath.Join("..", "..", "build", "systemd", "devstack-helper.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read helper unit: %v", err)
	}
	unit := string(data)
	if !strings.Contains(unit, "ProtectSystem=strict") {
		t.Fatal("helper unit no longer uses ProtectSystem=strict; the bridge paths may be unnecessary")
	}
	for _, want := range []string{
		"ReadWritePaths=-/etc/nginx/conf.d",
		"ReadWritePaths=-/etc/apache2/conf-enabled",
		"ReadWritePaths=-/etc/httpd/conf.d",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("helper unit is missing %q; external mode cannot write the bridge", want)
		}
	}
}

func TestHelperUnitBoundsCapabilities(t *testing.T) {
	path := filepath.Join("..", "..", "build", "systemd", "devstack-helper.service")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	for _, want := range []string{
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SETUID CAP_SETGID CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER",
		"AmbientCapabilities=",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("helper unit is missing %q", want)
		}
	}
}
