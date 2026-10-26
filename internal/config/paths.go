package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths describes the machine directory layout below the devstack home.
type Paths struct {
	Home           string
	Root           string
	Config         string
	DB             string
	PKI            string
	System         string
	Runtimes       string
	Sites          string
	Logs           string
	Run            string
	Cache          string
	Projects       string
	ProjectsRoot   string
	Services       string
	ServiceCompose string
	Secrets        string
	Certs          string
	Backups        string
	Tools          string
	CLI            string
}

// NewPaths returns the path layout rooted at home. It does not touch the
// filesystem; call Ensure for that.
func NewPaths(home string) Paths {
	return Paths{
		Home:           home,
		Root:           home,
		Config:         filepath.Join(home, "config.yaml"),
		DB:             filepath.Join(home, "state.db"),
		PKI:            filepath.Join(home, "pki"),
		System:         filepath.Join(home, "system"),
		Runtimes:       filepath.Join(home, "runtimes"),
		Sites:          filepath.Join(home, "sites"),
		Logs:           filepath.Join(home, "logs"),
		Run:            filepath.Join(home, "run"),
		Cache:          filepath.Join(home, "cache"),
		Projects:       filepath.Join(home, "projects"),
		ProjectsRoot:   filepath.Join(home, "projects"),
		Services:       filepath.Join(home, "services"),
		ServiceCompose: filepath.Join(home, "services", "compose.yaml"),
		Secrets:        filepath.Join(home, "secrets"),
		Certs:          filepath.Join(home, "certs"),
		Backups:        filepath.Join(home, "backups"),
		Tools:          filepath.Join(home, "tools"),
		CLI:            filepath.Join(home, "config", "cli"),
	}
}

// Ensure creates the machine root and every directory it owns. It is
// idempotent.
func (p Paths) Ensure() error {
	for _, dir := range []string{
		p.Home,
		p.PKI,
		p.System,
		p.Runtimes,
		p.Sites,
		p.Logs,
		p.Run,
		p.Cache,
		p.Projects,
		p.Services,
		p.Certs,
		p.Backups,
		p.Tools,
		p.CLI,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	if err := os.MkdirAll(p.Secrets, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", p.Secrets, err)
	}
	if err := os.Chmod(p.Secrets, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", p.Secrets, err)
	}
	// The system directory owns the CA private key. It must never be
	// group/world accessible, even if an earlier install created it looser.
	if err := os.Chmod(p.System, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", p.System, err)
	}

	return nil
}
