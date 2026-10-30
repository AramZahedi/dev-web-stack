package api

import (
	"net/http"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func TestRuntimeInUseCodeMatchesRuntimeKind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		buildID string
		code    string
	}{
		{name: "node", buildID: "node@22.11.0", code: codeRuntimeInUse},
		{name: "php", buildID: "php@8.4.27-r1", code: codePHPVersionInUse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, code, _, details := classifyError(&sites.RuntimeInUseError{
				BuildID: tc.buildID,
				Sites:   []string{"app"},
			})
			if status != http.StatusConflict || code != tc.code {
				t.Fatalf("in-use = %d %s, want 409 %s", status, code, tc.code)
			}
			names, ok := details.(map[string]any)["sites"].([]string)
			if !ok || len(names) != 1 || names[0] != "app" {
				t.Fatalf("in-use details = %#v", details)
			}
		})
	}
}
