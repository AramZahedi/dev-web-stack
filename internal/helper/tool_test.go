package helper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func prepareManagedTool(t *testing.T, home, id string) (target string) {
	t.Helper()
	switch id {
	case helperproto.ToolPHPDefault:
		target = filepath.Join(home, "tools", "php-default", "bin", "php")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"php", "php-config", "phpize"} {
			if err := os.WriteFile(filepath.Join(filepath.Dir(target), name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(home, "tools", "php-default", "VERSION"), []byte("php@8.4.12-r1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	case helperproto.ToolComposer:
		target = filepath.Join(home, "tools", "composer", "bin", "composer")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		phar := filepath.Join(home, "tools", "composer", "composer.phar")
		if err := os.WriteFile(phar, []byte("phar"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown tool %s", id)
	}
	return target
}

func startToolHelper(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID:   os.Getuid(),
		ToolRoot:   home,
		ToolBinDir: binDir,
	})
	return socketPath, home
}

func TestToolInstallBacksUpForeignFileAndRequiresForce(t *testing.T) {
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	target := prepareManagedTool(t, home, helperproto.ToolPHPDefault)
	// The helper's configured bin directory is exposed through status.
	status, err := c.ToolStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var php helperproto.ToolStatus
	for _, tool := range status.Tools {
		if tool.ID == helperproto.ToolPHPDefault {
			php = tool
		}
	}
	if php.ID == "" || !php.Managed || php.Installed {
		t.Fatalf("initial php status = %+v", php)
	}
	if php.Target != target {
		t.Fatalf("target = %q, want %q", php.Target, target)
	}

	wrapper := php.WrapperPath
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n# foreign\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolPHPDefault, false); !errors.Is(err, helperclient.ErrToolConflict) {
		t.Fatalf("install without force error = %v", err)
	}
	data, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "foreign") {
		t.Fatalf("foreign wrapper was modified:\n%s", data)
	}

	result, err := c.ToolInstall(ctx, helperproto.ToolPHPDefault, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed {
		t.Fatalf("install status = %+v", result)
	}
	if result.Backup == "" {
		t.Fatal("install did not report a backup")
	}
	backup, err := os.ReadFile(result.Backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(backup), "foreign") {
		t.Fatalf("backup content = %q", backup)
	}
	linkTarget, err := os.Readlink(wrapper)
	if err != nil {
		t.Fatalf("wrapper is not a symlink: %v", err)
	}
	if linkTarget != target {
		t.Fatalf("wrapper -> %q, want %q", linkTarget, target)
	}
	binDir := filepath.Dir(wrapper)
	for _, name := range []string{"php-config", "phpize"} {
		link, err := os.Readlink(filepath.Join(binDir, name))
		if err != nil {
			t.Fatalf("%s link: %v", name, err)
		}
		if link != filepath.Join(filepath.Dir(target), name) {
			t.Fatalf("%s -> %q", name, link)
		}
	}
}

func TestToolInstallRemoveComposer(t *testing.T) {
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	target := prepareManagedTool(t, home, helperproto.ToolComposer)

	result, err := c.ToolInstall(ctx, helperproto.ToolComposer, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed || result.Version == "" {
		t.Fatalf("status = %+v", result)
	}
	wrapper := result.WrapperPath
	if _, err := os.Lstat(wrapper); err != nil {
		t.Fatal(err)
	}

	removed, err := c.ToolRemove(ctx, helperproto.ToolComposer)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Installed || removed.Managed {
		t.Fatalf("status after remove = %+v", removed)
	}
	if _, err := os.Lstat(wrapper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapper remains: %v", err)
	}
	phar := filepath.Join(home, "tools", "composer", "composer.phar")
	if _, err := os.Stat(phar); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("phar remains: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed target remains: %v", err)
	}
}

func TestToolRemoveRefusesForeignWrapper(t *testing.T) {
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	prepareManagedTool(t, home, helperproto.ToolComposer)
	status, err := c.ToolStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper string
	for _, tool := range status.Tools {
		if tool.ID == helperproto.ToolComposer {
			wrapper = tool.WrapperPath
		}
	}
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("foreign composer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ToolRemove(ctx, helperproto.ToolComposer); !errors.Is(err, helperclient.ErrToolConflict) {
		t.Fatalf("remove error = %v", err)
	}
	if _, err := os.Stat(wrapper); err != nil {
		t.Fatalf("foreign wrapper removed: %v", err)
	}
}

func TestToolRequestsRejectUnknownIDs(t *testing.T) {
	socketPath, _ := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	if _, err := c.ToolInstall(ctx, "/usr/bin/python", false); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("install error = %v", err)
	}
	if _, err := c.ToolRemove(ctx, "sh"); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("remove error = %v", err)
	}
}
