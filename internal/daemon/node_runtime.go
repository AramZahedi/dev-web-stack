package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

var (
	errNodeReleasesUnavailable = errors.New("daemon: node release discovery is not configured")
	errLocalRuntimeUnavailable = errors.New("daemon: local runtime manager is not configured")
)

// nodeReleaseSource is the discovery surface shared by the local and Windows
// runtime services. It is deliberately narrower than *nodereleases.Client so
// tests can pin preflight metadata without a network.
type nodeReleaseSource interface {
	List(context.Context, nodereleases.ListOptions) (nodereleases.Snapshot, error)
	Refresh(context.Context) (nodereleases.Snapshot, error)
	Preflight(context.Context, string) (nodereleases.Preflight, error)
}

// runtimeInstallAgent is the narrow agent surface used to install one pinned
// archive. The checksum and size travel with the artifact and are revalidated
// by the agent.
type runtimeInstallAgent interface {
	InstallRuntime(context.Context, agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error)
}

// newNodeReleaseClient creates the daemon-owned discovery client. The client
// derives its private cache from home and performs no network I/O until the
// first list, refresh, or preflight call.
func newNodeReleaseClient(home string) (*nodereleases.Client, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("daemon: node release discovery requires a home directory")
	}
	client, err := nodereleases.New(nodereleases.Config{Home: home})
	if err != nil {
		return nil, fmt.Errorf("daemon: create node release client: %w", err)
	}
	return client, nil
}

// nodeReleaseService exposes discovery and preflight to the API. It is embedded
// by both runtime service implementations.
type nodeReleaseService struct {
	releases nodeReleaseSource
}

func newNodeReleaseService(releases nodeReleaseSource) nodeReleaseService {
	return nodeReleaseService{releases: releases}
}

func (s nodeReleaseService) NodeReleases(ctx context.Context, major string, all, offline bool) (nodereleases.Snapshot, error) {
	if s.releases == nil {
		return nodereleases.Snapshot{}, errNodeReleasesUnavailable
	}
	number, err := parseNodeMajor(major)
	if err != nil {
		return nodereleases.Snapshot{}, err
	}
	return s.releases.List(ctx, nodereleases.ListOptions{Major: number, All: all, Offline: offline})
}

func (s nodeReleaseService) NodeRelease(ctx context.Context, version string) (nodereleases.Preflight, error) {
	if s.releases == nil {
		return nodereleases.Preflight{}, errNodeReleasesUnavailable
	}
	selector := strings.TrimSpace(version)
	selector = strings.TrimPrefix(selector, "node@")
	if selector == "" {
		return nodereleases.Preflight{}, errors.New("daemon: node version is required")
	}
	return s.releases.Preflight(ctx, selector)
}

func (s nodeReleaseService) RefreshNodeReleases(ctx context.Context) (nodereleases.Snapshot, error) {
	if s.releases == nil {
		return nodereleases.Snapshot{}, errNodeReleasesUnavailable
	}
	return s.releases.Refresh(ctx)
}

// parseNodeMajor converts an optional query value into a release major. Zero
// means every major; a malformed or non-positive value is rejected instead of
// being silently treated as "all".
func parseNodeMajor(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	major, err := strconv.Atoi(raw)
	if err != nil || major < 1 {
		return 0, fmt.Errorf("daemon: invalid node major %q", raw)
	}
	return major, nil
}

// nodeReleaseSelector reports whether id asks discovery for a Node release.
// PHP and other embedded runtime IDs keep their existing catalog path.
func nodeReleaseSelector(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "node@") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(id, "node@")), true
}

// nodeReleaseArtifact converts validated preflight metadata into the pinned
// artifact the WSL agent installs. It rechecks the fields the local manager and
// agent also require so a buggy discovery layer cannot forward an unsafe entry.
func nodeReleaseArtifact(entry catalog.Entry) (agentproto.RuntimeArtifact, error) {
	if entry.Kind != "node" ||
		entry.Build != "linux-x64" ||
		entry.Version == "" ||
		entry.ID != "node@"+entry.Version ||
		entry.Revision != 0 ||
		entry.URL == "" ||
		len(entry.SHA256) != 64 ||
		entry.Size <= 0 {
		return agentproto.RuntimeArtifact{}, fmt.Errorf("daemon: invalid pinned node artifact %q", entry.ID)
	}
	return agentproto.RuntimeArtifact{
		ID:      entry.ID,
		Kind:    entry.Kind,
		Version: entry.Version,
		Build:   entry.Build,
		URL:     entry.URL,
		SHA256:  entry.SHA256,
		Size:    entry.Size,
	}, nil
}

// localRuntimeManager is the local runtime surface used by the daemon API.
// *runtime.Manager implements it.
type localRuntimeManager interface {
	List(context.Context) ([]model.RuntimeBuild, error)
	Install(context.Context, string) (model.RuntimeBuild, error)
	InstallEntry(context.Context, catalog.Entry) (model.RuntimeBuild, error)
	Remove(context.Context, string) error
	Available() []catalog.Entry
}

// localNodeRuntimeService adds Node discovery to the local runtime manager.
// Node selectors install the exact preflighted entry; every other runtime keeps
// the embedded catalog path.
type localNodeRuntimeService struct {
	nodeReleaseService
	manager localRuntimeManager
}

func newLocalNodeRuntimeService(manager localRuntimeManager, releases nodeReleaseSource) *localNodeRuntimeService {
	return &localNodeRuntimeService{
		nodeReleaseService: newNodeReleaseService(releases),
		manager:            manager,
	}
}

func (s *localNodeRuntimeService) List(ctx context.Context) ([]model.RuntimeBuild, error) {
	if s == nil || s.manager == nil {
		return nil, errLocalRuntimeUnavailable
	}
	return s.manager.List(ctx)
}

func (s *localNodeRuntimeService) Install(ctx context.Context, id string) (model.RuntimeBuild, error) {
	if s == nil || s.manager == nil {
		return model.RuntimeBuild{}, errLocalRuntimeUnavailable
	}
	if selector, ok := nodeReleaseSelector(id); ok {
		preflight, err := s.NodeRelease(ctx, selector)
		if err != nil {
			return model.RuntimeBuild{}, err
		}
		if _, err := nodeReleaseArtifact(preflight.Entry); err != nil {
			return model.RuntimeBuild{}, err
		}
		return s.manager.InstallEntry(ctx, preflight.Entry)
	}
	return s.manager.Install(ctx, id)
}

func (s *localNodeRuntimeService) Remove(ctx context.Context, buildID string) error {
	if s == nil || s.manager == nil {
		return errLocalRuntimeUnavailable
	}
	return s.manager.Remove(ctx, buildID)
}

func (s *localNodeRuntimeService) Available() []catalog.Entry {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Available()
}

var _ interface {
	List(context.Context) ([]model.RuntimeBuild, error)
	Install(context.Context, string) (model.RuntimeBuild, error)
	Remove(context.Context, string) error
	Available() []catalog.Entry
} = (*localNodeRuntimeService)(nil)
