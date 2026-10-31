//go:build !windows

package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const (
	maxDatabaseImportBytes int64 = 1 << 30
	backupTimestampLayout        = "20060102T150405.000000000Z"
	restoreTimestampLayout       = "20060102T150405_000000000Z"
)

var backupFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.sql\.gz$`)

func encodeBackupID(database, filename string) string {
	return database + "--" + filename
}

func decodeBackupID(id string, engine dbengine.Engine) (database, filename string, err error) {
	if id == "" || len(id) > 300 {
		return "", "", fmt.Errorf("%w: invalid backup ID", databases.ErrInvalidRequest)
	}
	if strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, 0) || strings.Contains(id, "..") {
		return "", "", fmt.Errorf("%w: invalid backup ID", databases.ErrInvalidRequest)
	}
	index := strings.Index(id, "--")
	if index <= 0 || index+2 >= len(id) {
		return "", "", fmt.Errorf("%w: invalid backup ID", databases.ErrInvalidRequest)
	}
	database = id[:index]
	filename = id[index+2:]
	if err := engine.ValidateDatabaseName(database); err != nil {
		return "", "", invalidIdentifier(err)
	}
	if !backupFilenamePattern.MatchString(filename) {
		return "", "", fmt.Errorf("%w: invalid backup filename", databases.ErrInvalidRequest)
	}
	return database, filename, nil
}

func (c *DockerServiceController) backupRoot(serviceID string) string {
	return filepath.Join(c.root, "backups", "databases", serviceID)
}

func (c *DockerServiceController) resolveBackup(serviceID, backupID string) (string, string, string, dbengine.Engine, model.DatabaseEngine, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return "", "", "", nil, "", err
	}
	if engine == nil {
		return "", "", "", nil, engineID, fmt.Errorf("%w: %s does not support backups", databases.ErrUnsupported, engineID)
	}
	database, filename, err := decodeBackupID(backupID, engine)
	if err != nil {
		return "", "", "", nil, engineID, err
	}
	root := c.backupRoot(serviceID)
	dir := filepath.Join(root, database)
	path := filepath.Join(dir, filename)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", "", nil, engineID, fmt.Errorf("%w: invalid backup path", databases.ErrInvalidRequest)
	}
	if err := rejectSymlinkPath(c.root, dir); err != nil {
		return "", "", "", nil, engineID, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	if info, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", "", nil, engineID, fmt.Errorf("%w: backup %q", databases.ErrNotFound, backupID)
		}
		return "", "", "", nil, engineID, fmt.Errorf("inspect backup directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", "", nil, engineID, fmt.Errorf("%w: invalid backup directory", databases.ErrInvalidRequest)
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", "", nil, engineID, fmt.Errorf("%w: backup %q", databases.ErrNotFound, backupID)
		}
		return "", "", "", nil, engineID, fmt.Errorf("inspect backup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", "", "", nil, engineID, fmt.Errorf("%w: invalid backup file", databases.ErrInvalidRequest)
	}
	return path, database, filename, engine, engineID, nil
}

// ListBackups walks the helper backup tree. It works while the service is
// stopped because it only touches the agent filesystem.
func (c *DockerServiceController) ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error) {
	engineID, err := c.engineID(serviceID)
	if err != nil {
		return nil, err
	}
	if engineID == model.DatabaseRedis {
		return nil, fmt.Errorf("%w: Redis does not support backups", databases.ErrUnsupported)
	}
	root := c.backupRoot(serviceID)
	if err := rejectSymlinkPath(c.root, root); err != nil {
		return nil, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []model.DatabaseBackup{}, nil
		}
		return nil, fmt.Errorf("list backups: %w", err)
	}
	backups := make([]model.DatabaseBackup, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".staging" {
			continue
		}
		database := entry.Name()
		if err := dbengine.ValidateIdentifier(database); err != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, database))
		if err != nil {
			return nil, fmt.Errorf("list backups for %q: %w", database, err)
		}
		for _, file := range files {
			if file.IsDir() || !backupFilenamePattern.MatchString(file.Name()) {
				continue
			}
			path := filepath.Join(root, database, file.Name())
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			sha, err := hashFile(ctx, path)
			if err != nil {
				return nil, err
			}
			created := info.ModTime().UTC()
			kind := model.BackupManual
			timestamp := strings.TrimSuffix(file.Name(), ".sql.gz")
			for _, candidate := range []model.DatabaseBackupKind{
				model.BackupManual, model.BackupPreDrop, model.BackupPreRestore, model.BackupPreImport,
			} {
				suffix := "." + string(candidate)
				if strings.HasSuffix(timestamp, suffix) {
					kind = candidate
					timestamp = strings.TrimSuffix(timestamp, suffix)
					break
				}
			}
			if parsed, err := time.Parse(backupTimestampLayout, timestamp); err == nil {
				created = parsed
			}
			backups = append(backups, model.DatabaseBackup{
				ID:        encodeBackupID(database, file.Name()),
				ServiceID: serviceID,
				Engine:    engineID,
				Database:  database,
				SizeBytes: info.Size(),
				SHA256:    sha,
				Kind:      kind,
				Status:    model.BackupReady,
				CreatedAt: created,
			})
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].CreatedAt.Equal(backups[j].CreatedAt) {
			return backups[i].ID < backups[j].ID
		}
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

func hashFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open backup for hashing: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	buf := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, err := hash.Write(buf[:n]); err != nil {
				return "", fmt.Errorf("hash backup: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("hash backup: %w", readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CreateBackup streams a logical dump through gzip into the service backup
// tree and returns a completed job. It requires an active service.
func (c *DockerServiceController) CreateBackup(ctx context.Context, serviceID, database string, req databases.CreateBackupRequest) (model.DatabaseJob, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if engine == nil {
		return model.DatabaseJob{}, fmt.Errorf("%w: %s does not support backups", databases.ErrUnsupported, engineID)
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return model.DatabaseJob{}, invalidIdentifier(err)
	}
	kind := req.Kind
	if kind == "" {
		kind = model.BackupManual
	}
	if !validBackupKind(kind) {
		return model.DatabaseJob{}, fmt.Errorf("%w: unsupported backup kind %q", databases.ErrInvalidRequest, kind)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	command, err := engine.DumpCommand(database)
	if err != nil {
		return model.DatabaseJob{}, engineError(err)
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	stream, err := c.runEngineStream(ctx, serviceID, command)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	defer func() { _ = stream.Close() }()

	dir := filepath.Join(c.backupRoot(serviceID), database)
	if err := rejectSymlinkPath(c.root, dir); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("create backup directory: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return model.DatabaseJob{}, fmt.Errorf("%w: invalid backup directory", databases.ErrInvalidRequest)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("set backup directory mode: %w", err)
	}
	filename := c.backupFilename(dir, kind)
	finalPath := filepath.Join(dir, filename)

	tmp, err := os.CreateTemp(dir, "backup-*.sql.gz.tmp")
	if err != nil {
		return model.DatabaseJob{}, fmt.Errorf("create backup file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return model.DatabaseJob{}, fmt.Errorf("set backup file mode: %w", err)
	}
	counter := &countingWriter{writer: tmp}
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(counter, hash))
	if _, err := io.Copy(gz, stream); err != nil {
		_ = gz.Close()
		cleanup()
		return model.DatabaseJob{}, fmt.Errorf("stream backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		cleanup()
		return model.DatabaseJob{}, fmt.Errorf("close backup gzip: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return model.DatabaseJob{}, fmt.Errorf("sync backup file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return model.DatabaseJob{}, fmt.Errorf("close backup file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return model.DatabaseJob{}, fmt.Errorf("publish backup: %w", err)
	}
	syncDir(dir)
	now := c.now()
	return model.DatabaseJob{
		ID:        fmt.Sprintf("job_%d", now.UnixNano()),
		ServiceID: serviceID,
		Database:  database,
		Kind:      model.JobBackup,
		Status:    model.JobSucceeded,
		Progress:  100,
		BackupID:  encodeBackupID(database, filename),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func validBackupKind(kind model.DatabaseBackupKind) bool {
	switch kind {
	case model.BackupManual, model.BackupPreDrop, model.BackupPreRestore, model.BackupPreImport:
		return true
	default:
		return false
	}
}

func (c *DockerServiceController) backupFilename(dir string, kind model.DatabaseBackupKind) string {
	if kind == "" {
		kind = model.BackupManual
	}
	timestamp := c.now().UTC().Format(backupTimestampLayout)
	suffix := "." + string(kind) + ".sql.gz"
	name := timestamp + suffix
	for index := 2; ; index++ {
		_, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return name
		}
		if err != nil {
			return name
		}
		name = fmt.Sprintf("%s-%d%s", timestamp, index, suffix)
	}
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

// DeleteBackup removes one backup file. It works while the service is stopped.
func (c *DockerServiceController) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	path, _, _, _, _, err := c.resolveBackup(serviceID, backupID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// DownloadBackup opens a backup file for streaming. It works while the service
// is stopped.
func (c *DockerServiceController) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	path, _, _, _, _, err := c.resolveBackup(serviceID, backupID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	return file, nil
}

// RestoreBackup restores a backup into a target database. Overwrite requires a
// matching typed confirmation and terminates target sessions first.
func (c *DockerServiceController) RestoreBackup(ctx context.Context, serviceID, backupID string, req databases.RestoreBackupRequest) (model.DatabaseJob, error) {
	path, sourceDatabase, _, engine, _, err := c.resolveBackup(serviceID, backupID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	target := req.Target
	if target == "" {
		if req.Overwrite {
			target = sourceDatabase
		} else {
			target = restoreTargetName(sourceDatabase, c.now())
		}
	}
	if err := engine.ValidateDatabaseName(target); err != nil {
		return model.DatabaseJob{}, invalidIdentifier(err)
	}
	if engine.ProtectedDatabase(target) {
		return model.DatabaseJob{}, fmt.Errorf("%w: database %q", databases.ErrProtected, target)
	}
	if req.Overwrite && req.Confirm != target {
		return model.DatabaseJob{}, fmt.Errorf("%w: confirmation does not match database %q", databases.ErrInvalidRequest, target)
	}
	if !req.Overwrite && target == sourceDatabase {
		return model.DatabaseJob{}, fmt.Errorf("%w: restoring into the source database requires overwrite", databases.ErrInvalidRequest)
	}
	exists, err := c.databaseExists(ctx, serviceID, target)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	// Validate the backup before any destructive step. A corrupt or empty
	// backup must never drop the target.
	reader, err := openBackupGzip(path)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	defer func() { _ = reader.Close() }()
	command, err := engine.RestoreCommand(target)
	if err != nil {
		return model.DatabaseJob{}, engineError(err)
	}
	preBackupID := ""
	if req.Overwrite && exists {
		preJob, err := c.CreateBackup(ctx, serviceID, target, databases.CreateBackupRequest{Kind: model.BackupPreRestore})
		if err != nil {
			return model.DatabaseJob{}, fmt.Errorf("pre-restore backup: %w", err)
		}
		preBackupID = preJob.BackupID
	}
	recoveryID := preBackupID
	if recoveryID == "" {
		recoveryID = backupID
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if req.Overwrite {
		if exists {
			if err := c.dropDatabaseDirect(ctx, serviceID, engine, target); err != nil {
				return failedJob(serviceID, target, model.JobRestore, recoveryID, c.now(), err), err
			}
		}
		if err := c.createDatabaseDirect(ctx, serviceID, engine, target); err != nil {
			return failedJob(serviceID, target, model.JobRestore, recoveryID, c.now(), err), err
		}
	} else {
		if exists {
			return model.DatabaseJob{}, fmt.Errorf("%w: target database %q already exists", databases.ErrConflict, target)
		}
		if err := c.createDatabaseDirect(ctx, serviceID, engine, target); err != nil {
			return failedJob(serviceID, target, model.JobRestore, recoveryID, c.now(), err), err
		}
	}
	if err := c.runEngineInput(ctx, serviceID, reader, command); err != nil {
		c.terminateCanceledSessions(ctx, serviceID, engine, target)
		return failedJob(serviceID, target, model.JobRestore, recoveryID, c.now(), err), err
	}
	now := c.now()
	return model.DatabaseJob{
		ID:        fmt.Sprintf("job_%d", now.UnixNano()),
		ServiceID: serviceID,
		Database:  target,
		Kind:      model.JobRestore,
		Status:    model.JobSucceeded,
		Progress:  100,
		BackupID:  preBackupID,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ImportDatabase stages an uploaded SQL or SQL.gz file under the backup tree,
// restores it, and always deletes the staging file. Replace mode drops and
// recreates the target after typed confirmation.
func (c *DockerServiceController) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req databases.ImportRequest) (model.DatabaseJob, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if engine == nil {
		return model.DatabaseJob{}, fmt.Errorf("%w: %s does not support imports", databases.ErrUnsupported, engineID)
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return model.DatabaseJob{}, invalidIdentifier(err)
	}
	if engine.ProtectedDatabase(database) {
		return model.DatabaseJob{}, fmt.Errorf("%w: database %q", databases.ErrProtected, database)
	}
	mode := req.Mode
	if mode == "" {
		mode = databases.ImportMerge
	}
	if mode != databases.ImportMerge && mode != databases.ImportReplace {
		return model.DatabaseJob{}, fmt.Errorf("%w: unsupported import mode %q", databases.ErrInvalidRequest, mode)
	}
	if mode == databases.ImportReplace && req.Confirm != database {
		return model.DatabaseJob{}, fmt.Errorf("%w: confirmation does not match database %q", databases.ErrInvalidRequest, database)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	exists, err := c.databaseExists(ctx, serviceID, database)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if !exists {
		return model.DatabaseJob{}, fmt.Errorf("%w: database %q", databases.ErrNotFound, database)
	}
	stagingDir := filepath.Join(c.backupRoot(serviceID), ".staging")
	if err := rejectSymlinkPath(c.root, stagingDir); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("create import staging directory: %w", err)
	}
	if err := os.Chmod(stagingDir, 0o700); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("set import staging directory mode: %w", err)
	}
	staged, err := os.CreateTemp(stagingDir, "import-*.sql.gz")
	if err != nil {
		return model.DatabaseJob{}, fmt.Errorf("create import staging file: %w", err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	if err := staged.Chmod(0o600); err != nil {
		_ = staged.Close()
		return model.DatabaseJob{}, fmt.Errorf("set import staging mode: %w", err)
	}
	if _, err := copyLimited(staged, file, maxDatabaseImportBytes); err != nil {
		_ = staged.Close()
		return model.DatabaseJob{}, err
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return model.DatabaseJob{}, fmt.Errorf("sync import staging file: %w", err)
	}
	if err := staged.Close(); err != nil {
		return model.DatabaseJob{}, fmt.Errorf("close import staging file: %w", err)
	}

	// Validate the staged SQL/gzip and resolve the restore command before any
	// destructive step.
	if err := validateImportFile(stagedPath); err != nil {
		return model.DatabaseJob{}, err
	}
	stagedReader, err := openSQLFile(stagedPath)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	defer func() { _ = stagedReader.Close() }()
	reader, err := validateRestoreReader(stagedReader)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	command, err := engine.RestoreCommand(database)
	if err != nil {
		return model.DatabaseJob{}, engineError(err)
	}
	preBackupID := ""
	if mode == databases.ImportReplace || req.PreImportBackup {
		preJob, err := c.CreateBackup(ctx, serviceID, database, databases.CreateBackupRequest{Kind: model.BackupPreImport})
		if err != nil {
			return model.DatabaseJob{}, fmt.Errorf("pre-import backup: %w", err)
		}
		preBackupID = preJob.BackupID
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if mode == databases.ImportReplace {
		if err := c.dropDatabaseDirect(ctx, serviceID, engine, database); err != nil {
			return failedJob(serviceID, database, model.JobImport, preBackupID, c.now(), err), err
		}
		if err := c.createDatabaseDirect(ctx, serviceID, engine, database); err != nil {
			return failedJob(serviceID, database, model.JobImport, preBackupID, c.now(), err), err
		}
	}
	if err := c.runEngineInput(ctx, serviceID, reader, command); err != nil {
		c.terminateCanceledSessions(ctx, serviceID, engine, database)
		return failedJob(serviceID, database, model.JobImport, preBackupID, c.now(), err), err
	}
	now := c.now()
	return model.DatabaseJob{
		ID:        fmt.Sprintf("job_%d", now.UnixNano()),
		ServiceID: serviceID,
		Database:  database,
		Kind:      model.JobImport,
		Status:    model.JobSucceeded,
		Progress:  100,
		BackupID:  preBackupID,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ExportDatabase streams a logical dump directly to the caller.
func (c *DockerServiceController) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	engine, engineID, err := c.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, fmt.Errorf("%w: %s does not support exports", databases.ErrUnsupported, engineID)
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return nil, invalidIdentifier(err)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.DumpCommand(database)
	if err != nil {
		return nil, engineError(err)
	}
	return c.runEngineStream(ctx, serviceID, command)
}

func (c *DockerServiceController) databaseExists(ctx context.Context, serviceID, database string) (bool, error) {
	list, err := c.ListDatabases(ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, item := range list {
		if item.Name == database {
			return true, nil
		}
	}
	return false, nil
}

func (c *DockerServiceController) createDatabaseDirect(ctx context.Context, serviceID string, engine dbengine.Engine, database string) error {
	sql, err := engine.CreateDatabaseSQL(database, "", "")
	if err != nil {
		return engineError(err)
	}
	_, err = c.runDatabaseSQL(ctx, serviceID, sql)
	return err
}

func (c *DockerServiceController) dropDatabaseDirect(ctx context.Context, serviceID string, engine dbengine.Engine, database string) error {
	if err := c.terminateSessions(ctx, serviceID, engine, database); err != nil {
		return err
	}
	sql, err := engine.DropDatabaseSQL(database)
	if err != nil {
		return engineError(err)
	}
	_, err = c.runDatabaseSQL(ctx, serviceID, sql)
	return err
}

func restoreTargetName(source string, now time.Time) string {
	suffix := "_restore_" + now.UTC().Format(restoreTimestampLayout)
	maxSource := 63 - len(suffix)
	if maxSource < 1 {
		maxSource = 1
	}
	if len(source) > maxSource {
		source = source[:maxSource]
	}
	return source + suffix
}

// openBackupGzip opens and validates the backup stream before a destructive
// restore. The returned reader replays the validated prefix.
func openBackupGzip(path string) (io.ReadCloser, error) {
	if err := validateGzipFile(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open backup gzip: %w", err)
	}
	reader, err := validateRestoreReader(gz)
	if err != nil {
		_ = gz.Close()
		_ = file.Close()
		return nil, err
	}
	return &combinedReadCloser{Reader: reader, closers: []io.Closer{gz, file}}, nil
}

// validateRestoreReader reads a small prefix so corrupt/empty streams fail
// before the target is dropped, then replays the prefix to the restore command.
func validateRestoreReader(reader io.Reader) (io.Reader, error) {
	prefix := make([]byte, 4096)
	n, err := reader.Read(prefix)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: validate restore stream: %v", databases.ErrInvalidRequest, err)
	}
	if n > 0 {
		return io.MultiReader(bytes.NewReader(prefix[:n]), reader), nil
	}
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: restore stream is empty", databases.ErrInvalidRequest)
	}
	return reader, nil
}

// validateGzipFile fully decompresses a gzip file so a truncated trailer or
// corrupted stream fails before a destructive restore.
func validateGzipFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup gzip: %w", err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open backup gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("%w: backup gzip is invalid: %v", databases.ErrInvalidRequest, err)
	}
	return nil
}

// validateImportFile checks a staged import before the target is touched.
func validateImportFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open staged import: %w", err)
	}
	header := make([]byte, 2)
	n, readErr := io.ReadFull(file, header)
	_ = file.Close()
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: inspect staged import: %v", databases.ErrInvalidRequest, readErr)
	}
	if n == 2 && header[0] == 0x1f && header[1] == 0x8b {
		return validateGzipFile(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect staged import: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%w: import stream is empty", databases.ErrInvalidRequest)
	}
	return nil
}

func openSQLFile(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open staged import: %w", err)
	}
	header := make([]byte, 2)
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = file.Close()
		return nil, fmt.Errorf("inspect staged import: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("rewind staged import: %w", err)
	}
	if n == 2 && header[0] == 0x1f && header[1] == 0x8b {
		gz, err := gzip.NewReader(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("open staged gzip import: %w", err)
		}
		return &combinedReadCloser{Reader: gz, closers: []io.Closer{gz, file}}, nil
	}
	return file, nil
}

type combinedReadCloser struct {
	io.Reader
	closers []io.Closer
}

func (c *combinedReadCloser) Close() error {
	var errs []error
	for _, closer := range c.closers {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	limited := &io.LimitedReader{R: src, N: limit + 1}
	n, err := io.Copy(dst, limited)
	if n > limit {
		return n, fmt.Errorf("%w: import exceeds the 1 GiB limit", databases.ErrInvalidRequest)
	}
	if err != nil {
		return n, fmt.Errorf("stage import: %w", err)
	}
	return n, nil
}

// terminateCanceledSessions kills engine sessions left behind when the daemon
// cancels an import/restore. Killing the local docker exec client is not enough:
// the SQL client inside the container keeps running the current statement.
func (c *DockerServiceController) terminateCanceledSessions(ctx context.Context, serviceID string, engine dbengine.Engine, database string) {
	if !errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_ = c.terminateSessions(cleanupCtx, serviceID, engine, database)
}

func failedJob(serviceID, database string, kind model.DatabaseJobKind, backupID string, now time.Time, cause error) model.DatabaseJob {
	return model.DatabaseJob{
		ID:        fmt.Sprintf("job_%d", now.UnixNano()),
		ServiceID: serviceID,
		Database:  database,
		Kind:      kind,
		Status:    model.JobFailed,
		Progress:  0,
		Message:   cause.Error(),
		BackupID:  backupID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
