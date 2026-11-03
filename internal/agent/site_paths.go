package agent

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// siteFolder is the readable on-disk key for a site. New daemons send the
// unique, immutable site name; old persisted specs without a name fall back to
// the opaque ID so an upgrade stays readable.
func siteFolder(spec agentproto.SiteSpec) string {
	name := strings.TrimSpace(spec.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return spec.ID
	}
	return name
}

// migrateSiteFolders renames legacy ID-based directories, logs, and PID files
// to the readable site name. It is idempotent and never overwrites an existing
// name-based path.
func migrateSiteFolders(root string, spec agentproto.SiteSpec) error {
	key := siteFolder(spec)
	if key == "" || key == spec.ID {
		return nil
	}
	var errs []error
	renameIfAbsent := func(oldPath, newPath string) {
		if _, err := os.Stat(oldPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			return
		}
		if _, err := os.Stat(newPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			return
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			errs = append(errs, err)
		}
	}
	renameIfAbsent(filepath.Join(root, "sites", spec.ID), filepath.Join(root, "sites", key))
	return errors.Join(errs...)
}

var legacySiteFolderPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// archiveOrphanSiteFolders moves pre-name legacy ID directories that no longer
// belong to a registered site into sites/_orphaned. Data is preserved, but the
// main sites directory stays readable.
func archiveOrphanSiteFolders(root string, known map[string]struct{}) error {
	sitesRoot := filepath.Join(root, "sites")
	entries, err := os.ReadDir(sitesRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	archiveRoot := filepath.Join(sitesRoot, "_orphaned")
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "_orphaned" || !legacySiteFolderPattern.MatchString(entry.Name()) {
			continue
		}
		if _, ok := known[entry.Name()]; ok {
			continue
		}
		if err := os.MkdirAll(archiveRoot, 0o750); err != nil {
			return err
		}
		target := filepath.Join(archiveRoot, entry.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(filepath.Join(sitesRoot, entry.Name()), target); err != nil {
			return err
		}
	}
	return nil
}
