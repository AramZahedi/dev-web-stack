import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import UsersTab from './UsersTab.svelte';
import * as db from '../../databaseApi';
import type { Database, DatabaseUser } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../databaseApi')>();
  return {
    ...actual,
    createServiceUser: vi.fn(),
    updateServiceUser: vi.fn(),
    deleteServiceUser: vi.fn(),
    setServiceUserPassword: vi.fn(),
    revealServiceUserPassword: vi.fn(),
  };
});

const MYSQL_SERVICE: ServiceView = {
  id: 'mysql',
  display_name: 'MySQL 8.4',
  group: 'mysql-family',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'a1b2c3d4e5f6',
  image: 'mysql:8.4.13',
  ports: [{ host: 3306, container: 3306, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/mysql/config',
  data_path: '/data/devstack/services/mysql/data',
};

const POSTGRES_SERVICE: ServiceView = {
  ...MYSQL_SERVICE,
  id: 'postgresql',
  display_name: 'PostgreSQL 18',
  group: 'standalone',
  image: 'postgres:18.1',
  ports: [{ host: 5432, container: 5432, protocol: 'tcp', loopback: true }],
};

const REDIS_SERVICE: ServiceView = {
  ...MYSQL_SERVICE,
  id: 'redis',
  display_name: 'Redis 8.8',
  group: 'standalone',
  image: 'redis:8.8.3',
  ports: [{ host: 6379, container: 6379, protocol: 'tcp', loopback: true }],
};

const MYSQL_DATABASES: Database[] = [
  {
    name: 'app',
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 13_000_000,
    table_count: 12,
    linked_sites: [],
    managed: true,
  },
  {
    name: 'blog',
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 4_200_000,
    table_count: 7,
    linked_sites: [],
    managed: true,
  },
];

const POSTGRES_DATABASES: Database[] = [
  {
    name: 'app',
    service_id: 'postgresql',
    engine: 'postgres',
    size_bytes: 8_000_000,
    table_count: 9,
    linked_sites: [],
    managed: true,
  },
];

const MYSQL_USERS: DatabaseUser[] = [
  {
    id: 'usr_app',
    service_id: 'mysql',
    engine: 'mysql',
    username: 'app',
    host: '%',
    enabled: true,
    managed: true,
    password_managed: true,
    grants: [{ database: 'app', preset: 'read_write_schema' }],
    linked_sites: [],
  },
  {
    id: 'usr_ext',
    service_id: 'mysql',
    engine: 'mysql',
    username: 'external',
    host: '%',
    enabled: true,
    managed: false,
    password_managed: false,
    grants: [],
    linked_sites: [],
  },
];

const LINKED_USER: DatabaseUser = {
  ...MYSQL_USERS[0],
  linked_sites: [{ id: 'site_blog', name: 'blog' }],
};

const POSTGRES_USER: DatabaseUser = {
  id: 'usr_pg',
  service_id: 'postgresql',
  engine: 'postgres',
  username: 'app',
  enabled: true,
  managed: true,
  password_managed: true,
  grants: [{ database: 'app', preset: 'read_write_schema' }],
  linked_sites: [],
};

const REDIS_USERS: DatabaseUser[] = [
  {
    id: 'usr_cache',
    service_id: 'redis',
    engine: 'redis',
    username: 'cache',
    enabled: true,
    managed: true,
    password_managed: true,
    access_level: 'read-only',
    grants: [],
    linked_sites: [],
  },
  {
    id: 'usr_default',
    service_id: 'redis',
    engine: 'redis',
    username: 'default',
    enabled: true,
    managed: false,
    password_managed: true,
    access_level: 'full',
    grants: [],
    linked_sites: [],
  },
];

beforeEach(() => {
  vi.mocked(db.revealServiceUserPassword).mockResolvedValue({ value: 's3cret' });
  vi.mocked(db.setServiceUserPassword).mockResolvedValue({ value: 'n3w-secret' });
  vi.mocked(db.deleteServiceUser).mockResolvedValue(undefined);
  vi.mocked(db.createServiceUser).mockResolvedValue({
    id: 'usr_api',
    service_id: 'mysql',
    engine: 'mysql',
    username: 'api',
    host: '%',
    enabled: true,
    managed: true,
    password_managed: true,
    grants: [{ database: 'app', preset: 'read_write_schema' }],
    linked_sites: [],
  });
  vi.mocked(db.updateServiceUser).mockResolvedValue(MYSQL_USERS[0]);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('UsersTab', () => {
  it('shows managed and external users with grants', () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('app')).toBeTruthy();
    expect(screen.getByText('external')).toBeTruthy();
    expect(screen.getByText('read/write + schema on app')).toBeTruthy();
    expect(screen.getByText('Managed')).toBeTruthy();
    expect(screen.getByText('External')).toBeTruthy();
  });

  it('reveals a password only after the audited call', async () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.queryByText('s3cret')).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: /reveal password for app/i }));

    expect(await screen.findByText('s3cret')).toBeTruthy();
    expect(db.revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('blocks deleting a user linked to a site', () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [LINKED_USER],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    const remove = screen.getByRole('button', { name: /delete app/i }) as HTMLButtonElement;
    expect(remove.disabled).toBe(true);
    expect(screen.getByText(/unlink sites/i)).toBeTruthy();
  });

  it('deletes an unlinked user after the typed confirmation', async () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /delete app/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete user' }));

    await waitFor(() =>
      expect(db.deleteServiceUser).toHaveBeenCalledWith('mysql', 'usr_app', {
        confirm: 'app',
        reassign_owned: false,
      }),
    );
  });

  it('offers PostgreSQL owned-object reassignment before deleting a role', async () => {
    render(UsersTab, {
      props: {
        service: POSTGRES_SERVICE,
        users: [POSTGRES_USER],
        databases: POSTGRES_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /delete app/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(
      within(dialog).getByLabelText('Reassign owned objects to devstack admin'),
    );
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete user' }));

    await waitFor(() =>
      expect(db.deleteServiceUser).toHaveBeenCalledWith('postgresql', 'usr_pg', {
        confirm: 'app',
        reassign_owned: true,
      }),
    );
  });

  it('creates a managed user with a database-scoped grant', async () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/^username/i), {
      target: { value: 'api' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create user' }));

    await waitFor(() =>
      expect(db.createServiceUser).toHaveBeenCalledWith('mysql', {
        username: 'api',
        host: '%',
        grants: [{ database: 'app', preset: 'read_write_schema' }],
      }),
    );
  });

  it('edits grants through the user dialog', async () => {
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /edit app/i }));
    const dialog = await screen.findByRole('dialog');
    const preset = within(dialog).getByLabelText('Grant 1 preset') as HTMLSelectElement;
    await fireEvent.change(preset, { target: { value: 'read_only' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));

    await waitFor(() =>
      expect(db.updateServiceUser).toHaveBeenCalledWith('mysql', 'usr_app', {
        enabled: true,
        grants: [{ database: 'app', preset: 'read_only' }],
      }),
    );
  });

  it('excludes protected databases from grant targets and defaults', async () => {
    const protectedDatabase: Database = {
      name: 'devstack',
      service_id: 'mysql',
      engine: 'mysql',
      size_bytes: 1_000_000,
      table_count: 2,
      linked_sites: [],
      managed: false,
    };
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: MYSQL_USERS,
        databases: [protectedDatabase, ...MYSQL_DATABASES],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
    const dialog = await screen.findByRole('dialog');
    const select = within(dialog).getByLabelText('Grant 1 database') as HTMLSelectElement;

    expect(select.value).toBe('app');
    expect(within(select).queryByRole('option', { name: 'devstack' })).toBeNull();
  });

  it('hides host and grants for Redis and shows the access level', () => {
    render(UsersTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        databases: [],
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('cache')).toBeTruthy();
    expect(screen.getByText('read-only')).toBeTruthy();
    expect(screen.queryByText('Grants')).toBeNull();
    expect(screen.queryByText('Host')).toBeNull();
    expect(screen.getByText(/full means all data commands/i)).toBeTruthy();
  });
});
