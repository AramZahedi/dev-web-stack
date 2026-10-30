package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// CreateBackup starts a logical backup and records its job and metadata.
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
	unlock := m.lockService(serviceID)
	defer unlock()

	if req.Kind == "" {
		req.Kind = model.BackupManual
	}
	record := m.newJobRecord(serviceID, database, model.JobBackup)
	return m.executeJob(ctx, record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		return m.deps.Platform.CreateBackup(jobCtx, serviceID, database, req)
	}, req.Kind)
}

// RestoreBackup restores a backup to a new or existing target. An overwrite
// requires typed confirmation and a stopped-site check. A pre-restore backup
// is created first when requested.
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
	targetExists, err := m.databaseExists(ctx, serviceID, target)
	if err != nil {
		return model.DatabaseJob{}, fmt.Errorf("check restore target %q: %w", target, err)
	}
	if req.Overwrite {
		if strings.TrimSpace(req.Confirm) != target {
			return model.DatabaseJob{}, fmt.Errorf("%w: type %q to confirm the overwrite", ErrConflict, target)
		}
		if !targetExists {
			return model.DatabaseJob{}, fmt.Errorf("%w: restore target %q", ErrNotFound, target)
		}
		if err := m.ensureLinkedSitesStopped(ctx, serviceID, target); err != nil {
			return model.DatabaseJob{}, err
		}
		// An overwrite always needs a recovery point, even when the caller
		// omitted the flag.
		req.PreRestoreBackup = true
	}
	preBackupID := ""
	if req.PreRestoreBackup && targetExists {
		preJob, err := m.CreateBackup(ctx, serviceID, target, CreateBackupRequest{Kind: model.BackupPreRestore})
		if err != nil {
			return m.failedJobRecord(serviceID, target, model.JobRestore, "", fmt.Errorf("pre-restore backup: %w", err))
		}
		if preJob.Status != model.JobSucceeded || preJob.BackupID == "" {
			return m.failedJobRecord(serviceID, target, model.JobRestore, "", fmt.Errorf("pre-restore backup did not succeed: %s", preJob.Message))
		}
		preBackupID = preJob.BackupID
	}
	// The manager owns the pre-operation backup. Clear the platform flag so the
	// adapter does not create a second backup.
	req.PreRestoreBackup = false
	unlock := m.lockService(serviceID)
	defer unlock()

	record := m.newJobRecord(serviceID, target, model.JobRestore)
	record.BackupID = preBackupID
	return m.executeJob(ctx, record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		return m.deps.Platform.RestoreBackup(jobCtx, serviceID, backupID, req)
	}, model.BackupPreRestore)
}

// ImportDatabase imports SQL into an existing database. Replace mode requires
// typed confirmation and a stopped-site check; a pre-import backup is optional.
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
	preBackupID := ""
	if req.PreImportBackup {
		preJob, err := m.CreateBackup(ctx, serviceID, database, CreateBackupRequest{Kind: model.BackupPreImport})
		if err != nil {
			return m.failedJobRecord(serviceID, database, model.JobImport, "", fmt.Errorf("pre-import backup: %w", err))
		}
		if preJob.Status != model.JobSucceeded || preJob.BackupID == "" {
			return m.failedJobRecord(serviceID, database, model.JobImport, "", fmt.Errorf("pre-import backup did not succeed: %s", preJob.Message))
		}
		preBackupID = preJob.BackupID
	}
	// The manager owns the pre-import backup; never ask the platform for a
	// second one.
	req.PreImportBackup = false
	unlock := m.lockService(serviceID)
	defer unlock()

	record := m.newJobRecord(serviceID, database, model.JobImport)
	record.BackupID = preBackupID
	return m.executeJob(ctx, record, func(jobCtx context.Context) (model.DatabaseJob, error) {
		return m.deps.Platform.ImportDatabase(jobCtx, serviceID, database, file, filename, req)
	}, model.BackupPreImport)
}

// newJobRecord creates a queued job row.
func (m *Manager) newJobRecord(serviceID, database string, kind model.DatabaseJobKind) model.DatabaseJobRecord {
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
	_ = m.deps.Store.CreateDatabaseJob(context.Background(), record)
	return record
}

