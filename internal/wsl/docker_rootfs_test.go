package wsl_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentdocker"
)

func TestDockerRootfsContractUsesCanonicalExt4Storage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Docker Compose container paths are POSIX and execute inside WSL")
	}
	if agentdocker.DockerDataRoot != "/data/devstack/docker" {
		t.Fatalf("Docker data root = %q, want /data/devstack/docker", agentdocker.DockerDataRoot)
	}
	compose, err := agentdocker.RenderCompose("/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(compose), `\`, "/")
	for _, id := range []string{"mysql", "mariadb", "postgres", "redis"} {
		if !strings.Contains(text, "/data/devstack/services/"+id+"/data") {
			t.Errorf("Compose does not store %s data under WSL ext4", id)
		}
	}
	if strings.Contains(strings.ToLower(text), "c:/") || strings.Contains(text, "/mnt/c/") || strings.Contains(text, "wsl.localhost") {
		t.Fatalf("rootfs Compose contract contains an NTFS/UNC bind mount:\n%s", text)
	}
}
