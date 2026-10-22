package model

import (
	"encoding/json"
	"reflect"
	"testing"
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
		PHPExtensions:   map[string]bool{"xdebug": true},
		PHPIni:          map[string]string{"memory_limit": "512M"},
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
		"kind", "php_version", "php_extensions", "php_ini", "node_version",
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
