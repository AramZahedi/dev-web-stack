package cli

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

func TestExecWSLSitePassesBackendPathAndSiteEnvironment(t *testing.T) {
	previousRun := runWSL
	previousEnv := newWSLEnvFile
	t.Cleanup(func() {
		runWSL = previousRun
		newWSLEnvFile = previousEnv
	})
	var got []string
	var envFile *tooling.WSLEnvFile
	var envMode os.FileMode
	var envContent string
	newWSLEnvFile = func(env map[string]string) (*tooling.WSLEnvFile, error) {
		file, err := tooling.NewWSLEnvFile(env)
		envFile = file
		return file, err
	}
	runWSL = func(_ context.Context, args []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got = append([]string(nil), args...)
		if envFile != nil {
			info, err := os.Stat(envFile.Path)
			if err != nil {
				return err
			}
			envMode = info.Mode().Perm()
			content, err := os.ReadFile(envFile.Path)
			if err != nil {
				return err
			}
			envContent = string(content)
		}
		return nil
	}
	site := model.Site{Name: "blog", BackendPath: "/mnt/c/devstack/projects/blog"}
	env := map[string]string{"DEVSTACK_SITE": "blog", "APP_MODE": "development"}
	if err := execWSLSite(context.Background(), "devstack", site, env, nil, true, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0] != "-d" || got[1] != "devstack" {
		t.Fatalf("wsl args = %#v", got)
	}
	script := got[5]
	for _, want := range []string{"cd -- '/mnt/c/devstack/projects/blog'", "exec \"${SHELL:-/bin/bash}\" -i", "devstack-wsl-env-"} {
		if !strings.Contains(script, want) {
			t.Fatalf("WSL script missing %q: %s", want, script)
		}
	}
	if strings.Contains(script, "development") {
		t.Fatalf("site environment leaked into WSL argv: %s", script)
	}
	if envFile == nil {
		t.Fatal("private env file was not created")
	}
	if !strings.Contains(envContent, "export APP_MODE='development'") {
		t.Fatal("env file does not contain the site environment")
	}
	if runtime.GOOS != "windows" && envMode != 0o600 {
		t.Fatalf("env file mode = %o, want 0600", envMode)
	}
	if _, err := os.Stat(envFile.Path); !os.IsNotExist(err) {
		t.Fatalf("env file was not cleaned up: %v", err)
	}
}

func TestExecWSLSiteKeepsDatabaseSecretsOutOfArgv(t *testing.T) {
	previous := runWSL
	t.Cleanup(func() { runWSL = previous })
	var got []string
	runWSL = func(_ context.Context, args []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got = append([]string(nil), args...)
		return nil
	}
	site := model.Site{Name: "blog", BackendPath: "/mnt/c/devstack/projects/blog"}
	env := map[string]string{
		"DB_HOST":      "127.0.0.1",
		"DB_PASSWORD":  "s3cret-db-password",
		"DATABASE_URL": "mysql://app:s3cret-db-password@127.0.0.1:3306/app",
	}
	if err := execWSLSite(context.Background(), "devstack", site, env, nil, true, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no WSL arguments captured")
	}
	joined := strings.Join(got, "\x00")
	for _, secret := range []string{"s3cret-db-password", "mysql://app:s3cret-db-password@127.0.0.1:3306/app"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %q appeared in WSL argv/script", secret)
		}
	}
	if !strings.Contains(got[len(got)-1], "devstack-wsl-env-") {
		t.Fatalf("WSL script does not reference a private env file: %s", got[len(got)-1])
	}
}

func TestExecWSLSitePreservesCommandArguments(t *testing.T) {
	previous := runWSL
	t.Cleanup(func() { runWSL = previous })
	var got []string
	runWSL = func(_ context.Context, args []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got = append([]string(nil), args...)
		return nil
	}
	site := model.Site{Name: "blog", BackendPath: "/home/dev/projects/blog"}
	if err := execWSLSite(context.Background(), "devstack", site, nil, []string{"npm", "run", "dev server"}, false, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[5], "exec 'npm' 'run' 'dev server'") {
		t.Fatalf("command arguments were not quoted: %s", got[5])
	}
}
