import type {
  DatabaseEngine,
  DatabaseGrant,
  GrantPreset,
} from '../../databaseTypes';

export interface UserFormValue {
  username: string;
  host: string;
  enabled: boolean;
  accessLevel: string;
  grants: DatabaseGrant[];
}

export const GRANT_PRESETS: { value: GrantPreset; label: string; summary: string }[] = [
  { value: 'read_only', label: 'Read-only', summary: 'read-only' },
  { value: 'read_write', label: 'Read/write', summary: 'read/write' },
  { value: 'read_write_schema', label: 'Read/write + schema', summary: 'read/write + schema' },
  { value: 'admin', label: 'Admin (database-scoped)', summary: 'admin' },
  { value: 'custom', label: 'Custom privileges', summary: 'custom' },
];

const PRESET_SUMMARY = new Map(
  GRANT_PRESETS.map((preset) => [preset.value, preset.summary]),
);

export function grantSummary(grants: DatabaseGrant[] | undefined): string {
  if (!grants || grants.length === 0) return 'No grants';
  return grants
    .map((grant) => `${PRESET_SUMMARY.get(grant.preset) ?? grant.preset} on ${grant.database}`)
    .join(', ');
}

export function isProtectedUsername(engine: DatabaseEngine, username: string): boolean {
  const name = username.toLowerCase();
  if (engine === 'redis') return name === 'default';
  if (engine === 'postgres') return name === 'postgres' || name === 'devstack';
  return (
    name === 'root' ||
    name === 'devstack' ||
    name === 'mysql.sys' ||
    name === 'mysql.session' ||
    name === 'mysql.infoschema'
  );
}

export const CUSTOM_PRIVILEGES: Record<DatabaseEngine, string[]> = {
  mysql: [
    'SELECT',
    'INSERT',
    'UPDATE',
    'DELETE',
    'CREATE',
    'ALTER',
    'DROP',
    'INDEX',
    'REFERENCES',
    'CREATE TEMPORARY TABLES',
    'LOCK TABLES',
    'EXECUTE',
    'CREATE VIEW',
    'SHOW VIEW',
    'TRIGGER',
  ],
  mariadb: [
    'SELECT',
    'INSERT',
    'UPDATE',
    'DELETE',
    'CREATE',
    'ALTER',
    'DROP',
    'INDEX',
    'REFERENCES',
    'CREATE TEMPORARY TABLES',
    'LOCK TABLES',
    'EXECUTE',
    'CREATE VIEW',
    'SHOW VIEW',
    'TRIGGER',
  ],
  postgres: [
    'SELECT',
    'INSERT',
    'UPDATE',
    'DELETE',
    'TRUNCATE',
    'REFERENCES',
    'TRIGGER',
    'CREATE',
    'CONNECT',
    'TEMPORARY',
    'EXECUTE',
    'USAGE',
  ],
  redis: [],
};

export function accessLevelLabel(level: string | undefined): string {
  return level === 'full' ? 'full' : 'read-only';
}

export function accessLevelDescription(level: string | undefined): string {
  return level === 'full'
    ? 'All data commands; admin and dangerous commands stay blocked.'
    : 'Read commands only; admin and dangerous commands stay blocked.';
}
