package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPHPManifestDefaultsAndDependencyOrder(t *testing.T) {
	m, err := LoadPHP()
	if err != nil {
		t.Fatal(err)
	}
	d, ok := m.DefaultsFor("8.4")
	if !ok || d.Ini["memory_limit"] != "256M" || d.Ini["opcache.revalidate_freq"] != "0" {
		t.Fatalf("%+v", d)
	}
	if !d.Extensions["opcache"] || d.Extensions["xdebug"] || d.Extensions["ffi"] {
		t.Fatalf("extension defaults = %+v", d.Extensions)
	}
	if _, ok := d.Extensions["imap"]; ok {
		t.Fatal("imap must not be available on 8.4")
	}

	resolved, err := m.Resolve("8.4", map[string]bool{"redis": true, "xdebug": true})
	if err != nil {
		t.Fatal(err)
	}
	index := func(name string) int {
		for i, ext := range resolved {
			if ext.Name == name {
				return i
			}
		}
		return -1
	}
	if index("igbinary") < 0 || index("igbinary") > index("redis") {
		t.Fatalf("order = %+v", resolved)
	}
	if index("xdebug") < 0 || index("opcache") < 0 {
		t.Fatalf("zend extensions = %+v", resolved)
	}
	for _, ext := range resolved {
		if (ext.Name == "xdebug" || ext.Name == "opcache") && ext.Type != ExtensionZend {
			t.Errorf("%s type = %q, want zend_extension", ext.Name, ext.Type)
		}
	}
	igbinary := resolved[index("igbinary")]
	if !igbinary.Required || len(igbinary.RequiredBy) != 1 || igbinary.RequiredBy[0] != "redis" {
		t.Fatalf("igbinary = %+v, want required by redis", igbinary)
	}

	if _, err := m.Resolve("8.4", map[string]bool{"imap": true}); err == nil {
		t.Fatal("imap must be unavailable on 8.4")
	}
	if _, err := m.Resolve("8.4", map[string]bool{"core": true}); err == nil {
		t.Fatal("built-in module must not be toggleable")
	}
	if _, err := m.Resolve("8.4", map[string]bool{"filter": true}); err == nil {
		t.Fatal("filter must be built-in and not toggleable")
	}
	if _, err := m.Resolve("8.4", map[string]bool{"nope": true}); err == nil {
		t.Fatal("unknown extension must be rejected")
	}
	if _, err := m.Resolve("8.1", nil); err == nil {
		t.Fatal("unsupported version must be rejected")
	}
	if _, err := m.Resolve("8.2", map[string]bool{"imap": true}); err != nil {
		t.Fatalf("imap on 8.2: %v", err)
	}
	if _, err := m.Resolve("8.3", map[string]bool{"imap": true}); err != nil {
		t.Fatalf("imap on 8.3: %v", err)
	}
}

func TestPHPManifestCoversSpecExtensions(t *testing.T) {
	m, err := LoadPHP()
	if err != nil {
		t.Fatal(err)
	}
	builtins := make(map[string]bool, len(m.Builtin))
	for _, name := range m.Builtin {
		builtins[name] = true
	}
	for _, want := range []string{"core", "ctype", "date", "filter", "hash", "json", "pcre", "random", "spl", "standard"} {
		if !builtins[want] {
			t.Errorf("built-in %q is missing", want)
		}
	}

	standard := []string{
		"bcmath", "bz2", "calendar", "curl", "dba", "dom", "exif", "ffi",
		"fileinfo", "ftp", "gd", "gettext", "gmp", "iconv", "intl", "ldap",
		"mbstring", "mysqli", "opcache", "openssl", "pcntl", "pdo", "pdo_mysql",
		"pdo_pgsql", "pdo_sqlite", "pgsql", "phar", "posix", "readline",
		"session", "simplexml", "soap", "sockets", "sodium", "sqlite3",
		"sysvmsg", "sysvsem", "sysvshm", "tidy", "tokenizer", "xml",
		"xmlreader", "xmlwriter", "xsl", "zip", "zlib",
	}
	extras := []string{"redis", "apcu", "xdebug", "imagick", "memcached", "ssh2", "yaml", "igbinary", "msgpack", "grpc", "pcov", "imap"}
	byName := make(map[string]Extension, len(m.Extensions))
	for _, ext := range m.Extensions {
		if byName[ext.Name].Name != "" {
			t.Fatalf("duplicate extension %q", ext.Name)
		}
		byName[ext.Name] = ext
		if builtins[ext.Name] {
			t.Errorf("extension %q is also built-in", ext.Name)
		}
		if ext.SO != ext.Name+".so" {
			t.Errorf("%s so = %q, want %q", ext.Name, ext.SO, ext.Name+".so")
		}
		if ext.Type != ExtensionShared && ext.Type != ExtensionZend {
			t.Errorf("%s type = %q", ext.Name, ext.Type)
		}
	}
	if _, ok := byName["filter"]; ok {
		t.Fatal("filter must be built-in, not a toggleable extension")
	}
	for _, name := range standard {
		ext, ok := byName[name]
		if !ok {
			t.Errorf("standard extension %q is missing", name)
			continue
		}
		if !ext.Standard {
			t.Errorf("%s is not marked standard", name)
		}
		wantDefault := name != "ffi"
		if ext.Default != wantDefault {
			t.Errorf("%s default = %v, want %v", name, ext.Default, wantDefault)
		}
	}
	for _, name := range extras {
		ext, ok := byName[name]
		if !ok {
			t.Errorf("extra extension %q is missing", name)
			continue
		}
		if ext.Standard || ext.Default {
			t.Errorf("%s standard/default = %v/%v, want false/false", name, ext.Standard, ext.Default)
		}
	}

	wantVersions := map[string][]string{
		"imap": {"8.2", "8.3"},
	}
	for name, ext := range byName {
		want, ok := wantVersions[name]
		if !ok {
			want = []string{"8.2", "8.3", "8.4", "8.5"}
		}
		if strings.Join(ext.Versions, ",") != strings.Join(want, ",") {
			t.Errorf("%s versions = %v, want %v", name, ext.Versions, want)
		}
	}
	for _, version := range []string{"8.2", "8.3", "8.4", "8.5"} {
		d, ok := m.DefaultsFor(version)
		if !ok {
			t.Fatalf("missing defaults for %s", version)
		}
		if d.Ini["display_errors"] != "On" || d.Ini["error_reporting"] != "E_ALL" ||
			d.Ini["upload_max_filesize"] != "64M" || d.Ini["post_max_size"] != "64M" ||
			d.Ini["max_execution_time"] != "120" || d.Ini["date.timezone"] != "UTC" ||
			d.Ini["opcache.enable"] != "1" || d.Ini["opcache.validate_timestamps"] != "1" {
			t.Errorf("%s baseline ini = %+v", version, d.Ini)
		}
	}
}

