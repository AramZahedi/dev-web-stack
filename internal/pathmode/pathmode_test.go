package pathmode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestValidateSourceMode(t *testing.T) {
	tests := []struct {
		mode     model.SourceMode
		platform string
		ok       bool
	}{
		{model.SourceLocal, "linux", true},
		{model.SourceWindows, "linux", false},
		{model.SourceWSL, "linux", false},
		{model.SourceLocal, "windows", false},
		{model.SourceWindows, "windows", true},
		{model.SourceWSL, "windows", true},
		{"", "windows", false},
		{"bogus", "windows", false},
	}
	for _, tt := range tests {
		err := ValidateSourceMode(tt.mode, tt.platform)
		if tt.ok && err != nil {
			t.Fatalf("%s/%s: %v", tt.mode, tt.platform, err)
		}
		if !tt.ok && !errors.Is(err, ErrInvalidSourceMode) {
			t.Fatalf("%s/%s: %v, want ErrInvalidSourceMode", tt.mode, tt.platform, err)
		}
	}
}

func TestTranslateSourceEnforcesConfiguredDistroAndCDrive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   model.SourceMode
		path   string
		distro string
		ok     bool
	}{
		{"configured localhost distro", model.SourceWSL, `\\wsl.localhost\Ubuntu\home\aram\site`, "Ubuntu", true},
		{"configured legacy alias", model.SourceWSL, `\\wsl$\Ubuntu\home\aram\site`, "Ubuntu", true},
		{"other distro", model.SourceWSL, `\\wsl.localhost\Other\home\aram\site`, "Ubuntu", false},
		{"C drive", model.SourceWindows, `C:\devstack\projects\site`, "", true},
		{"other drive", model.SourceWindows, `D:\projects\site`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, backend, err := TranslateSourceForDistro(tc.mode, tc.path, tc.distro)
			if tc.ok && err != nil {
				t.Fatalf("TranslateSourceForDistro: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("TranslateSourceForDistro = %q, %q, want error", host, backend)
			}
		})
	}
}

