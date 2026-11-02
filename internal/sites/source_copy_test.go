package sites

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

type fakeSourceCopier struct {
	request  agentproto.SourceCopyRequest
	response agentproto.SourceCopyResponse
	err      error
	calls    int
}

func (f *fakeSourceCopier) CopySource(_ context.Context, req agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	f.calls++
	f.request = req
	return f.response, f.err
}

func TestChangeSourceModeCopiesBeforeUpdatingAndKeepsSource(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("source copy requires Windows source modes")
	}
	svc, fakes := newTestService(t)
	copier := &fakeSourceCopier{response: agentproto.SourceCopyResponse{Files: 3, Bytes: 42, SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	svc.deps.SourceCopier = copier

	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		SourceMode: model.SourceWindows, HostPath: `C:\projects\blog`, BackendPath: "/mnt/c/projects/blog",
		Status: model.StatusStopped, DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ChangeSourceMode(ctx, "blog", model.SourceWSL, `\\wsl.localhost\devstack\data\devstack\projects\blog`, false)
	if err != nil {
		t.Fatal(err)
	}
	if copier.calls != 1 {
		t.Fatalf("copy calls = %d, want 1", copier.calls)
	}
	if copier.request.Mode != string(model.SourceWSL) || copier.request.Source != "/mnt/c/projects/blog" || copier.request.Destination != "/data/devstack/projects/blog" || copier.request.RemoveSource {
		t.Fatalf("copy request = %+v", copier.request)
	}
	if got.SourceMode != model.SourceWSL || got.HostPath != `\\wsl.localhost\devstack\data\devstack\projects\blog` || got.BackendPath != "/data/devstack/projects/blog" {
		t.Fatalf("updated site paths = %+v", got)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceMode != model.SourceWSL || stored.HostPath != got.HostPath {
		t.Fatalf("stored site = %+v", stored)
	}
}

func TestChangeSourceModeRequiresStoppedSiteBeforeCopy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("source copy requires Windows source modes")
	}
	svc, fakes := newTestService(t)
	copier := &fakeSourceCopier{response: agentproto.SourceCopyResponse{SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	svc.deps.SourceCopier = copier
	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		SourceMode: model.SourceWindows, HostPath: `C:\projects\blog`, BackendPath: "/mnt/c/projects/blog",
		Status: model.StatusRunning, DesiredState: model.DesiredRunning,
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ChangeSourceMode(ctx, "blog", model.SourceWSL, `\\wsl.localhost\devstack\data\devstack\projects\blog`, false); err == nil {
		t.Fatal("ChangeSourceMode succeeded for a running site")
	}
	if copier.calls != 0 {
		t.Fatalf("copy calls = %d, want 0", copier.calls)
	}
}

func TestChangeSourceModeRejectsDestinationOutsideManagedRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("source copy requires Windows source modes")
	}
	tests := []struct {
		name        string
		destination string
	}{
		{name: "root itself", destination: `\\wsl.localhost\devstack\data\devstack\projects`},
		{name: "sibling prefix", destination: `\\wsl.localhost\devstack\data\devstack\projects-old\blog`},
		{name: "outside root", destination: `\\wsl.localhost\devstack\home\aram\blog`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestService(t)
			copier := &fakeSourceCopier{response: agentproto.SourceCopyResponse{SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
			svc.deps.SourceCopier = copier
			site := model.Site{
				ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
				SourceMode: model.SourceWindows, HostPath: `C:\projects\blog`, BackendPath: "/mnt/c/projects/blog",
				Status: model.StatusStopped, DesiredState: model.DesiredStopped,
			}
			if err := fakes.store.CreateSite(ctx, site); err != nil {
				t.Fatal(err)
			}

			_, err := svc.ChangeSourceMode(ctx, "blog", model.SourceWSL, tc.destination, false)
			if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
				t.Fatalf("ChangeSourceMode error = %v, want ErrOutsideProjectsRoot", err)
			}
			if copier.calls != 0 {
				t.Fatalf("copy calls = %d, want 0 for a rejected destination", copier.calls)
			}
			stored, err := fakes.store.GetSite(ctx, site.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.SourceMode != site.SourceMode || stored.HostPath != site.HostPath || stored.BackendPath != site.BackendPath {
				t.Fatalf("site changed after rejected destination: %+v", stored)
			}
		})
	}
}

func TestChangeSourceModeCopyFailureLeavesSiteUnchanged(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("source copy requires Windows source modes")
	}
	svc, fakes := newTestService(t)
	copier := &fakeSourceCopier{err: errors.New("agent copy failed")}
	svc.deps.SourceCopier = copier
	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		SourceMode: model.SourceWindows, HostPath: `C:\projects\blog`, BackendPath: "/mnt/c/projects/blog",
		Status: model.StatusStopped, DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ChangeSourceMode(ctx, "blog", model.SourceWSL, `\\wsl.localhost\devstack\data\devstack\projects\blog`, false); err == nil {
		t.Fatal("ChangeSourceMode succeeded after the copy failed")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SourceMode != model.SourceWindows || stored.HostPath != site.HostPath || stored.BackendPath != site.BackendPath {
		t.Fatalf("site changed after failed copy: %+v", stored)
	}
}

func TestChangeSourceModeRejectsNonLocalOnLinux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux-specific source mode rejection test")
	}
	svc, fakes := newTestService(t)
	site := model.Site{
		ID: "site", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic,
		SourceMode: model.SourceLocal, HostPath: "/home/aram/blog", BackendPath: "/home/aram/blog",
		Status: model.StatusStopped, DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangeSourceMode(ctx, "blog", model.SourceWSL, "/home/aram/blog", false); err == nil {
		t.Fatal("ChangeSourceMode should reject non-local source mode on Linux")
	}
}
