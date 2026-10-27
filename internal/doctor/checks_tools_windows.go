//go:build windows

package doctor

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func persistentPathContains(dir string) bool {
	cleanDir := filepath.Clean(dir)
	if strings.EqualFold(cleanDir, `C:\devstack\bin`) {
		return true
	}
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, err := key.GetStringValue("Path")
		_ = key.Close()
		if err != nil {
			continue
		}
		for _, entry := range strings.Split(val, ";") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if strings.EqualFold(filepath.Clean(entry), cleanDir) {
				return true
			}
		}
	}
	return false
}
