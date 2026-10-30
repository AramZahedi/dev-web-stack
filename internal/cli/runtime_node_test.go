package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// nodeReleaseFakeClient wraps the shared fake with the Node release discovery
// surface so the runtime tests can assert flags and preflight selectors without
// editing fakeClient.
type nodeReleaseFakeClient struct {
	*fakeClient

	snapshot     nodereleases.Snapshot
	preflight    nodereleases.Preflight
	listErr      error
	preflightErr error
	refreshErr   error

	listCalls    []nodeListCall
	releaseCalls []string
	refreshCalls int
}

type nodeListCall struct {
	major   string
	all     bool
	offline bool
}

func (f *nodeReleaseFakeClient) NodeReleases(_ context.Context, major string, all, offline bool) (nodereleases.Snapshot, error) {
	f.listCalls = append(f.listCalls, nodeListCall{major: major, all: all, offline: offline})
	if f.listErr != nil {
		return nodereleases.Snapshot{}, f.listErr
	}
	return f.snapshot, nil
}

func (f *nodeReleaseFakeClient) NodeRelease(_ context.Context, version string) (nodereleases.Preflight, error) {
	f.releaseCalls = append(f.releaseCalls, version)
	if f.preflightErr != nil {
		return nodereleases.Preflight{}, f.preflightErr
	}
	return f.preflight, nil
}

func (f *nodeReleaseFakeClient) RefreshNodeReleases(context.Context) (nodereleases.Snapshot, error) {
	f.refreshCalls++
	if f.refreshErr != nil {
		return nodereleases.Snapshot{}, f.refreshErr
	}
	return f.snapshot, nil
}

// runRuntimeNodeCLI executes the command tree against any Client implementation
// and returns the same exit/stdout/stderr triple as runCLIExit.
func runRuntimeNodeCLI(t *testing.T, c Client, input string, args ...string) (int, string, string) {
	t.Helper()
	root := NewRootCmd(func(string) (Client, error) { return c, nil })
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(input))
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

func testNodeRelease(version, npm, lts string, security bool) nodereleases.Release {
	return nodereleases.Release{
		Version:  version,
		NPM:      npm,
		LTS:      lts,
		Security: security,
		File:     "node-v" + version + "-linux-x64.tar.gz",
	}
}

func testPHPEntry(id, version string) catalog.Entry {
	return catalog.Entry{ID: id, Kind: "php", Version: version, Build: "linux-x64"}
}

func TestRuntimeAvailableShowsLatestNodePerMajor(t *testing.T) {
	useTestSeams(t)
	fake := &nodeReleaseFakeClient{
		fakeClient: &fakeClient{available: []catalog.Entry{testPHPEntry("php@8.4.4-r1", "8.4.4")}},
		snapshot: nodereleases.Snapshot{
			Majors: []nodereleases.Major{
				{
					Major:  24,
					Latest: testNodeRelease("24.22.1", "10.9.4", "Krypton", false),
					Releases: []nodereleases.Release{
						testNodeRelease("24.22.1", "10.9.4", "Krypton", false),
						testNodeRelease("24.21.0", "10.9.3", "Krypton", false),
					},
				},
				{
					Major:  22,
					Latest: testNodeRelease("22.23.4", "10.9.4", "Jod", true),
					Releases: []nodereleases.Release{
						testNodeRelease("22.23.4", "10.9.4", "Jod", true),
						testNodeRelease("22.22.0", "10.9.3", "Jod", false),
					},
				},
			},
			Source: nodereleases.SourceLive,
		},
	}

	code, stdout, stderr := runRuntimeNodeCLI(t, fake, "", "runtime", "available")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"Node releases", "24.22.1", "22.23.4", "PHP runtimes", "php@8.4.4-r1"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output %q is missing %q", stdout, want)
		}
	}
	for _, unwanted := range []string{"24.21.0", "22.22.0"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("default output %q leaks older release %q", stdout, unwanted)
		}
	}
	if len(fake.releaseCalls) != 0 {
		t.Fatalf("available prefetched releases: %v", fake.releaseCalls)
	}
	wantCalls := []nodeListCall{{major: "", all: false, offline: false}}
	if len(fake.listCalls) != 1 || fake.listCalls[0] != wantCalls[0] {
		t.Fatalf("NodeReleases calls = %+v, want %+v", fake.listCalls, wantCalls)
	}
}

