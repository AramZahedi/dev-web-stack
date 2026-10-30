package agentdocker

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
)

const maxDatabaseImportBytes int64 = 1 << 30

var backupFileNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\.sql\.gz$`)

func (d *Docker) databaseBackupRoot(serviceID string) (string, error) {
	service, err := d.service(serviceID)
	if err != nil {
		return "", err
	}
	if _, ok := databaseEngineID(service.ID); !ok {
		return "", fmt.Errorf("agentdocker: %s is not a database service", service.ID)
	}
	return filepath.Join(d.root, "backups", "databases", string(service.ID)), nil
}

func validateBackupID(backupID string) (string, string, error) {
	parts := strings.Split(backupID, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: invalid backup ID", databases.ErrInvalidRequest)
	}
	database, file := parts[0], parts[1]
	if err := dbengine.ValidateIdentifier(database); err != nil {
		return "", "", fmt.Errorf("%w: invalid backup database", databases.ErrInvalidRequest)
	}
	if !backupFileNamePattern.MatchString(file) || strings.Contains(file, "..") {
		return "", "", fmt.Errorf("%w: invalid backup file", databases.ErrInvalidRequest)
	}
	return database, file, nil
}

func (d *Docker) databaseBackupPath(serviceID, backupID string) (string, error) {
	root, err := d.databaseBackupRoot(serviceID)
	if err != nil {
		return "", err
	}
	database, file, err := validateBackupID(backupID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, database, file)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: backup path escapes the service tree", databases.ErrInvalidRequest)
	}
	return path, nil
}

func backupTimestamp() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}

func newDatabaseJobID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("job_%d", time.Now().UnixNano())
	}
	return "job_" + hex.EncodeToString(raw)
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func openRegularBackup(path string) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, databases.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: backup is not a regular file", databases.ErrInvalidRequest)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return file, info, nil
}

// copyBackupStream compresses the engine dump directly into a 0600 file and
// returns the compressed size and SHA-256.
func copyBackupStream(ctx context.Context, stream io.ReadCloser, target string) (int64, string, error) {
	if err := ensureRealDirectory(filepath.Dir(target), 0o700); err != nil {
		return 0, "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".backup-*.tmp")
	if err != nil {
		return 0, "", fmt.Errorf("agentdocker: create backup: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("agentdocker: secure backup: %w", err)
	}
	hasher := sha256.New()
	counter := &countingWriter{writer: io.MultiWriter(tmp, hasher)}
	gz := gzip.NewWriter(counter)
	copyErr := func() error {
		defer func() { _ = stream.Close() }()
		if _, err := io.Copy(gz, stream); err != nil {
			_ = gz.Close()
			return fmt.Errorf("agentdocker: stream backup: %w", err)
		}
		if err := gz.Close(); err != nil {
			return fmt.Errorf("agentdocker: finalize backup: %w", err)
		}
		return nil
	}()
	if copyErr != nil {
		_ = tmp.Close()
		return 0, "", copyErr
	}
	if err := ctx.Err(); err != nil {
		_ = tmp.Close()
		return 0, "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("agentdocker: sync backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", fmt.Errorf("agentdocker: close backup: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return 0, "", fmt.Errorf("agentdocker: publish backup: %w", err)
	}
	return counter.count, hex.EncodeToString(hasher.Sum(nil)), nil
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}
