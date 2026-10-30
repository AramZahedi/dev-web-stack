package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

// runNodeCLI mounts the Node command tree on a minimal root command because
// root command registration is owned by the integration lane.
func runNodeCLI(t *testing.T, fake *fakeClient, args ...string) (int, string, string) {
	t.Helper()
	app := &App{newClient: func(home string) (Client, error) {
		fake.lastHome = home
		return fake, nil
	}}
	root := &cobra.Command{Use: "devstack", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&app.jsonOutput, "json", false, "emit machine-readable JSON on stdout")
	root.AddCommand(app.newNodeCmd())

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		PrintError(root, err)
		if code := ExitCode(err); code > 0 {
			return code, stdout.String(), stderr.String()
		}
		return 1, stdout.String(), stderr.String()
	}
	return 0, stdout.String(), stderr.String()
}

func TestNodeDefaultCLITableAndJSON(t *testing.T) {
	useTestSeams(t)
	status := tooling.NodeDefaultStatus{
		Configured:    "22.11.0",
		Build:         &model.RuntimeBuild{ID: "node@22.11.0", Version: "22.11.0"},
		ShimDir:       "/data/devstack/tools/node-default/bin",
		ShimInstalled: true,
		Source:        tooling.NodeDefaultSourceSetting,
	}
	fake := &fakeClient{nodeDefaultStatus: status}

	out := runCLIOutput(t, fake)
	for _, want := range []string{"Configured", "22.11.0", "node@22.11.0", "Shim", "/data/devstack/tools/node-default/bin", "true", "setting"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output %q is missing %q", out, want)
		}
	}
	if fake.nodeDefaultCalls != 1 || fake.nodeDefaultSet != "" {
		t.Fatalf("no-argument default calls=%d set=%q", fake.nodeDefaultCalls, fake.nodeDefaultSet)
	}

	out = runCLIOutput(t, fake, "--status", "--json")
	var decoded tooling.NodeDefaultStatus
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("status json: %v (%q)", err, out)
	}
	if decoded.Configured != "22.11.0" || decoded.Source != tooling.NodeDefaultSourceSetting {
		t.Fatalf("status json = %+v", decoded)
	}

	out = runCLIOutput(t, fake, "22.11.0", "--force")
	if fake.nodeDefaultSet != "22.11.0" || !fake.nodeDefaultForce {
		t.Fatalf("set = %q force=%v", fake.nodeDefaultSet, fake.nodeDefaultForce)
	}
	if !strings.Contains(out, "node@22.11.0") {
		t.Fatalf("set output = %q", out)
	}

	fake.nodeClearStatus = tooling.NodeDefaultStatus{
		Build:         &model.RuntimeBuild{ID: "node@24.1.0", Version: "24.1.0"},
		ShimDir:       "/data/devstack/tools/node-default/bin",
		ShimInstalled: true,
		Source:        tooling.NodeDefaultSourceNewest,
	}
	out = runCLIOutput(t, fake, "--clear")
	if fake.nodeDefaultClear != 1 {
		t.Fatalf("clear calls = %d", fake.nodeDefaultClear)
	}
	if !strings.Contains(out, "node@24.1.0") || !strings.Contains(out, "newest") {
		t.Fatalf("clear output = %q", out)
	}

	fake.nodeDefaultStatus = tooling.NodeDefaultStatus{}
	out = runCLIOutput(t, fake)
	if !strings.Contains(out, "No Node build is installed") {
		t.Fatalf("empty status output = %q", out)
	}

	for _, args := range [][]string{
		{"--status", "--clear"},
		{"--status", "22"},
		{"--clear", "22"},
	} {
		code, _, stderr := runNodeCLI(t, fake, append([]string{"node", "default"}, args...)...)
		if code == 0 || !strings.Contains(stderr, "INVALID_FLAGS") {
			t.Fatalf("args %v accepted: code=%d stderr=%q", args, code, stderr)
		}
	}
}

func runCLIOutput(t *testing.T, fake *fakeClient, args ...string) string {
	t.Helper()
	code, stdout, stderr := runNodeCLI(t, fake, append([]string{"node", "default"}, args...)...)
	if code != 0 {
		t.Fatalf("devstack node default %v exited %d\nstdout:\n%s\nstderr:\n%s", args, code, stdout, stderr)
	}
	return stdout
}
