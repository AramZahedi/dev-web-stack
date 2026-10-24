package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestEnumValues(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"SiteStatic", string(SiteStatic), "static"},
		{"SitePHP", string(SitePHP), "php"},
		{"SiteNode", string(SiteNode), "node"},
		{"DesiredRunning", string(DesiredRunning), "running"},
		{"DesiredStopped", string(DesiredStopped), "stopped"},
		{"StatusStopped", string(StatusStopped), "stopped"},
		{"StatusStarting", string(StatusStarting), "starting"},
		{"StatusRunning", string(StatusRunning), "running"},
		{"StatusStopping", string(StatusStopping), "stopping"},
		{"StatusCrashed", string(StatusCrashed), "crashed"},
		{"StatusError", string(StatusError), "error"},
		{"BackendLocal", string(BackendLocal), "local"},
		{"BackendWSL", string(BackendWSL), "wsl"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestSiteJSONRoundTrip(t *testing.T) {
	site := Site{
		ID:              "s1",
		Name:            "blog",
		Domain:          "blog.test",
		WWW:             true,
		Backend:         BackendLocal,
		HostPath:        "/home/aram/code/blog",
		BackendPath:     "/home/aram/code/blog",
		Kind:            SitePHP,
		PHPVersion:      "8.5",
		PHPBuild:        "php@8.5.1-r2",
		PHPExtensions:   map[string]bool{"xdebug": true},
		PHPIni:          map[string]string{"memory_limit": "512M"},
		PHPIniRaw:       "memory_limit = 512M\n",
		PHPConfigHash:   "hash-8.5",
		NodeVersion:     "",
		Command:         "",
		Env:             map[string]string{"APP_ENV": "local"},
		DocumentRoot:    "public",
		FrontController: true,
		SPAFallback:     false,
		TLS:             true,
		ForceHTTPS:      true,
		DesiredState:    DesiredRunning,
		Status:          StatusRunning,
		HTTPPort:        41001,
		FastCGIPort:     43001,
	}

	data, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"id", "name", "domain", "www", "backend", "host_path", "backend_path",
		"kind", "php_version", "php_build", "php_ini", "php_ini_raw",
		"php_config_hash", "php_extensions", "node_version",
		"command", "env", "document_root", "front_controller", "spa_fallback",
		"tls", "force_https", "desired_state", "status", "http_port",
		"fastcgi_port", "created_at", "updated_at",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("marshaled site is missing %q", key)
		}
	}

	var got Site
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Env, site.Env) {
		t.Fatalf("Env = %#v, want %#v", got.Env, site.Env)
	}
	if got.Kind != SitePHP || got.Backend != BackendLocal || got.Status != StatusRunning {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestPHPConfigJSONRoundTrip(t *testing.T) {
	defaults := PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "256M"},
		Extensions: map[string]bool{"xdebug": false, "opcache": true},
	}
	data, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	var gotDefaults PHPDefaults
	if err := json.Unmarshal(data, &gotDefaults); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDefaults, defaults) {
		t.Fatalf("PHPDefaults round trip = %+v, want %+v", gotDefaults, defaults)
	}

	applied := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cfg := SitePHPConfig{
		SiteID:     "s1",
		Ini:        defaults.Ini,
		Extensions: defaults.Extensions,
		ConfigHash: "hash",
		AppliedAt:  applied,
	}
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var gotConfig SitePHPConfig
	if err := json.Unmarshal(data, &gotConfig); err != nil {
		t.Fatal(err)
	}
	if !gotConfig.AppliedAt.Equal(applied) {
		t.Fatalf("AppliedAt = %v, want %v", gotConfig.AppliedAt, applied)
	}
	gotConfig.AppliedAt = cfg.AppliedAt
	if !reflect.DeepEqual(gotConfig, cfg) {
		t.Fatalf("SitePHPConfig round trip = %+v, want %+v", gotConfig, cfg)
	}
}

func TestRecordsCompileWithSpecFields(t *testing.T) {
	build := RuntimeBuild{
		ID:      "node-24.9.0",
		Kind:    "node",
		Version: "24.9.0",
		Backend: BackendLocal,
		Path:    "/home/aram/.devstack/runtimes/node/24.9.0-r1",
	}
	cert := CertRecord{
		SiteID:   "s1",
		Serial:   "01",
		Domains:  []string{"blog.test"},
		KeyPath:  "/home/aram/.devstack/pki/blog.key",
		CertPath: "/home/aram/.devstack/pki/blog.crt",
	}
	event := Event{ID: 1, SiteID: "s1", Type: "status", Message: "running"}

	if build.Kind != "node" || cert.Domains[0] != "blog.test" || event.Message != "running" {
		t.Fatalf("unexpected records: %+v %+v %+v", build, cert, event)
	}
}