func TestResolveWithDefaultsOverlaysVersionDefaults(t *testing.T) {
	m, err := LoadPHP()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := m.ResolveWithDefaults("8.4",
		map[string]bool{"ffi": true, "redis": true, "gd": false},
		map[string]bool{"xdebug": true})
	if err != nil {
		t.Fatal(err)
	}
	enabled := make(map[string]ResolvedExtension, len(resolved))
	for _, ext := range resolved {
		enabled[ext.Name] = ext
	}
	if _, ok := enabled["gd"]; ok {
		t.Fatal("edited default did not disable gd")
	}
	if ffi, ok := enabled["ffi"]; !ok || ffi.Required {
		t.Fatalf("ffi not enabled explicitly: %+v", ffi)
	}
	if _, ok := enabled["redis"]; !ok {
		t.Fatal("edited default did not enable redis")
	}
	if _, ok := enabled["igbinary"]; !ok {
		t.Fatal("redis dependency igbinary was not auto-included")
	}
	if ext, ok := enabled["xdebug"]; !ok || ext.Type != ExtensionZend {
		t.Fatalf("xdebug = %+v", ext)
	}

	resolved, err = m.ResolveWithDefaults("8.4", map[string]bool{"xdebug": true}, map[string]bool{"xdebug": false})
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range resolved {
		if ext.Name == "xdebug" {
			t.Fatal("explicit override did not beat the supplied defaults")
		}
	}

	if _, err := m.ResolveWithDefaults("8.4", map[string]bool{"imap": true}, nil); err == nil {
		t.Fatal("unsupported default must be rejected")
	}
	if _, err := m.ResolveWithDefaults("8.4", map[string]bool{"core": true}, nil); err == nil {
		t.Fatal("built-in default must be rejected")
	}
	if _, err := m.ResolveWithDefaults("8.4", map[string]bool{"nope": true}, nil); err == nil {
		t.Fatal("unknown default must be rejected")
	}
}

func TestDefaultsForReturnsCopy(t *testing.T) {
	m, err := LoadPHP()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := m.DefaultsFor("8.4")
	d.Ini["memory_limit"] = "1G"
	d.Extensions["redis"] = true
	again, _ := m.DefaultsFor("8.4")
	if again.Ini["memory_limit"] != "256M" || again.Extensions["redis"] {
		t.Fatal("DefaultsFor returned aliased maps")
	}
}

