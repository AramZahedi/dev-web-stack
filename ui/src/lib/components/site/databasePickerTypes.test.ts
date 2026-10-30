import { describe, expect, it } from 'vitest';
import {
  isSecretEnvKey,
  sanitizeDatabaseUrl,
  withoutPassword,
} from './databasePickerTypes';

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

  it('only treats an @ in the authority as a credential separator', () => {
    expect(sanitizeDatabaseUrl('mysql://app:p@ss@127.0.0.1:3306/db@name')).toBe(
      'mysql://app@127.0.0.1:3306/db@name',
    );
    expect(sanitizeDatabaseUrl('mysql://app:secret@host/db?tag=@work')).toBe(
      'mysql://app@host/db?tag=@work',
    );
    expect(sanitizeDatabaseUrl('mysql://app:secret@host#frag@ment')).toBe(
      'mysql://app@host#frag@ment',
    );
  });

  it('handles scheme-less user:pass@host values defensively', () => {
    expect(sanitizeDatabaseUrl('app:secret@127.0.0.1:3306/app')).toBe(
      'app@127.0.0.1:3306/app',
    );
    expect(sanitizeDatabaseUrl('app:p@ss@db.internal/app')).toBe(
      'app@db.internal/app',
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

describe('isSecretEnvKey', () => {
  it('covers password variants, passphrases, passcodes and DSNs', () => {
    for (const key of [
      'DB_APP_PASSWORD',
      'DB_APP_PWD',
      'DB_APP_PASS',
      'DB_APP_PASSPHRASE',
      'DB_APP_PASSCODE',
      'DB_DSN',
      'DB_APP_DSN',
      'API_TOKEN',
    ]) {
      expect(isSecretEnvKey(key), key).toBe(true);
    }
  });

  it('does not treat connection metadata as a secret', () => {
    for (const key of ['DB_HOST', 'DB_DATABASE', 'DB_USERNAME', 'DB_PORT']) {
      expect(isSecretEnvKey(key), key).toBe(false);
    }
  });
});

describe('withoutPassword', () => {
  it('strips a caller-supplied password from a site database request', () => {
    expect(
      withoutPassword({
        service_id: 'mysql',
        database: 'app',
        user_id: 'usr_app',
        password: 's3cret',
        primary: true,
      }),
    ).toEqual({
      service_id: 'mysql',
      database: 'app',
      user_id: 'usr_app',
      primary: true,
    });
  });
});
