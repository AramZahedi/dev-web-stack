//go:build !windows

package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// startNodeLocked launches a Node site command on its allocated loopback
// port. The caller holds r.mu. The returned done channel closes after the
// process has been reaped by watchNode.
func (r *Runtime) startNodeLocked(site *runtimeSite) (agentproto.SiteActionResponse, *exec.Cmd, chan struct{}, error) {
	nodeRoot, err := r.nodeRuntimePathLocked(site.spec.RuntimeVersion)
	if err != nil {
		return agentproto.SiteActionResponse{}, nil, nil, err
	}
	if strings.TrimSpace(site.spec.BackendPath) == "" {
		return agentproto.SiteActionResponse{}, nil, nil, fmt.Errorf("agent: node site %q has no backend path", site.spec.ID)
	}
	cmd := exec.Command("/bin/sh", "-c", site.spec.Command)
	cmd.Dir = site.spec.BackendPath
	env, err := r.nodeEnvironment(site, nodeRoot)
	if err != nil {
		return agentproto.SiteActionResponse{}, nil, nil, err
	}
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agentproto.SiteActionResponse{}, nil, nil, fmt.Errorf("agent: open Node stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return agentproto.SiteActionResponse{}, nil, nil, fmt.Errorf("agent: open Node stderr: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return agentproto.SiteActionResponse{}, nil, nil, fmt.Errorf("agent: start node site %q: %w", site.spec.ID, err)
	}

	done := make(chan struct{})
	site.nodeCmd = cmd
	site.nodePID = cmd.Process.Pid
	site.nodeDone = done
	site.status = "running"
	site.desiredRunning = true
	if err := r.saveLocked(); err != nil {
		site.nodeCmd = nil
		site.nodePID = 0
		site.nodeDone = nil
		site.desiredRunning = false
		site.status = "stopped"
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		go func() { _ = cmd.Wait() }()
		return agentproto.SiteActionResponse{}, nil, nil, err
	}
	go r.consumeNodeStream(site.spec.ID, stdout)
	go r.consumeNodeStream(site.spec.ID, stderr)
	r.logs.append(site.spec.ID, fmt.Sprintf("started Node site (PID %d on port %d)", cmd.Process.Pid, site.spec.HTTPPort))
	return agentproto.SiteActionResponse{Status: "running", PID: cmd.Process.Pid}, cmd, done, nil
}

func (r *Runtime) nodeEnvironment(site *runtimeSite, nodeRoot string) ([]string, error) {
	values := make(map[string]string, len(site.spec.Env)+4)
	for key, value := range site.spec.Env {
		values[key] = value
	}
	values["PORT"] = strconv.Itoa(site.spec.HTTPPort)
	values["HOST"] = "127.0.0.1"
	if base := os.Getenv("PATH"); base != "" {
		values["PATH"] = filepath.Join(nodeRoot, "bin") + ":" + base
	} else {
		values["PATH"] = filepath.Join(nodeRoot, "bin")
	}
	withCA, err := r.Environment(values)
	if err != nil {
		return nil, err
	}

	merged := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			merged[key] = value
		}
	}
	for key, value := range withCA {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+merged[key])
	}
	return env, nil
}

func (r *Runtime) consumeNodeStream(site string, reader io.ReadCloser) {
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line != "" {
			r.logs.append(site, line)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		r.logs.append(site, "node log stream: "+err.Error())
	}
}

// watchNode reaps the process and records an unexpected exit without
// overwriting a stop that was requested concurrently.
func (r *Runtime) watchNode(id string, cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)
	r.mu.Lock()
	current := r.sites[id]
	if current != nil && current.nodeCmd == cmd {
		wasDesired := current.desiredRunning
		current.nodeCmd = nil
		current.nodePID = 0
		current.nodeDone = nil
		if wasDesired {
			current.desiredRunning = false
			current.status = "crashed"
			message := "node site exited unexpectedly"
			if err != nil {
				message = "node site crashed: " + err.Error()
			}
			_ = r.saveLocked()
			r.logs.append(id, message)
		} else {
			current.status = "stopped"
			_ = r.saveLocked()
		}
	}
	r.mu.Unlock()
}

// stopNodeProcess terminates the Node process group, allowing the watcher to
// reap it. It never calls Wait itself.
func stopNodeProcess(cmd *exec.Cmd, pid int, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil || pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if done != nil {
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}
