package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxAgentLogLines = 5000

type logHub struct {
	mu    sync.Mutex
	root  string
	lines map[string][]string
	subs  map[string]map[chan string]struct{}
}

func newLogHub(root string) *logHub {
	return &logHub{root: root, lines: make(map[string][]string), subs: make(map[string]map[chan string]struct{})}
}

func (h *logHub) append(site, message string) {
	line := time.Now().UTC().Format(time.RFC3339Nano) + " " + message
	h.mu.Lock()
	lines := append(h.lines[site], line)
	if len(lines) > maxAgentLogLines {
		lines = append([]string(nil), lines[len(lines)-maxAgentLogLines:]...)
	}
	h.lines[site] = lines
	for subscriber := range h.subs[site] {
		select {
		case subscriber <- line:
		default:
		}
	}
	file, err := os.OpenFile(filepath.Join(h.root, site+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_ = file.Chmod(0o600)
		_, _ = fmt.Fprintln(file, line)
		_ = file.Close()
	}
	h.mu.Unlock()
}

func (h *logHub) read(site string, follow bool, tail int, ctx context.Context) (io.ReadCloser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tail < 0 {
		return nil, fmt.Errorf("agent: invalid log tail %d", tail)
	}
	if tail == 0 || tail > maxAgentLogLines {
		tail = 200
	}
	h.mu.Lock()
	lines := append([]string(nil), h.lines[site]...)
	if tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}
	if !follow {
		h.mu.Unlock()
		if len(lines) == 0 {
			if data, err := os.ReadFile(filepath.Join(h.root, site+".log")); err == nil {
				lines = tailLines(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), tail)
			}
		}
		return io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + lineEnding(lines))), nil
	}
	subscriber := make(chan string, 64)
	if h.subs[site] == nil {
		h.subs[site] = make(map[chan string]struct{})
	}
	h.subs[site][subscriber] = struct{}{}
	h.mu.Unlock()
	readCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	go func() {
		defer func() {
			cancel()
			h.mu.Lock()
			delete(h.subs[site], subscriber)
			h.mu.Unlock()
			close(subscriber)
			_ = writer.Close()
		}()
		for _, line := range lines {
			if _, err := io.WriteString(writer, line+"\n"); err != nil {
				return
			}
		}
		for {
			select {
			case line := <-subscriber:
				if _, err := io.WriteString(writer, line+"\n"); err != nil {
					return
				}
			case <-readCtx.Done():
				return
			}
		}
	}()
	return &pipeReadCloser{
		Reader: reader,
		close: func() error {
			cancel()
			_ = reader.Close()
			return writer.Close()
		},
	}, nil
}

type pipeReadCloser struct {
	io.Reader
	close func() error
}

func (r *pipeReadCloser) Close() error { return r.close() }

func tailLines(lines []string, count int) []string {
	if count < len(lines) {
		return lines[len(lines)-count:]
	}
	return lines
}

func lineEnding(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "\n"
}
