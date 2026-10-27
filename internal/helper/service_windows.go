//go:build windows

package helper

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// RunWindowsService runs the fixed helper server under the Windows Service
// Control Manager. The service does not accept arbitrary commands or paths.
func RunWindowsService(name string, cfg Config) error {
	if name == "" {
		return fmt.Errorf("helper: Windows service name is empty")
	}
	return svc.Run(name, windowsService{cfg: cfg})
}

type windowsService struct {
	cfg Config
}

func (s windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	server, err := NewServer(s.cfg)
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		return false, 1
	}
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
		case svc.Stop, svc.Shutdown:
			statuses <- svc.Status{State: svc.StopPending}
			cancel()
			if err := server.Close(); err != nil {
				return false, 1
			}
			return false, 0
		default:
			continue
		}
	}
	cancel()
	if err := server.Close(); err != nil {
		return false, 1
	}
	return false, 0
}
