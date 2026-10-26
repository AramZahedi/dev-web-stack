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

func TestPHPFPMBudgetValidate(t *testing.T) {
	valid := PHPFPMBudget{
		Mode:            "dynamic",
		MaxChildren:     12,
		StartServers:    3,
		MinSpareServers: 2,
		MaxSpareServers: 4,
		ListenBacklog:   511,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid budget rejected: %v", err)
	}
	static := PHPFPMBudget{Mode: "static", MaxChildren: 8, ListenBacklog: 128}
	if err := static.Validate(); err != nil {
		t.Fatalf("valid static budget rejected: %v", err)
	}

	cases := []struct {
		name   string
		budget PHPFPMBudget
	}{
		{"unknown mode", PHPFPMBudget{Mode: "ondemand", MaxChildren: 8}},
		{"zero children", PHPFPMBudget{Mode: "dynamic", StartServers: 1}},
		{"negative children", PHPFPMBudget{Mode: "dynamic", MaxChildren: -1}},
		{"too many children", PHPFPMBudget{Mode: "dynamic", MaxChildren: 100001}},
		{"negative start", PHPFPMBudget{Mode: "dynamic", MaxChildren: 8, StartServers: -1}},
		{"start exceeds children", PHPFPMBudget{Mode: "dynamic", MaxChildren: 4, StartServers: 5}},
		{"min exceeds max spare", PHPFPMBudget{Mode: "dynamic", MaxChildren: 8, MinSpareServers: 5, MaxSpareServers: 4}},
		{"start below min spare", PHPFPMBudget{Mode: "dynamic", MaxChildren: 8, StartServers: 1, MinSpareServers: 4, MaxSpareServers: 6}},
		{"start above max spare", PHPFPMBudget{Mode: "dynamic", MaxChildren: 8, StartServers: 7, MinSpareServers: 1, MaxSpareServers: 6}},
		{"max spare exceeds children", PHPFPMBudget{Mode: "dynamic", MaxChildren: 4, MaxSpareServers: 5}},
		{"backlog out of range", PHPFPMBudget{Mode: "dynamic", MaxChildren: 8, ListenBacklog: 65536}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.budget.Validate(); err == nil {
				t.Fatalf("budget %+v accepted", tc.budget)
			}
		})
	}
}

func TestSitePHPFPMJSONRoundTrip(t *testing.T) {
	site := Site{
		ID: "s1", Name: "blog", Domain: "blog.test", Kind: SitePHP,
		PHPFPM: &PHPFPMBudget{Mode: "static", MaxChildren: 8, ListenBacklog: 256},
	}
	data, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	var got Site
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.PHPFPM == nil || *got.PHPFPM != *site.PHPFPM {
		t.Fatalf("PHPFPM = %+v, want %+v", got.PHPFPM, site.PHPFPM)
	}
}

func TestBuildProfileJSONRoundTrip(t *testing.T) {
	bp := BuildProfile{
		NodeVersion: "20.10.0",
		Command:     "npm run build",
		Output:      "dist",
		Env:         map[string]string{"NODE_ENV": "production"},
	}
	data, err := json.Marshal(bp)
	if err != nil {
		t.Fatal(err)
	}
	var got BuildProfile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, bp) {
		t.Fatalf("got %+v, want %+v", got, bp)
	}
}

func TestSiteContractsJSONRoundTrip(t *testing.T) {
	site := Site{
		ID:                  "s1",
		Name:                "blog",
		Domain:              "blog.test",
		Kind:                SitePHP,
		FrontControllerFile: "bootstrap.php",
		FrontControllerMode: "generated",
		ApacheAllowOverride: "fileinfo",
		SPAFallbackFile:     "app.html",
		Build: &BuildProfile{
			NodeVersion: "20.10.0",
			Command:     "npm run build",
			Output:      "dist",
			Env:         map[string]string{"NODE_ENV": "production"},
		},
	}
	data, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	var got Site
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.FrontControllerFile != "bootstrap.php" ||
		got.FrontControllerMode != "generated" ||
		got.ApacheAllowOverride != "fileinfo" ||
		got.SPAFallbackFile != "app.html" ||
		got.Build == nil ||
		got.Build.Command != "npm run build" {
		t.Fatalf("site contracts round trip failed: %+v", got)
	}
}

func TestValidateSiteContracts(t *testing.T) {
	// Valid defaults / empty
	if err := ValidateSiteContracts("", "", "", "", nil); err != nil {
		t.Fatalf("expected nil for empty contracts, got %v", err)
	}

	// Valid populated
	bp := &BuildProfile{
		Command:     "npm run build",
		NodeVersion: "20",
		Output:      "dist",
	}
	if err := ValidateSiteContracts("index.php", "auto", "all", "index.html", bp); err != nil {
		t.Fatalf("expected nil for valid contracts, got %v", err)
	}

	// Invalid front controller extension
	if err := ValidateSiteContracts("index.html", "", "", "", nil); err == nil {
		t.Fatal("expected error for non-.php front controller")
	}

	// Invalid front controller traversal
	if err := ValidateSiteContracts("../bad.php", "", "", "", nil); err == nil {
		t.Fatal("expected error for traversing front controller")
	}

	// Invalid mode
	if err := ValidateSiteContracts("", "unknown_mode", "", "", nil); err == nil {
		t.Fatal("expected error for unknown mode")
	}

	// Invalid override
	if err := ValidateSiteContracts("", "", "invalid_override", "", nil); err == nil {
		t.Fatal("expected error for unknown override")
	}

	// Invalid SPA file extension
	if err := ValidateSiteContracts("", "", "", "index.php", nil); err == nil {
		t.Fatal("expected error for .php SPA file")
	}

	// Build profile missing command
	if err := ValidateSiteContracts("", "", "", "", &BuildProfile{Output: "dist"}); err == nil {
		t.Fatal("expected error for build profile missing command/node_version")
	}

	// Build profile missing node version
	if err := ValidateSiteContracts("", "", "", "", &BuildProfile{Command: "npm run build"}); err == nil {
		t.Fatal("expected error for build profile missing node_version")
	}

	// NUL byte check
	if err := ValidateSiteContracts("index\x00.php", "", "", "", nil); err == nil {
		t.Fatal("expected error for NUL byte in front controller")
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
