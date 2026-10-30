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

/** Builds the default new-database target for a restore, within the identifier limit. */
export function restoreDatabaseName(database: string, now = new Date()): string {
  const stamp = now.toISOString().replace(/[-:T]/g, '').slice(0, 14);
  const suffix = `_restore_${stamp}`;
  const maxBase = Math.max(1, 63 - suffix.length);
  return `${database.slice(0, maxBase)}${suffix}`;
}
