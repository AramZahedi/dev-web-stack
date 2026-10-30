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