func TestValidateCreatedPathRejectsDotBasenameOnly(t *testing.T) {
	if err := ValidateCreatedPath(`C:\devstack\projects\.draft`); err == nil {
		t.Fatal("dot-prefixed created project directory was accepted")
	}
	if err := ValidateCreatedPath(`C:\devstack\projects\site`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := TranslateSource(model.SourceWindows, `C:\devstack\projects\site\.git`); err != nil {
		t.Fatalf("project-owned dot directory was rejected: %v", err)
	}
}

func TestContainedPathUsesWindowsCaseInsensitiveBoundaries(t *testing.T) {
	if !ContainsPath(`C:\Projects\Site`, `c:\projects\site\public`, true) {
		t.Fatal("Windows containment should ignore case")
	}
	if ContainsPath(`C:\Projects\Site`, `C:\Projects\Site-old\public`, true) {
		t.Fatal("sibling path was treated as contained")
	}
	if ContainsPath("/projects/Site", "/projects/site/public", false) {
		t.Fatal("Linux containment should remain case-sensitive")
	}
}

func TestValidateContainedPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "project")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inside, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}
	if err := ValidateContainedPath(inside, filepath.Join(link, "secret"), false); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestTranslateSource(t *testing.T) {
	tests := []struct {
		name    string
		mode    model.SourceMode
		input   string
		host    string
		backend string
		wantErr bool
	}{
		{"windows drive", model.SourceWindows, `C:\Users\Aram\app`, `C:\Users\Aram\app`, "/mnt/c/Users/Aram/app", false},
		{"windows slashes", model.SourceWindows, `C:/Users/Aram/app/`, `C:\Users\Aram\app`, "/mnt/c/Users/Aram/app", false},
		{"windows lowercase C drive", model.SourceWindows, `c:\code\app`, `C:\code\app`, "/mnt/c/code/app", false},
		{"wsl localhost", model.SourceWSL, `\\wsl.localhost\Ubuntu\home\aram\app`, `\\wsl.localhost\Ubuntu\home\aram\app`, "/home/aram/app", false},
		{"wsl dollar", model.SourceWSL, `\\wsl$\Ubuntu\home\aram\app`, `\\wsl.localhost\Ubuntu\home\aram\app`, "/home/aram/app", false},
		{"local", model.SourceLocal, "/home/aram/app", "/home/aram/app", "/home/aram/app", false},
		{"local clean", model.SourceLocal, "/home/aram/./app/", "/home/aram/app", "/home/aram/app", false},
		{"empty", model.SourceWindows, "", "", "", true},
		{"windows relative", model.SourceWindows, `app`, "", "", true},
		{"windows UNC", model.SourceWindows, `\\server\share\app`, "", "", true},
		{"windows device", model.SourceWindows, `\\?\C:\app`, "", "", true},
		{"windows ADS", model.SourceWindows, `C:\app\file:stream`, "", "", true},
		{"windows escape", model.SourceWindows, `C:\app\..\other`, "", "", true},
		{"windows root only", model.SourceWindows, `C:\`, "", "", true},
		{"wsl drive", model.SourceWSL, `C:\app`, "", "", true},
		{"wsl other UNC", model.SourceWSL, `\\server\share\app`, "", "", true},
		{"wsl root only", model.SourceWSL, `\\wsl.localhost\Ubuntu`, "", "", true},
		{"wsl escape", model.SourceWSL, `\\wsl.localhost\Ubuntu\home\..\..\etc`, "", "", true},
		{"local relative", model.SourceLocal, "app", "", "", true},
		{"local escape", model.SourceLocal, "/home/aram/../../etc", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, backend, err := TranslateSource(tt.mode, tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("TranslateSource = %q, %q, want error", host, backend)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if host != tt.host || backend != tt.backend {
				t.Fatalf("TranslateSource = %q, %q; want %q, %q", host, backend, tt.host, tt.backend)
			}
		})
	}
}

func TestProxyDocumentRoot(t *testing.T) {
	tests := []struct {
		name string
		site model.Site
		want string
		err  bool
	}{
		{"windows", model.Site{SourceMode: model.SourceWindows, HostPath: `C:\app`, DocumentRoot: "public"}, `C:\app\public`, false},
		{"wsl", model.Site{SourceMode: model.SourceWSL, HostPath: `\\wsl.localhost\Ubuntu\home\a\app`, DocumentRoot: "public"}, `\\wsl.localhost\Ubuntu\home\a\app\public`, false},
		{"local", model.Site{SourceMode: model.SourceLocal, HostPath: "/home/a/app", DocumentRoot: "public"}, "/home/a/app/public", false},
		{"empty root", model.Site{SourceMode: model.SourceWindows, HostPath: `C:\app`}, `C:\app`, false},
		{"absolute", model.Site{SourceMode: model.SourceWindows, HostPath: `C:\app`, DocumentRoot: `C:\other`}, "", true},
		{"escape", model.Site{SourceMode: model.SourceWindows, HostPath: `C:\app`, DocumentRoot: `../other`}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProxyDocumentRoot(tt.site)
			if tt.err {
				if err == nil {
					t.Fatalf("ProxyDocumentRoot = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ProxyDocumentRoot = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultSourceMode(t *testing.T) {
	if got := DefaultSourceMode(model.SitePHP, "linux"); got != model.SourceLocal {
		t.Fatalf("linux php = %q", got)
	}
	if got := DefaultSourceMode(model.SitePHP, "windows"); got != model.SourceWSL {
		t.Fatalf("windows php = %q", got)
	}
	if got := DefaultSourceMode(model.SiteNode, "windows"); got != model.SourceWSL {
		t.Fatalf("windows node = %q", got)
	}
	if got := DefaultSourceMode(model.SiteStatic, "windows"); got != model.SourceWindows {
		t.Fatalf("windows static = %q", got)
	}
}
