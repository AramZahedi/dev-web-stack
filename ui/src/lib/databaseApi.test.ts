import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>();
  return { ...actual, isMockMode: true, request: vi.fn(), getSession: vi.fn() };
});

import { createServiceDatabase, listServiceDatabases, revealServiceUserPassword } from './databaseApi';
import { resetMockDatabaseStateForTests } from './databaseMock';

describe('databaseApi mock contract', () => {
  beforeEach(() => resetMockDatabaseStateForTests());

  it('lists databases in mock mode and resets state', async () => {
    const result = await listServiceDatabases('mysql');
    expect(result.databases.map((database) => database.name)).toContain('app');
    const created = await createServiceDatabase('mysql', { name: 'blog2' });
    expect(created.name).toBe('blog2');
    expect((await listServiceDatabases('mysql')).databases.map((database) => database.name)).toContain('blog2');
  });

  it('reveals a user secret only through the reveal call', async () => {
    const secret = await revealServiceUserPassword('mysql', 'usr_app');
    expect(secret.value).toBe('mock_db_password');
  });
});
