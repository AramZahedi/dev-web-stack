package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, file := range []string{"bin/php", "sbin/php-fpm"} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "lib", "php", "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParseOptionsAcceptsRootAndPort(t *testing.T) {
	root := fakeRoot(t)
	opts, err := parseOptions([]string{"--root", root + "/", "--port", "43001"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.root != root {
		t.Fatalf("root = %q, want %q", opts.root, root)
	}
	if opts.port != 43001 {
		t.Fatalf("port = %d", opts.port)
	}
}

func TestParseOptionsRejections(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing root", []string{"--port", "43001"}, "root"},
		{"missing port", []string{"--root", "/tmp/php"}, "port"},
		{"relative root", []string{"--root", "php", "--port", "43001"}, "absolute"},
		{"port zero", []string{"--root", "/tmp/php", "--port", "0"}, "port"},
		{"port below range", []string{"--root", "/tmp/php", "--port", "42999"}, "port"},
		{"port above range", []string{"--root", "/tmp/php", "--port", "44000"}, "port"},
		{"unknown flag", []string{"--root", "/tmp/php", "--port", "43001", "--nope"}, "nope"},
		{"positional", []string{"--root", "/tmp/php", "--port", "43001", "extra"}, "unexpected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := parseOptionsWithOutput(tt.args, &stderr)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRoot(t *testing.T) {
	root := fakeRoot(t)
	if err := validateRoot(root); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		remove string
		want   string
	}{
		{"missing php", "bin/php", "bin/php"},
		{"missing fpm", "sbin/php-fpm", "sbin/php-fpm"},
		{"missing extensions", "lib/php/extensions", "lib/php/extensions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fakeRoot(t)
			if err := os.RemoveAll(filepath.Join(root, tt.remove)); err != nil {
				t.Fatal(err)
			}
			err := validateRoot(root)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRootRejectsNonExecutablePHP(t *testing.T) {
	root := fakeRoot(t)
	if err := os.Chmod(filepath.Join(root, "bin", "php"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRoot(root); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("error = %v, want an executable error", err)
	}
}

func TestCheckNotRoot(t *testing.T) {
	if err := checkNotRoot(0); err == nil {
		t.Fatal("uid 0 must be refused")
	}
	if err := checkNotRoot(1000); err != nil {
		t.Fatalf("uid 1000 = %v", err)
	}
}

func TestRunRefusesRootBeforeTouchingTheFilesystem(t *testing.T) {
	// The root refusal is checked before the root is validated, so an empty root
	// still reports the uid problem first.
	err := runWithEUID([]string{"--root", "/does/not/exist", "--port", "43001"}, 0)
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("error = %v, want a root refusal", err)
	}
}
