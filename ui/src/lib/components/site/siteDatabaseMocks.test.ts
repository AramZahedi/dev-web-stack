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

  it('keeps database and user linked_sites in sync with links', async () => {
    const link = await mockDatabaseApi.linkSiteDatabase('s_test', {
      service_id: 'mysql',
      database: 'app',
      user_id: 'usr_app',
      primary: true,
    });

    let database = await mockDatabaseApi.getServiceDatabase('mysql', 'app');
    expect(database.linked_sites?.map((site) => site.id)).toContain('s_test');
    let users = await mockDatabaseApi.listServiceUsers('mysql');
    expect(
      users.users.find((user) => user.id === 'usr_app')?.linked_sites?.map((site) => site.id),
    ).toContain('s_test');

    await mockDatabaseApi.unlinkSiteDatabase('s_test', link.id);
    database = await mockDatabaseApi.getServiceDatabase('mysql', 'app');
    expect(database.linked_sites?.map((site) => site.id)).not.toContain('s_test');
    users = await mockDatabaseApi.listServiceUsers('mysql');
    expect(
      users.users.find((user) => user.id === 'usr_app')?.linked_sites?.map((site) => site.id),
    ).not.toContain('s_test');
  });

  it('removes database links when a site is deleted', async () => {
    const site = await mockApi.createSite({
      name: 'link-cleanup',
      path: '/home/dev/projects/scratch',
      kind: 'static',
      database: {
        service_id: 'mysql',
        database: 'app',
        user_id: 'usr_app',
        primary: true,
      },
    });
    expect((await mockDatabaseApi.listSiteDatabases(site.id)).links).toHaveLength(1);

    await mockApi.deleteSite(site.id);

    expect((await mockDatabaseApi.listSiteDatabases(site.id)).links).toHaveLength(0);
    const database = await mockDatabaseApi.getServiceDatabase('mysql', 'app');
    expect(database.linked_sites?.map((entry) => entry.id)).not.toContain(site.id);
  });
});
