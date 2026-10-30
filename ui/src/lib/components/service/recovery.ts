import type { DatabaseJob } from '../../databaseTypes';

/**
 * Builds a safe new-database target for recovering a pre-operation backup.
 * Keeps the generated name within the 63-character identifier limit.
 */
export function recoveryDatabaseName(database: string, now = new Date()): string {
  const stamp = now.toISOString().replace(/[-:T]/g, '').slice(0, 14);
  const suffix = `_recovery_${stamp}`;
  const maxBase = Math.max(1, 63 - suffix.length);
  return `${database.slice(0, maxBase)}${suffix}`;
}

/** A failed/canceled restore or import with a pre-operation backup to recover. */
export function isRecoveryJob(job: DatabaseJob): boolean {
  return (
    (job.status === 'failed' || job.status === 'canceled') &&
    Boolean(job.backup_id) &&
    (job.kind === 'restore' || job.kind === 'import')
  );
}

/** Builds the default new-database target for a restore, within the identifier limit. */
export function restoreDatabaseName(database: string, now = new Date()): string {
  const stamp = now.toISOString().replace(/[-:T]/g, '').slice(0, 14);
  const suffix = `_restore_${stamp}`;
  const maxBase = Math.max(1, 63 - suffix.length);
  return `${database.slice(0, maxBase)}${suffix}`;
}
