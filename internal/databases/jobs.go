package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// CreateBackup starts a logical backup in the background and returns the queued
// job immediately.
func (m *Manager) CreateBackup(ctx context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error) {
	serviceID = canonicalServiceID(serviceID)
	database = strings.TrimSpace(database)
	if !isSQLService(serviceID) {
		return model.DatabaseJob{}, fmt.Errorf("%w: service %q has no SQL backups", ErrUnsupported, serviceID)
	}
	if database == "" {
		return model.DatabaseJob{}, fmt.Errorf("%w: database is required", ErrInvalidRequest)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	if req.Kind == "" {
		req.Kind = model.BackupManual
	}
	record, err := m.newJobRecord(serviceID, database, model.JobBackup)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	return m.startJob(record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		unlock := m.lockService(serviceID)
		defer unlock()
		return m.deps.Platform.CreateBackup(jobCtx, serviceID, database, req)
	}, req.Kind), nil
}

// RestoreBackup validates the request synchronously and runs the restore in the
// background. Overwrite validation and the pre-restore backup happen under the
// service lock in the job goroutine.
func (m *Manager) RestoreBackup(ctx context.Context, serviceID, backupID string, req RestoreBackupRequest) (model.DatabaseJob, error) {
	serviceID = canonicalServiceID(serviceID)
	if !isSQLService(serviceID) {
		return model.DatabaseJob{}, fmt.Errorf("%w: service %q has no SQL restore", ErrUnsupported, serviceID)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	backup, err := m.backupRecord(ctx, serviceID, backupID)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = fmt.Sprintf("%s_restore_%s", backup.DatabaseName, m.now().Format("20060102-150405"))
	}
	req.Target = target
	if protectedDatabase(target) {
		return model.DatabaseJob{}, fmt.Errorf("%w: database %q is protected", ErrProtected, target)
	}
	if req.Overwrite {
		if strings.TrimSpace(req.Confirm) != target {
			return model.DatabaseJob{}, fmt.Errorf("%w: type %q to confirm the overwrite", ErrConflict, target)
		}
		if err := m.ensureLinkedSitesStopped(ctx, serviceID, target); err != nil {
			return model.DatabaseJob{}, err
		}
	}
	record, err := m.newJobRecord(serviceID, target, model.JobRestore)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	return m.startJob(record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		unlock := m.lockService(serviceID)
		defer unlock()

		targetExists, err := m.databaseExists(jobCtx, serviceID, target)
		if err != nil {
			return model.DatabaseJob{}, fmt.Errorf("check restore target %q: %w", target, err)
		}
		if req.Overwrite {
			if !targetExists {
				return model.DatabaseJob{}, fmt.Errorf("%w: restore target %q", ErrNotFound, target)
			}
			if err := m.ensureLinkedSitesStopped(jobCtx, serviceID, target); err != nil {
				return model.DatabaseJob{}, err
			}
			req.PreRestoreBackup = true
		}
		preBackupID := ""
		if req.PreRestoreBackup && targetExists {
			preJob, err := m.createBackupLocked(jobCtx, serviceID, target, CreateBackupRequest{Kind: model.BackupPreRestore})
			if err != nil {
				return model.DatabaseJob{}, fmt.Errorf("pre-restore backup: %w", err)
			}
			if preJob.Status != model.JobSucceeded || preJob.BackupID == "" {
				return model.DatabaseJob{}, fmt.Errorf("pre-restore backup did not succeed: %s", preJob.Message)
			}
			preBackupID = preJob.BackupID
		}
		req.PreRestoreBackup = false
		result, err := m.deps.Platform.RestoreBackup(jobCtx, serviceID, backupID, req)
		if result.BackupID == "" {
			result.BackupID = preBackupID
		}
		return result, err
	}, model.BackupPreRestore), nil
}

