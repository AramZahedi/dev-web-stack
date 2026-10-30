import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import BackupsTab from './BackupsTab.svelte';
import * as api from '../../api';
import * as db from '../../databaseApi';
import { clearToasts, toasts } from '../../stores';
import { clearRecoveryJobID, loadRecoveryJobID } from './recoveryStore';
import type { Database, DatabaseBackup, DatabaseJob } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api')>();
  return {
    ...actual,
    getSite: vi.fn(),
    stopSite: vi.fn(),
  };
});

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
  clearToasts();
  clearRecoveryJobID('mysql');
  vi.mocked(api.getSite).mockResolvedValue({
    id: 'site_blog',
    name: 'blog',
    status: 'running',
    desired_state: 'running',
  } as never);
  vi.mocked(api.stopSite).mockResolvedValue(undefined);
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
    expect(screen.getByText(/2.3 MB total/i)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /download app backup/i }));

    await waitFor(() =>
      expect(db.downloadServiceBackup).toHaveBeenCalledWith('mysql', 'bkp_1'),
    );
    expect(screen.queryByText(/activate the service to download/i)).toBeNull();
  });

  it('forces and locks the pre-restore backup for overwrite restore', async () => {
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

    await fireEvent.click(within(dialog).getByLabelText(/overwrite existing database/i));

    const backupToggle = within(dialog).getByRole('switch', {
      name: 'Pre-restore backup',
    }) as HTMLButtonElement;
    expect(backupToggle.getAttribute('aria-checked')).toBe('true');
    expect(backupToggle.disabled).toBe(true);

    const confirm = within(dialog).getByRole('button', {
      name: 'Restore backup',
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);

    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await waitFor(() => expect(confirm.disabled).toBe(false));
    await fireEvent.click(confirm);

    await waitFor(() =>
      expect(db.restoreServiceBackup).toHaveBeenCalledWith('mysql', 'bkp_1', {
        overwrite: true,
        pre_restore_backup: true,
        target: 'app',
        confirm: 'app',
      }),
    );
  });

  it('restores into a new database with no pre-restore backup by default', async () => {
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

    const backupToggle = within(dialog).getByRole('switch', {
      name: 'Pre-restore backup',
    }) as HTMLButtonElement;
    expect(backupToggle.getAttribute('aria-checked')).toBe('false');
    expect(backupToggle.disabled).toBe(false);

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
          pre_restore_backup: false,
          target: expect.stringMatching(/^app_restore_/),
        }),
      ),
    );
  });

  it('shows an inactive-service error instead of creating a backup while stopped', async () => {
    render(BackupsTab, {
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

  it('requires linked sites to be stopped before overwriting', async () => {
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: [
          {
            ...MYSQL_DATABASES[0],
            linked_sites: [{ id: 'site_blog', name: 'blog' }],
          },
        ],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(within(dialog).getByLabelText(/overwrite existing database/i));
    expect(within(dialog).getByText('blog')).toBeTruthy();

    const confirm = within(dialog).getByRole('button', {
      name: 'Restore backup',
    }) as HTMLButtonElement;
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    expect(confirm.disabled).toBe(true);

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Stop site blog' }));
    await waitFor(() => expect(api.stopSite).toHaveBeenCalledWith('site_blog'));
    await waitFor(() => expect(confirm.disabled).toBe(false));
  });

  it('offers the pre-operation backup after a failed restore', async () => {
    vi.mocked(db.restoreServiceBackup)
      .mockResolvedValueOnce({
        ...RUNNING_JOB,
        kind: 'restore',
        status: 'failed',
        progress: 60,
        message: 'restore failed midway',
        backup_id: 'bkp_1',
      })
      .mockResolvedValueOnce({
        ...RUNNING_JOB,
        kind: 'restore',
        status: 'succeeded',
        progress: 100,
      });

    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: MYSQL_DATABASES,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));
    const restoreDialog = await screen.findByRole('dialog');
    await fireEvent.input(within(restoreDialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(
      within(restoreDialog).getByRole('button', { name: 'Restore backup' }),
    );

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
      expect(db.restoreServiceBackup).toHaveBeenLastCalledWith(
        'mysql',
        'bkp_1',
        expect.objectContaining({
          overwrite: false,
          confirm: 'app',
          pre_restore_backup: false,
          target: expect.stringMatching(/^app_recovery_/),
        }),
      ),
    );
  });

  it('disables overwrite restore when the database inventory is unavailable', async () => {
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: [],
        databasesLoading: true,
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));
    const dialog = await screen.findByRole('dialog');
    await fireEvent.click(within(dialog).getByLabelText(/overwrite existing database/i));
    await fireEvent.input(within(dialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });

    expect(within(dialog).getByText(/database inventory is unavailable/i)).toBeTruthy();
    expect(
      (within(dialog).getByRole('button', { name: 'Restore backup' }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(db.restoreServiceBackup).not.toHaveBeenCalled();
  });

  it('restores the recovery card after a simulated reload', async () => {
    const failedJob: DatabaseJob = {
      ...RUNNING_JOB,
      id: 'job_restore_reload',
      kind: 'restore',
      status: 'failed',
      progress: 60,
      message: 'restore failed midway',
      backup_id: 'bkp_1',
    };
    vi.mocked(db.restoreServiceBackup).mockResolvedValue(failedJob);
    const props = {
      service: MYSQL_SERVICE,
      backups: MYSQL_BACKUPS,
      databases: MYSQL_DATABASES,
      onRefresh: vi.fn(),
    };
    const first = render(BackupsTab, { props });
    await fireEvent.click(screen.getByRole('button', { name: /restore app backup/i }));
    const restoreDialog = await screen.findByRole('dialog');
    await fireEvent.input(within(restoreDialog).getByLabelText(/type .app. to confirm/i), {
      target: { value: 'app' },
    });
    await fireEvent.click(
      within(restoreDialog).getByRole('button', { name: 'Restore backup' }),
    );

    expect(await screen.findByText(/restore incomplete/i)).toBeTruthy();
    expect(loadRecoveryJobID('mysql')).toBe(failedJob.id);
    first.unmount();

    vi.mocked(db.getDatabaseJob).mockResolvedValue(failedJob);
    render(BackupsTab, { props });

    expect(await screen.findByText(/restore incomplete/i)).toBeTruthy();
  });

  it('reports a canceled restore job as an informational toast', async () => {
    vi.mocked(db.restoreServiceBackup).mockResolvedValue({
      ...RUNNING_JOB,
      kind: 'restore',
      status: 'canceled',
      message: 'canceled by user',
    });
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

    await waitFor(() => {
      const toast = get(toasts).find((entry) => entry.title === 'restore canceled');
      expect(toast?.kind).toBe('info');
    });
    expect(screen.getByText('canceled by user')).toBeTruthy();
  });

  it('excludes protected and missing databases from backup creation', async () => {
    const protectedDatabase: Database = {
      name: 'devstack',
      service_id: 'mysql',
      engine: 'mysql',
      size_bytes: 1_000_000,
      table_count: 2,
      linked_sites: [],
      managed: false,
    };
    const missingDatabase: Database = {
      ...MYSQL_DATABASES[0],
      name: 'gone',
      missing: true,
    };
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: MYSQL_BACKUPS,
        databases: [protectedDatabase, missingDatabase, ...MYSQL_DATABASES],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Create backup' }));
    const select = screen.getByLabelText('Database') as HTMLSelectElement;

    expect([...select.options].map((option) => option.value)).toEqual(['app']);
  });

  it('does not allow overwriting a protected database', async () => {
    const protectedBackup: DatabaseBackup = {
      ...MYSQL_BACKUPS[0],
      id: 'bkp_devstack',
      database: 'devstack',
    };
    render(BackupsTab, {
      props: {
        service: MYSQL_SERVICE,
        backups: [protectedBackup],
        databases: [
          {
            name: 'devstack',
            service_id: 'mysql',
            engine: 'mysql',
            size_bytes: 1_000_000,
            table_count: 2,
            linked_sites: [],
            managed: false,
          },
        ],
        onRefresh: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /restore devstack backup/i }));
    const overwrite = screen.getByLabelText(/overwrite existing database/i) as HTMLInputElement;
    expect(overwrite.disabled).toBe(true);
  });

  it('toasts a backup job that finishes during polling', async () => {
    vi.mocked(db.createServiceBackup).mockResolvedValue(RUNNING_JOB);
    vi.mocked(db.getDatabaseJob)
      .mockResolvedValueOnce({ ...RUNNING_JOB, progress: 60 })
      .mockResolvedValueOnce({
        ...RUNNING_JOB,
        status: 'succeeded',
        progress: 100,
        message: 'backup complete',
      });

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

    await waitFor(
      () => {
        const toast = get(toasts).find((entry) => entry.title === 'backup succeeded');
        expect(toast?.kind).toBe('success');
      },
      { timeout: 5000 },
    );
    expect(screen.getByText('backup complete')).toBeTruthy();
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