// failedJobRecord creates and persists a failed job without calling the
// platform. It is used when a pre-operation backup cannot be made.
func (m *Manager) failedJobRecord(serviceID, database string, kind model.DatabaseJobKind, backupID string, cause error) (model.DatabaseJob, error) {
	record := m.newJobRecord(serviceID, database, kind)
	record.Status = string(model.JobFailed)
	record.BackupID = backupID
	record.Message = safeErrorMessage(cause)
	record.UpdatedAt = m.now().Unix()
	_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
	job := recordToJob(record)
	m.auditJob(context.Background(), job, "failed")
	return job, cause
}

// executeJob runs one platform operation while tracking its queued/running/
// terminal states. Platform calls may themselves be asynchronous; a completed
// platform job is recorded as succeeded, a failed one as failed.
func (m *Manager) executeJob(ctx context.Context, record model.DatabaseJobRecord, fn func(context.Context) (model.DatabaseJob, error), backupKind model.DatabaseBackupKind) (model.DatabaseJob, error) {
	jobCtx, cancel := context.WithCancel(ctx)
	m.jobsMu.Lock()
	m.cancels[record.ID] = cancel
	m.jobsMu.Unlock()
	defer func() {
		cancel()
		m.jobsMu.Lock()
		delete(m.cancels, record.ID)
		m.jobsMu.Unlock()
	}()

	record.Status = string(model.JobRunning)
	record.UpdatedAt = m.now().Unix()
	if err := m.deps.Store.UpdateDatabaseJob(context.Background(), record); err != nil {
		return recordToJob(record), err
	}
	job := recordToJob(record)
	m.emit(ctx, "", "database.job.updated", fmt.Sprintf("database job %s running", job.ID))

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
	if err != nil || job.Status == model.JobFailed {
		if errors.Is(jobCtx.Err(), context.Canceled) {
			job.Status = model.JobCanceled
		} else {
			job.Status = model.JobFailed
			if job.Message == "" {
				job.Message = safeErrorMessage(err)
			}
		}
		job.UpdatedAt = m.now()
		record = jobToRecord(job)
		_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
		m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s %s", job.ID, job.Status))
		m.emitJobEvent(context.Background(), job)
		m.auditJob(context.Background(), job, string(job.Status))
		if err == nil {
			err = fmt.Errorf("%w: database job %s %s: %s", ErrConflict, job.ID, job.Status, job.Message)
		}
		return job, err
	}
	if job.Status == model.JobRunning || job.Status == model.JobQueued {
		// The platform started an asynchronous job. Keep the daemon job running;
		// GetJob refreshes it from the platform.
		job.UpdatedAt = m.now()
		record = jobToRecord(job)
		_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
		m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s running", job.ID))
		return job, nil
	}
	job.Status = model.JobSucceeded
	job.Progress = 100
	job.UpdatedAt = m.now()
	record = jobToRecord(job)
	_ = m.deps.Store.UpdateDatabaseJob(context.Background(), record)
	if job.BackupID != "" {
		m.recordBackupMetadata(ctx, record.ServiceID, record.DatabaseName, job.BackupID, backupKind)
	}
	m.emit(context.Background(), "", "database.job.updated", fmt.Sprintf("database job %s succeeded", job.ID))
	m.emitJobEvent(context.Background(), job)
	m.auditJob(context.Background(), job, "ok")
	return job, nil
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

// recordBackupMetadata persists the platform's backup view. When the platform
// does not return metadata for a just-created backup, a minimal ready record is
// stored so the backup remains addressable.
func (m *Manager) recordBackupMetadata(ctx context.Context, serviceID, database, backupID string, kind model.DatabaseBackupKind) {
	backups, err := m.deps.Platform.ListBackups(ctx, serviceID)
	if err == nil {
		for _, backup := range backups {
			if backup.ID != backupID {
				continue
			}
			record := backupFromView(backup)
			if _, getErr := m.deps.Store.GetDatabaseBackup(ctx, backupID); errors.Is(getErr, store.ErrNotFound) {
				_ = m.deps.Store.CreateDatabaseBackup(ctx, record)
			} else if getErr == nil {
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

// backupRecord resolves a backup from metadata, falling back to the platform
// inventory for backups created outside this daemon process.
func (m *Manager) backupRecord(ctx context.Context, serviceID, backupID string) (model.DatabaseBackupRecord, error) {
	record, err := m.deps.Store.GetDatabaseBackup(ctx, backupID)
	if err == nil {
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
		if backup.ID == backupID {
			record = backupFromView(backup)
			_ = m.deps.Store.CreateDatabaseBackup(ctx, record)
			return record, nil
		}
	}
	return model.DatabaseBackupRecord{}, fmt.Errorf("%w: backup %q", ErrNotFound, backupID)
}
