package servicecontrol

import (
	"errors"
	"path"
	"strings"
)

var fixedAgentServiceIDs = map[string]struct{}{
	"nginx": {}, "apache": {}, "mysql": {}, "mariadb": {},
	"postgres": {}, "redis": {}, "phpmyadmin": {},
}

// AgentServicePaths are the canonical ext4 paths owned by the WSL agent.
type AgentServicePaths struct {
	Config  string
	Data    string
	Logs    string
	Secrets string
}

// AgentPaths resolves fixed service IDs under the canonical WSL root. It is a
// path description only; Windows callers must perform all filesystem access
// through the authenticated agent API.
type AgentPaths struct{ root string }

// NewAgentPaths refuses Windows/UNC roots and any root other than the fixed
// managed WSL storage root.
func NewAgentPaths(root string) (*AgentPaths, error) {
	root = strings.TrimSpace(root)
	if strings.Contains(root, `\`) || !path.IsAbs(root) || path.Clean(root) != "/data/devstack" {
		return nil, errors.New("servicecontrol: agent root must be /data/devstack in WSL")
	}
	return &AgentPaths{root: path.Clean(root)}, nil
}

// ForService returns canonical config/data/log/secret paths for one catalog ID.
func (p *AgentPaths) ForService(id string) (AgentServicePaths, error) {
	if p == nil {
		return AgentServicePaths{}, errors.New("servicecontrol: agent paths are not configured")
	}
	if _, ok := fixedAgentServiceIDs[id]; !ok {
		return AgentServicePaths{}, errors.New("servicecontrol: unknown fixed service ID")
	}
	base := path.Join(p.root, "services", id)
	return AgentServicePaths{
		Config:  path.Join(base, "config"),
		Data:    path.Join(base, "data"),
		Logs:    path.Join(base, "logs"),
		Secrets: path.Join(base, "secrets"),
	}, nil
}

// SecretDirectoryMode is the required private mode for agent-side service
// secret directories.
func (*AgentPaths) SecretDirectoryMode() uint32 { return 0o700 }
