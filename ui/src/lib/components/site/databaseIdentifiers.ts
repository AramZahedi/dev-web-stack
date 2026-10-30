import type { Database, DatabaseUser } from '../../databaseTypes';

const PROTECTED_DATABASES = new Set([
  'mysql',
  'information_schema',
  'performance_schema',
  'sys',
  'postgres',
  'template0',
  'template1',
  'devstack',
]);

const PROTECTED_USERS = new Set([
  'root',
  'postgres',
  'mysql.sys',
  'mysql.session',
  'mysql.infoschema',
  'default',
  'devstack',
]);

export function isDatabaseIdentifier(value: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(value);
}

export function isProtectedDatabase(name: string): boolean {
  return PROTECTED_DATABASES.has(name.toLowerCase());
}

export function isProtectedUser(username: string): boolean {
  return PROTECTED_USERS.has(username.toLowerCase());
}

export function isSelectableDatabase(database: Database): boolean {
  return !database.missing && !isProtectedDatabase(database.name);
}

export function isSelectableUser(user: DatabaseUser): boolean {
  return user.enabled !== false && !isProtectedUser(user.username);
}
