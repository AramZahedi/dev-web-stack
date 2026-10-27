//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

const allowedHelperSID = "S-1-5-21-123456789-123456789-123456789-1001"

func TestParseWindowsOptionsRequiresInstallingSID(t *testing.T) {
	emptyConfig := filepath.Join(t.TempDir(), "helper.yaml")
	if _, err := parseWindowsOptions([]string{"--config", emptyConfig}); err == nil {
		t.Fatal("options accepted without an installing SID")
	}
	opts, err := parseWindowsOptions([]string{"--config", emptyConfig, "--allow-sid", allowedHelperSID, "--pipe", `\\.\pipe\devstack-helper-test`})
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if opts.allowSID != allowedHelperSID || opts.pipe != `\\.\pipe\devstack-helper-test` {
		t.Fatalf("options = %+v", opts)
	}
}

func TestParseWindowsOptionsRejectsUnixSocketSetting(t *testing.T) {
	emptyConfig := filepath.Join(t.TempDir(), "helper.yaml")
	if _, err := parseWindowsOptions([]string{"--config", emptyConfig, "--allow-sid", allowedHelperSID, "--socket", `C:\temp\helper.sock`}); err == nil {
		t.Fatal("Windows helper accepted a Unix-socket option")
	}
}