// ImportDatabase stages the upload synchronously and imports it in the
// background. Replace validation and the pre-import backup happen under the
// service lock in the job goroutine.
func (m *Manager) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req ImportRequest) (model.DatabaseJob, error) {
	serviceID = canonicalServiceID(serviceID)
	database = strings.TrimSpace(database)
	if !isSQLService(serviceID) {
		return model.DatabaseJob{}, fmt.Errorf("%w: service %q has no SQL import", ErrUnsupported, serviceID)
	}
	if database == "" {
		return model.DatabaseJob{}, fmt.Errorf("%w: database is required", ErrInvalidRequest)
	}
	if protectedDatabase(database) {
		return model.DatabaseJob{}, fmt.Errorf("%w: database %q is protected", ErrProtected, database)
	}
	if !validImportFilename(filename) {
		return model.DatabaseJob{}, fmt.Errorf("%w: import file must be .sql or .sql.gz", ErrInvalidRequest)
	}
	if req.Mode == "" {
		req.Mode = ImportMerge
	}
	if req.Mode != ImportMerge && req.Mode != ImportReplace {
		return model.DatabaseJob{}, fmt.Errorf("%w: import mode %q", ErrInvalidRequest, req.Mode)
	}
	if _, err := m.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseJob{}, err
	}
	if req.Mode == ImportReplace {
		if strings.TrimSpace(req.Confirm) != database {
			return model.DatabaseJob{}, fmt.Errorf("%w: type %q to confirm replacing the database contents", ErrConflict, database)
		}
		if err := m.ensureLinkedSitesStopped(ctx, serviceID, database); err != nil {
			return model.DatabaseJob{}, err
		}
	}
	stagedPath, err := m.stageImport(file)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	record, err := m.newJobRecord(serviceID, database, model.JobImport)
	if err != nil {
		_ = os.Remove(stagedPath)
		return model.DatabaseJob{}, err
	}
	return m.startJob(record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		defer func() { _ = os.Remove(stagedPath) }()
		unlock := m.lockService(serviceID)
		defer unlock()

		if req.Mode == ImportReplace {
			if err := m.ensureLinkedSitesStopped(jobCtx, serviceID, database); err != nil {
				return model.DatabaseJob{}, err
			}
		}
		preBackupID := ""
		if req.PreImportBackup {
			preJob, err := m.createBackupLocked(jobCtx, serviceID, database, CreateBackupRequest{Kind: model.BackupPreImport})
			if err != nil {
				return model.DatabaseJob{}, fmt.Errorf("pre-import backup: %w", err)
			}
			if preJob.Status != model.JobSucceeded || preJob.BackupID == "" {
				return model.DatabaseJob{}, fmt.Errorf("pre-import backup did not succeed: %s", preJob.Message)
			}
			preBackupID = preJob.BackupID
		}
		req.PreImportBackup = false
		staged, err := os.Open(stagedPath)
		if err != nil {
			return model.DatabaseJob{}, fmt.Errorf("open staged import: %w", err)
		}
		defer func() { _ = staged.Close() }()
		result, err := m.deps.Platform.ImportDatabase(jobCtx, serviceID, database, staged, filename, req)
		if result.BackupID == "" {
			result.BackupID = preBackupID
		}
		return result, err
	}, model.BackupPreImport), nil
}

// validImportFilename accepts the SQL dump formats the platform can restore.
func validImportFilename(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sql.gz")
}

// stageImport copies an upload into a private temporary file so the background
// job owns the reader after the HTTP request returns.
func (m *Manager) stageImport(file io.Reader) (string, error) {
	if file == nil {
		return "", fmt.Errorf("%w: import file is required", ErrInvalidRequest)
	}
	staged, err := os.CreateTemp(m.stagingDir, "devstack-import-*")
	if err != nil {
		return "", fmt.Errorf("stage import: %w", err)
	}
	path := staged.Name()
	cleanup := func() {
		_ = staged.Close()
		_ = os.Remove(path)
	}
	if err := staged.Chmod(0o600); err != nil {
		cleanup()
		return "", fmt.Errorf("stage import permissions: %w", err)
	}
	if _, err := io.Copy(staged, file); err != nil {
		cleanup()
		return "", fmt.Errorf("stage import: %w", err)
	}
	if err := staged.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("stage import: %w", err)
	}
	return path, nil
}

// createBackupLocked runs a synchronous pre-operation backup while the caller
// holds the service lock.
func (m *Manager) createBackupLocked(ctx context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error) {
	if req.Kind == "" {
		req.Kind = model.BackupManual
	}
	job, err := m.deps.Platform.CreateBackup(ctx, serviceID, database, req)
	if err != nil {
		return job, err
	}
	if job.Status == "" || job.Status == model.JobQueued || job.Status == model.JobRunning {
		job.Status = model.JobSucceeded
	}
	if job.Status != model.JobSucceeded {
		return job, fmt.Errorf("pre-operation backup %s", job.Status)
	}
	if job.BackupID != "" {
		m.recordBackupMetadata(ctx, serviceID, database, job.BackupID, req.Kind)
	}
	m.audit(ctx, "database.backup.created", serviceID, "", "database", database, "ok", map[string]any{"backup_id": job.BackupID, "kind": string(req.Kind)})
	return job, nil
}

