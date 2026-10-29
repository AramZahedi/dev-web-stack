package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

type agentConfigAPI interface {
	ServiceConfig(context.Context, string) (agentproto.ServiceConfigResponse, error)
	ServiceContent(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceApply(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceReset(context.Context, agentproto.ServiceConfigRequest) error
}

// AgentServiceConfig is the Windows daemon's config adapter. All config file
// reads, writes, backups, and resets execute in the agent's WSL ext4 tree.
type AgentServiceConfig struct {
	api   agentConfigAPI
	paths *AgentPaths
}

// NewAgentServiceConfig creates a typed WSL service-config adapter.
func NewAgentServiceConfig(api agentConfigAPI, wslRoot string) (*AgentServiceConfig, error) {
	if api == nil {
		return nil, errors.New("servicecontrol: agent config API is required")
	}
	paths, err := NewAgentPaths(wslRoot)
	if err != nil {
		return nil, err
	}
	return &AgentServiceConfig{api: api, paths: paths}, nil
}

// List fetches config metadata without accessing WSL over UNC or duplicating it
// on NTFS.
func (c *AgentServiceConfig) List(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	if err := c.validateService(id); err != nil {
		return agentproto.ServiceConfigResponse{}, err
	}
	response, err := c.api.ServiceConfig(ctx, agentAPIID(id))
	if err != nil {
		return agentproto.ServiceConfigResponse{}, fmt.Errorf("servicecontrol: list %s config through agent: %w", id, err)
	}
	for _, file := range response.Files {
		if _, err := cleanAgentConfigRel(file.Path); err != nil {
			return agentproto.ServiceConfigResponse{}, fmt.Errorf("servicecontrol: agent returned unsafe config metadata: %w", err)
		}
	}
	return response, nil
}

// Content reads one safe service config file from WSL.
func (c *AgentServiceConfig) Content(ctx context.Context, request agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	if err := c.validateRequest(request); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	req := request
	req.ID = agentAPIID(req.ID)
	response, err := c.api.ServiceContent(ctx, req)
	if err != nil {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("servicecontrol: read %s config through agent: %w", request.ID, err)
	}
	if _, err := cleanAgentConfigRel(response.Path); err != nil {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("servicecontrol: agent returned unsafe config path: %w", err)
	}
	return response, nil
}

// Apply writes one safe service config through the agent; agent-side backup is
// authoritative and the daemon never holds a local config copy.
func (c *AgentServiceConfig) Apply(ctx context.Context, request agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	if err := c.validateRequest(request); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if len(request.Content) > 1<<20 || !utf8.ValidString(request.Content) || strings.ContainsRune(request.Content, 0) {
		return agentproto.ServiceContentResponse{}, errors.New("servicecontrol: config content must be UTF-8 text no larger than 1 MiB")
	}
	req := request
	req.ID = agentAPIID(req.ID)
	response, err := c.api.ServiceApply(ctx, req)
	if err != nil {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("servicecontrol: apply %s config through agent: %w", request.ID, err)
	}
	if _, err := cleanAgentConfigRel(response.Path); err != nil {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("servicecontrol: agent returned unsafe config path: %w", err)
	}
	return response, nil
}

// Reset restores the newest WSL-side service config backup.
func (c *AgentServiceConfig) Reset(ctx context.Context, request agentproto.ServiceConfigRequest) error {
	if err := c.validateRequest(request); err != nil {
		return err
	}
	req := request
	req.ID = agentAPIID(req.ID)
	if err := c.api.ServiceReset(ctx, req); err != nil {
		return fmt.Errorf("servicecontrol: reset %s config through agent: %w", request.ID, err)
	}
	return nil
}

func (c *AgentServiceConfig) validateRequest(request agentproto.ServiceConfigRequest) error {
	if err := c.validateService(request.ID); err != nil {
		return err
	}
	_, err := cleanAgentConfigRel(request.Rel)
	return err
}

func (c *AgentServiceConfig) validateService(id string) error {
	if _, err := c.paths.ForService(agentAPIID(id)); err != nil {
		return err
	}
	return nil
}

func cleanAgentConfigRel(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || path.IsAbs(value) {
		return "", errors.New("servicecontrol: config path must be a safe relative WSL path")
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("servicecontrol: config path traverses outside the service config tree")
	}
	for _, segment := range strings.Split(clean, "/") {
		lower := strings.ToLower(segment)
		if segment == "" || strings.HasPrefix(segment, ".") || containsAny(lower, "secret", "password", "passwd", "credential", "token", "private", "apikey", "api_key") {
			return "", errors.New("servicecontrol: config path selects reserved or sensitive data")
		}
	}
	return clean, nil
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
