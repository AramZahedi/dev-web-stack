package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// checkTools warns when bare php/composer resolve to something other than the
// devstack-managed shims.
func (s *Service) checkTools() Check {
	check := Check{ID: "tools", Title: "PHP and Composer shims"}
	home := strings.TrimSpace(s.deps.Config.Home)
	if home == "" {
		check.Status = StatusSkipped
		check.Detail = "the devstack home is unknown, so managed tool shims cannot be checked"
		check.Remediation = "No action is required."
		return check
	}
	paths := config.NewPaths(home)
	binDir := strings.TrimSpace(s.deps.ToolBinDir)
	if binDir == "" {
		binDir = "/usr/local/bin"
	}
	lookPath := s.deps.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	targets := map[string]string{
		"php":      filepath.Join(tooling.ShimDir(paths), "php"),
		"composer": filepath.Join(home, "tools", "composer", "bin", "composer"),
	}
	var problems []string
	checked := 0
	stalePATHSession := false
	for _, name := range []string{"php", "composer"} {
		managed := targets[name]
		wrapper := filepath.Join(binDir, name)
		if !regularExecutable(managed) && regularExecutable(managed+".cmd") {
			managed += ".cmd"
		}
		if !regularExecutable(wrapper) && regularExecutable(wrapper+".cmd") {
			wrapper += ".cmd"
		}
		managedExists := regularExecutable(managed)
		resolved, err := lookPath(name)
		if err != nil || strings.TrimSpace(resolved) == "" {
			cmdShim := filepath.Join(binDir, name+".cmd")
			if regularExecutable(cmdShim) && s.persistedPathContains(binDir) {
				stalePATHSession = true
				checked++
				continue
			}
			problems = append(problems, fmt.Sprintf("`%s` does not resolve on PATH; managed shim is %s", name, wrapper))
			continue
		}
		if !managedExists {
			problems = append(problems, fmt.Sprintf("`%s` resolves to %s but no devstack-managed shim exists at %s", name, resolved, wrapper))
			continue
		}
		if sameFile(resolved, wrapper) || sameFile(resolved, managed) {
			checked++
			continue
		}
		problems = append(problems, fmt.Sprintf("`%s` resolves to %s, not the devstack shim %s", name, resolved, wrapper))
	}

	if len(problems) != 0 {
		check.Status = StatusWarn
		check.Detail = strings.Join(problems, "; ")
		check.Remediation = "Run `devstack php default <line>` and `devstack tool install composer`, or move the devstack bin directory earlier on PATH."
		return check
	}
	check.Status = StatusOK
	if stalePATHSession {
		check.Detail = fmt.Sprintf("managed shims exist in %s (note: a new shell session is required if the current daemon process inherited a stale PATH)", binDir)
	} else {
		check.Detail = fmt.Sprintf("php and composer resolve to the managed shims in %s", binDir)
	}
	return check
}

func regularExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" || isWindowsExt(path) {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func isWindowsExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cmd" || ext == ".bat" || ext == ".exe"
}

func (s *Service) persistedPathContains(dir string) bool {
	if s.deps.PersistedPathContains != nil {
		return s.deps.PersistedPathContains(dir)
	}
	return persistentPathContains(dir)
}

func sameFile(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}
