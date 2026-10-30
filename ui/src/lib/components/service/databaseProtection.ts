import type { DatabaseEngine } from '../../databaseTypes';

/**
 * Engine system objects that must never be dropped, renamed, imported into, or
 * overwritten by a restore. The names are shared across engines because the
 * dashboard cannot know which engine a name belongs to until it is resolved.
 */
export const PROTECTED_DATABASES = new Set([
  'devstack',
  'postgres',
  'template0',
  'template1',
  'mysql',
  'information_schema',
  'performance_schema',
  'sys',
]);

export function isProtectedDatabase(_engine: DatabaseEngine, name: string): boolean {
  return PROTECTED_DATABASES.has(name.trim().toLowerCase());
}
