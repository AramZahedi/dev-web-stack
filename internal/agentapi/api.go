// Package agentapi is the module seam between the agent server and feature
// modules owned by the Docker, relay, and web lanes. The server mounts modules
// behind authentication without importing their implementation packages.
package agentapi

import (
	"net/http"
	"strings"
)

// Module is one authenticated agent feature mounted at a fixed prefix.
type Module interface {
	Name() string
	MountPath() string
	Handler() http.Handler
}

// Mount registers a module below mux. Mount paths are absolute and are
// normalised to a trailing slash so a module owns its whole subtree.
func Mount(mux *http.ServeMux, module Module) {
	path := module.MountPath()
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/") + "/"
	mux.Handle(path, module.Handler())
}
