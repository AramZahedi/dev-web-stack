import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import DatabaseTab from './DatabaseTab.svelte';
import * as api from '../../api';
import * as db from '../../databaseApi';
import { clearToasts, toasts } from '../../stores';
import type { Database, DatabaseJob } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>();
  return {
    ...actual,
    getSite: vi.fn(),
    stopSite: vi.fn(),
    restartSite: vi.fn(),
  };
});

vi.mock('../../databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../databaseApi')>();
  return {
    ...actual,
    createServiceDatabase: vi.fn(),
    renameServiceDatabase: vi.fn(),
    dropServiceDatabase: vi.fn(),
    createServiceBackup: vi.fn(),
    restoreServiceBackup: vi.fn(),
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

const MARIADB_SERVICE: ServiceView = {
  ...MYSQL_SERVICE,
  id: 'mariadb',
  display_name: 'MariaDB 11.8',
  image: 'mariadb:11.8',
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

const UNLINKED_APP: Database = { ...MYSQL_DATABASES[0], linked_sites: [] };

beforeEach(() => {
  clearToasts();
  vi.mocked(api.getSite).mockResolvedValue({
    id: 'site_blog',
    name: 'blog',
    status: 'running',
    desired_state: 'running',
  } as never);
  vi.mocked(api.stopSite).mockResolvedValue(undefined);
  vi.mocked(api.restartSite).mockResolvedValue(undefined);
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

  it('hides rename for MariaDB as well as MySQL', () => {
    render(DatabaseTab, {
      props: {
        service: MARIADB_SERVICE,
        databases: [{ ...MYSQL_DATABASES[0], service_id: 'mariadb', engine: 'mariadb' }],
        onRefresh: vi.fn(),
      },
    });

    expect(screen.queryByRole('button', { name: /rename app/i })).toBeNull();
  });

  it('requires the exact database name before dropping', async () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: [UNLINKED_APP, MYSQL_DATABASES[1]],
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
      props: {
        service: MYSQL_SERVICE,
        databases: [UNLINKED_APP, MYSQL_DATABASES[1]],
        onRefresh: refresh,
      },
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

  it('requires linked sites to be stopped before dropping', async () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /drop app/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('blog')).toBeTruthy();

    const confirm = within(dialog).getByRole('button', {
      name: 'Drop database',
    }) as HTMLButtonElement;
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    expect(confirm.disabled).toBe(true);

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Stop site blog' }));
    await waitFor(() => expect(api.stopSite).toHaveBeenCalledWith('site_blog'));
    await waitFor(() => expect(confirm.disabled).toBe(false));
  });

  it('requires linked sites to be stopped before renaming', async () => {
    render(DatabaseTab, {
      props: {
        service: POSTGRES_SERVICE,
        databases: [
          {
            ...POSTGRES_DATABASES[0],
            linked_sites: [{ id: 'site_blog', name: 'blog' }],
          },
        ],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /rename app/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('blog')).toBeTruthy();

    await fireEvent.input(within(dialog).getByLabelText(/database name/i), {
      target: { value: 'app_v2' },
    });
    await fireEvent.input(
      within(dialog).getByLabelText(/type the current name to confirm/i),
      { target: { value: 'app' } },
    );
    const rename = within(dialog).getByRole('button', {
      name: 'Rename database',
    }) as HTMLButtonElement;
    expect(rename.disabled).toBe(true);

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Stop site blog' }));
    await waitFor(() => expect(api.stopSite).toHaveBeenCalledWith('site_blog'));
    await waitFor(() => expect(rename.disabled).toBe(false));
  });

  it('requires linked sites to be stopped for a replace import', async () => {
    const { container } = render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /import app/i }));
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: [new File(['sql'], 'dump.sql', { type: 'text/plain' })],
    });
    await fireEvent.change(input);

    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(within(dialog).getByLabelText(/replace contents/i));

    expect(within(dialog).getByText('blog')).toBeTruthy();
    const importButton = within(dialog).getByRole('button', {
      name: 'Import database',
    }) as HTMLButtonElement;
    expect(importButton.disabled).toBe(true);

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Stop site blog' }));
    await waitFor(() => expect(api.stopSite).toHaveBeenCalledWith('site_blog'));
  });

  it('offers the pre-operation backup when an import leaves the target incomplete', async () => {
    vi.mocked(db.importServiceDatabase).mockResolvedValue({
      id: 'job_import_failed',
      service_id: 'mysql',
      database: 'app',
      kind: 'import',
      status: 'failed',
      progress: 70,
      message: 'import failed midway',
      backup_id: 'bkp_pre_import',
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:02Z',
    });
    vi.mocked(db.restoreServiceBackup).mockResolvedValue({
      id: 'job_recovery',
      service_id: 'mysql',
      database: 'app_recovery',
      kind: 'restore',
      status: 'succeeded',
      progress: 100,
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:03Z',
    });

    const { container } = render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: [UNLINKED_APP, MYSQL_DATABASES[1]],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /import app/i }));
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: [new File(['sql'], 'dump.sql', { type: 'text/plain' })],
    });
    await fireEvent.change(input);
    const importDialog = await screen.findByRole('dialog');
    await fireEvent.click(within(importDialog).getByRole('button', { name: 'Import database' }));

    expect(await screen.findByText(/restore incomplete/i)).toBeTruthy();
    await fireEvent.click(
      screen.getByRole('button', { name: /restore pre-operation backup/i }),
    );
    const recoveryDialog = await screen.findByRole('dialog');
    await fireEvent.input(within(recoveryDialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(
      within(recoveryDialog).getByRole('button', { name: 'Restore backup' }),
    );

    await waitFor(() =>
      expect(db.restoreServiceBackup).toHaveBeenCalledWith(
        'mysql',
        'bkp_pre_import',
        expect.objectContaining({
          overwrite: false,
          confirm: 'app',
          pre_restore_backup: false,
          target: expect.stringMatching(/^app_recovery_/),
        }),
      ),
    );
  });

  it('requires the target name before a replace import', async () => {
    vi.mocked(db.importServiceDatabase).mockResolvedValue({
      id: 'job_import',
      service_id: 'mysql',
      database: 'app',
      kind: 'import',
      status: 'succeeded',
      progress: 100,
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:02Z',
    });
    const { container } = render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: [UNLINKED_APP, MYSQL_DATABASES[1]],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /import app/i }));
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: [new File(['sql'], 'dump.sql', { type: 'text/plain' })],
    });
    await fireEvent.change(input);

    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(within(dialog).getByLabelText(/replace contents/i));

    const importButton = within(dialog).getByRole('button', {
      name: 'Import database',
    }) as HTMLButtonElement;
    expect(importButton.disabled).toBe(true);

    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await waitFor(() => expect(importButton.disabled).toBe(false));
    await fireEvent.click(importButton);

    await waitFor(() =>
      expect(db.importServiceDatabase).toHaveBeenCalledWith(
        'mysql',
        'app',
        expect.any(File),
        expect.objectContaining({ mode: 'replace', confirm: 'app' }),
      ),
    );
  });

  it('reports a failed backup job as an error toast and keeps the detail', async () => {    const failed: DatabaseJob = {
      id: 'job_failed',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'failed',
      progress: 40,
      message: 'disk full',
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:02Z',
    };
    vi.mocked(db.createServiceBackup).mockResolvedValue(failed);

    render(DatabaseTab, {
      props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() },
    });

    await fireEvent.click(screen.getByRole('button', { name: /back up app/i }));

    await waitFor(() => {
      const toast = get(toasts).find((entry) => entry.title === 'backup failed');
      expect(toast?.kind).toBe('error');
    });
    expect(screen.getByRole('status', { name: 'Database job' }).textContent).toContain('disk full');
    expect(screen.getByText('failed')).toBeTruthy();
  });

  it('reports a canceled job as an informational toast', async () => {
    vi.mocked(db.createServiceBackup).mockResolvedValue({
      id: 'job_canceled',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'canceled',
      progress: 20,
      message: 'canceled by user',
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:02Z',
    });

    render(DatabaseTab, {
      props: { service: MYSQL_SERVICE, databases: MYSQL_DATABASES, onRefresh: vi.fn() },
    });

    await fireEvent.click(screen.getByRole('button', { name: /back up app/i }));

    await waitFor(() => {
      const toast = get(toasts).find((entry) => entry.title === 'backup canceled');
      expect(toast?.kind).toBe('info');
    });
    expect(screen.getByText('canceled by user')).toBeTruthy();
  });

  it('prompts to restart linked sites after a PostgreSQL rename', async () => {
    vi.mocked(api.getSite).mockResolvedValue({
      id: 'site_blog',
      name: 'blog',
      status: 'stopped',
      desired_state: 'stopped',
    } as never);
    render(DatabaseTab, {
      props: {
        service: POSTGRES_SERVICE,
        databases: [
          {
            ...POSTGRES_DATABASES[0],
            linked_sites: [{ id: 'site_blog', name: 'blog' }],
          },
        ],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /rename app/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/database name/i), {
      target: { value: 'app_v2' },
    });
    await fireEvent.input(
      within(dialog).getByLabelText(/type the current name to confirm/i),
      { target: { value: 'app' } },
    );
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rename database' }));

    expect(await screen.findByText(/restart linked sites to apply/i)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /restart site blog/i }));
    await waitFor(() => expect(api.restartSite).toHaveBeenCalledWith('site_blog'));
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

  it('renames a PostgreSQL database only after typing the current name', async () => {
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

    const rename = within(dialog).getByRole('button', {
      name: 'Rename database',
    }) as HTMLButtonElement;
    expect(rename.disabled).toBe(true);

    await fireEvent.input(input, { target: { value: 'app_v2' } });
    expect(rename.disabled).toBe(true);

    const confirmInput = within(dialog).getByLabelText(
      /type the current name to confirm/i,
    ) as HTMLInputElement;
    await fireEvent.input(confirmInput, { target: { value: 'wrong' } });
    expect(rename.disabled).toBe(true);
    await fireEvent.click(rename);
    expect(db.renameServiceDatabase).not.toHaveBeenCalled();

    await fireEvent.input(confirmInput, { target: { value: 'app' } });
    await waitFor(() => expect(rename.disabled).toBe(false));
    await fireEvent.click(rename);

    await waitFor(() =>
      expect(db.renameServiceDatabase).toHaveBeenCalledWith('postgresql', 'app', {
        name: 'app_v2',
        confirm: 'app',
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

  it('does not offer destructive actions for a protected database', () => {
    render(DatabaseTab, {
      props: {
        service: MYSQL_SERVICE,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('Protected')).toBeTruthy();
    expect((screen.getByRole('button', { name: /drop devstack/i }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: /import devstack/i }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: /back up devstack/i }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('does not offer rename for a protected PostgreSQL database', () => {
    render(DatabaseTab, {
      props: {
        service: POSTGRES_SERVICE,
        databases: [{ ...POSTGRES_DATABASES[0], name: 'postgres', managed: false }],
        onRefresh: vi.fn(),
      },
    });

    expect((screen.getByRole('button', { name: /rename postgres/i }) as HTMLButtonElement).disabled).toBe(true);
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
