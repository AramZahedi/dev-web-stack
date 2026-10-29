package daemon

import (
	"context"
	"fmt"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

// localSourceCopier is the Linux daemon's verified source-mode copy. On
// Windows the agent implements the same boundary inside the managed distro.
type localSourceCopier struct{}

func (localSourceCopier) CopySource(ctx context.Context, req agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	result, err := wsl.CopySource(ctx, req.Source, req.Destination, req.Mode, req.RemoveSource)
	if err != nil {
		return agentproto.SourceCopyResponse{}, fmt.Errorf("daemon: copy source: %w", err)
	}
	return agentproto.SourceCopyResponse{
		Files:  result.Files,
		Bytes:  result.Bytes,
		SHA256: result.SHA256,
	}, nil
}
