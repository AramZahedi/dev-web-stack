//go:build !windows

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

func prepareNodeDefault(t *testing.T, home, id string) (root, binDir string) {
	t.Helper()
	root = filepath.Join(home, "runtimes", id)
	binDir = filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolDir := filepath.Join(home, "tools", "node-default")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "VERSION"), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, binDir
}

func nodeToolStatus(t *testing.T, c *helperclient.Client) helperproto.ToolStatus {
	t.Helper()
	status, err := c.ToolStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range status.Tools {
		if tool.ID == helperproto.ToolNodeDefault {
			return tool
		}
	}
	t.Fatalf("node-default status missing from %+v", status.Tools)
	return helperproto.ToolStatus{}
}

func TestHelperInstallsNodeDefaultWrappers(t *testing.T) {
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	root, buildBin := prepareNodeDefault(t, home, "node@22.11.0")

	status := nodeToolStatus(t, c)
	if !status.Managed || status.Installed || status.Conflict != "" {
		t.Fatalf("initial node-default status = %+v", status)
	}
	if status.Target != filepath.Join(buildBin, "node") {
		t.Fatalf("target = %q, want %q", status.Target, filepath.Join(buildBin, "node"))
	}
	if status.Version != "node@22.11.0" {
		t.Fatalf("version = %q, want node@22.11.0", status.Version)
	}

	binDir := filepath.Dir(status.WrapperPath)
	foreignNPM := filepath.Join(binDir, "npm")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignNPM, []byte("foreign npm\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, false); !errors.Is(err, helperclient.ErrToolConflict) {
		t.Fatalf("install without force error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "node")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install without force wrote the primary wrapper: %v", err)
	}
	if data, err := os.ReadFile(foreignNPM); err != nil || string(data) != "foreign npm\n" {
		t.Fatalf("foreign npm changed: %q, %v", data, err)
	}

	installed, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, true)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Managed {
		t.Fatalf("installed status = %+v", installed)
	}
	if installed.Backup == "" {
		t.Fatal("install did not report a backup for the foreign npm wrapper")
	}
	if data, err := os.ReadFile(installed.Backup); err != nil || !strings.Contains(string(data), "foreign npm") {
		t.Fatalf("backup content = %q, %v", data, err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		wrapper := filepath.Join(binDir, name)
		info, err := os.Lstat(wrapper)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%s is not a regular devstack wrapper: %v", name, info.Mode())
		}
		if perm := info.Mode().Perm(); perm != 0o755 {
			t.Fatalf("%s mode = %04o, want 0755", name, perm)
		}
		data, err := os.ReadFile(wrapper)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "export PATH='"+buildBin+"':\"$PATH\"") {
			t.Fatalf("%s does not pin the exact build PATH:\n%s", name, text)
		}
		if !strings.Contains(text, "exec '"+filepath.Join(buildBin, name)+"' \"$@\"") {
			t.Fatalf("%s does not exec the exact build binary:\n%s", name, text)
		}
	}

	reinstalled, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, false)
	if err != nil {
		t.Fatalf("idempotent reinstall: %v", err)
	}
	if !reinstalled.Installed {
		t.Fatalf("reinstalled status = %+v", reinstalled)
	}

	// Changing the registered default leaves the old wrappers owned but stale;
	// a normal install repoints them without a conflict or backup.
	_, nextBin := prepareNodeDefault(t, home, "node@24.0.0")
	stale := nodeToolStatus(t, c)
	if !stale.Managed || stale.Installed || stale.Conflict != "" {
		t.Fatalf("stale wrapper status = %+v", stale)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, false); err != nil {
		t.Fatalf("install after default change: %v", err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		data, err := os.ReadFile(filepath.Join(binDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "exec '"+filepath.Join(nextBin, name)+"'") {
			t.Fatalf("%s was not repointed at the new default:\n%s", name, data)
		}
	}

	removed, err := c.ToolRemove(ctx, helperproto.ToolNodeDefault)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Installed || removed.Managed {
		t.Fatalf("status after remove = %+v", removed)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(binDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s wrapper remains: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "tools", "node-default")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("node-default tool artifacts remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "node")); err != nil {
		t.Fatalf("runtime build was removed: %v", err)
	}

	// A foreign wrapper must block removal and must never be deleted.
	prepareNodeDefault(t, home, "node@22.11.0")
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, false); err != nil {
		t.Fatalf("reinstall after remove: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "node"), []byte("foreign node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ToolRemove(ctx, helperproto.ToolNodeDefault); !errors.Is(err, helperclient.ErrToolConflict) {
		t.Fatalf("remove with a foreign wrapper error = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(binDir, "node")); err != nil || string(data) != "foreign node\n" {
		t.Fatalf("foreign node was removed or changed: %q, %v", data, err)
	}
	for _, name := range []string{"npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(binDir, name)); err != nil {
			t.Fatalf("%s was removed despite the conflicting node wrapper: %v", name, err)
		}
	}
}

func TestHelperNodeDefaultRejectsEscapingTarget(t *testing.T) {
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(filepath.Join(outside, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if err := os.WriteFile(filepath.Join(outside, "bin", name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolDir := filepath.Join(home, "tools", "node-default")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	versionPath := filepath.Join(toolDir, "VERSION")
	statusBefore := nodeToolStatus(t, c)
	binDir := filepath.Dir(statusBefore.WrapperPath)

	for _, target := range []string{
		"../../etc/passwd",
		"node@22.11.0/../../outside",
		"node@22.11.0/bin",
		"node@22.11.0-r1",
		"php@8.4.12-r1",
		"node@22.11.0\n../../outside",
		"",
	} {
		if err := os.WriteFile(versionPath, []byte(target+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		status := nodeToolStatus(t, c)
		if status.Managed || status.Installed {
			t.Fatalf("target %q reported managed=%v installed=%v", target, status.Managed, status.Installed)
		}
		if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, true); err == nil {
			t.Fatalf("target %q was accepted", target)
		}
		if _, err := os.Lstat(filepath.Join(binDir, "node")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target %q wrote a system wrapper", target)
		}
	}

	// A registered id that resolves to a symlink outside runtimes is still an
	// escaping target.
	runtimes := filepath.Join(home, "runtimes")
	if err := os.MkdirAll(runtimes, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(runtimes, "node@22.11.0")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(versionPath, []byte("node@22.11.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := nodeToolStatus(t, c); status.Managed || status.Installed {
		t.Fatalf("symlinked target reported managed=%v installed=%v", status.Managed, status.Installed)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, true); err == nil {
		t.Fatal("symlinked target was accepted")
	}
	if _, err := os.Lstat(filepath.Join(binDir, "node")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("symlinked target wrote a system wrapper")
	}

	// Real Node tarballs symlink npm/npx into the build's lib directory. That is
	// allowed, but a binary symlink that escapes the build is not.
	linked := filepath.Join(runtimes, "node@25.0.0")
	if err := os.MkdirAll(filepath.Join(linked, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(linked, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "lib", "npm-cli.js"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "bin", "node"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm", "npx"} {
		if err := os.Symlink("../lib/npm-cli.js", filepath.Join(linked, "bin", name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if err := os.WriteFile(versionPath, []byte("node@25.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := nodeToolStatus(t, c); !status.Managed {
		t.Fatalf("in-build npm/npx symlinks were rejected: %+v", status)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, true); err != nil {
		t.Fatalf("install with in-build npm/npx symlinks: %v", err)
	}
	if _, err := c.ToolRemove(ctx, helperproto.ToolNodeDefault); err != nil {
		t.Fatalf("remove symlinked build wrappers: %v", err)
	}

	if err := os.Remove(filepath.Join(linked, "bin", "npm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "bin", "npm"), filepath.Join(linked, "bin", "npm")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionPath, []byte("node@25.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := nodeToolStatus(t, c); status.Managed || status.Installed {
		t.Fatalf("out-of-build npm symlink reported managed=%v installed=%v", status.Managed, status.Installed)
	}
	if _, err := c.ToolInstall(ctx, helperproto.ToolNodeDefault, true); err == nil {
		t.Fatal("out-of-build npm symlink was accepted")
	}
	if _, err := os.Lstat(filepath.Join(binDir, "node")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("out-of-build npm symlink wrote a system wrapper")
	}
}

func TestToolRemoveKeepsSystemLinksWhenHomeArtifactRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove files in read-only directories")
	}
	socketPath, home := startToolHelper(t)
	c := dialClient(t, socketPath)
	ctx := context.Background()
	prepareManagedTool(t, home, helperproto.ToolComposer)
	installed, err := c.ToolInstall(ctx, helperproto.ToolComposer, false)
	if err != nil {
		t.Fatal(err)
	}
	composerDir := filepath.Join(home, "tools", "composer")
	if err := os.Chmod(composerDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(composerDir, 0o755) })

	if _, err := c.ToolRemove(ctx, helperproto.ToolComposer); err == nil {
		t.Fatal("remove succeeded despite an unwritable home artifact")
	}
	if _, err := os.Lstat(installed.WrapperPath); err != nil {
		t.Fatalf("system wrapper was removed before the home artifact: %v", err)
	}
}
