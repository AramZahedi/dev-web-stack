import type { GrantPreset } from '../../databaseTypes';

/** Display data derived from the picker selection for review and dialogs. */
export interface DatabasePickerSummary {
  serviceID: string;
  serviceName: string;
  engine: string;
  database: string;
  username: string;
  grant: GrantPreset | '';
  grantLabel: string;
  host: string;
  port: number;
  passwordManaged: boolean;
  hasAccess: boolean;
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

/** Removes a password embedded in a database URL before it reaches the DOM. */
export function sanitizeDatabaseUrl(value: string): string {
  const scheme = value.indexOf('://');
  if (scheme < 0) return value;
  const rest = value.slice(scheme + 3);
  const at = rest.indexOf('@');
  if (at < 0) return value;
  const authority = rest.slice(0, at);
  const colon = authority.indexOf(':');
  if (colon < 0) return value;
  return `${value.slice(0, scheme + 3)}${authority.slice(0, colon)}${rest.slice(at)}`;
}
