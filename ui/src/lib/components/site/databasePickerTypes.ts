import type { CreateSiteDatabaseRequest, GrantPreset } from '../../databaseTypes';

/** Display data derived from the picker selection for review and dialogs. */
export interface DatabasePickerSummary {
  serviceName: string;
  database: string;
  username: string;
  grantLabel: string;
  host: string;
  port: number;
  passwordManaged: boolean;
  userManaged: boolean;
}

const GRANT_LABELS: Record<GrantPreset, string> = {
  read_only: 'Read-only',
  read_write: 'Read/write',
  read_write_schema: 'Read/write + schema',
  admin: 'Admin (database scope)',
  custom: 'Custom',
};

export function grantLabel(preset: GrantPreset | ''): string {
  if (!preset) return 'No access yet';
  return GRANT_LABELS[preset] ?? preset;
}

/** Removes a caller-supplied password; v1 never stores external passwords. */
export function withoutPassword(
  request: CreateSiteDatabaseRequest,
): CreateSiteDatabaseRequest {
  const { password: _password, ...rest } = request;
  return rest;
}

/** Removes a password embedded in a database URL before it reaches the DOM. */
export function sanitizeDatabaseUrl(value: string): string {
  const scheme = value.indexOf('://');
  const authorityStart = scheme >= 0 ? scheme + 3 : 0;
  const rest = value.slice(authorityStart);
  // The authority ends at the first path, query, or fragment delimiter. Only
  // that slice may contain credential separators; an @ later in the path is
  // part of the path, not the user info.
  const authorityEnd = rest.search(/[/?#]/);
  const authority = authorityEnd >= 0 ? rest.slice(0, authorityEnd) : rest;
  const tail = authorityEnd >= 0 ? rest.slice(authorityEnd) : '';
  // The authority may contain an unescaped @ inside the password. Use the last
  // @ so user:p@ss@host is treated as user + password p@ss + host.
  const at = authority.lastIndexOf('@');
  if (at < 0) return value;
  const userInfo = authority.slice(0, at);
  const host = authority.slice(at + 1);
  const colon = userInfo.indexOf(':');
  if (colon < 0) return value;
  // Scheme-less values are only rewritten when the host looks like a host.
  if (scheme < 0 && (!host || /\s/.test(host))) return value;
  const prefix = scheme >= 0 ? value.slice(0, authorityStart) : '';
  return `${prefix}${userInfo.slice(0, colon)}@${host}${tail}`;
}

/** True when an environment key is likely to carry a secret value. */
export function isSecretEnvKey(key: string): boolean {
  return (
    /(key|token|secret|password|passwd|credential|auth)/i.test(key) ||
    /(^|_)(pwd|pass)(word|phrase|code)?(_|$)/i.test(key) ||
    /(^|_)dsn(_|$)/i.test(key)
  );
}

/** True when an environment key carries a URL that may embed credentials. */
export function isDatabaseUrlEnvKey(key: string): boolean {
  return key === 'URL' || key.endsWith('_URL');
}

/**
 * True when a database link owns the environment key at runtime. Link values
 * are layered over the persisted site env, so a user-set DB_* or DATABASE_URL
 * value is silently replaced unless the dashboard warns about it.
 */
export function isDatabaseLinkEnvKey(key: string): boolean {
  return key === 'DATABASE_URL' || /^DB_/i.test(key);
}
