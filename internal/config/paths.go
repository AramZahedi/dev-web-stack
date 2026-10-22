package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths describes the machine directory layout below the devstack home.
type Paths struct {
	Home     string
	Config   string
	DB       string
	PKI      string
	Runtimes string
	Sites    string
	Logs     string
	Run      string
	Cache    string
}

// NewPaths returns the path layout rooted at home. It does not touch the
// filesystem; call Ensure for that.
func NewPaths(home string) Paths {
	return Paths{
		Home:     home,
		Config:   filepath.Join(home, "config.yaml"),
		DB:       filepath.Join(home, "state.db"),
		PKI:      filepath.Join(home, "pki"),
		Runtimes: filepath.Join(home, "runtimes"),
		Sites:    filepath.Join(home, "sites"),
		Logs:     filepath.Join(home, "logs"),
		Run:      filepath.Join(home, "run"),
		Cache:    filepath.Join(home, "cache"),
	}
}

// Ensure creates the machine root and every directory it owns. It is
// idempotent.
func (p Paths) Ensure() error {
	for _, dir := range []string{
		p.Home,
		p.PKI,
		p.Runtimes,
		p.Sites,
		p.Logs,
		p.Run,
		p.Cache,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
