package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestCLIServingStatusAlias(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		vhostStatus: helperproto.VhostStatusResponse{
			Mode:     config.ServingModeBuiltin,
			Server:   config.ServingModeBuiltin,
			ConfigOK: true,
		},
	}
	out := runCLI(t, fake, "serving", "status")
	if !strings.Contains(out, "builtin") {
		t.Fatalf("serving status output = %q, want builtin", out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("serving status output = %q, want ok", out)
	}
}

func TestCLIVhostUseWindowsUnsupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("external serving modes are rejected only by the Windows daemon")
	}
	useTestSeams(t)

	t.Run("vhost use nginx fails with clear message", func(t *testing.T) {
		fake := &fakeClient{
			err: helperproto.ErrNotImplemented,
		}
		code, _, stderr := runCLIExit(t, fake, "vhost", "use", "nginx", "--yes")
		if code == 0 {
			t.Fatal("want non-zero exit code on Windows when switching to external mode")
		}
		want := "external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
		if fake.servingModeSet {
			t.Fatal("servingModeSet should be false")
		}
	})

	t.Run("serving use apache fails with clear message", func(t *testing.T) {
		fake := &fakeClient{
			err: helperproto.ErrNotImplemented,
		}
		code, _, stderr := runCLIExit(t, fake, "serving", "use", "apache", "--yes")
		if code == 0 {
			t.Fatal("want non-zero exit code on Windows when switching to external mode")
		}
		want := "external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
		if fake.servingModeSet {
			t.Fatal("servingModeSet should be false")
		}
	})

	t.Run("serving sync fails when not supported", func(t *testing.T) {
		fake := &fakeClient{
			err: helperproto.ErrNotImplemented,
		}
		code, _, stderr := runCLIExit(t, fake, "serving", "sync")
		if code == 0 {
			t.Fatal("want non-zero exit code on Windows when syncing external vhosts")
		}
		want := "external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})
}