func TestParsePHPRejectsInvalidManifests(t *testing.T) {
	base := func() PHPManifest {
		return PHPManifest{
			Builtin: []string{"core", "filter"},
			Extensions: []Extension{
				{Name: "a", Type: ExtensionShared, SO: "a.so", Default: true, Standard: true, Versions: []string{"8.4"}},
				{Name: "b", Type: ExtensionShared, SO: "b.so", Versions: []string{"8.4"}},
			},
			Versions: map[string]PHPVersionDefaults{
				"8.4": {Ini: map[string]string{"memory_limit": "256M"}, Extensions: map[string]bool{"a": true, "b": false}},
			},
		}
	}
	tests := []struct {
		name    string
		mutate  func(*PHPManifest)
		wantErr string
	}{
		{name: "valid", mutate: func(*PHPManifest) {}},
		{name: "duplicate extension", mutate: func(m *PHPManifest) {
			m.Extensions = append(m.Extensions, m.Extensions[0])
		}, wantErr: "duplicate"},
		{name: "builtin extension collision", mutate: func(m *PHPManifest) {
			m.Extensions[0].Name = "core"
			m.Extensions[0].SO = "core.so"
			d := m.Versions["8.4"]
			d.Extensions = map[string]bool{"core": true, "b": false}
			m.Versions["8.4"] = d
		}, wantErr: "built-in"},
		{name: "unknown requires", mutate: func(m *PHPManifest) {
			m.Extensions[0].Requires = []string{"missing"}
		}, wantErr: "requires"},
		{name: "builtin requires", mutate: func(m *PHPManifest) {
			m.Extensions[0].Requires = []string{"core"}
		}, wantErr: "built-in"},
		{name: "dependency cycle", mutate: func(m *PHPManifest) {
			m.Extensions[0].Requires = []string{"b"}
			m.Extensions[1].Requires = []string{"a"}
		}, wantErr: "cycle"},
		{name: "unsupported version key", mutate: func(m *PHPManifest) {
			m.Versions["9.0"] = PHPVersionDefaults{Extensions: map[string]bool{"a": true}}
		}, wantErr: "version"},
		{name: "extension version without defaults", mutate: func(m *PHPManifest) {
			m.Extensions[0].Versions = []string{"8.4", "8.5"}
		}, wantErr: "version"},
		{name: "default for unsupported extension", mutate: func(m *PHPManifest) {
			m.Extensions[0].Versions = []string{"8.2"}
			m.Versions["8.2"] = PHPVersionDefaults{Extensions: map[string]bool{"a": true}}
		}, wantErr: "not available"},
		{name: "invalid so", mutate: func(m *PHPManifest) {
			m.Extensions[0].SO = "../a.so"
		}, wantErr: "so"},
		{name: "invalid type", mutate: func(m *PHPManifest) {
			m.Extensions[0].Type = "module"
		}, wantErr: "type"},
		{name: "empty builtin", mutate: func(m *PHPManifest) {
			m.Builtin = nil
		}, wantErr: "built-in"},
		{name: "empty versions", mutate: func(m *PHPManifest) {
			m.Versions = nil
		}, wantErr: "version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base()
			test.mutate(&manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParsePHP(data)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ParsePHP: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParsePHP accepted invalid manifest, want error containing %q", test.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("ParsePHP error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestResolveRejectsCycleInConstructedManifest(t *testing.T) {
	m := PHPManifest{
		Builtin: []string{"core"},
		Extensions: []Extension{
			{Name: "a", Type: ExtensionShared, SO: "a.so", Versions: []string{"8.4"}, Requires: []string{"b"}},
			{Name: "b", Type: ExtensionShared, SO: "b.so", Versions: []string{"8.4"}, Requires: []string{"a"}},
		},
		Versions: map[string]PHPVersionDefaults{
			"8.4": {Extensions: map[string]bool{"a": true, "b": false}},
		},
	}
	if _, err := m.Resolve("8.4", nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Fatalf("Resolve error = %v, want cycle", err)
	}
}

func TestResolveWrapsErrInvalidExtension(t *testing.T) {
	m, err := LoadPHP()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		overrides map[string]bool
	}{
		{name: "unknown", overrides: map[string]bool{"nope": true}},
		{name: "built-in", overrides: map[string]bool{"core": true}},
		{name: "unavailable", overrides: map[string]bool{"imap": true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := m.Resolve("8.4", test.overrides); err == nil || !errors.Is(err, ErrInvalidExtension) {
				t.Fatalf("Resolve(%s) error = %v, want ErrInvalidExtension", test.name, err)
			}
		})
	}

	cycle := PHPManifest{
		Builtin: []string{"core"},
		Extensions: []Extension{
			{Name: "a", Type: ExtensionShared, SO: "a.so", Versions: []string{"8.4"}, Requires: []string{"b"}},
			{Name: "b", Type: ExtensionShared, SO: "b.so", Versions: []string{"8.4"}, Requires: []string{"a"}},
		},
		Versions: map[string]PHPVersionDefaults{
			"8.4": {Extensions: map[string]bool{"a": true, "b": false}},
		},
	}
	if _, err := cycle.Resolve("8.4", nil); err == nil || !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("cycle Resolve error = %v, want ErrInvalidExtension", err)
	}

	if _, err := m.Resolve("9.9", nil); err == nil || errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("unsupported version error = %v, want a non-extension error", err)
	}
	if _, err := ParsePHP([]byte("{")); err == nil || errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("malformed manifest error = %v, want a non-extension error", err)
	}
}
