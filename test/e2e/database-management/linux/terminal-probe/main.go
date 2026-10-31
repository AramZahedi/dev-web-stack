// Command terminal-probe drives the site terminal WebSocket in the cycle 5
// disposable environment. It prints only the non-secret E2E_TERM lines so a
// password or DSN can never reach evidence.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const probeCommand = `for k in DB_CONNECTION DB_HOST DB_PORT DB_DATABASE DB_USERNAME; do eval "v=\$$k"; printf 'E2E_TERM %s=%s\n' "$k" "$v"; done; if [ -n "${DB_PASSWORD+x}" ]; then p=true; else p=false; fi; echo "E2E_TERM DB_PASSWORD_PRESENT=$p"; if [ -n "${DATABASE_URL+x}" ]; then u=true; else u=false; fi; echo "E2E_TERM DATABASE_URL_PRESENT=$u"; echo "E2E_TERM_""DONE"`

func main() {
	base := flag.String("base", "http://127.0.0.1:27170", "daemon UI base URL")
	site := flag.String("site", "", "site reference")
	ticket := flag.String("ticket", "", "terminal ticket")
	simple := flag.Bool("simple", false, "send a short echo command instead of the environment probe")
	override := flag.String("command", "", "override the probe command (diagnostics only)")
	commandFile := flag.String("command-file", "", "read the probe command from a file (diagnostics only)")
	flag.Parse()
	if strings.TrimSpace(*site) == "" || strings.TrimSpace(*ticket) == "" {
		fmt.Fprintln(os.Stderr, "terminal-probe: -site and -ticket are required")
		os.Exit(2)
	}
	wsURL := "ws" + strings.TrimPrefix(strings.TrimRight(*base, "/"), "http") +
		"/api/v1/sites/" + url.PathEscape(*site) + "/terminal?ticket=" + url.QueryEscape(*ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Origin": {*base}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminal-probe: dial:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()

	// The PTY shell needs a moment to initialise readline. Sending a long
	// command before the prompt can be echoed by the terminal driver and then
	// discarded, which made the probe silently report no environment.
	time.Sleep(750 * time.Millisecond)
	write := func(value string) error {
		payload, _ := json.Marshal(map[string]string{"type": "input", "data": value})
		return conn.Write(ctx, websocket.MessageText, payload)
	}
	command := probeCommand
	if *simple {
		command = "echo READY"
	}
	if strings.TrimSpace(*override) != "" {
		command = *override
	}
	if strings.TrimSpace(*commandFile) != "" {
		data, err := os.ReadFile(*commandFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "terminal-probe: read command file:", err)
			os.Exit(2)
		}
		command = strings.TrimSpace(string(data))
	}
	if err := write(command + "\r"); err != nil {
		fmt.Fprintln(os.Stderr, "terminal-probe: write command:", err)
		os.Exit(1)
	}

	var output strings.Builder
	frames := 0
	sawDone := false
	for !sawDone {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "terminal-probe: read:", err)
			os.Exit(1)
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
			continue
		}
		switch frame.Type {
		case "output":
			frames++
			output.WriteString(frame.Data)
			for _, line := range strings.Split(output.String(), "\n") {
				if candidate, ok := probeLine(line); ok && candidate == "E2E_TERM_DONE" {
					sawDone = true
				}
			}
		case "exit":
			fmt.Fprintln(os.Stderr, "terminal-probe: shell exited before the probe finished")
			os.Exit(1)
		}
	}
	// Only ask the shell to exit after the probe output is complete; sending
	// exit immediately races the PTY drain and can drop the last frames.
	if err := write("exit 0\r"); err != nil {
		fmt.Fprintln(os.Stderr, "terminal-probe: write exit:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "terminal-probe: frames=%d output=%d done=%v\n", frames, output.Len(), sawDone)
	if os.Getenv("E2E_TERM_DEBUG") == "1" {
		fmt.Fprintln(os.Stderr, output.String())
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if candidate, ok := probeLine(line); ok {
			fmt.Println(candidate)
		}
	}
	if !sawDone {
		fmt.Fprintln(os.Stderr, "terminal-probe: probe did not finish")
		os.Exit(1)
	}
}

// probeLine extracts an actual probe output line and rejects the echoed
// command, which contains unexpanded placeholders and the literal marker.
func probeLine(line string) (string, bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	idx := strings.Index(line, "E2E_TERM")
	if idx < 0 {
		return "", false
	}
	candidate := strings.TrimSpace(line[idx:])
	if strings.Contains(candidate, "$") || strings.Contains(candidate, "%s") {
		return "", false
	}
	if strings.HasPrefix(candidate, "E2E_TERM DB_") ||
		strings.HasPrefix(candidate, "E2E_TERM DATABASE_") ||
		candidate == "E2E_TERM_DONE" {
		return candidate, true
	}
	return "", false
}
