package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/terminal"
)

// TestTerminalWebSocketEndToEnd drives the real API handler, ticket flow, and
// PTY manager over an httptest WebSocket. The site is static, so no managed
// runtime is required; the command proves the shell executes in the project
// root as the current user.
func TestTerminalWebSocketEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("terminal PTY end-to-end requires Linux")
	}
	_, fx := newTestAPIWithFakes(t)
	project := t.TempDir()
	site := model.Site{
		ID:          "site-static",
		Name:        "static-app",
		Kind:        model.SiteStatic,
		HostPath:    project,
		BackendPath: project,
		Status:      model.StatusStopped,
	}
	addTerminalSite(fx, site)

	home := t.TempDir()
	manager := terminal.NewManager(terminal.Deps{
		Paths:    config.NewPaths(home),
		Runtimes: fx.runtimes,
		PHP:      fx.sites,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		UID:      os.Getuid(),
		Shell:    "/bin/sh",
	})
	t.Cleanup(manager.Close)
	fx.server.terminal = manager

	rec := fx.request(t, http.MethodPost, "/api/v1/sites/static-app/terminal/session", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ticket status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ticket terminal.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}

	server := httptest.NewServer(fx.authedHandler())
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/v1/sites/static-app/terminal?ticket=" + url.QueryEscape(ticket.Token)
	origin := "http://" + strings.TrimPrefix(server.URL, "http://")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{origin}},
	})
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })

	input, _ := json.Marshal(map[string]string{"type": "input", "data": "printf 'E2E=%s\\n' \"$DEVSTACK_SITE\"; pwd\r"})
	if err := conn.Write(ctx, websocket.MessageText, input); err != nil {
		t.Fatalf("write input: %v", err)
	}
	exit, _ := json.Marshal(map[string]string{"type": "input", "data": "exit 0\r"})
	if err := conn.Write(ctx, websocket.MessageText, exit); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	var output strings.Builder
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read terminal frame: %v (output %q)", err, output.String())
		}
		if typ != websocket.MessageText {
			continue
		}
		var frame struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Code *int   `json:"code"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		switch frame.Type {
		case "output":
			output.WriteString(frame.Data)
		case "exit":
			if frame.Code == nil || *frame.Code != 0 {
				t.Fatalf("exit frame = %+v", frame)
			}
			text := output.String()
			if !strings.Contains(text, "E2E=static-app") {
				t.Fatalf("DEVSTACK_SITE missing from PTY output: %q", text)
			}
			if !strings.Contains(text, project) {
				t.Fatalf("PTY cwd is not the project root: %q", text)
			}
			return
		}
	}
}
