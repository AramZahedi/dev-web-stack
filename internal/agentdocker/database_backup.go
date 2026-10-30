package agentdocker

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// CreateBackup streams an engine dump through gzip into the service backup
// tree. The daemon stores the metadata row; the agent returns the file ID.
func (d *Docker) CreateBackup(ctx context.Context, serviceID, database string, req databases.CreateBackupRequest) (model.DatabaseJob, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return model.DatabaseJob{}, databases.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return model.DatabaseJob{}, mapEngineError(err)
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	return d.createBackupLocked(ctx, serviceID, engine, database, req.Kind)
}

func (d *Docker) createBackupLocked(ctx context.Context, serviceID string, engine dbengine.Engine, database string, kind model.DatabaseBackupKind) (model.DatabaseJob, error) {
	service, err := d.service(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if kind == "" {
		kind = model.BackupManual
	}
	job := newDatabaseJob(string(service.ID), database, model.JobBackup)
	command, err := engine.DumpCommand(database)
	if err != nil {
		return failDatabaseJob(job, mapEngineError(err))
	}
	stream, err := d.runDatabaseStream(ctx, serviceID, command)
	if err != nil {
		return failDatabaseJob(job, err)
	}
	root := filepath.Join(d.root, "backups", "databases", string(service.ID))
	dir := filepath.Join(root, database)
	if err := ensureRealDirectory(dir, 0o700); err != nil {
		_ = stream.Close()
		return failDatabaseJob(job, fmt.Errorf("agentdocker: prepare backup directory: %w", err))
	}
	name := backupTimestamp() + ".sql.gz"
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); err == nil {
		name = backupTimestamp() + "-" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".sql.gz"
		target = filepath.Join(dir, name)
	}
	if _, _, err := copyBackupStream(ctx, stream, target); err != nil {
		return failDatabaseJob(job, err)
	}
	job.Status = model.JobSucceeded
	job.Progress = 100
	job.BackupID = database + "/" + name
	job.Message = string(kind)
	job.UpdatedAt = time.Now().UTC()
	return job, nil
}

// ListBackups walks the backup tree and never requires an active service.
func (d *Docker) ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() == model.DatabaseRedis {
		return nil, databases.ErrUnsupported
	}
	root := filepath.Join(d.root, "backups", "databases", string(service.ID))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []model.DatabaseBackup{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentdocker: list backups: %w", err)
	}
	backups := make([]model.DatabaseBackup, 0)
	for _, databaseEntry := range entries {
		if !databaseEntry.IsDir() || databaseEntry.Name() == ".staging" {
			continue
		}
		if err := dbengine.ValidateIdentifier(databaseEntry.Name()); err != nil {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, databaseEntry.Name()))
		if err != nil {
			return nil, fmt.Errorf("agentdocker: list %s backups: %w", databaseEntry.Name(), err)
		}
		for _, fileEntry := range files {
			if fileEntry.IsDir() || !backupFileNamePattern.MatchString(fileEntry.Name()) {
				continue
			}
			info, err := fileEntry.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			path := filepath.Join(root, databaseEntry.Name(), fileEntry.Name())
			sha, err := hashFile(path)
			if err != nil {
				continue
			}
			createdAt := info.ModTime().UTC()
			if parsed, err := time.Parse("20060102T150405.000000000Z", strings.TrimSuffix(fileEntry.Name(), ".sql.gz")); err == nil {
				createdAt = parsed
			}
			backups = append(backups, model.DatabaseBackup{
				ID:        databaseEntry.Name() + "/" + fileEntry.Name(),
				ServiceID: string(service.ID),
				Engine:    engine.ID(),
				Database:  databaseEntry.Name(),
				SizeBytes: info.Size(),
				SHA256:    sha,
				Kind:      model.BackupManual,
				Status:    model.BackupReady,
				CreatedAt: createdAt,
			})
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt.After(backups[j].CreatedAt)
	})
	return backups, nil
}

