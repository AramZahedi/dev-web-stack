import { beforeEach, describe, expect, it } from 'vitest';
import { mockApi, resetMockServiceStateForTests } from '../../mock';
import {
  mockDatabaseApi,
  resetMockDatabaseStateForTests,
} from '../../databaseMock';

describe('mock database site flows', () => {
  beforeEach(() => {
    resetMockServiceStateForTests();
    resetMockDatabaseStateForTests();
  });

  it('creates a site database link when Add Site carries a database', async () => {
    const site = await mockApi.createSite({
      name: 'db-mock-check',
      path: '/home/dev/projects/scratch',
      kind: 'static',
      database: {
        service_id: 'mysql',
        new_database: { name: 'mock_app' },
        new_user: {
          username: 'mock_user',
          grants: [{ database: 'mock_app', preset: 'read_write_schema' }],
        },
        grant: 'read_write_schema',
        primary: true,
      },
    });

    const { links } = await mockDatabaseApi.listSiteDatabases(site.id);
    expect(links).toHaveLength(1);
    expect(links[0]).toMatchObject({
      database: 'mock_app',
      username: 'mock_user',
      primary: true,
    });
  });

  it('rejects duplicate links and a second primary link', async () => {
    await mockDatabaseApi.linkSiteDatabase('s_test', {
      service_id: 'mysql',
      database: 'app',
      user_id: 'usr_app',
      primary: true,
    });

    await expect(
      mockDatabaseApi.linkSiteDatabase('s_test', {
        service_id: 'mysql',
        database: 'app',
        user_id: 'usr_app',
        primary: false,
      }),
    ).rejects.toMatchObject({ code: 'DATABASE_LINK_EXISTS' });

    await expect(
      mockDatabaseApi.linkSiteDatabase('s_test', {
        service_id: 'mysql',
        database: 'blog',
        user_id: 'usr_app',
        primary: true,
      }),
    ).rejects.toMatchObject({ code: 'DATABASE_PRIMARY_EXISTS' });

    const secondary = await mockDatabaseApi.linkSiteDatabase('s_test', {
      service_id: 'mysql',
      database: 'blog',
      user_id: 'usr_app',
      primary: false,
    });
    expect(secondary.primary).toBe(false);
  });
});
