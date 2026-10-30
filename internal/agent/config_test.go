package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentConfigTerminalLimitsDefaultsAndBounds(t *testing.T) {
	root := t.TempDir()
	base := func() Config { return Config{Root: root} }

	config := base()
	if err := config.applyDefaultsAndValidate(); err != nil {
		t.Fatal(err)
	}
	if config.TerminalIdleSeconds != DefaultTerminalIdleSeconds {
		t.Fatalf("terminal idle default = %d", config.TerminalIdleSeconds)
	}
	if config.TerminalMaxSeconds != DefaultTerminalMaxSeconds {
		t.Fatalf("terminal max default = %d", config.TerminalMaxSeconds)
	}

	config = base()
	config.TerminalIdleSeconds = 5
	config.TerminalMaxSeconds = 60
	if err := config.applyDefaultsAndValidate(); err != nil {
		t.Fatal(err)
	}
	if config.TerminalIdleSeconds != 5 || config.TerminalMaxSeconds != 60 {
		t.Fatalf("configured limits = %d/%d", config.TerminalIdleSeconds, config.TerminalMaxSeconds)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "negative idle", mutate: func(c *Config) { c.TerminalIdleSeconds = -1 }, want: "terminal_idle_seconds"},
		{name: "huge idle", mutate: func(c *Config) { c.TerminalIdleSeconds = maxTerminalIdleSeconds + 1 }, want: "terminal_idle_seconds"},
		{name: "negative max", mutate: func(c *Config) { c.TerminalMaxSeconds = -1 }, want: "terminal_max_seconds"},
		{name: "huge max", mutate: func(c *Config) { c.TerminalMaxSeconds = maxTerminalSessionSeconds + 1 }, want: "terminal_max_seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := base()
			tc.mutate(&config)
			err := config.applyDefaultsAndValidate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a %s validation error", err, tc.want)
			}
		})
	}
}

func TestAgentConfigTerminalLimitsRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "agent.yaml")
	if err := WriteConfig(path, Config{Root: root, TerminalIdleSeconds: 42, TerminalMaxSeconds: 4242}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TerminalIdleSeconds != 42 || loaded.TerminalMaxSeconds != 4242 {
		t.Fatalf("loaded limits = %d/%d", loaded.TerminalIdleSeconds, loaded.TerminalMaxSeconds)
	}
}

func TestAgentServerAppliesTerminalLimits(t *testing.T) {
	runtime, err := NewRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := GenerateTLSIdentity(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Root:                t.TempDir(),
		Distro:              "devstack",
		TerminalIdleSeconds: 5,
		TerminalMaxSeconds:  60,
	}
	if _, err := NewServerWithRuntime(config, "secret-token", identity, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.terminalIdleTimeout != 5*time.Second {
		t.Fatalf("runtime idle timeout = %s", runtime.terminalIdleTimeout)
	}
	if runtime.terminalMaxDuration != time.Minute {
		t.Fatalf("runtime max duration = %s", runtime.terminalMaxDuration)
	}
}
