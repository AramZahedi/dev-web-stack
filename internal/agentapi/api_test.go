package agentapi

import (
	"net/http"
	"net/url"
	"testing"
)

type fakeModule struct{}

func (fakeModule) Name() string          { return "fake" }
func (fakeModule) MountPath() string     { return "/fake" }
func (fakeModule) Handler() http.Handler { return http.NotFoundHandler() }

func TestModuleContract(t *testing.T) {
	var m Module = fakeModule{}
	if m.Name() != "fake" || m.MountPath() != "/fake" {
		t.Fatalf("module = %q %q", m.Name(), m.MountPath())
	}
	if m.Handler() == nil {
		t.Fatal("nil handler")
	}
}

func TestMount(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, fakeModule{})
	_, got := mux.Handler(&http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/fake/x"}})
	if got != "/fake/" {
		t.Fatalf("mounted pattern = %q", got)
	}
}
