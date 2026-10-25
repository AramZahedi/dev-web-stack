package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// clientFrame is the text-frame protocol accepted from the browser.
type clientFrame struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// serverFrame is the text-frame protocol sent to the browser.
type serverFrame struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Code    *int   `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type exitResult struct {
	code int
	err  error
}

func (s *Session) serve(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The API validates Origin against its Host allowlist before calling
		// Serve, so a second library check would reject the loopback aliases
		// (127.0.0.1, localhost, devstack.test) the dashboard legitimately uses.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.manager.release(s)
		return fmt.Errorf("%w: %v", ErrUpgrade, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

	started := time.Now()
	reason, code, runErr := s.run(ctx, conn)
	s.manager.audit("stopped", s, "reason", reason, "exit_code", code,
		"duration_ms", time.Since(started).Milliseconds(), "error", errorText(runErr))
	s.manager.release(s)
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// run owns the PTY, the WebSocket writer, and the session deadline. Only this
// goroutine writes text frames, so coder/websocket's single-writer rule holds.
func (s *Session) run(ctx context.Context, conn *websocket.Conn) (string, int, error) {
	ptyFile, cmd, err := s.startPTY()
	if err != nil {
		_ = s.writeFrame(ctx, conn, serverFrame{Type: "error", Message: "failed to start the site shell"})
		return "start_failed", -1, err
	}
	s.manager.audit("started", s, "pid", cmd.Process.Pid)
	defer func() {
		s.mu.Lock()
		if s.ptyFile == ptyFile {
			s.ptyFile = nil
		}
		s.mu.Unlock()
		_ = ptyFile.Close()
	}()

	outCh := make(chan []byte)
	done := make(chan struct{})
	exitCh := make(chan exitResult, 1)
	readDone := make(chan struct{})
	go s.readPTY(ptyFile, cmd, outCh, exitCh, done, readDone)

	clientGone := make(chan struct{}, 1)
	activity := make(chan struct{}, 1)
	touch := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}
	go s.readClient(ctx, conn, ptyFile, clientGone, touch)

	idle := time.NewTimer(s.manager.idleTimeout)
	defer idle.Stop()
	absolute := time.NewTimer(s.manager.maxDuration)
	defer absolute.Stop()

	var written int64
	reason := "exit"
	exitCode := 0
	var runErr error

	for {
		select {
		case chunk := <-outCh:
			touch()
			if written >= s.manager.maxOutput {
				reason = "output_limit"
				goto shutdown
			}
			remaining := s.manager.maxOutput - written
			truncated := false
			if int64(len(chunk)) > remaining {
				chunk = chunk[:remaining]
				truncated = true
			}
			if len(chunk) > 0 {
				if err := s.writeFrame(ctx, conn, serverFrame{Type: "output", Data: string(chunk)}); err != nil {
					reason = "disconnect"
					runErr = err
					goto shutdown
				}
				written += int64(len(chunk))
			}
			if truncated || written >= s.manager.maxOutput {
				reason = "output_limit"
				goto shutdown
			}
		case result := <-exitCh:
			exitCode = result.code
			runErr = result.err
			goto finish
		case <-readDone:
			// exitCh is sent immediately after readDone closes.
			result := <-exitCh
			exitCode = result.code
			runErr = result.err
			goto finish
		case <-clientGone:
			reason = "disconnect"
			goto shutdown
		case <-idle.C:
			reason = "idle_timeout"
			goto shutdown
		case <-absolute.C:
			reason = "max_duration"
			goto shutdown
		case <-s.manager.closed:
			reason = "shutdown"
			goto shutdown
		case <-ctx.Done():
			reason = "disconnect"
			goto shutdown
		}
	}

finish:
	_ = s.writeFrame(ctx, conn, serverFrame{Type: "exit", Code: &exitCode})
	conn.CloseNow()
	return reason, exitCode, runErr

shutdown:
	close(done)
	s.terminate(ptyFile, readDone)
	code := s.waitExit(exitCh)
	if reason != "disconnect" {
		_ = s.writeFrame(ctx, conn, serverFrame{Type: "exit", Code: &code, Reason: reason})
	}
	conn.CloseNow()
	return reason, code, runErr
}

func (s *Session) startPTY() (*os.File, *exec.Cmd, error) {
	cmd := exec.Command(s.Shell)
	cmd.Dir = s.Dir
	cmd.Env = s.Env
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: initialPTYCols,
		Rows: initialPTYRows,
	})
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	s.ptyFile = ptyFile
	s.process = cmd.Process
	s.mu.Unlock()
	return ptyFile, cmd, nil
}

func (s *Session) readPTY(ptyFile *os.File, cmd *exec.Cmd, outCh chan<- []byte, exitCh chan<- exitResult, done <-chan struct{}, readDone chan struct{}) {
	buffer := make([]byte, outputReadBuffer)
	for {
		n, err := ptyFile.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			select {
			case outCh <- chunk:
			case <-done:
				goto wait
			}
		}
		if err != nil {
			break
		}
	}
wait:
	waitErr := cmd.Wait()
	result := exitResult{code: exitCode(waitErr, cmd.ProcessState), err: waitErr}
	s.mu.Lock()
	s.process = nil
	s.mu.Unlock()
	close(readDone)
	exitCh <- result
}

func (s *Session) readClient(ctx context.Context, conn *websocket.Conn, ptyFile *os.File, clientGone chan<- struct{}, touch func()) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			select {
			case clientGone <- struct{}{}:
			default:
			}
			return
		}
		touch()
		switch typ {
		case websocket.MessageBinary:
			if _, err := ptyFile.Write(data); err != nil {
				select {
				case clientGone <- struct{}{}:
				default:
				}
				return
			}
		case websocket.MessageText:
			var frame clientFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			switch frame.Type {
			case "input":
				if _, err := ptyFile.Write([]byte(frame.Data)); err != nil {
					select {
					case clientGone <- struct{}{}:
					default:
					}
					return
				}
			case "resize":
				cols, rows := frame.Cols, frame.Rows
				if cols == 0 || rows == 0 || cols > maxPTYDimension || rows > maxPTYDimension {
					continue
				}
				_ = pty.Setsize(ptyFile, &pty.Winsize{Cols: cols, Rows: rows})
			}
		}
	}
}

func (s *Session) writeFrame(ctx context.Context, conn *websocket.Conn, frame serverFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteWait)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

func (s *Session) terminate(ptyFile *os.File, readDone <-chan struct{}) {
	// If the child was already reaped, its PID may be reused; never signal it.
	select {
	case <-readDone:
	default:
		s.mu.Lock()
		process := s.process
		s.mu.Unlock()
		if process != nil {
			signalGroup(process.Pid, false)
			time.Sleep(100 * time.Millisecond)
			signalGroup(process.Pid, true)
		}
	}
	_ = ptyFile.Close()
}

func (s *Session) waitExit(exitCh <-chan exitResult) int {
	select {
	case result := <-exitCh:
		return result.code
	case <-time.After(processShutdownWait):
		s.mu.Lock()
		process := s.process
		s.mu.Unlock()
		if process != nil {
			signalGroup(process.Pid, true)
		}
		select {
		case result := <-exitCh:
			return result.code
		case <-time.After(time.Second):
			return -1
		}
	}
}

func exitCode(waitErr error, state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	if waitErr == nil {
		return 0
	}
	return state.ExitCode()
}