func TestRuntimeAvailableMajorAllShowsOlderReleases(t *testing.T) {
	useTestSeams(t)
	fake := &nodeReleaseFakeClient{
		fakeClient: &fakeClient{available: []catalog.Entry{testPHPEntry("php@8.4.4-r1", "8.4.4")}},
		snapshot: nodereleases.Snapshot{
			Majors: []nodereleases.Major{{
				Major:  22,
				Latest: testNodeRelease("22.23.4", "10.9.4", "Jod", false),
				Releases: []nodereleases.Release{
					testNodeRelease("22.23.4", "10.9.4", "Jod", false),
					testNodeRelease("22.22.0", "10.9.3", "Jod", false),
				},
			}},
			Source: nodereleases.SourceCache,
		},
	}

	code, stdout, stderr := runRuntimeNodeCLI(t, fake, "", "runtime", "available", "--major", "22", "--all")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"22.23.4", "22.22.0", "PHP runtimes"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output %q is missing %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "24.22.1") {
		t.Fatalf("major filter leaked another major: %q", stdout)
	}
	wantCalls := []nodeListCall{{major: "22", all: true, offline: false}}
	if len(fake.listCalls) != 1 || fake.listCalls[0] != wantCalls[0] {
		t.Fatalf("NodeReleases calls = %+v, want %+v", fake.listCalls, wantCalls)
	}
}

func TestRuntimeAvailableFlagsDriveDiscovery(t *testing.T) {
	useTestSeams(t)
	snapshot := nodereleases.Snapshot{
		Majors: []nodereleases.Major{{
			Major:  22,
			Latest: testNodeRelease("22.23.4", "10.9.4", "Jod", false),
		}},
		Source: nodereleases.SourceLive,
	}

	t.Run("refresh", func(t *testing.T) {
		fake := &nodeReleaseFakeClient{fakeClient: &fakeClient{}, snapshot: snapshot}
		code, stdout, stderr := runRuntimeNodeCLI(t, fake, "", "runtime", "available", "--kind", "node", "--refresh")
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
		if fake.refreshCalls != 1 || len(fake.listCalls) != 0 {
			t.Fatalf("refresh calls = %d, list calls = %+v", fake.refreshCalls, fake.listCalls)
		}
		if strings.Contains(stdout, "PHP runtimes") {
			t.Fatalf("--kind node printed PHP entries: %q", stdout)
		}
	})

	t.Run("offline", func(t *testing.T) {
		fake := &nodeReleaseFakeClient{fakeClient: &fakeClient{}, snapshot: snapshot}
		code, _, stderr := runRuntimeNodeCLI(t, fake, "", "runtime", "available", "--kind", "node", "--offline")
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
		want := nodeListCall{major: "", all: false, offline: true}
		if len(fake.listCalls) != 1 || fake.listCalls[0] != want {
			t.Fatalf("NodeReleases calls = %+v, want %+v", fake.listCalls, want)
		}
	})
}

