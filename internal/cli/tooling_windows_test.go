package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestExecWSLSitePassesBackendPathAndSiteEnvironment(t *testing.T) {
	previous := runWSL
	t.Cleanup(func() { runWSL = previous })
	var got []string
	runWSL = func(_ context.Context, args []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got = append([]string(nil), args...)
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
	for _, want := range []string{"cd -- '/mnt/c/devstack/projects/blog'", "export APP_MODE='development'", "exec \"${SHELL:-/bin/bash}\" -i"} {
		if !strings.Contains(got[5], want) {
			t.Fatalf("WSL script missing %q: %s", want, got[5])
		}
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