// DeleteBackup removes one file below the service backup tree. It works while
// the service is stopped.
func (d *Docker) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	service, err := d.service(serviceID)
	if err != nil {
		return err
	}
	path, err := d.databaseBackupPath(serviceID, backupID)
	if err != nil {
		return err
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	file, _, err := openRegularBackup(path)
	if err != nil {
		return err
	}
	_ = file.Close()
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("agentdocker: delete backup: %w", err)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err == nil && len(entries) == 0 {
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

// DownloadBackup opens a backup for streaming. It works while the service is
// stopped because it only touches the agent filesystem.
func (d *Docker) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	path, err := d.databaseBackupPath(serviceID, backupID)
	if err != nil {
		return nil, err
	}
	file, _, err := openRegularBackup(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// RestoreBackup restores a gzip dump into a new or overwritten database.
func (d *Docker) RestoreBackup(ctx context.Context, serviceID, backupID string, req databases.RestoreBackupRequest) (model.DatabaseJob, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return model.DatabaseJob{}, databases.ErrUnsupported
	}
	sourceDatabase, _, err := validateBackupID(backupID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	path, err := d.databaseBackupPath(serviceID, backupID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = sourceDatabase
	}
	if err := engine.ValidateDatabaseName(target); err != nil {
		return model.DatabaseJob{}, mapEngineError(err)
	}
	if engine.ProtectedDatabase(target) {
		return model.DatabaseJob{}, databases.ErrProtected
	}
	if req.Overwrite && req.Confirm != target {
		return model.DatabaseJob{}, fmt.Errorf("%w: confirmation does not match %q", databases.ErrInvalidRequest, target)
	}
	if !req.Overwrite && target == sourceDatabase {
		return model.DatabaseJob{}, fmt.Errorf("%w: restoring into the source database requires overwrite", databases.ErrInvalidRequest)
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	job := newDatabaseJob(string(service.ID), target, model.JobRestore)
	exists, err := d.databaseExists(ctx, serviceID, target)
	if err != nil {
		return failDatabaseJob(job, err)
	}
	if !req.Overwrite && exists {
		return failDatabaseJob(job, fmt.Errorf("%w: target %q already exists", databases.ErrConflict, target))
	}
	if req.PreRestoreBackup && exists {
		if _, err := d.createBackupLocked(ctx, serviceID, engine, target, model.BackupPreRestore); err != nil {
			return failDatabaseJob(job, err)
		}
	}
	command, err := databaseSQLCommand(engine.ID())
	if err != nil {
		return failDatabaseJob(job, err)
	}
	if req.Overwrite && exists {
		if err := d.terminateDatabaseSessions(ctx, serviceID, engine, target); err != nil {
			return failDatabaseJob(job, err)
		}
		dropSQL, err := engine.DropDatabaseSQL(target)
		if err != nil {
			return failDatabaseJob(job, mapEngineError(err))
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(dropSQL)); err != nil {
			return failDatabaseJob(job, err)
		}
	}
	if !exists || req.Overwrite {
		createSQL, err := engine.CreateDatabaseSQL(target, "", "")
		if err != nil {
			return failDatabaseJob(job, mapEngineError(err))
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(createSQL)); err != nil {
			return failDatabaseJob(job, err)
		}
	}
	file, _, err := openRegularBackup(path)
	if err != nil {
		return failDatabaseJob(job, err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return failDatabaseJob(job, fmt.Errorf("agentdocker: open backup %s: %w", backupID, err))
	}
	defer func() { _ = gz.Close() }()
	restoreCommand, err := engine.RestoreCommand(target)
	if err != nil {
		return failDatabaseJob(job, mapEngineError(err))
	}
	if err := d.runDatabaseInput(ctx, serviceID, restoreCommand, gz); err != nil {
		return failDatabaseJob(job, err)
	}
	job.Status = model.JobSucceeded
	job.Progress = 100
	job.UpdatedAt = time.Now().UTC()
	return job, nil
}

// ImportDatabase stages an uploaded SQL or SQL.gz file under the backup tree,
// enforces the 1 GiB limit, and pipes it into the target.
func (d *Docker) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req databases.ImportRequest) (model.DatabaseJob, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	if engine.ID() == model.DatabaseRedis {
		return model.DatabaseJob{}, databases.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return model.DatabaseJob{}, mapEngineError(err)
	}
	if engine.ProtectedDatabase(database) {
		return model.DatabaseJob{}, databases.ErrProtected
	}
	mode := req.Mode
	if mode == "" {
		mode = databases.ImportMerge
	}
	if mode != databases.ImportMerge && mode != databases.ImportReplace {
		return model.DatabaseJob{}, fmt.Errorf("%w: unknown import mode %q", databases.ErrInvalidRequest, mode)
	}
	if mode == databases.ImportReplace && req.Confirm != database {
		return model.DatabaseJob{}, fmt.Errorf("%w: confirmation does not match %q", databases.ErrInvalidRequest, database)
	}
	if limited, ok := file.(*io.LimitedReader); ok && limited.N > maxDatabaseImportBytes {
		return model.DatabaseJob{}, fmt.Errorf("%w: import exceeds 1 GiB", databases.ErrInvalidRequest)
	}
	if lener, ok := file.(interface{ Len() int }); ok && int64(lener.Len()) > maxDatabaseImportBytes {
		return model.DatabaseJob{}, fmt.Errorf("%w: import exceeds 1 GiB", databases.ErrInvalidRequest)
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	job := newDatabaseJob(string(service.ID), database, model.JobImport)
	stagingDir := filepath.Join(d.root, "backups", "databases", string(service.ID), ".staging")
	if err := ensureRealDirectory(stagingDir, 0o700); err != nil {
		return failDatabaseJob(job, fmt.Errorf("agentdocker: prepare import staging: %w", err))
	}
	suffix := ".sql"
	if strings.HasSuffix(strings.ToLower(filename), ".gz") {
		suffix = ".sql.gz"
	}
	staged, err := os.CreateTemp(stagingDir, "import-*"+suffix)
	if err != nil {
		return failDatabaseJob(job, fmt.Errorf("agentdocker: stage import: %w", err))
	}
	stagedName := staged.Name()
	defer func() { _ = os.Remove(stagedName) }()
	if err := staged.Chmod(0o600); err != nil {
		_ = staged.Close()
		return failDatabaseJob(job, fmt.Errorf("agentdocker: secure import staging: %w", err))
	}
	written, err := io.Copy(staged, io.LimitReader(file, maxDatabaseImportBytes+1))
	if err != nil {
		_ = staged.Close()
		return failDatabaseJob(job, fmt.Errorf("agentdocker: read import: %w", err))
	}
	if written > maxDatabaseImportBytes {
		_ = staged.Close()
		return failDatabaseJob(job, fmt.Errorf("%w: import exceeds 1 GiB", databases.ErrInvalidRequest))
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return failDatabaseJob(job, fmt.Errorf("agentdocker: sync import staging: %w", err))
	}
	if err := staged.Close(); err != nil {
		return failDatabaseJob(job, fmt.Errorf("agentdocker: close import staging: %w", err))
	}
	raw, err := os.Open(stagedName)
	if err != nil {
		return failDatabaseJob(job, err)
	}
	defer func() { _ = raw.Close() }()
	buffered := bufio.NewReader(raw)
	var input io.Reader = buffered
	if magic, _ := buffered.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(buffered)
		if err != nil {
			return failDatabaseJob(job, fmt.Errorf("agentdocker: open gzip import: %w", err))
		}
		defer func() { _ = gz.Close() }()
		input = gz
	}
	if mode == databases.ImportReplace {
		if req.PreImportBackup {
			if _, err := d.createBackupLocked(ctx, serviceID, engine, database, model.BackupPreImport); err != nil {
				return failDatabaseJob(job, err)
			}
		}
		if err := d.terminateDatabaseSessions(ctx, serviceID, engine, database); err != nil {
			return failDatabaseJob(job, err)
		}
		command, err := databaseSQLCommand(engine.ID())
		if err != nil {
			return failDatabaseJob(job, err)
		}
		dropSQL, err := engine.DropDatabaseSQL(database)
		if err != nil {
			return failDatabaseJob(job, mapEngineError(err))
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(dropSQL)); err != nil {
			return failDatabaseJob(job, err)
		}
		createSQL, err := engine.CreateDatabaseSQL(database, "", "")
		if err != nil {
			return failDatabaseJob(job, mapEngineError(err))
		}
		if _, err := d.runDatabaseSQL(ctx, serviceID, command, []byte(createSQL)); err != nil {
			return failDatabaseJob(job, err)
		}
	}
	restoreCommand, err := engine.RestoreCommand(database)
	if err != nil {
		return failDatabaseJob(job, mapEngineError(err))
	}
	if err := d.runDatabaseInput(ctx, serviceID, restoreCommand, input); err != nil {
		return failDatabaseJob(job, err)
	}
	job.Status = model.JobSucceeded
	job.Progress = 100
	job.UpdatedAt = time.Now().UTC()
	return job, nil
}

// ExportDatabase streams a dump directly to the caller.
func (d *Docker) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	engine, _, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() == model.DatabaseRedis {
		return nil, databases.ErrUnsupported
	}
	if err := engine.ValidateDatabaseName(database); err != nil {
		return nil, mapEngineError(err)
	}
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return nil, err
	}
	command, err := engine.DumpCommand(database)
	if err != nil {
		return nil, mapEngineError(err)
	}
	return d.runDatabaseStream(ctx, serviceID, command)
}

func (d *Docker) databaseExists(ctx context.Context, serviceID, database string) (bool, error) {
	list, err := d.ListDatabases(ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, candidate := range list {
		if candidate.Name == database {
			return true, nil
		}
	}
	return false, nil
}

func newDatabaseJob(serviceID, database string, kind model.DatabaseJobKind) model.DatabaseJob {
	now := time.Now().UTC()
	return model.DatabaseJob{
		ID:        newDatabaseJobID(),
		ServiceID: serviceID,
		Database:  database,
		Kind:      kind,
		Status:    model.JobRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func failDatabaseJob(job model.DatabaseJob, err error) (model.DatabaseJob, error) {
	job.Status = model.JobFailed
	job.Message = err.Error()
	job.UpdatedAt = time.Now().UTC()
	return job, err
}
