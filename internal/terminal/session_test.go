//go:build !windows

package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type wsMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data"`
	Code    *int   `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func newTerminalServer(t *testing.T, manager *Manager, site model.Site) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		err := manager.Serve(r.Context(), w, r, site, r.URL.Query().Get("ticket"))
		if err != nil {
			status := http.StatusForbidden
			if isTerminalLimit(err) {
				status = http.StatusTooManyRequests
			}
			http.Error(w, err.Error(), status)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, "ws" + strings.TrimPrefix(server.URL, "http") + "/terminal"
}

func dialTerminal(t *testing.T, baseURL, token string) (*websocket.Conn, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, baseURL+"?ticket="+url.QueryEscape(token), nil)
	if err != nil {
		t.Fatalf("dial terminal: %v (response %+v)", err, resp)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, resp
}

func sendJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func sendInput(t *testing.T, conn *websocket.Conn, input string) {
	t.Helper()
	sendJSON(t, conn, map[string]any{"type": "input", "data": input})
}

func readOne(t *testing.T, conn *websocket.Conn, timeout time.Duration) wsMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v, want text", typ)
	}
	var message wsMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode message %q: %v", data, err)
	}
	return message
}

// readUntilExit collects output frames until an exit frame arrives.
func readUntilExit(t *testing.T, conn *websocket.Conn, timeout time.Duration) (string, wsMessage) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var output strings.Builder
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		message := readOne(t, conn, remaining)
		switch message.Type {
		case "output":
			output.WriteString(message.Data)
		case "exit":
			return output.String(), message
		case "error":
			t.Fatalf("server error frame: %s", message.Message)
		default:
			t.Fatalf("unexpected server frame type %q", message.Type)
		}
	}
	t.Fatalf("timed out waiting for exit; output so far: %q", output.String())
	return "", wsMessage{}
}

func TestSessionRunsRealPTYCommand(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	server, wsURL := newTerminalServer(t, manager, site)
	_ = server
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	sendInput(t, conn, "php --version\r")
	sendInput(t, conn, "printf 'SITE=%s\\n' \"$DEVSTACK_SITE\"\r")
	sendInput(t, conn, "pwd\r")
	sendInput(t, conn, "id -u\r")
	sendInput(t, conn, "exit 7\r")

	output, exit := readUntilExit(t, conn, 10*time.Second)
	if !strings.Contains(output, "SITE_PHP") {
		t.Fatalf("site PHP shim was not first on PATH: %q", output)
	}
	if strings.Contains(output, "GLOBAL_PHP") {
		t.Fatalf("global PHP shim won over the site shim: %q", output)
	}
	if !strings.Contains(output, "SITE=app") {
		t.Fatalf("DEVSTACK_SITE missing from the PTY environment: %q", output)
	}
	if !strings.Contains(output, site.HostPath) {
		t.Fatalf("shell cwd is not the project root: %q", output)
	}
	if !strings.Contains(output, strconv.Itoa(deps.UID)) {
		t.Fatalf("shell is not running as uid %d: %q", deps.UID, output)
	}
	if exit.Code == nil || *exit.Code != 7 {
		t.Fatalf("exit frame = %+v, want code 7", exit)
	}
}

func TestSessionAcceptsBinaryInput(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo binary-ok\r")); err != nil {
		t.Fatalf("write binary input: %v", err)
	}
	sendInput(t, conn, "exit 0\r")
	output, _ := readUntilExit(t, conn, 10*time.Second)
	if !strings.Contains(output, "binary-ok") {
		t.Fatalf("binary input was not forwarded: %q", output)
	}
}

func TestSessionResize(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	sendJSON(t, conn, map[string]any{"type": "resize", "cols": 100, "rows": 42})
	sendInput(t, conn, "stty size\r")
	sendInput(t, conn, "exit 0\r")
	output, _ := readUntilExit(t, conn, 10*time.Second)
	if !strings.Contains(output, "42 100") {
		t.Fatalf("PTY was not resized to 42x100: %q", output)
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.IdleTimeout = 200 * time.Millisecond
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	_, exit := readUntilExit(t, conn, 5*time.Second)
	if exit.Reason != "idle_timeout" {
		t.Fatalf("exit reason = %q, want idle_timeout", exit.Reason)
	}
	if err := waitForSessions(manager, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestSessionOutputLimit(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	deps.MaxOutput = 4096
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	sendInput(t, conn, "head -c 200000 /dev/zero | tr '\\0' a\r")
	output, exit := readUntilExit(t, conn, 10*time.Second)
	if exit.Reason != "output_limit" {
		t.Fatalf("exit reason = %q, want output_limit", exit.Reason)
	}
	if int64(len(output)) > deps.MaxOutput {
		t.Fatalf("output = %d bytes, cap is %d", len(output), deps.MaxOutput)
	}
}

func TestSessionDisconnectKillsProcessGroup(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)

	sendInput(t, conn, "echo PID=$$\r")
	sendInput(t, conn, "exec sleep 60\r")

	pid := 0
	deadline := time.Now().Add(5 * time.Second)
	pidPattern := regexp.MustCompile(`PID=([0-9]+)`)
	for time.Now().Before(deadline) && pid == 0 {
		message := readOne(t, conn, time.Until(deadline))
		if message.Type != "output" {
			continue
		}
		if match := pidPattern.FindStringSubmatch(message.Data); len(match) == 2 {
			parsed, err := strconv.Atoi(match[1])
			if err == nil {
				pid = parsed
			}
		}
	}
	if pid == 0 {
		t.Fatal("did not read the shell pid")
	}

	conn.CloseNow()
	if err := waitForSessions(manager, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d survived client disconnect", pid)
}

func TestSessionTicketIsOneUseOverWebSocket(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	first, _ := dialTerminal(t, wsURL, ticket.Token)
	first.CloseNow()
	if err := waitForSessions(manager, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"?ticket="+url.QueryEscape(ticket.Token), nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("reused ticket was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reused ticket status = %+v, want 403", resp)
	}
}

func TestSessionAuditDoesNotLogInput(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	var logBuffer strings.Builder
	deps.Logger = newBufferLogger(&logBuffer)
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)
	const secret = "AUDIT_SECRET_COMMAND"
	sendInput(t, conn, "echo "+secret+"\r")
	sendInput(t, conn, "exit 0\r")
	readUntilExit(t, conn, 10*time.Second)
	if err := waitForSessions(manager, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	audit := logBuffer.String()
	if strings.Contains(audit, secret) {
		t.Fatalf("audit log contains terminal input: %s", audit)
	}
	if strings.Contains(audit, ticket.Token) {
		t.Fatalf("audit log contains the terminal ticket: %s", audit)
	}
	if !strings.Contains(audit, "terminal.session") {
		t.Fatalf("audit log has no terminal session entries: %s", audit)
	}
}

func isTerminalLimit(err error) bool {
	return errors.Is(err, ErrLimit)
}

func waitForSessions(manager *Manager, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if manager.ActiveSessions() == want {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return &sessionWaitError{got: manager.ActiveSessions(), want: want}
}

type sessionWaitError struct {
	got, want int
}

func (e *sessionWaitError) Error() string {
	return "terminal: active sessions = " + strconv.Itoa(e.got) + ", want " + strconv.Itoa(e.want)
}

func TestSessionRunsSiteNodeShim(t *testing.T) {
	home := t.TempDir()
	paths, site, build := testNodeFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	manager := NewManager(deps)
	t.Cleanup(manager.Close)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)
	sendInput(t, conn, "node --version\r")
	sendInput(t, conn, "exit 0\r")
	output, _ := readUntilExit(t, conn, 10*time.Second)
	if !strings.Contains(output, "SITE_NODE") {
		t.Fatalf("site Node shim was not used: %q", output)
	}
}

func TestManagerCloseReapsLiveSession(t *testing.T) {
	home := t.TempDir()
	paths, site, build, info := testPHPFixture(t, home)
	deps := testDeps(paths)
	deps.Runtimes = staticRuntimes{build}
	deps.PHP = staticPHP{info: info}
	manager := NewManager(deps)

	_, wsURL := newTerminalServer(t, manager, site)
	ticket, err := manager.IssueTicket(context.Background(), site)
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	conn, _ := dialTerminal(t, wsURL, ticket.Token)
	sendInput(t, conn, "echo PID=$$\r")
	sendInput(t, conn, "exec sleep 60\r")

	pid := 0
	deadline := time.Now().Add(5 * time.Second)
	pidPattern := regexp.MustCompile(`PID=([0-9]+)`)
	for time.Now().Before(deadline) && pid == 0 {
		message := readOne(t, conn, time.Until(deadline))
		if message.Type != "output" {
			continue
		}
		if match := pidPattern.FindStringSubmatch(message.Data); len(match) == 2 {
			parsed, err := strconv.Atoi(match[1])
			if err == nil {
				pid = parsed
			}
		}
	}
	if pid == 0 {
		t.Fatal("did not read the shell pid")
	}

	manager.Close()
	if err := waitForSessions(manager, 0, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d survived manager shutdown", pid)
}
