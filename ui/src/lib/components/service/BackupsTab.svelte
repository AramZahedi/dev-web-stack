<script lang="ts">
  import { onDestroy } from 'svelte';
  import { Archive, Download, RotateCcw, Trash2 } from 'lucide-svelte';
  import * as api from '../../api';
  import * as dbApi from '../../databaseApi';
  import { describeError, type DescribedError } from '../../errors';
  import { checksumPrefix, formatBytes, formatDateTime } from '../../format';
  import { addToast, toastError } from '../../stores';
  import type {
    Database,
    DatabaseBackup,
    DatabaseJob,
    DatabaseSiteRef,
  } from '../../databaseTypes';
  import type { ServiceView } from '../../types';
  import Badge from '../Badge.svelte';
  import Button from '../Button.svelte';
  import Card from '../Card.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import EmptyState from '../EmptyState.svelte';
  import ErrorBanner from '../ErrorBanner.svelte';
  import Modal from '../Modal.svelte';
  import Toggle from '../Toggle.svelte';
  import JobRecovery from './JobRecovery.svelte';
  import LinkedSitesStop from './LinkedSitesStop.svelte';
  import { isProtectedDatabase } from './databaseProtection';
  import { saveBlob } from './fileDownload';
  import { jobToastKind } from './jobStatus';
  import { restoreDatabaseName } from './recovery';
  import {
    allLinkedSitesStopped,
    readLinkedSiteStates,
    type LinkedSiteState,
  } from './linkedSites';

  interface Props {
    service: ServiceView;
    backups: DatabaseBackup[];
    databases?: Database[];
    databasesLoading?: boolean;
    databasesError?: string | null;
    loading?: boolean;
    onRefresh: () => void;
    onjobchange?: (active: boolean) => void;
  }

  let {
    service,
    backups,
    databases = [],
    databasesLoading = false,
    databasesError = null,
    loading = false,
    onRefresh,
    onjobchange,
  }: Props = $props();

  const active = $derived(service.active);
  const inventoryUnavailable = $derived(databasesLoading || Boolean(databasesError));

  let actionError = $state('');
  let linkedStates = $state<Record<string, LinkedSiteState>>({});
  let stoppingSites = $state<Record<string, boolean>>({});
  let siteStopError = $state('');

  let createOpen = $state(false);
  let createDatabase = $state('');
  let creating = $state(false);
  let createError = $state<DescribedError | null>(null);

  let restoreTarget = $state<DatabaseBackup | null>(null);
  let restoreMode = $state<'new' | 'overwrite'>('new');
  let restoreName = $state('');
  let preRestoreBackup = $state(false);
  let restoring = $state(false);
  let restoreError = $state<DescribedError | null>(null);

  let deleteTarget = $state<DatabaseBackup | null>(null);
  let deleting = $state(false);
  let deleteError = $state<DescribedError | null>(null);

  let job = $state<DatabaseJob | null>(null);
  let jobError = $state<DescribedError | null>(null);
  let jobTimer: ReturnType<typeof setInterval> | undefined;

  const databaseOptions = $derived.by(() => {
    if (databases.length > 0) {
      return databases
        .filter(
          (database) =>
            !database.missing && !isProtectedDatabase(database.engine, database.name),
        )
        .map((database) => database.name);
    }
    return [
      ...new Set(
        backups
          .filter((backup) => !isProtectedDatabase(backup.engine, backup.database))
          .map((backup) => backup.database),
      ),
    ];
  });

  const restoreProtected = $derived(
    restoreTarget ? isProtectedDatabase(restoreTarget.engine, restoreTarget.database) : false,
  );

  const restoreDatabase = $derived(
    restoreTarget ? (databases.find((database) => database.name === restoreTarget?.database) ?? null) : null,
  );
  const restoreSites = $derived<DatabaseSiteRef[]>(restoreDatabase?.linked_sites ?? []);
  const totalBackupBytes = $derived(
    backups.reduce((total, backup) => total + backup.size_bytes, 0),
  );
  const restoreNameError = $derived.by(() => {
    if (restoreMode !== 'new') return '';
    if (!restoreName) return 'A target database name is required.';
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(restoreName)) {
      return 'Use letters, numbers and underscores; start with a letter or underscore.';
    }
    if (restoreName.length > 63) return 'Database names are limited to 63 characters.';
    return '';
  });

  async function prepareLinkedSites(sites: DatabaseSiteRef[] | undefined): Promise<void> {
    linkedStates = {};
    stoppingSites = {};
    siteStopError = '';
    linkedStates = await readLinkedSiteStates(sites);
  }

  function sitesReady(sites: DatabaseSiteRef[] | undefined): boolean {
    return allLinkedSitesStopped(sites, linkedStates);
  }

  async function stopLinkedSite(site: DatabaseSiteRef): Promise<void> {
    if (stoppingSites[site.id]) return;
    stoppingSites = { ...stoppingSites, [site.id]: true };
    siteStopError = '';
    try {
      await api.stopSite(site.id);
      linkedStates = { ...linkedStates, [site.id]: 'stopped' };
      addToast({
        kind: 'success',
        title: `${site.name} stopped`,
        message: 'The site is stopped; the overwrite restore can proceed.',
      });
    } catch (caught) {
      siteStopError = describeError(caught).message;
    } finally {
      stoppingSites = { ...stoppingSites, [site.id]: false };
    }
  }

  function jobRunning(candidate: DatabaseJob | null): boolean {
    return candidate?.status === 'queued' || candidate?.status === 'running';
  }

  function stopJobPolling(): void {
    if (jobTimer) {
      clearInterval(jobTimer);
      jobTimer = undefined;
    }
    onjobchange?.(false);
  }

  async function pollJob(id: string): Promise<void> {
    try {
      const next = await dbApi.getDatabaseJob(id);
      if (!jobRunning(next)) {
        finishJob(next);
        return;
      }
      job = next;
      jobError = null;
    } catch (caught) {
      jobError = describeError(caught);
      stopJobPolling();
    }
  }

  function finishJob(next: DatabaseJob): void {
    job = next;
    jobError = null;
    stopJobPolling();
    addToast({
      kind: jobToastKind(next.status),
      title: `${next.kind} ${next.status}`,
      message: next.message || `The ${next.kind} job for ${next.database} finished.`,
    });
    onRefresh();
  }

  function watchJob(next: DatabaseJob): void {
    job = next;
    jobError = null;
    if (!jobRunning(next)) {
      finishJob(next);
      return;
    }
    stopJobPolling();
    onjobchange?.(true);
    jobTimer = setInterval(() => void pollJob(next.id), 1200);
  }

  async function cancelJob(): Promise<void> {
    if (!job) return;
    try {
      await dbApi.cancelDatabaseJob(job.id);
      job = { ...job, status: 'canceled' };
      stopJobPolling();
      addToast({
        kind: 'info',
        title: 'Job canceled',
        message: 'The target may be partially modified; the pre-operation backup is the recovery path.',
      });
      onRefresh();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not cancel the job', `${described.code}: ${described.message}`);
    }
  }

  onDestroy(stopJobPolling);

  function openCreate(): void {
    if (!active) {
      actionError = 'Activate the service to create a backup.';
      return;
    }
    actionError = '';
    createError = null;
    createDatabase = databaseOptions[0] ?? '';
    createOpen = true;
  }

  async function confirmCreate(): Promise<void> {
    if (!createDatabase || creating) return;
    creating = true;
    createError = null;
    try {
      const next = await dbApi.createServiceBackup(service.id, createDatabase, { kind: 'manual' });
      createOpen = false;
      watchJob(next);
    } catch (caught) {
      createError = describeError(caught);
    } finally {
      creating = false;
    }
  }

  function defaultRestoreName(database: string): string {
    return restoreDatabaseName(database);
  }

  function beginRestore(backup: DatabaseBackup): void {
    if (!active) {
      actionError = 'Activate the service to restore backups.';
      return;
    }
    actionError = '';
    restoreError = null;
    restoreMode = 'new';
    restoreName = defaultRestoreName(backup.database);
    preRestoreBackup = false;
    restoreTarget = backup;
    const linked = databases.find((database) => database.name === backup.database)?.linked_sites;
    void prepareLinkedSites(linked);
  }

  function setRestoreMode(mode: 'new' | 'overwrite'): void {
    if (mode === 'overwrite' && restoreProtected) return;
    restoreMode = mode;
    // An overwrite always takes a pre-restore backup; the toggle is locked on.
    preRestoreBackup = mode === 'overwrite';
    if (mode === 'overwrite') void prepareLinkedSites(restoreSites);
  }

  async function confirmRestore(): Promise<void> {
    const target = restoreTarget;
    if (!target || restoring) return;
    if (restoreMode === 'overwrite' && isProtectedDatabase(target.engine, target.database)) return;
    if (restoreMode === 'overwrite' && !sitesReady(restoreSites)) return;
    restoring = true;
    restoreError = null;
    try {
      const next = await dbApi.restoreServiceBackup(service.id, target.id, {
        target: restoreMode === 'overwrite' ? target.database : restoreName,
        overwrite: restoreMode === 'overwrite',
        confirm: target.database,
        pre_restore_backup: restoreMode === 'overwrite' ? true : preRestoreBackup,
      });
      restoreTarget = null;
      addToast({
        kind: 'success',
        title: `Restore queued for ${target.database}`,
        message:
          restoreMode === 'overwrite'
            ? 'The target is dropped and recreated after the pre-restore backup.'
            : `The dump is restored into ${restoreName}.`,
      });
      watchJob(next);
    } catch (caught) {
      restoreError = describeError(caught);
    } finally {
      restoring = false;
    }
  }

  async function downloadBackup(backup: DatabaseBackup): Promise<void> {
    try {
      const blob = await dbApi.downloadServiceBackup(service.id, backup.id);
      saveBlob(blob, `${service.id}-${backup.database}-${backup.id}.sql.gz`);
      addToast({
        kind: 'success',
        title: `Downloading ${backup.database} backup`,
        message: 'The gzip-compressed SQL dump is downloading to this browser.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not download ${backup.database}`, `${described.code}: ${described.message}`);
    }
  }

  function openDelete(backup: DatabaseBackup): void {
    deleteError = null;
    deleteTarget = backup;
  }

  async function confirmDelete(): Promise<void> {
    const target = deleteTarget;
    if (!target || deleting) return;
    deleting = true;
    deleteError = null;
    try {
      await dbApi.deleteServiceBackup(service.id, target.id);
      deleteTarget = null;
      addToast({
        kind: 'success',
        title: `Backup ${target.id} deleted`,
        message: 'The database itself was not touched.',
      });
      onRefresh();
    } catch (caught) {
      deleteError = describeError(caught);
    } finally {
      deleting = false;
    }
  }
</script>

<div class="backups-tab">
  {#if !active}
    <p class="inactive-note" role="status">
      Backups remain available to download and delete while the service is stopped. Activate the
      service to create or restore backups.
    </p>
  {/if}

  {#if actionError}
    <p class="action-error" role="alert">{actionError}</p>
  {/if}

  {#if job}
    <div class="job-card" role="status" aria-label="Database job">
      <div class="job-head">
        <strong>{job.kind} · {job.database}</strong>
        <Badge
          variant={job.status === 'failed'
            ? 'danger'
            : job.status === 'succeeded'
              ? 'accent'
              : job.status === 'canceled'
                ? 'warning'
                : 'info'}
        >
          {job.status}
        </Badge>
      </div>
      <div
        class="progress"
        role="progressbar"
        aria-label={`${job.kind} progress`}
        aria-valuenow={job.progress}
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <span style:width={`${Math.max(0, Math.min(100, job.progress))}%`}></span>
      </div>
      <p class="job-message">{job.message || 'Waiting for the daemon to report progress.'}</p>
      <JobRecovery {service} {job} onrecovered={watchJob} />
      {#if jobRunning(job)}
        <Button size="sm" variant="ghost" onclick={cancelJob}>Cancel job</Button>
      {/if}
    </div>
  {/if}

  {#if jobError}
    <ErrorBanner
      title="Could not read the database job"
      code={jobError.code}
      message={jobError.message}
      ondismiss={() => (jobError = null)}
    />
  {/if}

  <Card
    title="Backups"
    subtitle="Logical SQL dumps stored on the platform. Listing, downloading and deleting work while the service is stopped."
    headerWrap
  >
    {#snippet actions()}
      <Button size="sm" variant="primary" onclick={openCreate}>
        <Archive size={13} strokeWidth={2} aria-hidden="true" />
        Create backup
      </Button>
      <Badge>{formatBytes(totalBackupBytes)} total</Badge>
    {/snippet}

    {#if loading && backups.length === 0}
      <p class="loading-copy" aria-busy="true">Loading backups…</p>
    {:else if backups.length === 0}
      <EmptyState
        icon={Archive}
        title="No backups yet"
        description="Create a manual backup before a risky migration or drop."
        compact
      >
        <Button size="sm" variant="primary" onclick={openCreate}>Create backup</Button>
      </EmptyState>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">Database</th>
              <th scope="col">Created</th>
              <th scope="col">Size</th>
              <th scope="col">Kind</th>
              <th scope="col">Status</th>
              <th scope="col">SHA-256</th>
              <th scope="col"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each backups as backup (backup.id)}
              <tr>
                <td class="mono db-name">{backup.database}</td>
                <td>{formatDateTime(backup.created_at)}</td>
                <td class="mono">{formatBytes(backup.size_bytes)}</td>
                <td><Badge>{backup.kind.replace(/_/g, ' ')}</Badge></td>
                <td>
                  <Badge
                    variant={backup.status === 'ready'
                      ? 'accent'
                      : backup.status === 'failed'
                        ? 'danger'
                        : 'info'}
                  >
                    {backup.status}
                  </Badge>
                  {#if backup.error}
                    <p class="backup-error" role="alert">{backup.error}</p>
                  {/if}
                </td>
                <td class="mono checksum" title={backup.sha256}>
                  {checksumPrefix(backup.sha256)}
                </td>
                <td>
                  <div class="row-actions">
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Download ${backup.database} backup ${backup.id}`}
                      onclick={() => downloadBackup(backup)}
                    >
                      <Download size={12} strokeWidth={2} aria-hidden="true" />
                      Download
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Restore ${backup.database} backup ${backup.id}`}
                      onclick={() => beginRestore(backup)}
                    >
                      <RotateCcw size={12} strokeWidth={2} aria-hidden="true" />
                      Restore
                    </Button>
                    <Button
                      size="sm"
                      variant="danger-ghost"
                      ariaLabel={`Delete ${backup.database} backup ${backup.id}`}
                      onclick={() => openDelete(backup)}
                    >
                      <Trash2 size={12} strokeWidth={2} aria-hidden="true" />
                      Delete
                    </Button>
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</div>

<Modal
  open={createOpen}
  title="Create backup"
  description="Creates a gzip-compressed logical dump under the platform backups tree."
  size="sm"
  onclose={() => {
    if (!creating) createOpen = false;
  }}
>
  <div class="create-body">
    <label class="field-label" for="backup-database">Database</label>
    <select id="backup-database" class="input mono" bind:value={createDatabase}>
      {#each databaseOptions as option (option)}
        <option value={option}>{option}</option>
      {/each}
    </select>
    {#if databaseOptions.length === 0}
      <p class="dialog-error" role="alert">No databases are available to back up.</p>
    {/if}
    {#if createError}
      <p class="dialog-error" role="alert">
        <span class="mono">{createError.code}</span>
        {createError.message}
      </p>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={() => (createOpen = false)} disabled={creating}>
      Cancel
    </Button>
    <Button variant="primary" onclick={confirmCreate} loading={creating} disabled={databaseOptions.length === 0}>
      Create backup
    </Button>
  {/snippet}
</Modal>

<ConfirmDialog
  open={restoreTarget !== null}
  title={restoreTarget ? `Restore ${restoreTarget.database} backup?` : 'Restore backup?'}
  description="A restore can be applied to a new database or overwrite the existing one. Linked sites must be stopped for an overwrite."
  confirmLabel="Restore backup"
  danger={restoreMode === 'overwrite'}
  busy={restoring}
  requireText={restoreTarget?.database}
  confirmDisabled={restoreMode === 'overwrite'
    ? inventoryUnavailable || !sitesReady(restoreSites)
    : Boolean(restoreNameError)}
  onconfirm={confirmRestore}
  oncancel={() => {
    if (!restoring) restoreTarget = null;
  }}
>
  {#snippet details()}
    <div class="restore-details">
      <fieldset class="restore-modes">
        <legend>Restore mode</legend>
        <label class="radio-row">
          <input
            type="radio"
            name="restore-mode"
            value="new"
            checked={restoreMode === 'new'}
            onchange={() => setRestoreMode('new')}
          />
          <span>
            <strong>Restore to a new database (default)</strong>
            <span class="radio-copy">The source database and its links stay untouched.</span>
          </span>
        </label>
        <label class="radio-row">
          <input
            type="radio"
            name="restore-mode"
            value="overwrite"
            checked={restoreMode === 'overwrite'}
            disabled={restoreProtected}
            onchange={() => setRestoreMode('overwrite')}
          />
          <span>
            <strong>Overwrite existing database</strong>
            <span class="radio-copy">
              {#if restoreProtected}
                Protected system databases cannot be overwritten. Restore into a new database.
              {:else}
                Drops and recreates {restoreTarget?.database ?? 'the target'} after the pre-restore
                backup.
              {/if}
            </span>
          </span>
        </label>
      </fieldset>

      {#if restoreMode === 'new'}
        <label class="field-label" for="restore-target">New database name</label>
        <input id="restore-target" class="input mono" type="text" bind:value={restoreName} />
        {#if restoreNameError}
          <p class="dialog-error" role="alert">{restoreNameError}</p>
        {/if}
      {:else}
        <p class="overwrite-target">
          Target: <code class="mono">{restoreTarget?.database}</code>
        </p>
        {#if inventoryUnavailable}
          <p class="dialog-error" role="alert">
            Database inventory is unavailable. Overwrite restore is disabled until the database
            list loads.
          </p>
        {/if}
        <LinkedSitesStop
          sites={restoreSites}
          states={linkedStates}
          stopping={stoppingSites}
          error={siteStopError}
          onstop={stopLinkedSite}
        />
      {/if}

      <Toggle
        bind:checked={preRestoreBackup}
        label="Pre-restore backup"
        disabled={restoreMode === 'overwrite'}
        description={restoreMode === 'overwrite'
          ? 'Required for overwrite restores so a failure has a one-click recovery.'
          : 'Optional for a new database; back up the target before the restore.'}
      />

      {#if restoreError}
        <p class="dialog-error" role="alert">
          <span class="mono">{restoreError.code}</span>
          {restoreError.message}
        </p>
      {/if}
    </div>
  {/snippet}
</ConfirmDialog>

<ConfirmDialog
  open={deleteTarget !== null}
  title={deleteTarget ? `Delete backup ${deleteTarget.id}?` : 'Delete backup?'}
  description="Only the backup file is removed. The database, its users and its links are untouched."
  confirmLabel="Delete backup"
  danger
  busy={deleting}
  requireText={deleteTarget?.id}
  onconfirm={confirmDelete}
  oncancel={() => {
    if (!deleting) deleteTarget = null;
  }}
>
  {#snippet details()}
    {#if deleteError}
      <p class="dialog-error" role="alert">
        <span class="mono">{deleteError.code}</span>
        {deleteError.message}
      </p>
    {/if}
  {/snippet}
</ConfirmDialog>

<style>
  .backups-tab {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .inactive-note {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-left: 3px solid var(--warning);
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .action-error {
    padding: 8px 10px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .job-card {
    display: flex;
    flex-direction: column;
    gap: 7px;
    padding: 11px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-1);
  }

  .job-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    font-size: 12px;
  }

  .progress {
    height: 6px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--surface-3);
  }

  .progress span {
    display: block;
    height: 100%;
    background: var(--accent);
    transition: width 220ms var(--ease);
  }

  .job-message {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .loading-copy {
    color: var(--muted);
    font-size: 12px;
  }

  .table-wrap {
    overflow-x: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }

  th {
    padding: 0 8px 7px;
    border-bottom: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-align: left;
    text-transform: uppercase;
    white-space: nowrap;
  }

  td {
    padding: 9px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--fg);
    vertical-align: middle;
  }

  tbody tr:last-child td {
    border-bottom: 0;
  }

  .db-name {
    font-weight: 600;
  }

  .checksum {
    color: var(--muted);
    font-size: 10.5px;
  }

  .backup-error {
    margin-top: 3px;
    color: var(--danger-fg);
    font-size: 10.5px;
    line-height: 1.4;
  }

  .row-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 2px;
  }

  .create-body,
  .restore-details {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .field-label {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .restore-modes {
    display: flex;
    flex-direction: column;
    gap: 7px;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .restore-modes legend {
    margin-bottom: 6px;
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .radio-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--fg);
    font-size: 11.5px;
    line-height: 1.45;
    cursor: pointer;
  }

  .radio-row input {
    margin-top: 2px;
    accent-color: var(--accent);
  }

  .radio-row strong {
    display: block;
    font-size: 11.5px;
    font-weight: 600;
  }

  .radio-copy {
    display: block;
    margin-top: 1px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .overwrite-target {
    color: var(--muted);
    font-size: 11.5px;
  }

  .dialog-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .dialog-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  @media (max-width: 720px) {
    .row-actions {
      justify-content: flex-start;
    }
  }
</style>