// newJobRecord creates a queued job row.
func (m *Manager) newJobRecord(serviceID, database string, kind model.DatabaseJobKind) (model.DatabaseJobRecord, error) {
	now := m.now().Unix()
	record := model.DatabaseJobRecord{
		ID:           m.newID("job"),
		ServiceID:    serviceID,
		DatabaseName: database,
		Kind:         string(kind),
		Status:       string(model.JobQueued),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := m.deps.Store.CreateDatabaseJob(context.Background(), record); err != nil {
		return model.DatabaseJobRecord{}, err
	}
	return record, nil
}

// startJob registers cancellation, launches the worker, and returns the queued
// job immediately.
func (m *Manager) startJob(record model.DatabaseJobRecord, fn func(context.Context) (model.DatabaseJob, error), backupKind model.DatabaseBackupKind) model.DatabaseJob {
	jobCtx, cancel := context.WithCancel(context.Background())
	m.jobsMu.Lock()
	m.cancels[record.ID] = cancel
	m.jobsMu.Unlock()
	go m.runJob(jobCtx, cancel, record, fn, backupKind)
	return recordToJob(record)
}

// runJob owns the queued/running/terminal lifecycle of one background job.
func (m *Manager) runJob(jobCtx context.Context, cancel context.CancelFunc, record model.DatabaseJobRecord, fn func(context.Context) (model.DatabaseJob, error), backupKind model.DatabaseBackupKind) {
	defer func() {
		cancel()
		m.jobsMu.Lock()
		delete(m.cancels, record.ID)
		delete(m.platformJobs, record.ID)
		m.jobsMu.Unlock()
	}()

	record.Status = string(model.JobRunning)
	record.UpdatedAt = m.now().Unix()
	if err := m.deps.Store.UpdateDatabaseJob(context.Background(), record); err != nil {
		return
	}
	job := recordToJob(record)
	m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s running", job.ID))

	result, err := fn(jobCtx)
	if result.ID != "" {
		m.jobsMu.Lock()
		m.platformJobs[record.ID] = result.ID
		m.jobsMu.Unlock()
	}
	if result.Status != "" {
		job.Status = result.Status
	}
	if result.Progress > 0 {
		job.Progress = result.Progress
	}
	if result.Message != "" {
		job.Message = RedactString(result.Message)
	}
	if result.BackupID != "" {
		job.BackupID = result.BackupID
	}

	canceled := errors.Is(jobCtx.Err(), context.Canceled)
	switch {
	case canceled:
		job.Status = model.JobCanceled
	case err != nil || job.Status == model.JobFailed:
		job.Status = model.JobFailed
		if job.Message == "" {
			job.Message = safeErrorMessage(err)
		}
	case job.Status == model.JobRunning || job.Status == model.JobQueued:
		// The platform is still working; leave the daemon job running.
		job.UpdatedAt = m.now()
		record = jobToRecord(job)
		_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
		m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s running", job.ID))
		return
	default:
		job.Status = model.JobSucceeded
		job.Progress = 100
	}

	job.UpdatedAt = m.now()
	record = jobToRecord(job)
	_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
	if job.Status == model.JobSucceeded && job.BackupID != "" {
		m.recordBackupMetadata(context.Background(), record.ServiceID, record.DatabaseName, job.BackupID, backupKind)
	}
	m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s %s", job.ID, job.Status))
	m.emitJobEvent(context.Background(), job)
	m.auditJob(context.Background(), job, string(job.Status))
}

// emitJobEvent publishes the domain-specific SSE name in addition to the
// generic job update.
func (m *Manager) emitJobEvent(ctx context.Context, job model.DatabaseJob) {
	switch job.Kind {
	case model.JobBackup:
		m.emit(ctx, "", "database.backup.created", fmt.Sprintf("database backup %s for %s on %s", job.BackupID, job.Database, job.ServiceID))
	case model.JobRestore:
		m.emit(ctx, "", "database.backup.restored", fmt.Sprintf("database %s restored on %s", job.Database, job.ServiceID))
	case model.JobImport:
		m.emit(ctx, "", "database.import.completed", fmt.Sprintf("database %s import completed on %s", job.Database, job.ServiceID))
	}
}

// auditJob writes the durable audit row for one terminal job state.
func (m *Manager) auditJob(ctx context.Context, job model.DatabaseJob, result string) {
	action := "database.job.updated"
	switch job.Kind {
	case model.JobBackup:
		action = "database.backup.created"
	case model.JobRestore:
		action = "database.backup.restored"
	case model.JobImport:
		action = "database.import.completed"
	}
	detail := map[string]any{"job_id": job.ID, "kind": string(job.Kind), "status": string(job.Status)}
	if job.BackupID != "" {
		detail["backup_id"] = job.BackupID
	}
	m.audit(ctx, action, job.ServiceID, "", "database", job.Database, result, detail)
}

// ReconcileJobs fails queued/running jobs left over from a previous daemon
// process. Their platform job IDs lived only in memory, so the daemon cannot
// prove they are still running; failing them keeps the UI from showing a job
// that will never finish. Terminal jobs are untouched.
func (m *Manager) ReconcileJobs(ctx context.Context) error {
	jobs, err := m.deps.Store.ListAllDatabaseJobs(ctx)
	if err != nil {
		return err
	}
	for _, record := range jobs {
		if record.Status != string(model.JobQueued) && record.Status != string(model.JobRunning) {
			continue
		}
		record.Status = string(model.JobFailed)
		record.Message = "daemon restarted before the database job completed"
		record.UpdatedAt = m.now().Unix()
		if err := m.deps.Store.UpdateDatabaseJob(ctx, record); err != nil {
			return err
		}
		m.audit(ctx, "database.job.updated", record.ServiceID, "", "database", record.DatabaseName, "failed", map[string]any{"job_id": record.ID, "reason": "daemon_restart"})
		m.emit(ctx, "", "database.job.updated", fmt.Sprintf("database job %s failed after daemon restart", record.ID))
	}
	return nil
}

// recordBackupMetadata persists the platform's backup view. When the platform
// does not return metadata for a just-created backup, a minimal ready record is
// stored so the backup remains addressable.
func (m *Manager) recordBackupMetadata(ctx context.Context, serviceID, database, backupID string, kind model.DatabaseBackupKind) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	backups, err := m.deps.Platform.ListBackups(ctx, serviceID)
	if err == nil {
		for _, backup := range backups {
			if backup.ID != backupID {
				continue
			}
			record := backupFromView(backup)
			existing, getErr := m.deps.Store.GetDatabaseBackup(ctx, backupID)
			switch {
			case errors.Is(getErr, store.ErrNotFound):
				if record.Kind == "" {
					record.Kind = string(kind)
				}
				_ = m.deps.Store.CreateDatabaseBackup(ctx, record)
			case getErr == nil:
				if record.Path == "" {
					record.Path = existing.Path
				}
				if record.Kind == "" {
					record.Kind = existing.Kind
				}
				if record.CreatedAt == 0 {
					record.CreatedAt = existing.CreatedAt
				}
				_ = m.deps.Store.UpdateDatabaseBackup(ctx, record)
			}
			return
		}
	}
	record := model.DatabaseBackupRecord{
		ID:           backupID,
		ServiceID:    serviceID,
		Engine:       engineForService(serviceID),
		DatabaseName: database,
		Kind:         string(kind),
		Status:       string(model.BackupReady),
		CreatedAt:    m.now().Unix(),
	}
	if _, getErr := m.deps.Store.GetDatabaseBackup(ctx, backupID); errors.Is(getErr, store.ErrNotFound) {
		_ = m.deps.Store.CreateDatabaseBackup(ctx, record)
	}
}

