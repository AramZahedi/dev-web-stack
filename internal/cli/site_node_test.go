package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

// siteNodeClient wraps the shared fake so Node-pin tests can record updates
// without changing the fixture used by the rest of the CLI suite.
type siteNodeClient struct {
	*fakeClient
	kind      model.SiteKind
	updateRef string
	updateErr error
}

func (f *siteNodeClient) UpdateSite(_ context.Context, ref string, req sites.UpdateRequest) (model.Site, error) {
	f.updateRef = ref
	f.updateReq = req
	if f.updateErr != nil {
		return model.Site{}, f.updateErr
	}
	site := model.Site{ID: "site-1", Name: ref, Kind: f.kind, Status: model.StatusRunning}
	if req.NodeVersion == nil {
		return site, nil
	}
	site.NodeVersion = *req.NodeVersion
	if *req.NodeVersion == "" {
		site.NodeToolchain = &model.NodeToolchain{
			Version: "22.11.0", BuildID: "node@22.11.0",
			Source: model.NodeSourceGlobal, Installed: true,
		}
		return site, nil
	}
	site.NodeToolchain = &model.NodeToolchain{
		Version: *req.NodeVersion, BuildID: "node@" + *req.NodeVersion,
		Source: model.NodeSourceSite, Installed: true,
	}
	return site, nil
}

// runClientCLI mirrors runCLIExit for fakes that implement Client through a
// wrapper type rather than *fakeClient.
func runClientCLI(t *testing.T, c Client, args ...string) (int, string, string) {
	t.Helper()
	root := NewRootCmd(func(string) (Client, error) { return c, nil })
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		PrintError(root, err)
		if code := ExitCode(err); code > 0 {
			return code, stdout.String(), stderr.String()
		}
		return 1, stdout.String(), stderr.String()
	}
	return 0, stdout.String(), stderr.String()
}

