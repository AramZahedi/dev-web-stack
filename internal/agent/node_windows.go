//go:build windows

package agent

import (
	"errors"
	"os/exec"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// Node site processes are managed by the Linux agent; the Windows agent is a
// development stub and never serves Node sites.
func (r *Runtime) startNodeLocked(*runtimeSite) (agentproto.SiteActionResponse, *exec.Cmd, chan struct{}, error) {
	return agentproto.SiteActionResponse{}, nil, nil, errors.New("agent: Node sites require the Linux agent")
}

func (r *Runtime) watchNode(string, *exec.Cmd, chan struct{}) {}

func stopNodeProcess(*exec.Cmd, int, <-chan struct{}) {}
