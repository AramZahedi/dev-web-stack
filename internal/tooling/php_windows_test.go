//go:build windows

package tooling

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestBuildHostPathMapsWSLBuildsIntoTheDistroShare(t *testing.T) {
	build := model.RuntimeBuild{
		ID:      "php@8.4.12-r1",
		Kind:    "php",
		Version: "8.4.12",
		Backend: model.BackendWSL,
		Path:    "/data/devstack/runtimes/php@8.4.12-r1",
	}
	got := buildHostPath(build, joinBuildPath(build, "etc", "php.ini"), "")
	lower := strings.ToLower(got)
	if !strings.HasPrefix(lower, `\\wsl.localhost\devstack\`) &&
		!strings.HasPrefix(lower, `\\wsl$\devstack\`) {
		t.Fatalf("host path = %q, want a WSL UNC share", got)
	}
	if !strings.HasSuffix(got, `\data\devstack\runtimes\php@8.4.12-r1\etc\php.ini`) {
		t.Fatalf("host path = %q", got)
	}

	local := model.RuntimeBuild{
		ID:      "php@8.4.12-r1",
		Kind:    "php",
		Version: "8.4.12",
		Backend: model.BackendLocal,
		Path:    filepath.Join(t.TempDir(), "php"),
	}
	localHost := buildHostPath(local, joinBuildPath(local, "etc", "php.ini"), "")
	if localHost != filepath.Join(local.Path, "etc", "php.ini") {
		t.Fatalf("local host path = %q", localHost)
	}
}
