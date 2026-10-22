package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newServeStaticCmd serves one static site for the local backend. Task 11
// replaces the handler with internal/staticserve; the flags stay the same.
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
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(root) == "" {
				return errors.New("--root is required")
			}
			info, err := os.Stat(root)
			if err != nil {
				return fmt.Errorf("serve-static root %s: %w", root, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("serve-static root %s is not a directory", root)
			}

			server := &http.Server{
				Addr:              addr,
				Handler:           staticHandler(root, spa),
				ReadHeaderTimeout: 10 * time.Second,
			}
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "directory to serve")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "listen address")
	cmd.Flags().BoolVar(&spa, "spa", false, "fall back to index.html for non-file paths")
	return cmd
}

// staticHandler serves files and index.html for directories, never a directory
// listing. With spa enabled, missing non-file paths return index.html.
func staticHandler(root string, spa bool) http.Handler {
	files := http.FileServer(http.Dir(root))
	indexPath := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + r.URL.Path)
		target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

		info, err := os.Stat(target)
		switch {
		case err == nil && info.IsDir():
			if _, err := os.Stat(filepath.Join(target, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
		case err != nil:
			if !spa || path.Ext(clean) != "" {
				http.NotFound(w, r)
				return
			}
			if _, err := os.Stat(indexPath); err != nil {
				http.NotFound(w, r)
				return
			}
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			files.ServeHTTP(w, clone)
			return
		}
		files.ServeHTTP(w, r)
	})
}
