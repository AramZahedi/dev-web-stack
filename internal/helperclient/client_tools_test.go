package helperclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestToolMethodsUseProtocolPathsAndBodies(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
	)
	status := helperproto.ToolStatus{
		ID:          helperproto.ToolComposer,
		Installed:   true,
		Managed:     true,
		WrapperPath: "/usr/local/bin/composer",
		Target:      "/home/user/.devstack/tools/composer/bin/composer",
		Version:     "2.10.3",
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			t.Errorf("protocol header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == helperproto.PathToolsStatus:
			_ = json.NewEncoder(w).Encode(helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{status}})
		case r.Method == http.MethodPost && r.URL.Path == helperproto.PathToolsInstall:
			var req helperproto.ToolInstallRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode install: %v", err)
			}
			if req.ID != helperproto.ToolComposer || !req.Force {
				t.Errorf("install request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(helperproto.ToolResponse{Tool: status})
		case r.Method == http.MethodPost && r.URL.Path == helperproto.PathToolsRemove:
			var req helperproto.ToolRemoveRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode remove: %v", err)
			}
			if req.ID != helperproto.ToolComposer {
				t.Errorf("remove request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(helperproto.ToolResponse{Tool: status})
		default:
			http.NotFound(w, r)
		}
	})
	client := newUnixClient(t, handler)
	ctx := context.Background()

	tools, err := client.ToolStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].ID != helperproto.ToolComposer {
		t.Fatalf("tools = %+v", tools)
	}
	got, err := client.ToolInstall(ctx, helperproto.ToolComposer, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed || got.Version != "2.10.3" {
		t.Fatalf("install = %+v", got)
	}
	if _, err := client.ToolRemove(ctx, helperproto.ToolComposer); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET " + helperproto.PathToolsStatus,
		"POST " + helperproto.PathToolsInstall,
		"POST " + helperproto.PathToolsRemove,
	}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestToolErrorsMapToProtocolSentinels(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(helperproto.ErrorResponse{
			Code:    helperproto.CodeToolConflict,
			Message: "foreign wrapper exists",
		})
	})
	client := newUnixClient(t, handler)
	if _, err := client.ToolInstall(context.Background(), helperproto.ToolComposer, false); err == nil {
		t.Fatal("want conflict error")
	} else if !errors.Is(err, ErrToolConflict) {
		t.Fatalf("error = %v, want ErrToolConflict", err)
	}
}
