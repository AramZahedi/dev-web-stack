import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import BackupsTab from './BackupsTab.svelte';
import * as db from '../../databaseApi';
import type { Database, DatabaseBackup, DatabaseJob } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../databaseApi')>();
  return {
    ...actual,
    createServiceBackup: vi.fn(),
    deleteServiceBackup: vi.fn(),
    restoreServiceBackup: vi.fn(),
    downloadServiceBackup: vi.fn(),
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
];

const MYSQL_BACKUPS: DatabaseBackup[] = [
  {
    id: 'bkp_1',
    service_id: 'mysql',
    engine: 'mysql',
    database: 'app',
    size_bytes: 2_400_000,
    sha256: 'abcdef1234567890',
    kind: 'manual',
    status: 'ready',
    created_at: '2026-10-29T10:00:00Z',
  },
];

const RUNNING_JOB: DatabaseJob = {
  id: 'job_1',
  service_id: 'mysql',
  database: 'app',
  kind: 'backup',
  status: 'running',
  progress: 40,
  message: 'Dumping tables',
  created_at: '2026-10-29T10:00:00Z',
  updated_at: '2026-10-29T10:00:02Z',
};

beforeEach(() => {
  vi.mocked(db.downloadServiceBackup).mockResolvedValue(new Blob(['mock']));
  vi.mocked(db.deleteServiceBackup).mockResolvedValue(undefined);
  vi.mocked(db.cancelDatabaseJob).mockResolvedValue(undefined);
  vi.mocked(db.createServiceBackup).mockResolvedValue({
    ...RUNNING_JOB,
    status: 'succeeded',
    progress: 100,
  });
  vi.mocked(db.restoreServiceBackup).mockResolvedValue({
    ...RUNNING_JOB,
    kind: 'restore',
    status: 'succeeded',
    progress: 100,
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('BackupsTab', () => {
  it('lists and downloads backups while the service is stopped', async () => {
    render(BackupsTab, {
      props: {
        service: STOPPED_MYSQL,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    expect(screen.getByText('app')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /download app backup/i }));

    await waitFor(() =>
      expect(db.downloadServiceBackup).toHaveBeenCalledWith('mysql', 'bkp_1'),
    );
    expect(screen.queryByText(/activate the service to download/i)).toBeNull();
  });

  it('requires the target name for overwrite restore', async () => {
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));

    const confirm = screen.getByRole('button', { name: 'Restore backup' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);

    await fireEvent.input(screen.getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await waitFor(() => expect(confirm.disabled).toBe(false));
  });

  it('restores into a new database after the typed confirmation', async () => {
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Restore backup' }));

    await waitFor(() =>
      expect(db.restoreServiceBackup).toHaveBeenCalledWith(
        'mysql',
        'bkp_1',
        expect.objectContaining({
          overwrite: false,
          confirm: 'app',
          pre_restore_backup: true,
          target: expect.stringMatching(/^app_restore_/),
        }),
      ),
    );
  });

  it('shows an inactive-service error instead of creating a backup while stopped', async () => {    render(BackupsTab, {
      props: {
        service: STOPPED_MYSQL,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create backup' }));

    expect(screen.getByText(/activate the service to create a backup/i)).toBeTruthy();
    expect(db.createServiceBackup).not.toHaveBeenCalled();
  });

  it('shows an inactive-service error instead of restoring while stopped', async () => {
    render(BackupsTab, {
      props: {
        service: STOPPED_MYSQL,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));

    expect(screen.getByText(/activate the service to restore backups/i)).toBeTruthy();
    expect(db.restoreServiceBackup).not.toHaveBeenCalled();
  });

  it('deletes a backup after typing its id', async () => {
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /delete app backup/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/type .bkp_1. to confirm/i), {
      target: { value: 'bkp_1' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete backup' }));

    await waitFor(() => expect(db.deleteServiceBackup).toHaveBeenCalledWith('mysql', 'bkp_1'));
  });

  it('shows job progress and cancels a running backup job', async () => {
    vi.mocked(db.createServiceBackup).mockResolvedValue(RUNNING_JOB);
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create backup' }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create backup' }));

    expect(await screen.findByRole('status', { name: 'Database job' })).toBeTruthy();
    expect(screen.getByText('running')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel job' }));

    await waitFor(() => expect(db.cancelDatabaseJob).toHaveBeenCalledWith('job_1'));
  });
});
