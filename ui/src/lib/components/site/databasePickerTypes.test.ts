import { describe, expect, it } from 'vitest';
import { sanitizeDatabaseUrl } from './databasePickerTypes';

describe('sanitizeDatabaseUrl', () => {
  it('removes a password that contains an @ from the URL', () => {
    expect(sanitizeDatabaseUrl('mysql://app:p@ss@127.0.0.1:3306/app')).toBe(
      'mysql://app@127.0.0.1:3306/app',
    );
  });

  it('removes a simple password from the URL', () => {
    expect(sanitizeDatabaseUrl('postgres://app:secret@127.0.0.1:5432/app')).toBe(
      'postgres://app@127.0.0.1:5432/app',
    );
  });

  it('leaves URLs without credentials and non-URLs unchanged', () => {
    expect(sanitizeDatabaseUrl('mysql://app@127.0.0.1:3306/app')).toBe(
      'mysql://app@127.0.0.1:3306/app',
    );
    expect(sanitizeDatabaseUrl('not a url')).toBe('not a url');
    expect(sanitizeDatabaseUrl('')).toBe('');
  });
});
