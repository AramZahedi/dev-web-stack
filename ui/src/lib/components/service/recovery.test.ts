import { describe, expect, it } from 'vitest';
import { recoveryDatabaseName, restoreDatabaseName } from './recovery';

describe('database name generators', () => {
  it('keeps a new-restore name within the identifier limit', () => {
    const name = restoreDatabaseName('a'.repeat(63));
    expect(name.length).toBeLessThanOrEqual(63);
    expect(name).toMatch(/^a+_restore_\d{14}$/);
  });

  it('keeps a recovery name within the identifier limit', () => {
    const name = recoveryDatabaseName('a'.repeat(63));
    expect(name.length).toBeLessThanOrEqual(63);
    expect(name).toMatch(/^a+_recovery_\d{14}$/);
  });

  it('leaves short names intact apart from the suffix', () => {
    const now = new Date('2026-10-31T12:34:56Z');
    expect(restoreDatabaseName('app', now)).toBe('app_restore_20261031123456');
    expect(recoveryDatabaseName('app', now)).toBe('app_recovery_20261031123456');
  });
});
