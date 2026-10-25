package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/serviceconfig"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestServiceListTable(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
				Ports: []services.Port{
					{Host: 80, Container: 80, Protocol: "http", Loopback: true},
				},
			},
		},
	}

	out := runCLI(t, fake, "service", "list")
	for _, heading := range []string{"NAME", "GROUP", "STATE", "ACTIVE", "PORT"} {
		if !strings.Contains(out, heading) {
			t.Errorf("output missing heading %q, got:\n%s", heading, out)
		}
	}
	if !strings.Contains(out, "Nginx Web Server") || !strings.Contains(out, "web") || !strings.Contains(out, "active") {
		t.Errorf("output missing service values, got:\n%s", out)
	}
}

func TestServiceListEmpty(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{},
	}

	out := runCLI(t, fake, "service", "list")
	for _, heading := range []string{"NAME", "GROUP", "STATE", "ACTIVE", "PORT"} {
		if !strings.Contains(out, heading) {
			t.Errorf("output missing heading %q, got:\n%s", heading, out)
		}
	}

	jsonOut := runCLI(t, fake, "service", "list", "--json")
	var views []services.View
	if err := json.Unmarshal([]byte(jsonOut), &views); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("expected 0 views, got %d", len(views))
	}
}

func TestServiceListJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		servicesList: []services.View{
			{
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
			},
		},
	}

	out := runCLI(t, fake, "service", "list", "--json")
	var views []services.View
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if len(views) != 1 || views[0].ID != services.IDNginx {
		t.Fatalf("unexpected views: %+v", views)
	}
}

func TestServiceStatus(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{
			"nginx": {
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
				Image:       "fixture-nginx@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				ConfigPath:  "/data/devstack/services/nginx/config",
				DataPath:    "/data/devstack/services/nginx/data",
			},
		},
	}

	out := runCLI(t, fake, "service", "status", "nginx")
	if !strings.Contains(out, "Nginx Web Server") || !strings.Contains(out, "web") || !strings.Contains(out, "active") {
		t.Errorf("status output missing expected fields, got:\n%s", out)
	}
}

func TestServiceStatusJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{
			"nginx": {
				ID:          services.IDNginx,
				DisplayName: "Nginx Web Server",
				Group:       services.GroupWeb,
				State:       services.StateActive,
				Active:      true,
			},
		},
	}

	out := runCLI(t, fake, "service", "status", "nginx", "--json")
	var v services.View
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if v.ID != services.IDNginx {
		t.Fatalf("unexpected view: %+v", v)
	}
}

func TestServiceStatusNotFound(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		serviceMap: map[string]services.View{},
	}

	code, _, _ := runCLIExit(t, fake, "service", "status", "unknown")
	if code == 0 {
		t.Fatalf("expected non-zero exit code for unknown service status, got %d", code)
	}
}

func TestServiceLifecycleCommands(t *testing.T) {
	for _, action := range []string{"install", "activate", "deactivate", "restart"} {
		t.Run(action, func(t *testing.T) {
			useTestSeams(t)
			fake := &fakeClient{}

			out := runCLI(t, fake, "service", action, "nginx")
			if got := strings.Join(fake.serviceActions, ","); got != action {
				t.Fatalf("actions = %q, want %q", got, action)
			}
			if fake.serviceActionID != "nginx" {
				t.Fatalf("action ID = %q, want nginx", fake.serviceActionID)
			}
			if !strings.Contains(out, "nginx") || !strings.Contains(out, action) {
				t.Fatalf("output = %q, want id and action", out)
			}
		})
	}
}

func TestServiceLifecycleJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}

	out := runCLI(t, fake, "service", "activate", "mysql", "--json")
	var got struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if got.ID != "mysql" || got.Action != "activate" || got.Status != "ok" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestServiceUninstall(t *testing.T) {
	t.Run("preserves data by default", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{}

		out := runCLI(t, fake, "service", "uninstall", "mysql")
		if fake.serviceDeleteData {
			t.Fatal("delete_data = true by default")
		}
		if got := strings.Join(fake.serviceActions, ","); got != "uninstall" {
			t.Fatalf("actions = %q", got)
		}
		if !strings.Contains(out, "mysql") {
			t.Fatalf("output = %q", out)
		}
	})

	t.Run("delete data requires confirmation", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{}
		code, _, stderr := runCLIExitInput(t, fake, "n\n", "service", "uninstall", "mysql", "--delete-data")
		if code == 0 {
			t.Fatal("uninstall --delete-data without confirmation succeeded")
		}
		if len(fake.serviceActions) != 0 {
			t.Fatalf("actions = %v, want none", fake.serviceActions)
		}
		if !strings.Contains(stderr, "confirmation") && !strings.Contains(stderr, "declined") {
			t.Fatalf("stderr = %q, want confirmation failure", stderr)
		}
	})

	t.Run("interactive confirmation", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{}

		runCLIExitInput(t, fake, "y\n", "service", "uninstall", "mysql", "--delete-data")
		if !fake.serviceDeleteData {
			t.Fatal("delete_data = false, want true")
		}
	})

	t.Run("yes flag skips confirmation and prints backup", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{
			serviceUninstallResult: servicecontrol.UninstallResult{BackupPath: "/data/backups/mysql.tar.gz"},
		}

		out := runCLI(t, fake, "service", "uninstall", "mysql", "--delete-data", "--yes")
		if !fake.serviceDeleteData {
			t.Fatal("delete_data = false, want true")
		}
		if !strings.Contains(out, "/data/backups/mysql.tar.gz") {
			t.Fatalf("output = %q, want backup path", out)
		}
	})

	t.Run("json output includes backup path", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{
			serviceUninstallResult: servicecontrol.UninstallResult{BackupPath: "/data/backups/mysql.tar.gz"},
		}

		out := runCLI(t, fake, "service", "uninstall", "mysql", "--delete-data", "--yes", "--json")
		var got struct {
			ID         string `json:"id"`
			Action     string `json:"action"`
			Status     string `json:"status"`
			BackupPath string `json:"backup_path"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
		}
		if got.ID != "mysql" || got.Action != "uninstall" || got.Status != "ok" || got.BackupPath != "/data/backups/mysql.tar.gz" {
			t.Fatalf("unexpected output: %+v", got)
		}
	})
}

func TestServiceLogs(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{serviceLogs: "line one\nline two\n"}

	out := runCLI(t, fake, "service", "logs", "mysql", "--tail", "12")
	if fake.serviceLogsTail != 12 {
		t.Fatalf("tail = %d, want 12", fake.serviceLogsTail)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Fatalf("output = %q", out)
	}

	jsonOut := runCLI(t, fake, "service", "logs", "mysql", "--json")
	var got struct {
		ID   string `json:"id"`
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, jsonOut)
	}
	if got.ID != "mysql" || got.Logs != fake.serviceLogs {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestServiceLogsRejectsNegativeTail(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}

	code, _, _ := runCLIExit(t, fake, "service", "logs", "mysql", "--tail", "-1")
	if code == 0 {
		t.Fatal("negative tail succeeded")
	}
	if fake.serviceLogsTail != 0 {
		t.Fatalf("client called with tail %d", fake.serviceLogsTail)
	}
}

func TestServiceCredentialsReveal(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{serviceCredentials: servicecontrol.Credentials{
		Engine:   "mysql",
		Username: "devstack",
		Password: "secret-password",
		Database: "devstack",
		Host:     "127.0.0.1",
		Port:     3306,
	}}

	out := runCLI(t, fake, "service", "credentials", "mysql")
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "warning") {
		t.Fatalf("output = %q, want a credentials warning", out)
	}
	for _, want := range []string{"127.0.0.1", "3306", "devstack", "secret-password"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, missing %q", out, want)
		}
	}
	if got := strings.Join(fake.serviceCredentialCalls, ","); got != "reveal" {
		t.Fatalf("credential calls = %q", got)
	}
}

func TestServiceCredentialsJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{serviceCredentials: servicecontrol.Credentials{
		Engine: "mysql", Username: "devstack", Password: "secret-password", Database: "devstack", Host: "127.0.0.1", Port: 3306,
	}}

	out := runCLI(t, fake, "service", "credentials", "mysql", "--json")
	var got servicecontrol.Credentials
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v\noutput: %s", err, out)
	}
	if got.Password != "secret-password" || got.Port != 3306 {
		t.Fatalf("unexpected credentials: %+v", got)
	}
}

func TestServiceCredentialsRotate(t *testing.T) {
	t.Run("requires confirmation", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{serviceCredentials: servicecontrol.Credentials{Password: "new-secret"}}

		code, _, _ := runCLIExitInput(t, fake, "n\n", "service", "credentials", "mysql", "--rotate")
		if code == 0 {
			t.Fatal("rotate without confirmation succeeded")
		}
		if len(fake.serviceCredentialCalls) != 0 {
			t.Fatalf("credential calls = %v, want none", fake.serviceCredentialCalls)
		}
	})

	t.Run("interactive confirmation rotates", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{serviceCredentials: servicecontrol.Credentials{Password: "new-secret"}}

		out := runCLIExitMust(t, fake, "y\n", "service", "credentials", "mysql", "--rotate")
		if got := strings.Join(fake.serviceCredentialCalls, ","); got != "rotate" {
			t.Fatalf("credential calls = %q", got)
		}
		if !strings.Contains(out, "new-secret") {
			t.Fatalf("output = %q, want rotated password", out)
		}
	})

	t.Run("yes flag rotates", func(t *testing.T) {
		useTestSeams(t)
		fake := &fakeClient{serviceCredentials: servicecontrol.Credentials{Password: "new-secret"}}

		out := runCLI(t, fake, "service", "credentials", "mysql", "--rotate", "--yes")
		if got := strings.Join(fake.serviceCredentialCalls, ","); got != "rotate" {
			t.Fatalf("credential calls = %q", got)
		}
		if !strings.Contains(out, "new-secret") {
			t.Fatalf("output = %q, want rotated password", out)
		}
	})
}

func runCLIExitMust(t *testing.T, fake *fakeClient, input string, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLIExitInput(t, fake, input, args...)
	if code != 0 {
		t.Fatalf("devstack %s exited %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

func newConfigEditFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "new.conf")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServiceConfigListTableAndJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{configListing: servicecontrol.ConfigListing{
		Service: "nginx",
		Files: []servicecontrol.ConfigStatus{
			{
				Path:     "nginx.conf",
				Class:    serviceconfig.ClassBase,
				Size:     12,
				Conflict: servicecontrol.ConfigConflictSummary{Active: false},
			},
			{
				Path:      "conf.d/generated/app.conf",
				Class:     serviceconfig.ClassGenerated,
				Generated: true,
				Modified:  true,
				Size:      24,
				Conflict: servicecontrol.ConfigConflictSummary{
					Active:             true,
					ResolutionRequired: true,
					AllowedResolutions: []string{"keep", "use", "merge"},
				},
			},
		},
	}}

	out := runCLI(t, fake, "service", "config", "list", "nginx")
	for _, heading := range []string{"PATH", "CLASS", "GENERATED", "MODIFIED", "DETACHED", "SIZE", "CONFLICT"} {
		if !strings.Contains(out, heading) {
			t.Errorf("output missing heading %q:\n%s", heading, out)
		}
	}
	if !strings.Contains(out, "conf.d/generated/app.conf") || !strings.Contains(out, "conflict (keep|use|merge)") {
		t.Fatalf("output missing config rows:\n%s", out)
	}

	out = runCLI(t, fake, "service", "config", "list", "nginx", "--json")
	var listing servicecontrol.ConfigListing
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if listing.Service != "nginx" || len(listing.Files) != 2 {
		t.Fatalf("listing = %+v", listing)
	}
}

func TestServiceConfigShow(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{configContent: servicecontrol.ConfigContent{
		ConfigStatus: servicecontrol.ConfigStatus{Path: "nginx.conf", Class: serviceconfig.ClassBase},
		Content:      "worker_processes auto;\n",
	}}

	out := runCLI(t, fake, "service", "config", "show", "nginx", "nginx.conf")
	if out != "worker_processes auto;\n" {
		t.Fatalf("output = %q", out)
	}
	if fake.configActionID != "nginx" || fake.configActionPath != "nginx.conf" {
		t.Fatalf("request = %q/%q", fake.configActionID, fake.configActionPath)
	}

	out = runCLI(t, fake, "service", "config", "show", "nginx", "nginx.conf", "--json")
	var content servicecontrol.ConfigContent
	if err := json.Unmarshal([]byte(out), &content); err != nil || content.Content != "worker_processes auto;\n" {
		t.Fatalf("json: %v %q", err, out)
	}
}

func TestServiceConfigDiff(t *testing.T) {
	useTestSeams(t)
	file := newConfigEditFile(t, "worker_processes 2;\n")
	fake := &fakeClient{configDiff: servicecontrol.ConfigDiff{
		Path: "nginx.conf",
		Diff: "--- a/nginx.conf\n+++ b/nginx.conf\n-worker_processes 1;\n+worker_processes 2;\n",
		Conflict: servicecontrol.ConfigConflictSummary{
			Active:             true,
			Reason:             "tracked generated file has user modifications",
			AllowedResolutions: []string{"keep", "use", "merge"},
		},
	}}

	out := runCLI(t, fake, "service", "config", "diff", "nginx", "nginx.conf", "--file", file)
	if !strings.Contains(out, "+worker_processes 2;") {
		t.Fatalf("output = %q", out)
	}
	if fake.configActionBody != "worker_processes 2;\n" || fake.configActionPath != "nginx.conf" {
		t.Fatalf("request = %q/%q", fake.configActionBody, fake.configActionPath)
	}
}

func TestServiceConfigApply(t *testing.T) {
	useTestSeams(t)
	file := newConfigEditFile(t, "worker_processes 2;\n")
	fake := &fakeClient{configApply: servicecontrol.ConfigApplyResult{
		Service: "nginx", Path: "nginx.conf", Action: "apply", Changed: true, Live: true,
		BackupPath: "/tmp/backup", Hash: "abc", Size: 20,
	}}

	out := runCLI(t, fake, "service", "config", "apply", "nginx", "nginx.conf",
		"--file", file, "--resolution", "use", "--yes")
	if !strings.Contains(out, "Live") || !strings.Contains(out, "true") {
		t.Fatalf("output = %q", out)
	}
	if fake.configResolution != "use" || fake.configActionBody != "worker_processes 2;\n" {
		t.Fatalf("request = %+v", fake)
	}
}

func TestServiceConfigApplyRequiresConfirmation(t *testing.T) {
	useTestSeams(t)
	file := newConfigEditFile(t, "worker_processes 2;\n")

	fake := &fakeClient{configApply: servicecontrol.ConfigApplyResult{Service: "nginx", Path: "nginx.conf"}}
	code, _, _ := runCLIExitInput(t, fake, "n\n", "service", "config", "apply", "nginx", "nginx.conf", "--file", file)
	if code == 0 {
		t.Fatal("apply without confirmation succeeded")
	}
	if len(fake.configCalls) != 0 {
		t.Fatalf("calls = %v, want none after declining", fake.configCalls)
	}

	code, _, _ = runCLIExitInput(t, fake, "y\n", "service", "config", "apply", "nginx", "nginx.conf", "--file", file)
	if code != 0 {
		t.Fatalf("confirmed apply exited %d", code)
	}
	if len(fake.configCalls) != 1 || fake.configCalls[0] != "apply" {
		t.Fatalf("calls = %v", fake.configCalls)
	}
}

func TestServiceConfigApplyRejectsInvalidResolution(t *testing.T) {
	useTestSeams(t)
	file := newConfigEditFile(t, "x\n")
	fake := &fakeClient{}
	code, _, _ := runCLIExit(t, fake, "service", "config", "apply", "nginx", "nginx.conf", "--file", file, "--resolution", "overwrite", "--yes")
	if code == 0 {
		t.Fatal("invalid resolution succeeded")
	}
	if len(fake.configCalls) != 0 {
		t.Fatalf("calls = %v, want none", fake.configCalls)
	}
}

func TestServiceConfigReset(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{configReset: servicecontrol.ConfigApplyResult{
		Service: "nginx", Path: "nginx.conf", Action: "reset", Changed: true, Live: true,
		RestoredFrom: "/tmp/backup",
	}}

	out := runCLI(t, fake, "service", "config", "reset", "nginx", "nginx.conf", "--yes")
	if !strings.Contains(out, "reset") || !strings.Contains(out, "Restored from") {
		t.Fatalf("output = %q", out)
	}
	if len(fake.configCalls) != 1 || fake.configCalls[0] != "reset" {
		t.Fatalf("calls = %v", fake.configCalls)
	}

	code, _, _ := runCLIExitInput(t, fake, "n\n", "service", "config", "reset", "nginx", "nginx.conf")
	if code == 0 {
		t.Fatal("reset without confirmation succeeded")
	}
}
