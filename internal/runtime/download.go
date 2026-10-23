package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

// download fetches entry.URL into dest while hashing the stream. The size and
// SHA-256 are checked before the file is accepted, so no unverified byte can
// reach the extractor. A failed download removes dest.
func (m *Manager) download(ctx context.Context, entry catalog.Entry, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("runtime: create cache directory: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return fmt.Errorf("runtime: build download request for %q: %w", entry.URL, err)
	}
	// Ask for the raw bytes: the transport must not transparently decompress a
	// gzip body, because the pinned checksum covers the compressed tarball.
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "devstack-runtime-installer")

	response, err := m.client.Do(request)
	if err != nil {
		return fmt.Errorf("runtime: download %s: %w", entry.URL, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime: download %s: unexpected status %s", entry.URL, response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != entry.Size {
		return fmt.Errorf("runtime: download %s: content length %d, want %d: %w",
			entry.URL, response.ContentLength, entry.Size, ErrChecksumMismatch)
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("runtime: create download %s: %w", dest, err)
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(dest)
		}
	}()

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, entry.Size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		return fmt.Errorf("runtime: download %s: %w", entry.URL, copyErr)
	case syncErr != nil:
		return fmt.Errorf("runtime: sync download %s: %w", dest, syncErr)
	case closeErr != nil:
		return fmt.Errorf("runtime: close download %s: %w", dest, closeErr)
	}
	if written != entry.Size {
		return fmt.Errorf("runtime: download %s: size %d, want %d: %w",
			entry.URL, written, entry.Size, ErrChecksumMismatch)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != entry.SHA256 {
		return fmt.Errorf("runtime: download %s: sha256 %s, want %s: %w",
			entry.URL, got, entry.SHA256, ErrChecksumMismatch)
	}

	success = true
	return nil
}