func mustRunClientCLI(t *testing.T, c Client, args ...string) string {
	t.Helper()
	code, stdout, stderr := runClientCLI(t, c, args...)
	if code != 0 {
		t.Fatalf("devstack %s exited %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

func TestSiteNodeSetsExactInstalledVersion(t *testing.T) {
	useTestSeams(t)
	fake := &siteNodeClient{fakeClient: &fakeClient{
		runtimes: []model.RuntimeBuild{
			{ID: "node@22.9.0", Kind: "node", Version: "22.9.0"},
			{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"},
			{ID: "node@24.4.1", Kind: "node", Version: "24.4.1"},
		},
	}}
	for _, tc := range []struct {
		name      string
		requested string
		want      string
	}{
		{"major line", "22", "22.11.0"},
		{"minor line", "22.9", "22.9.0"},
		{"exact version", "24.4.1", "24.4.1"},
		{"build id", "node@24.4.1", "24.4.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake.updateReq = sites.UpdateRequest{}
			fake.updateRef = ""
			out := mustRunClientCLI(t, fake, "site", "node", "app", tc.requested)
			if fake.updateRef != "app" {
				t.Fatalf("update ref = %q, want app", fake.updateRef)
			}
			if fake.updateReq.NodeVersion == nil {
				t.Fatal("node_version was not sent")
			}
			if got := *fake.updateReq.NodeVersion; got != tc.want {
				t.Fatalf("node_version = %q, want exact %q", got, tc.want)
			}
			if len(fake.installs) != 0 {
				t.Fatalf("installs = %v, want no discovery or installs", fake.installs)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, out)
			}
		})
	}

	t.Run("invalid version", func(t *testing.T) {
		fake.updateRef = ""
		code, _, stderr := runClientCLI(t, fake, "site", "node", "app", "banana")
		if code == 0 || !strings.Contains(stderr, "invalid Node version") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if fake.updateRef != "" {
			t.Fatalf("invalid version reached the API: %q", fake.updateRef)
		}
	})
}

func TestSiteNodeClearInheritsGlobal(t *testing.T) {
	useTestSeams(t)
	fake := &siteNodeClient{fakeClient: &fakeClient{}, kind: model.SiteStatic}
	out := mustRunClientCLI(t, fake, "site", "node", "docs", "--clear")
	if fake.updateReq.NodeVersion == nil {
		t.Fatal("clear did not send node_version")
	}
	if got := *fake.updateReq.NodeVersion; got != "" {
		t.Fatalf("node_version = %q, want an explicit clear", got)
	}
	if fake.updateReq.NodeVersion == nil || fake.updateReq.Domain != "" {
		t.Fatalf("clear update = %+v, want only node_version", fake.updateReq)
	}
	for _, want := range []string{"docs", "inherit", "22.11.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	t.Run("json", func(t *testing.T) {
		out := mustRunClientCLI(t, fake, "site", "node", "docs", "--clear", "--json")
		var site model.Site
		if err := json.Unmarshal([]byte(out), &site); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		if site.NodeVersion != "" || site.NodeToolchain == nil || site.NodeToolchain.Source != model.NodeSourceGlobal {
			t.Fatalf("site = %+v", site)
		}
	})
}

func TestSiteNodeClearRejectedForNodeKind(t *testing.T) {
	useTestSeams(t)
	fake := &siteNodeClient{fakeClient: &fakeClient{}, kind: model.SiteNode}
	fake.updateErr = &client.Error{Code: "INVALID_REQUEST", Message: "sites: node site requires a version"}
	code, _, stderr := runClientCLI(t, fake, "site", "node", "api", "--clear")
	if code == 0 {
		t.Fatal("node-kind clear was accepted")
	}
	if fake.updateReq.NodeVersion == nil || *fake.updateReq.NodeVersion != "" {
		t.Fatalf("clear update = %+v, want an explicit null pin", fake.updateReq)
	}
	for _, want := range []string{"node site requires a version", "explicit Node version"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestSiteAddAcceptsNodeForStaticAndPHP(t *testing.T) {
	useTestSeams(t)

	t.Run("static", func(t *testing.T) {
		fake := &fakeClient{runtimes: []model.RuntimeBuild{
			{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"},
		}}
		runCLI(t, fake, "site", "add", "--name", "docs", "--path", "/tmp/docs", "--node", "22")
		if fake.addReq.Kind != "static" {
			t.Fatalf("kind = %q, want static", fake.addReq.Kind)
		}
		if fake.addReq.NodeVersion != "22.11.0" {
			t.Fatalf("node version = %q, want 22.11.0", fake.addReq.NodeVersion)
		}
		if fake.addReq.Command != "" {
			t.Fatalf("command = %q, want empty", fake.addReq.Command)
		}
	})

	t.Run("php", func(t *testing.T) {
		fake := &fakeClient{runtimes: []model.RuntimeBuild{
			{ID: "php@8.4.12-r1", Kind: "php", Version: "8.4.12"},
			{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"},
		}}
		runCLI(t, fake, "site", "add", "--name", "shop", "--path", "/tmp/shop",
			"--kind", "php", "--php", "8.4", "--node", "node@22.11.0")
		if fake.addReq.NodeVersion != "22.11.0" {
			t.Fatalf("node version = %q, want 22.11.0", fake.addReq.NodeVersion)
		}
		if fake.addReq.PHPVersion != "8.4" || fake.addReq.PHPBuild != "php@8.4.12-r1" {
			t.Fatalf("PHP fields = %q/%q", fake.addReq.PHPVersion, fake.addReq.PHPBuild)
		}
	})

	t.Run("uninstalled", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add",
			"--name", "docs", "--path", "/tmp/docs", "--node", "22")
		if code == 0 || !strings.Contains(stderr, "not installed") || !strings.Contains(stderr, "runtime install") {
			t.Fatalf("code=%d stderr=%q, want install-first remediation", code, stderr)
		}
		if fake.addReq.Name != "" {
			t.Fatalf("uninstalled pin created a site: %+v", fake.addReq)
		}
	})

	t.Run("node-only flags still rejected", func(t *testing.T) {
		fake := &fakeClient{}
		code, _, stderr := runCLIExit(t, fake, "site", "add",
			"--name", "docs", "--path", "/tmp/docs", "--node", "22", "--command", "npm start")
		if code == 0 || !strings.Contains(stderr, "only valid for node sites") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})
}

func TestToolCLINodeDefaultWhitelist(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	runCLI(t, fake, "tool", "install", helperproto.ToolNodeDefault)
	if fake.toolInstallID != helperproto.ToolNodeDefault {
		t.Fatalf("install = %q, want %s", fake.toolInstallID, helperproto.ToolNodeDefault)
	}
	runCLI(t, fake, "tool", "update", helperproto.ToolNodeDefault)
	if fake.toolInstallID != helperproto.ToolNodeDefault {
		t.Fatalf("update = %q, want %s", fake.toolInstallID, helperproto.ToolNodeDefault)
	}
	runCLI(t, fake, "tool", "remove", helperproto.ToolNodeDefault)
	if fake.toolRemoveID != helperproto.ToolNodeDefault {
		t.Fatalf("remove = %q, want %s", fake.toolRemoveID, helperproto.ToolNodeDefault)
	}
	code, _, stderr := runCLIExit(t, fake, "tool", "install", "missing")
	if code == 0 || !strings.Contains(stderr, helperproto.ToolNodeDefault) {
		t.Fatalf("code=%d stderr=%q, want node-default in the choices", code, stderr)
	}
}
