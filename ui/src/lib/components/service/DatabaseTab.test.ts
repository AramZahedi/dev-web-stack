import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import DatabaseTab from './DatabaseTab.svelte';
import * as db from '../../databaseApi';
import type { Database } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../databaseApi')>();
  return {
    ...actual,
    createServiceDatabase: vi.fn(),
    renameServiceDatabase: vi.fn(),
    dropServiceDatabase: vi.fn(),
    createServiceBackup: vi.fn(),
    importServiceDatabase: vi.fn(),
    exportServiceDatabase: vi.fn(),
    getDatabaseJob: vi.fn(),
    cancelDatabaseJob: vi.fn(),
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

const STOPPED_MYSQL: ServiceView = {
  ...MYSQL_SERVICE,
  state: 'installed',
  active: false,
  health: '',
};

const POSTGRES_SERVICE: ServiceView = {
  ...MYSQL_SERVICE,
  id: 'postgresql',
  display_name: 'PostgreSQL 18',
  group: 'standalone',
  image: 'postgres:18.1',
  ports: [{ host: 5432, container: 5432, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/postgresql/config',
  data_path: '/data/devstack/services/postgresql/data',
};

const MYSQL_DATABASES: Database[] = [
  {
    name: 'app',
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 13_000_000,
    table_count: 12,
    created_at: '2026-10-20T10:00:00Z',
    linked_sites: [{ id: 'site_blog', name: 'blog' }],
    managed: true,
  },
  {
    name: 'devstack',
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 1_100_000,
    table_count: 2,
    created_at: '2026-10-19T10:00:00Z',
    linked_sites: [],
    managed: false,
  },
];

const POSTGRES_DATABASES: Database[] = [
  {
    name: 'app',
    service_id: 'postgresql',
    engine: 'postgres',
    size_bytes: 8_000_000,
    table_count: 9,
    created_at: '2026-10-21T10:00:00Z',
    linked_sites: [],
    managed: true,
  },
];

beforeEach(() => {
  vi.mocked(db.dropServiceDatabase).mockResolvedValue(undefined);
  vi.mocked(db.createServiceDatabase).mockImplementation(async (_id, body) => ({
    name: body.name,
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 0,
    table_count: 0,
    linked_sites: [],
    managed: true,
  }));
  vi.mocked(db.renameServiceDatabase).mockImplementation(async (_id, _name, body) => ({
    ...POSTGRES_DATABASES[0],
    name: body.name,
  }));
  vi.mocked(db.createServiceBackup).mockResolvedValue({
    id: 'job_1',
    service_id: 'mysql',
    database: 'app',
    kind: 'backup',
    status: 'succeeded',
    progress: 100,
    created_at: '2026-10-29T10:00:00Z',
    updated_at: '2026-10-29T10:00:01Z',
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('DatabaseTab', () => {
  it('lists databases with size, tables, and linked sites', () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('app')).toBeTruthy();
    expect(screen.getByText('12.4 MB')).toBeTruthy();
    expect(screen.getByText('12')).toBeTruthy();
    expect(screen.getByText('blog')).toBeTruthy();
    expect(screen.getByText('devstack')).toBeTruthy();
  });

  it('hides rename for mysql and shows it for postgres', () => {
    const { rerender } = render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.queryByRole('button', { name: /rename app/i })).toBeNull();

    rerender({
      service: POSTGRES_SERVICE,
      databases: POSTGRES_DATABASES,
      onRefresh: vi.fn(),
    });

    expect(screen.getByRole('button', { name: /rename app/i })).toBeTruthy();
  });

  it('requires the exact database name before dropping', async () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /drop app/i }));

    const confirm = screen.getByRole('button', { name: 'Drop database' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);

    await fireEvent.input(screen.getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await waitFor(() => expect(confirm.disabled).toBe(false));
  });

  it('drops with the backup-first default and refreshes', async () => {
    const refresh = vi.fn();
    render(DatabaseTab, {
      props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: refresh },
    });

    await fireEvent.click(screen.getByRole('button', { name: /drop app/i }));
    const backupToggle = screen.getByRole('switch', { name: 'Back up first' });
    expect(backupToggle.getAttribute('aria-checked')).toBe('true');
    await fireEvent.input(screen.getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Drop database' }));

    await waitFor(() =>
      expect(db.dropServiceDatabase).toHaveBeenCalledWith('mysql', 'app', {
        confirm: 'app',
        backup_first: true,
      }),
    );
    expect(refresh).toHaveBeenCalled();
  });

  it('creates a database through the form dialog', async () => {
    const refresh = vi.fn();
    render(DatabaseTab, {
      props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: refresh },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create database' }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/database name/i), {
      target: { value: 'shop' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create database' }));

    await waitFor(() =>
      expect(db.createServiceDatabase).toHaveBeenCalledWith('mysql', { name: 'shop' }),
    );
    expect(refresh).toHaveBeenCalled();
  });

  it('renames a PostgreSQL database through the form dialog', async () => {
    render(DatabaseTab, {
      props: {
        service: POSTGRES_SERVICE,
        databases: POSTGRES_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /rename app/i }));
    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText(/database name/i) as HTMLInputElement;
    expect(input.value).toBe('app');
    await fireEvent.input(input, { target: { value: 'app_v2' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rename database' }));

    await waitFor(() =>
      expect(db.renameServiceDatabase).toHaveBeenCalledWith('postgresql', 'app', {
        name: 'app_v2',
      }),
    );
  });

  it('exports a database through the download endpoint', async () => {
    vi.mocked(db.exportServiceDatabase).mockResolvedValue(new Blob(['dump']));
    render(DatabaseTab, {
      props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() },
    });

    await fireEvent.click(screen.getByRole('button', { name: /export app/i }));

    await waitFor(() => expect(db.exportServiceDatabase).toHaveBeenCalledWith('mysql', 'app'));
  });

  it('flags a database missing on the engine and offers to manage its links', () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: [{ ...MYSQL_DATABASES[0], missing: true }],
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText(/missing on engine/i)).toBeTruthy();
    expect(screen.getByRole('button', { name: /unlink app/i })).toBeTruthy();
  });

  it('disables create while stopped and reports inactive backup, export and import attempts', async () => {
    render(DatabaseTab, {
      props: {
        service: STOPPED_MYSQL,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('app')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Create database' }) as HTMLButtonElement).disabled).toBe(true);

    await fireEvent.click(screen.getByRole('button', { name: /back up app/i }));
    expect(screen.getByText(/activate the service to back up databases/i)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: /export app/i }));
    expect(screen.getByText(/activate the service to export databases/i)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: /import app/i }));
    expect(screen.getByText(/activate the service to import databases/i)).toBeTruthy();

    expect(db.createServiceBackup).not.toHaveBeenCalled();
    expect(db.exportServiceDatabase).not.toHaveBeenCalled();
    expect(db.importServiceDatabase).not.toHaveBeenCalled();
  });
});
