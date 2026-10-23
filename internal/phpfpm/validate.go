package phpfpm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// validateTimeout bounds a php-fpm configuration test. It is a variable so
// tests can shorten the wait.
var validateTimeout = 10 * time.Second

// Argv is the exact command line used to start a per-site master. The master
// stays in the foreground so the supervisor can own its process group, and
// the generated php.ini is passed explicitly.
func Argv(fpmBin, configPath, iniPath string) []string {
	return []string{fpmBin, "--nodaemonize", "--fpm-config", configPath, "-c", iniPath}
}

// Validate runs `php-fpm -t -y <config> -c <ini>` with PHP_INI_SCAN_DIR
// cleared and a ten-second timeout. Both files must exist and be readable:
// php-fpm silently ignores a missing -c file, which would otherwise let a bad
// ini path pass validation. Every failure wraps ErrConfigInvalid and includes
// the trimmed command output.
func Validate(ctx context.Context, fpmBin, configPath, iniPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(fpmBin) == "" {
		return invalid("php-fpm binary", "is required")
	}
	if err := validateText("php-fpm binary", fpmBin); err != nil {
		return err
	}
	for _, file := range []struct {
		field string
		value string
	}{
		{"configuration file", configPath},
		{"php.ini", iniPath},
	} {
		if file.value == "" {
			return invalid(file.field, "is required")
		}
		if err := validateText(file.field, file.value); err != nil {
			return err
		}
		if err := readableFile(file.field, file.value); err != nil {
			return err
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()

	command := exec.CommandContext(runCtx, fpmBin, "-t", "-y", configPath, "-c", iniPath)
	command.Env = withEnv(os.Environ(), "PHP_INI_SCAN_DIR", "")
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	switch {
	case runCtx.Err() != nil && message != "":
		message += "\n" + runCtx.Err().Error()
	case runCtx.Err() != nil:
		message = runCtx.Err().Error()
	case message == "":
		message = err.Error()
	case !strings.Contains(message, err.Error()):
		message += "\n" + err.Error()
	}
	return fmt.Errorf("%w: %s", ErrConfigInvalid, message)
}

// readableFile verifies that path is an existing regular file that can be
// opened, so validation cannot silently pass on a missing php.ini.
func readableFile(field, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return invalid(field, "%q is not readable: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return invalid(field, "%q is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return invalid(field, "%q is not readable: %v", path, err)
	}
	if err := file.Close(); err != nil {
		return invalid(field, "%q is not readable: %v", path, err)
	}
	return nil
}

// withEnv returns base with key set to value, replacing any existing entry so
// duplicate names cannot leak an inherited value.
func withEnv(base []string, key, value string) []string {
	prefix := key + "="
	merged := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if !strings.HasPrefix(entry, prefix) {
			merged = append(merged, entry)
		}
	}
	return append(merged, prefix+value)
}
