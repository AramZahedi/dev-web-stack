package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

// CopySource moves a project tree inside the managed distro. The Windows
// daemon has already translated both paths; the agent only accepts absolute
// paths and hands the verified copy to internal/wsl.
func (r *Runtime) CopySource(ctx context.Context, req agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return agentproto.SourceCopyResponse{}, err
	}
	source := strings.TrimSpace(req.Source)
	destination := strings.TrimSpace(req.Destination)
	if source == "" || destination == "" {
		return agentproto.SourceCopyResponse{}, errors.New("agent: source and destination are required")
	}
	if strings.ContainsRune(source, 0) || strings.ContainsRune(destination, 0) {
		return agentproto.SourceCopyResponse{}, errors.New("agent: source paths must not contain NUL")
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return agentproto.SourceCopyResponse{}, errors.New("agent: source and destination must be absolute paths")
	}
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	if source == "/" || destination == "/" {
		return agentproto.SourceCopyResponse{}, errors.New("agent: source and destination must not be the filesystem root")
	}
	result, err := wsl.CopySource(ctx, source, destination, req.Mode, req.RemoveSource)
	if err != nil {
		return agentproto.SourceCopyResponse{}, fmt.Errorf("agent: copy source: %w", err)
	}
	return agentproto.SourceCopyResponse{
		Files:  result.Files,
		Bytes:  result.Bytes,
		SHA256: result.SHA256,
	}, nil
}