func TestRuntimeInstallNodeSelectorUsesPreflight(t *testing.T) {
	useTestSeams(t)
	sha := strings.Repeat("a", 64)
	size := int64(30 << 20)
	preflight := nodereleases.Preflight{
		Entry: catalog.Entry{
			ID:      "node@22.23.4",
			Kind:    "node",
			Version: "22.23.4",
			Build:   "linux-x64",
			URL:     "https://nodejs.org/dist/v22.23.4/node-v22.23.4-linux-x64.tar.gz",
			SHA256:  sha,
			Size:    size,
		},
		Release: testNodeRelease("22.23.4", "10.9.4", "Jod", false),
	}

	for _, selector := range []string{"22", "22.11", "22.11.0"} {
		t.Run(selector, func(t *testing.T) {
			fake := &nodeReleaseFakeClient{fakeClient: &fakeClient{}, preflight: preflight}
			code, stdout, stderr := runRuntimeNodeCLI(t, fake, "y\n", "runtime", "install", "node@"+selector)
			if code != 0 {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
			if len(fake.releaseCalls) != 1 || fake.releaseCalls[0] != selector {
				t.Fatalf("NodeRelease calls = %v, want [%q]", fake.releaseCalls, selector)
			}
			if len(fake.installs) != 1 || fake.installs[0] != "node@22.23.4" {
				t.Fatalf("installs = %v, want the resolved exact build", fake.installs)
			}
			for _, want := range []string{"22.23.4", "10.9.4", formatBytes(size), sha[:12]} {
				if !strings.Contains(stdout, want) {
					t.Fatalf("confirmation %q is missing %q", stdout, want)
				}
			}
		})
	}
}

func TestRuntimeInstallNodeSelectorRejectsUnknown(t *testing.T) {
	useTestSeams(t)
	fake := &nodeReleaseFakeClient{
		fakeClient:   &fakeClient{},
		preflightErr: &client.Error{Code: "NOT_FOUND", Message: "node release is not available"},
	}

	code, _, stderr := runRuntimeNodeCLI(t, fake, "y\n", "runtime", "install", "node@99")
	if code == 0 {
		t.Fatal("want a non-zero exit for an unknown node selector")
	}
	if len(fake.installs) != 0 {
		t.Fatalf("unknown selector still installed: %v", fake.installs)
	}
	if len(fake.releaseCalls) != 1 || fake.releaseCalls[0] != "99" {
		t.Fatalf("NodeRelease calls = %v, want [99]", fake.releaseCalls)
	}
	if !strings.Contains(stderr, "NODE_VERSION_UNAVAILABLE") || !strings.Contains(stderr, "runtime available") {
		t.Fatalf("stderr = %q, want an actionable unavailable error", stderr)
	}
}

func TestRuntimeInstallNodeSelectorRejectsMalformed(t *testing.T) {
	useTestSeams(t)
	for _, id := range []string{"node@", "node@22.x", "node@22.11.0.1", "node@022"} {
		t.Run(id, func(t *testing.T) {
			fake := &nodeReleaseFakeClient{fakeClient: &fakeClient{}}
			code, _, stderr := runRuntimeNodeCLI(t, fake, "y\n", "runtime", "install", id)
			if code == 0 || !strings.Contains(stderr, "INVALID_ARGUMENT") {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
			if len(fake.releaseCalls) != 0 || len(fake.installs) != 0 {
				t.Fatalf("malformed selector reached the client: releases=%v installs=%v", fake.releaseCalls, fake.installs)
			}
		})
	}
}

func TestRuntimeInstallNodeSelectorFallsBackToEmbeddedID(t *testing.T) {
	useTestSeams(t)
	sha := strings.Repeat("b", 64)
	entry := catalog.Entry{
		ID: "node@22.23.4", Kind: "node", Version: "22.23.4",
		Build: "linux-x64", SHA256: sha, Size: 30 << 20,
	}
	fake := &nodeReleaseFakeClient{
		fakeClient:   &fakeClient{available: []catalog.Entry{entry}},
		preflightErr: &client.Error{Code: "NOT_FOUND", Message: "node release is not available"},
	}

	code, _, stderr := runRuntimeNodeCLI(t, fake, "y\n", "runtime", "install", "node@22.23.4")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if len(fake.installs) != 1 || fake.installs[0] != "node@22.23.4" {
		t.Fatalf("installs = %v, want the embedded exact build", fake.installs)
	}
}

func TestRuntimeAvailableRejectsInvalidMajor(t *testing.T) {
	useTestSeams(t)
	fake := &nodeReleaseFakeClient{fakeClient: &fakeClient{}}

	code, _, stderr := runRuntimeNodeCLI(t, fake, "", "runtime", "available", "--major", "0")
	if code == 0 || !strings.Contains(stderr, "INVALID_ARGUMENT") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if len(fake.listCalls) != 0 {
		t.Fatalf("invalid major reached the client: %+v", fake.listCalls)
	}
}
