import { describe, expect, it } from 'vitest';
import {
  isDatabaseIdentifier,
  isProtectedDatabase,
  isProtectedUser,
  isSelectableDatabase,
  isSelectableUser,
} from './databaseIdentifiers';
import type { Database, DatabaseUser } from '../../databaseTypes';

describe('database identifiers', () => {
  it('accepts safe engine identifiers and rejects unsafe ones', () => {
    expect(isDatabaseIdentifier('app_1')).toBe(true);
    expect(isDatabaseIdentifier('_private')).toBe(true);
    expect(isDatabaseIdentifier('1app')).toBe(false);
    expect(isDatabaseIdentifier('app-db')).toBe(false);
    expect(isDatabaseIdentifier('app db')).toBe(false);
    expect(isDatabaseIdentifier('app;drop')).toBe(false);
    expect(isDatabaseIdentifier('a'.repeat(64))).toBe(false);
  });

  it('protects system databases and users', () => {
    expect(isProtectedDatabase('devstack')).toBe(true);
    expect(isProtectedDatabase('information_schema')).toBe(true);
    expect(isProtectedDatabase('app')).toBe(false);
    expect(isProtectedUser('root')).toBe(true);
    expect(isProtectedUser('mysql.sys')).toBe(true);
    expect(isProtectedUser('app_user')).toBe(false);
  });

  it('filters missing databases and disabled users', () => {
    const missing: Database = {
      name: 'missing',
      service_id: 'mysql',
      engine: 'mysql',
      size_bytes: 0,
      table_count: 0,
      managed: false,
      missing: true,
    };
    expect(isSelectableDatabase(missing)).toBe(false);
    const disabled: DatabaseUser = {
      id: 'usr_disabled',
      service_id: 'mysql',
      engine: 'mysql',
      username: 'disabled',
      enabled: false,
      managed: false,
      password_managed: false,
    };
    expect(isSelectableUser(disabled)).toBe(false);
  });
});
