package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// fakeNodeReleaseSource stands in for the discovery client in daemon tests.
type fakeNodeReleaseSource struct {
	snapshot     nodereleases.Snapshot
	preflight    nodereleases.Preflight
	listOpts     nodereleases.ListOptions
	listCalls    int
	refresh      int
	selectors    []string
	listErr      error
	refreshErr   error
	preflightErr error
}

func (f *fakeNodeReleaseSource) List(_ context.Context, options nodereleases.ListOptions) (nodereleases.Snapshot, error) {
	f.listCalls++
	f.listOpts = options
	return f.snapshot, f.listErr
}

func (f *fakeNodeReleaseSource) Refresh(context.Context) (nodereleases.Snapshot, error) {
	f.refresh++
	return f.snapshot, f.refreshErr
}

func (f *fakeNodeReleaseSource) Preflight(_ context.Context, selector string) (nodereleases.Preflight, error) {
	f.selectors = append(f.selectors, selector)
	return f.preflight, f.preflightErr
}

// fakeLocalRuntimeManager records the calls a local runtime service makes.
type fakeLocalRuntimeManager struct {
	builds     []model.RuntimeBuild
	available  []catalog.Entry
	installIDs []string
	entries    []catalog.Entry
	removed    []string
	listErr    error
	installErr error
	entryErr   error
	removeErr  error
}

func (f *fakeLocalRuntimeManager) List(context.Context) ([]model.RuntimeBuild, error) {
	return append([]model.RuntimeBuild(nil), f.builds...), f.listErr
}

func (f *fakeLocalRuntimeManager) Install(_ context.Context, id string) (model.RuntimeBuild, error) {
	f.installIDs = append(f.installIDs, id)
	return model.RuntimeBuild{ID: id, Backend: model.BackendLocal}, f.installErr
}

func (f *fakeLocalRuntimeManager) InstallEntry(_ context.Context, entry catalog.Entry) (model.RuntimeBuild, error) {
	f.entries = append(f.entries, entry)
	return model.RuntimeBuild{
		ID: entry.ID, Kind: entry.Kind, Version: entry.Version,
		Backend: model.BackendLocal, Checksum: entry.SHA256,
	}, f.entryErr
}

func (f *fakeLocalRuntimeManager) Remove(_ context.Context, buildID string) error {
	f.removed = append(f.removed, buildID)
	return f.removeErr
}

func (f *fakeLocalRuntimeManager) Available() []catalog.Entry {
	return append([]catalog.Entry(nil), f.available...)
}

// fakeNodePreflight is the fully pinned metadata discovery returns for an
// exact Node release.
func fakeNodePreflight(version string) nodereleases.Preflight {
	file := "node-v" + version + "-linux-x64.tar.gz"
	return nodereleases.Preflight{
		Entry: catalog.Entry{
			ID:      "node@" + version,
			Kind:    "node",
			Version: version,
			Build:   "linux-x64",
			URL:     "https://nodejs.org/dist/v" + version + "/" + file,
			SHA256:  strings.Repeat("ab", 32),
			Size:    58_290_700,
		},
		Release: nodereleases.Release{Version: version, File: file, NPM: "10.9.4"},
	}
}

func TestLocalDaemonWiresNodeReleaseClient(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, "cache", "node")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cached := `{"cached_at":"2026-10-30T00:00:00Z","releases":[{"version":"99.1.2","file":"node-v99.1.2-linux-x64.tar.gz"}]}`
	if err := os.WriteFile(filepath.Join(cacheDir, "index.json"), []byte(cached), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := newNodeReleaseClient(home)
	if err != nil {
		t.Fatalf("newNodeReleaseClient = %v", err)
	}
	if client == nil {
		t.Fatal("newNodeReleaseClient returned nil")
	}

	service := newLocalNodeRuntimeService(&fakeLocalRuntimeManager{}, client)
	snapshot, err := service.NodeReleases(context.Background(), "", false, true)
	if err != nil {
		t.Fatalf("NodeReleases = %v", err)
	}
	if snapshot.Source != nodereleases.SourceCache {
		t.Fatalf("snapshot source = %q, want %q (client is not rooted at the daemon home)", snapshot.Source, nodereleases.SourceCache)
	}
	found := false
	for _, release := range snapshot.All {
		if release.Version == "99.1.2" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("snapshot %+v does not contain the release cached under the daemon home", snapshot.All)
	}
}

