import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import UsersTab from './UsersTab.svelte';
import * as api from '../../api';
import * as db from '../../databaseApi';
import type { Database, DatabaseUser } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>();
  return {
    ...actual,
    restartSite: vi.fn(),
  };
});

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
  vi.mocked(api.restartSite).mockResolvedValue(undefined);
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
    expect(screen.getAllByText('mysql').length).toBeGreaterThan(0);
  });

  it('keeps the user form draft across a background inventory refresh', async () => {
    const props = {
      service: MYSQL_SERVICE,
      users: MYSQL_USERS,
      databases: MYSQL_DATABASES,
      onRefresh: vi.fn(),
    };
    const { rerender } = render(UsersTab, { props });

    await fireEvent.click(screen.getByRole('button', { name: 'Create user' }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/^username/i), {
      target: { value: 'draft' },
    });

    rerender({ ...props, databases: MYSQL_DATABASES.map((database) => ({ ...database })) });

    const input = within(screen.getByRole('dialog')).getByLabelText(
      /^username/i,
    ) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe('draft'));
  });

  it('identifies the host scope in the delete dialog', async () => {
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
    expect(within(dialog).getByText('Delete user app@%?')).toBeTruthy();
  });

  it('does not offer Set password for a protected metadata-only user', () => {
    const protectedUser: DatabaseUser = {
      id: 'usr_devstack',
      service_id: 'mysql',
      engine: 'mysql',
      username: 'devstack',
      host: '%',
      enabled: true,
      managed: false,
      password_managed: false,
      grants: [],
      linked_sites: [],
    };
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [protectedUser],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(
      (screen.getByRole('button', { name: /set password for devstack/i }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
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

  it('keeps existing protected database grants read-only and out of the update payload', async () => {
    const user: DatabaseUser = {
      ...MYSQL_USERS[0],
      grants: [
        { database: 'app', preset: 'read_write_schema' },
        { database: 'devstack', preset: 'admin' },
      ],
    };
    vi.mocked(db.updateServiceUser).mockResolvedValue(user);
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [user],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /edit app/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/protected or missing databases/i)).toBeTruthy();
    expect(within(dialog).getByText(/devstack/)).toBeTruthy();
    expect(within(dialog).queryByLabelText('Grant 2 database')).toBeNull();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));

    await waitFor(() =>
      expect(db.updateServiceUser).toHaveBeenCalledWith('mysql', 'usr_app', {
        enabled: true,
        grants: [{ database: 'app', preset: 'read_write_schema' }],
      }),
    );
  });

  it('swaps databases instead of duplicating grant rows', async () => {
    const user: DatabaseUser = {
      ...MYSQL_USERS[0],
      grants: [
        { database: 'app', preset: 'read_write_schema' },
        { database: 'blog', preset: 'read_only' },
      ],
    };
    vi.mocked(db.updateServiceUser).mockResolvedValue(user);
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [user],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /edit app/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.change(within(dialog).getByLabelText('Grant 1 database'), {
      target: { value: 'blog' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));

    await waitFor(() =>
      expect(db.updateServiceUser).toHaveBeenCalledWith('mysql', 'usr_app', {
        enabled: true,
        grants: [
          { database: 'blog', preset: 'read_write_schema' },
          { database: 'app', preset: 'read_only' },
        ],
      }),
    );
  });

  it('prompts to restart linked sites after a grant change', async () => {
    const linked = { ...MYSQL_USERS[0], linked_sites: [{ id: 'site_blog', name: 'blog' }] };
    vi.mocked(db.updateServiceUser).mockResolvedValue(linked);
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [linked],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /edit app/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.change(within(dialog).getByLabelText('Grant 1 preset'), {
      target: { value: 'read_only' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByText(/restart linked sites to apply/i)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /restart site blog/i }));
    await waitFor(() => expect(api.restartSite).toHaveBeenCalledWith('site_blog'));
  });

  it('prompts to restart linked sites after a password reset', async () => {
    const linked = { ...MYSQL_USERS[0], linked_sites: [{ id: 'site_blog', name: 'blog' }] };
    render(UsersTab, {
      props: {
        service: MYSQL_SERVICE,
        users: [linked],
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /reset password for app/i }));

    expect(
      await screen.findByText(/does not make another audited api call/i),
    ).toBeTruthy();
    expect(await screen.findByText(/restart linked sites to apply/i)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /restart site blog/i }));
    await waitFor(() => expect(api.restartSite).toHaveBeenCalledWith('site_blog'));
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
