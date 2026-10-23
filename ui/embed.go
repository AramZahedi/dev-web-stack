// Package ui embeds the built dashboard. The dist directory is populated by
// `npm run build`; only a .gitkeep placeholder is tracked until Task 19 wires
// the final embedding.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist returns the dashboard build rooted at the dist directory.
func Dist() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		// The subtree is embedded at compile time; this can only fail if the
		// embed directive is changed incorrectly.
		panic("ui: embedded dist directory is missing: " + err.Error())
	}
	return dist
}