func TestLocalNodeRuntimeServiceInstallsNodeSelectorThroughInstallEntry(t *testing.T) {
	preflight := fakeNodePreflight("22.23.4")
	releases := &fakeNodeReleaseSource{preflight: preflight}
	manager := &fakeLocalRuntimeManager{}
	service := newLocalNodeRuntimeService(manager, releases)

	build, err := service.Install(context.Background(), "node@22")
	if err != nil {
		t.Fatalf("Install(node@22) = %v", err)
	}
	if want := []string{"22"}; !reflect.DeepEqual(releases.selectors, want) {
		t.Fatalf("preflight selectors = %v, want %v", releases.selectors, want)
	}
	if want := []catalog.Entry{preflight.Entry}; !reflect.DeepEqual(manager.entries, want) {
		t.Fatalf("InstallEntry entries = %+v, want %+v", manager.entries, want)
	}
	if len(manager.installIDs) != 0 {
		t.Fatalf("embedded Install calls = %v, want none for a node selector", manager.installIDs)
	}
	if build.ID != preflight.Entry.ID || build.Backend != model.BackendLocal {
		t.Fatalf("build = %+v, want %s from InstallEntry", build, preflight.Entry.ID)
	}
}

func TestLocalNodeRuntimeServiceDoesNotUseNodeSelectorForPHP(t *testing.T) {
	releases := &fakeNodeReleaseSource{}
	manager := &fakeLocalRuntimeManager{}
	service := newLocalNodeRuntimeService(manager, releases)

	if _, err := service.Install(context.Background(), "php@8.4.27-r1"); err != nil {
		t.Fatalf("Install(php@8.4.27-r1) = %v", err)
	}
	if want := []string{"php@8.4.27-r1"}; !reflect.DeepEqual(manager.installIDs, want) {
		t.Fatalf("embedded Install calls = %v, want %v", manager.installIDs, want)
	}
	if len(manager.entries) != 0 || len(releases.selectors) != 0 {
		t.Fatalf("PHP install used node discovery: entries=%+v selectors=%v", manager.entries, releases.selectors)
	}
}

func TestLocalNodeRuntimeServicePropagatesNodeSelectorPreflightFailure(t *testing.T) {
	releases := &fakeNodeReleaseSource{preflightErr: errors.New("checksum unavailable")}
	manager := &fakeLocalRuntimeManager{}
	service := newLocalNodeRuntimeService(manager, releases)

	if _, err := service.Install(context.Background(), "node@22"); err == nil {
		t.Fatal("Install(node@22) succeeded when preflight failed")
	}
	if len(manager.entries) != 0 || len(manager.installIDs) != 0 {
		t.Fatalf("failed preflight reached the manager: entries=%+v installs=%v", manager.entries, manager.installIDs)
	}
}

func TestLocalNodeRuntimeServiceMapsNodeReleaseDiscoveryOptions(t *testing.T) {
	snapshot := nodereleases.Snapshot{Source: nodereleases.SourceLive}
	preflight := fakeNodePreflight("22.23.4")
	releases := &fakeNodeReleaseSource{snapshot: snapshot, preflight: preflight}
	service := newLocalNodeRuntimeService(&fakeLocalRuntimeManager{}, releases)

	got, err := service.NodeReleases(context.Background(), "22", true, true)
	if err != nil || got.Source != snapshot.Source {
		t.Fatalf("NodeReleases = %+v, %v", got, err)
	}
	want := nodereleases.ListOptions{Major: 22, All: true, Offline: true}
	if releases.listOpts != want {
		t.Fatalf("List options = %+v, want %+v", releases.listOpts, want)
	}
	if _, err := service.NodeReleases(context.Background(), "-1", false, false); err == nil {
		t.Fatal("NodeReleases accepted a negative major")
	}
	if _, err := service.NodeRelease(context.Background(), "node@22.23.4"); err != nil {
		t.Fatalf("NodeRelease = %v", err)
	}
	if want := []string{"22.23.4"}; !reflect.DeepEqual(releases.selectors, want) {
		t.Fatalf("preflight selectors = %v, want %v", releases.selectors, want)
	}
	if _, err := service.RefreshNodeReleases(context.Background()); err != nil {
		t.Fatalf("RefreshNodeReleases = %v", err)
	}
	if releases.refresh != 1 {
		t.Fatalf("refresh calls = %d, want 1", releases.refresh)
	}
}
