//go:build windows

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// fakeRuntimeInstallAgent records the runtime requests the Windows service
// sends instead of contacting a WSL agent.
type fakeRuntimeInstallAgent struct {
	requests []agentproto.RuntimeRequest
	response agentproto.RuntimeResponse
	err      error
}

func (f *fakeRuntimeInstallAgent) InstallRuntime(_ context.Context, req agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	f.requests = append(f.requests, req)
	return f.response, f.err
}

func TestWindowsRuntimeServiceInstallsNodeSelectorFromDiscovery(t *testing.T) {
	preflight := fakeNodePreflight("24.22.1")
	releases := &fakeNodeReleaseSource{preflight: preflight}
	agent := &fakeRuntimeInstallAgent{response: agentproto.RuntimeResponse{
		ID:       preflight.Entry.ID,
		Kind:     preflight.Entry.Kind,
		Version:  preflight.Entry.Version,
		Backend:  string(model.BackendWSL),
		Path:     "/data/devstack/runtimes/node/24.22.1-linux-x64",
		Checksum: preflight.Entry.SHA256,
	}}
	service := &windowsRuntimeService{
		nodeReleaseService: nodeReleaseService{releases: releases},
		installAgent:       agent,
	}

	build, err := service.Install(context.Background(), "node@24")
	if err != nil {
		t.Fatalf("Install(node@24) = %v", err)
	}
	if want := []string{"24"}; !reflect.DeepEqual(releases.selectors, want) {
		t.Fatalf("preflight selectors = %v, want %v", releases.selectors, want)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent install requests = %d, want 1", len(agent.requests))
	}
	request := agent.requests[0]
	if request.BuildID != "" || request.Kind != "" || request.Version != "" {
		t.Fatalf("request used catalog selectors alongside a pinned artifact: %+v", request)
	}
	want := agentproto.RuntimeArtifact{
		ID:      preflight.Entry.ID,
		Kind:    preflight.Entry.Kind,
		Version: preflight.Entry.Version,
		Build:   preflight.Entry.Build,
		URL:     preflight.Entry.URL,
		SHA256:  preflight.Entry.SHA256,
		Size:    preflight.Entry.Size,
	}
	if request.Artifact == nil {
		t.Fatal("agent request has no artifact")
	}
	if !reflect.DeepEqual(*request.Artifact, want) {
		t.Fatalf("artifact = %+v, want %+v", *request.Artifact, want)
	}
	if build.ID != preflight.Entry.ID || build.Version != preflight.Entry.Version ||
		build.Kind != preflight.Entry.Kind || build.Backend != model.BackendWSL ||
		build.Path != agent.response.Path || build.Checksum != preflight.Entry.SHA256 {
		t.Fatalf("build = %+v, want the agent response for %s", build, preflight.Entry.ID)
	}
}

func TestWindowsRuntimeServiceSendsPinnedArtifact(t *testing.T) {
	t.Run("sends exact preflight fields", func(t *testing.T) {
		preflight := fakeNodePreflight("22.23.4")
		releases := &fakeNodeReleaseSource{preflight: preflight}
		agent := &fakeRuntimeInstallAgent{response: agentproto.RuntimeResponse{
			ID: preflight.Entry.ID, Kind: preflight.Entry.Kind, Version: preflight.Entry.Version,
			Backend: string(model.BackendWSL), Checksum: preflight.Entry.SHA256,
		}}
		service := &windowsRuntimeService{
			nodeReleaseService: nodeReleaseService{releases: releases},
			installAgent:       agent,
		}

		if _, err := service.Install(context.Background(), "node@"+preflight.Entry.Version); err != nil {
			t.Fatalf("Install exact node id = %v", err)
		}
		if want := []string{"22.23.4"}; !reflect.DeepEqual(releases.selectors, want) {
			t.Fatalf("preflight selectors = %v, want %v", releases.selectors, want)
		}
		if len(agent.requests) != 1 || agent.requests[0].Artifact == nil {
			t.Fatalf("agent requests = %+v, want one pinned artifact", agent.requests)
		}
		artifact := *agent.requests[0].Artifact
		if artifact.ID != "node@22.23.4" || artifact.Kind != "node" ||
			artifact.Version != "22.23.4" || artifact.Build != "linux-x64" ||
			artifact.URL != preflight.Entry.URL || artifact.SHA256 != preflight.Entry.SHA256 ||
			artifact.Size != preflight.Entry.Size {
			t.Fatalf("artifact = %+v, want pinned preflight entry %+v", artifact, preflight.Entry)
		}
	})

	t.Run("preflight failure prevents every agent call", func(t *testing.T) {
		releases := &fakeNodeReleaseSource{preflightErr: errors.New("checksum unavailable")}
		agent := &fakeRuntimeInstallAgent{}
		service := &windowsRuntimeService{
			nodeReleaseService: nodeReleaseService{releases: releases},
			installAgent:       agent,
		}

		if _, err := service.Install(context.Background(), "node@22"); err == nil {
			t.Fatal("Install(node@22) succeeded when preflight failed")
		}
		if len(agent.requests) != 0 {
			t.Fatalf("agent install calls after preflight failure = %d, want 0", len(agent.requests))
		}
	})
}

func TestWindowsRuntimeServiceKeepsEmbeddedPHPInstall(t *testing.T) {
	releases := &fakeNodeReleaseSource{}
	agent := &fakeRuntimeInstallAgent{}
	service := &windowsRuntimeService{
		nodeReleaseService: nodeReleaseService{releases: releases},
		installAgent:       agent,
	}

	// Without a WSL backend the embedded path still fails before touching the
	// node installer, proving PHP installs do not route through discovery.
	if _, err := service.Install(context.Background(), "php@8.4.27-r1"); err == nil {
		t.Fatal("Install(php@8.4.27-r1) succeeded without a WSL agent")
	}
	if len(releases.selectors) != 0 {
		t.Fatalf("PHP install consulted node discovery: %v", releases.selectors)
	}
	if len(agent.requests) != 0 {
		t.Fatalf("PHP install used the pinned node installer: %+v", agent.requests)
	}
}

func TestWindowsRuntimeServiceExposesNodeDiscovery(t *testing.T) {
	snapshot := nodereleases.Snapshot{Source: nodereleases.SourceLive}
	preflight := fakeNodePreflight("22.23.4")
	releases := &fakeNodeReleaseSource{snapshot: snapshot, preflight: preflight}
	service := &windowsRuntimeService{nodeReleaseService: nodeReleaseService{releases: releases}}

	got, err := service.NodeReleases(context.Background(), "22", true, true)
	if err != nil || got.Source != snapshot.Source {
		t.Fatalf("NodeReleases = %+v, %v", got, err)
	}
	want := nodereleases.ListOptions{Major: 22, All: true, Offline: true}
	if releases.listOpts != want {
		t.Fatalf("List options = %+v, want %+v", releases.listOpts, want)
	}
	if _, err := service.NodeRelease(context.Background(), "22.23.4"); err != nil {
		t.Fatalf("NodeRelease = %v", err)
	}
	if _, err := service.RefreshNodeReleases(context.Background()); err != nil {
		t.Fatalf("RefreshNodeReleases = %v", err)
	}
}
