package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePHPTargetUsesProxyDocumentRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	route := Route{
		Kind:              KindPHP,
		DocumentRoot:      "/wsl/backend/root",
		ProxyDocumentRoot: dir,
		FrontController:   true,
	}
	target, ok := resolvePHPTarget(route, "/")
	if !ok {
		t.Fatal("target not found")
	}
	if target.documentRoot != dir {
		t.Fatalf("document root = %q, want %q", target.documentRoot, dir)
	}
	if got := fastCGIRoot(route, target); got != "/wsl/backend/root" {
		t.Fatalf("FPM document root = %q", got)
	}
	if got := fastCGIScriptPath(route, target); got != filepath.Join("/wsl/backend/root", "index.php") {
		t.Fatalf("FPM script = %q", got)
	}
}
