package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

type terminalTicket struct {
	token     string
	siteID    string
	cols      int
	rows      int
	expiresAt time.Time
}

type terminalClientFrame struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type terminalServerFrame struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Code    *int   `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// IssueTerminalTicket creates a short-lived one-time ticket for site PTY WebSocket access.
func (r *Runtime) IssueTerminalTicket(id string, req agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error) {
	if _, err := r.site(id); err != nil {
		return agentproto.TerminalTicketResponse{}, err
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return agentproto.TerminalTicketResponse{}, fmt.Errorf("agent: generate ticket: %w", err)
	}
	token := hex.EncodeToString(raw)

	cols, rows := req.Cols, req.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	expiresAt := time.Now().Add(30 * time.Second)

	r.termMu.Lock()
	if r.termTickets == nil {
		r.termTickets = make(map[string]*terminalTicket)
	}
	r.termTickets[token] = &terminalTicket{
		token:     token,
		siteID:    id,
		cols:      cols,
		rows:      rows,
		expiresAt: expiresAt,
	}
	r.termMu.Unlock()

	return agentproto.TerminalTicketResponse{
		URL:       agentproto.PathSiteTerminalFor(id),
		Ticket:    token,
		ExpiresAt: expiresAt,
	}, nil
}

func (r *Runtime) consumeTerminalTicket(token, id string) (*terminalTicket, bool) {
	r.termMu.Lock()
	defer r.termMu.Unlock()

	ticket, ok := r.termTickets[token]
	if !ok {
		return nil, false
	}
	delete(r.termTickets, token)

	if ticket.siteID != id || time.Now().After(ticket.expiresAt) {
		return nil, false
	}
	return ticket, true
}

// HandleTerminalWebSocket upgrades to WebSocket, validates the ticket, and runs a PTY shell.
func (r *Runtime) HandleTerminalWebSocket(w http.ResponseWriter, req *http.Request, siteID string) {
	token := strings.TrimSpace(req.URL.Query().Get("ticket"))
	if token == "" {
		token = strings.TrimSpace(req.Header.Get("X-Terminal-Ticket"))
	}
	if token == "" {
		if proto := req.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
			for _, p := range strings.Split(proto, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					token = p
					r.termMu.Lock()
					_, exists := r.termTickets[p]
					r.termMu.Unlock()
					if exists {
						break
					}
				}
			}
		}
	}
	if token == "" {
		writeAgentError(w, http.StatusForbidden, agentproto.CodeForbidden, "terminal ticket is required")
		return
	}

	ticket, ok := r.consumeTerminalTicket(token, siteID)
	if !ok {
		writeAgentError(w, http.StatusForbidden, agentproto.CodeForbidden, "invalid or expired terminal ticket")
		return
	}

	site, err := r.site(siteID)
	if err != nil {
		writeAgentError(w, http.StatusNotFound, agentproto.CodeNotFound, "site not found")
		return
	}

	acceptOpts := &websocket.AcceptOptions{
		// Origin checking is disabled because this is an internal loopback agent
		// protected by bearer token auth and single-use 30s TTL tickets.
		InsecureSkipVerify: true,
	}
	if req.Header.Get("Sec-WebSocket-Protocol") != "" {
		acceptOpts.Subprotocols = []string{token}
	}

	conn, err := websocket.Accept(w, req, acceptOpts)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	shell := "/bin/bash"
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	}
	cmd := exec.Command(shell)
	cmd.Dir = site.spec.BackendPath

	envMap := make(map[string]string)
	for k, v := range site.spec.Env {
		envMap[k] = v
	}
	caEnv, _ := r.Environment(envMap)
	caEnv["TERM"] = "xterm-256color"
	caEnv["DEVSTACK_SITE"] = siteID

	envList := os.Environ()
	for k, v := range caEnv {
		envList = append(envList, k+"="+v)
	}
	cmd.Env = envList

	// Start PTY or fallback pipe
	sz := &pty.Winsize{Rows: uint16(ticket.rows), Cols: uint16(ticket.cols)}
	ptyFile, ptyErr := pty.StartWithSize(cmd, sz)

	var inWriter io.WriteCloser
	var outReader io.Reader

	if errors.Is(ptyErr, pty.ErrUnsupported) || ptyErr != nil && runtime.GOOS == "windows" {
		// Non-pty fallback for environments that don't support unix ptys
		stdin, sErr := cmd.StdinPipe()
		if sErr != nil {
			_ = conn.Close(websocket.StatusInternalError, "failed to open stdin pipe")
			return
		}
		stdout, oErr := cmd.StdoutPipe()
		if oErr != nil {
			_ = conn.Close(websocket.StatusInternalError, "failed to open stdout pipe")
			return
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "failed to start shell")
			return
		}
		inWriter = stdin
		outReader = stdout
	} else if ptyErr != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("failed to start pty: %v", ptyErr))
		return
	} else {
		inWriter = ptyFile
		outReader = ptyFile
		defer func() { _ = ptyFile.Close() }()
	}

	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	var writeMu sync.Mutex
	sendFrame := func(frame terminalServerFrame) error {
		data, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, websocket.MessageText, data)
	}

	// PTY stdout -> WebSocket frames
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := outReader.Read(buf)
			if n > 0 {
				_ = sendFrame(terminalServerFrame{Type: "output", Data: string(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()

	// WebSocket messages -> PTY stdin / resize
	go func() {
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			var frame terminalClientFrame
			if err := json.Unmarshal(msg, &frame); err == nil {
				switch frame.Type {
				case "input":
					_, _ = inWriter.Write([]byte(frame.Data))
				case "resize":
					if ptyFile != nil {
						_ = pty.Setsize(ptyFile, &pty.Winsize{Rows: frame.Rows, Cols: frame.Cols})
					}
				}
			}
		}
	}()

	// Wait for process exit or context done
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
	case err := <-done:
		exitCode := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		_ = sendFrame(terminalServerFrame{Type: "exit", Code: &exitCode})
		_ = conn.Close(websocket.StatusNormalClosure, "process exited")
	}
}
