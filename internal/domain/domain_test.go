package domain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateDomain(t *testing.T) {
	good := []string{"blog.test", "my-shop.test", "www.blog.test", "devstack.test"}
	for _, d := range good {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	bad := []string{"blog.dev", "Blog.test", "blog..test", "-bad.test", "a_b.test"}
	for _, d := range bad {
		if err := ValidateDomain(d); err == nil {
			t.Errorf("%s: want error", d)
		}
	}
	if err := ValidateSiteDomain("devstack.test"); err == nil {
		t.Fatal("devstack.test must be reserved for sites")
	}
}

func TestValidateDomainRules(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		wantOK bool
	}{
		{"simple", "blog.test", true},
		{"hyphenated", "my-shop.test", true},
		{"subdomain", "www.blog.test", true},
		{"numeric label", "123.test", true},
		{"single character label", "a.test", true},
		{"max label length", strings.Repeat("a", 63) + ".test", true},
		{"label too long", strings.Repeat("a", 64) + ".test", false},
		{"empty", "", false},
		{"suffix only", "test", false},
		{"dot suffix only", ".test", false},
		{"trailing dot", "blog.test.", false},
		{"other tld", "blog.dev", false},
		{"uppercase label", "Blog.test", false},
		{"uppercase tld", "blog.TEST", false},
		{"empty label", "blog..test", false},
		{"leading hyphen", "-blog.test", false},
		{"trailing hyphen", "blog-.test", false},
		{"underscore", "my_blog.test", false},
		{"space", "my blog.test", false},
		{"non-ascii", "café.test", false},
		{"slash", "blog/test.test", false},
		{"trailing newline", "blog.test\n", false},
		{"wildcard", "*.blog.test", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDomain(tt.domain)
			if tt.wantOK && err != nil {
				t.Fatalf("ValidateDomain(%q) = %v, want nil", tt.domain, err)
			}
			if !tt.wantOK && err == nil {
				t.Fatalf("ValidateDomain(%q) = nil, want error", tt.domain)
			}
		})
	}
}

func TestValidateDomainTotalLength(t *testing.T) {
	// 197+56 = 253 characters: the DNS limit must be accepted.
	build := func(extra int) string {
		return strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
			strings.Repeat("c", 63) + "." + strings.Repeat("d", extra) + ".test"
	}
	atLimit := build(56)
	if len(atLimit) != 253 {
		t.Fatalf("test domain length = %d, want 253", len(atLimit))
	}
	if err := ValidateDomain(atLimit); err != nil {
		t.Fatalf("253-character domain rejected: %v", err)
	}
	if err := ValidateDomain(build(57)); err == nil {
		t.Fatal("254-character domain accepted")
	}
}

func TestValidateSiteDomain(t *testing.T) {
	for _, d := range []string{"blog.test", "my-shop.test", "www.blog.test"} {
		if err := ValidateSiteDomain(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	if err := ValidateSiteDomain("devstack.test"); err == nil {
		t.Fatal("devstack.test must be reserved for sites")
	}
	if err := ValidateSiteDomain("blog.dev"); err == nil {
		t.Fatal("blog.dev must be rejected")
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{"lowercase", "blog", "blog", true},
		{"mixed case", "Blog", "blog", true},
		{"upper hyphen", "MY-Shop", "my-shop", true},
		{"single character", "a", "a", true},
		{"numeric", "123", "123", true},
		{"max length", strings.Repeat("a", 63), strings.Repeat("a", 63), true},
		{"empty", "", "", false},
		{"name too long", strings.Repeat("a", 64), "", false},
		{"space", "my shop", "", false},
		{"leading space", " blog", "", false},
		{"trailing space", "blog ", "", false},
		{"tab", "my\tshop", "", false},
		{"newline", "my\nshop", "", false},
		{"forward slash", "a/b", "", false},
		{"backslash", `a\b`, "", false},
		{"path traversal", "../evil", "", false},
		{"dot", ".", "", false},
		{"domain", "blog.test", "", false},
		{"leading hyphen", "-blog", "", false},
		{"trailing hyphen", "blog-", "", false},
		{"underscore", "my_blog", "", false},
		{"non-ascii", "café", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.input)
			if !tt.wantOK {
				if err == nil {
					t.Fatalf("NormalizeName(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName(%q) = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDomainFor(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"blog", "blog.test"},
		{"my-shop", "my-shop.test"},
	} {
		if got := DomainFor(tt.name); got != tt.want {
			t.Errorf("DomainFor(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestValidateProjectPath(t *testing.T) {
	dir := t.TempDir()
	got, err := ValidateProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ValidateProjectPath(%q) = %q, want %q", dir, got, want)
	}
}

func TestValidateProjectPathResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := ValidateProjectPath(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ValidateProjectPath(%q) = %q, want %q", link, got, want)
	}
}

func TestValidateProjectPathRejects(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o755) })

	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "relative/project"},
		{"missing", filepath.Join(dir, "missing")},
		{"file", file},
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		tests = append(tests, struct {
			name string
			path string
		}{"unreadable", private})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateProjectPath(tt.path); err == nil {
				t.Fatalf("ValidateProjectPath(%q) = nil error, want error", tt.path)
			}
		})
	}
}