// backupRecord resolves a backup from metadata, falling back to the platform
// inventory for backups created outside this daemon process.
func (m *Manager) backupRecord(ctx context.Context, serviceID, backupID string) (model.DatabaseBackupRecord, error) {
	serviceID = canonicalServiceID(serviceID)
	record, err := m.deps.Store.GetDatabaseBackup(ctx, backupID)
	if err == nil {
		if record.ServiceID != "" && canonicalServiceID(record.ServiceID) != serviceID {
			return model.DatabaseBackupRecord{}, fmt.Errorf("%w: backup %q", ErrNotFound, backupID)
		}
		return record, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.DatabaseBackupRecord{}, err
	}
	backups, listErr := m.deps.Platform.ListBackups(ctx, serviceID)
	if listErr != nil {
		return model.DatabaseBackupRecord{}, listErr
	}
	for _, backup := range backups {
		if backup.ID != backupID {
			continue
		}
		if backup.ServiceID != "" && canonicalServiceID(backup.ServiceID) != serviceID {
			continue
		}
		record = backupFromView(backup)
		_ = m.deps.Store.CreateDatabaseBackup(ctx, record)
		return record, nil
	}
	return model.DatabaseBackupRecord{}, fmt.Errorf("%w: backup %q", ErrNotFound, backupID)
}
