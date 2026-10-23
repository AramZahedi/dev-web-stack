package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/staticserve"
)

// newServeStaticCmd serves one static site for the local backend. The
// supervisor starts it with --root, --addr, and optional --spa flags.
func newServeStaticCmd() *cobra.Command {
	var (
		root string
		addr string
		spa  bool
	)
	cmd := &cobra.Command{
		Use:    "serve-static",
		Short:  "Serve a static site directory (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServeStatic(cmd.Context(), root, addr, spa)
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "directory to serve")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "listen address")
	cmd.Flags().BoolVar(&spa, "spa", false, "fall back to index.html for non-file paths")
	return cmd
}

// runServeStatic serves root until ctx is cancelled or the server fails to
// start. Listen errors are returned to the caller so the process exits
// non-zero.
func runServeStatic(ctx context.Context, root, addr string, spa bool) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("--root is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	handler, err := staticserve.NewStaticHandler(root, spa)
	if err != nil {
		return fmt.Errorf("serve-static: %w", err)
	}
	defer func() { _ = handler.Close() }()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("serve-static: listen on %s: %w", addr, err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve-static: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve-static: %w", err)
		}
		return nil
	}
}
