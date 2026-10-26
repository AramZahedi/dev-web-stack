//go:build !windows

package platform

import (
	"context"
	"os/exec"
)

type linuxCollector struct{}

func (linuxCollector) Collect(context.Context) (any, error) {
	return nil, nil
}

func runShell(ctx context.Context, dir, command string, env map[string]string) (BuildResult, error) {
	cmd := NewCommand(ctx, "sh", "-lc", command)
	cmd.Dir = dir
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytesBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := BuildResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	return result, nil
}

type bytesBuffer struct{ data []byte }

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *bytesBuffer) String() string { return string(b.data) }
