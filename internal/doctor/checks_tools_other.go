//go:build !windows

package doctor

import (
	"path/filepath"
	"strings"
)

func persistentPathContains(dir string) bool {
	cleanDir := filepath.Clean(dir)
	return strings.EqualFold(cleanDir, `C:\devstack\bin`)
}
