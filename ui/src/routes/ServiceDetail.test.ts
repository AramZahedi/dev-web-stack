import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import ServiceDetail from './ServiceDetail.svelte';
import * as api from '../lib/api';
import * as db from '../lib/databaseApi';
import type {
  Database,
  DatabaseBackup,
  DatabaseUser,
} from '../lib/databaseTypes';
import type { ServiceView } from '../lib/types';

vi.mock('../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api')>();
  return {
    ...actual,
    getService: vi.fn(),
    installService: vi.fn(),
    activateService: vi.fn(),
    deactivateService: vi.fn(),
    restartService: vi.fn(),
  };
});

vi.mock('../lib/databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/databaseApi')>();
  return {
    ...actual,
    listServiceDatabases: vi.fn(),
    listServiceUsers: vi.fn(),
    listServiceBackups: vi.fn(),
    createServiceBackup: vi.fn(),
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

const REDIS_SERVICE: ServiceView = {
  id: 'redis',
  display_name: 'Redis 8.8',
  group: 'standalone',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'b1b2c3d4e5f6',
  image: 'redis:8.8.3',
  ports: [{ host: 6379, container: 6379, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/redis/config',
  data_path: '/data/devstack/services/redis/data',
};

const MYSQL_DATABASES: Database[] = [
  {
    name: 'app',
    service_id: 'mysql',
    engine: 'mysql',
    size_bytes: 13_000_000,
    table_count: 12,
    linked_sites: [{ id: 'site_blog', name: 'blog' }],
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

const MYSQL_BACKUPS: DatabaseBackup[] = [
  {
    id: 'bkp_1',
    service_id: 'mysql',
    engine: 'mysql',
    database: 'app',
    size_bytes: 2_400_000,
    sha256: 'abc123',
    kind: 'manual',
    status: 'ready',
    created_at: '2026-10-29T10:00:00Z',
  },
];

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
  vi.mocked(api.getService).mockResolvedValue(MYSQL_SERVICE);
  vi.mocked(api.deactivateService).mockResolvedValue(undefined);
  vi.mocked(db.listServiceDatabases).mockResolvedValue({ databases: MYSQL_DATABASES });
  vi.mocked(db.listServiceUsers).mockResolvedValue({ users: MYSQL_USERS });
  vi.mocked(db.listServiceBackups).mockResolvedValue({ backups: MYSQL_BACKUPS });
});

afterEach(() => {
  vi.clearAllMocks();
  window.location.hash = '';
});

describe('ServiceDetail', () => {
  it('renders service detail with database tabs', async () => {
    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    expect(await screen.findByRole('heading', { name: 'MySQL 8.4' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Databases' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Users' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Backups' })).toBeTruthy();
    expect(screen.queryByRole('tab', { name: 'Password' })).toBeNull();
    await waitFor(() => expect(db.listServiceDatabases).toHaveBeenCalledWith('mysql'));
    expect(db.listServiceUsers).toHaveBeenCalledWith('mysql');
    expect(db.listServiceBackups).toHaveBeenCalledWith('mysql');
  });

  it('renders Redis with users and password only', async () => {
    vi.mocked(api.getService).mockResolvedValue(REDIS_SERVICE);
    vi.mocked(db.listServiceUsers).mockResolvedValue({ users: REDIS_USERS });
    vi.mocked(db.listServiceDatabases).mockRejectedValue(new Error('not a SQL service'));
    vi.mocked(db.listServiceBackups).mockRejectedValue(new Error('not a SQL service'));

    render(ServiceDetail, { props: { params: { id: 'redis' } } });

    expect(await screen.findByRole('tab', { name: 'Users' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Password' })).toBeTruthy();
    expect(screen.queryByRole('tab', { name: 'Databases' })).toBeNull();
    expect(screen.queryByRole('tab', { name: 'Backups' })).toBeNull();
    expect(db.listServiceDatabases).not.toHaveBeenCalled();
    expect(db.listServiceBackups).not.toHaveBeenCalled();
  });

  it('preselects the tab and user from the route query', async () => {
    window.location.hash = '#/services/mysql?tab=users&user=usr_app';

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    const usersTab = await screen.findByRole('tab', { name: 'Users' });
    await waitFor(() => expect(usersTab.getAttribute('aria-selected')).toBe('true'));
    expect(screen.getByRole('tabpanel', { name: 'Users' })).toBeTruthy();
  });

  it('keeps one failed inventory call from blanking the page', async () => {
    vi.mocked(db.listServiceBackups).mockRejectedValue(new Error('backup index unavailable'));

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    expect(await screen.findByRole('heading', { name: 'MySQL 8.4' })).toBeTruthy();
    expect(await screen.findByText(/backup index unavailable/i)).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Databases' })).toBeTruthy();
  });

  it('renders the service header before inventory calls finish', async () => {
    vi.mocked(db.listServiceDatabases).mockImplementation(
      () => new Promise(() => {}) as never,
    );

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    expect(await screen.findByRole('heading', { name: 'MySQL 8.4' })).toBeTruthy();
  });

  it('shows not found for an empty service id instead of hanging', async () => {
    render(ServiceDetail, { props: { params: {} } });

    expect(await screen.findByText('Service not found')).toBeTruthy();
  });

  it('applies a same-service hash change to the selected tab', async () => {
    render(ServiceDetail, { props: { params: { id: 'mysql' } } });
    await screen.findByRole('heading', { name: 'MySQL 8.4' });

    window.location.hash = '#/services/mysql?tab=backups';
    window.dispatchEvent(new HashChangeEvent('hashchange'));

    const backupsTab = screen.getByRole('tab', { name: 'Backups' });
    await waitFor(() => expect(backupsTab.getAttribute('aria-selected')).toBe('true'));
  });

  it('supports Home and End on the tablist', async () => {
    render(ServiceDetail, { props: { params: { id: 'mysql' } } });
    const databasesTab = await screen.findByRole('tab', { name: 'Databases' });

    await fireEvent.keyDown(databasesTab, { key: 'End' });
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Backups' }).getAttribute('aria-selected'),
      ).toBe('true'),
    );

    await fireEvent.keyDown(screen.getByRole('tab', { name: 'Backups' }), { key: 'Home' });
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'Databases' }).getAttribute('aria-selected'),
      ).toBe('true'),
    );
  });

  it('activates a stopped service from the detail header', async () => {
    const stopped = { ...MYSQL_SERVICE, active: false, state: 'installed' as const };
    vi.mocked(api.getService).mockResolvedValue(stopped);
    vi.mocked(api.activateService).mockResolvedValue(undefined);

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    const activate = await screen.findByRole('button', { name: 'Activate' });
    await fireEvent.click(activate);
    await waitFor(() => expect(api.activateService).toHaveBeenCalledWith('mysql'));
  });

  it('warns about linked sites, backups and running jobs before deactivating', async () => {
    vi.mocked(db.listServiceBackups).mockResolvedValue({
      backups: [{ ...MYSQL_BACKUPS[0], status: 'running' }],
    });

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });

    await fireEvent.click(await screen.findByRole('button', { name: 'Deactivate' }));
    const dialog = await screen.findByRole('dialog');

    expect(within(dialog).getByText(/blog/)).toBeTruthy();
    expect(within(dialog).getByText(/1 backup/i)).toBeTruthy();
    expect(within(dialog).getByText(/running backup jobs/i)).toBeTruthy();
    expect(within(dialog).getByText(/links are kept/i)).toBeTruthy();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Deactivate service' }));
    await waitFor(() => expect(api.deactivateService).toHaveBeenCalledWith('mysql'));
  });

  it('keeps a running job visible to the deactivate warning after switching tabs', async () => {
    vi.mocked(db.createServiceBackup).mockResolvedValue({
      id: 'job_route',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'running',
      progress: 40,
      message: 'Dumping tables',
      created_at: '2026-10-29T10:00:00Z',
      updated_at: '2026-10-29T10:00:02Z',
    });

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });
    await screen.findByRole('heading', { name: 'MySQL 8.4' });

    await fireEvent.click(await screen.findByRole('button', { name: /back up app/i }));
    await screen.findByRole('status', { name: 'Database job' });

    await fireEvent.click(screen.getByRole('tab', { name: 'Users' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/backup on app/i)).toBeTruthy();
  });

  it('fails closed on deactivate when the backup inventory is unavailable', async () => {
    vi.mocked(db.listServiceBackups).mockRejectedValue(new Error('backup index unavailable'));

    render(ServiceDetail, { props: { params: { id: 'mysql' } } });
    await screen.findByRole('heading', { name: 'MySQL 8.4' });
    await fireEvent.click(screen.getByRole('button', { name: 'Deactivate' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/backup inventory failed to load/i)).toBeTruthy();
    expect(
      (within(dialog).getByRole('button', { name: 'Deactivate service' }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(api.deactivateService).not.toHaveBeenCalled();
  });
});
