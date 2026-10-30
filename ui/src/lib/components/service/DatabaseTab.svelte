<script lang="ts">
  import { onDestroy } from 'svelte';
  import {
    Archive,
    Database as DatabaseIcon,
    Download,
    FileUp,
    Pencil,
    Plus,
    Trash2,
  } from 'lucide-svelte';
  import * as dbApi from '../../databaseApi';
  import { describeError, type DescribedError } from '../../errors';
  import { formatBytes, formatDate } from '../../format';
  import { addToast, toastError } from '../../stores';
  import type {
    Database,
    DatabaseEngine,
    DatabaseJob,
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
  import DatabaseFormDialog from './DatabaseFormDialog.svelte';
  import { saveBlob } from './fileDownload';
  import { jobToastKind } from './jobStatus';

  interface Props {
    service: ServiceView;
    databases: Database[];
    loading?: boolean;
    onRefresh: () => void;
    onjobchange?: (active: boolean) => void;
  }

  let { service, databases, loading = false, onRefresh, onjobchange }: Props = $props();

  function engineFor(id: string): DatabaseEngine {
    if (id === 'postgresql' || id === 'postgres') return 'postgres';
    if (id === 'mariadb') return 'mariadb';
    if (id === 'redis') return 'redis';
    return 'mysql';
  }

  const engine = $derived<DatabaseEngine>(databases[0]?.engine ?? engineFor(service.id));
  const canRename = $derived(engine === 'postgres');
  const active = $derived(service.active);

  let actionError = $state('');
  let unlinkTarget = $state<Database | null>(null);

  let createOpen = $state(false);
  let renameTarget = $state<Database | null>(null);
  let formBusy = $state(false);
  let formError = $state('');

  let dropTarget = $state<Database | null>(null);
  let backupFirst = $state(true);
  let dropping = $state(false);
  let dropError = $state<DescribedError | null>(null);

  let importTarget = $state<Database | null>(null);
  let importFile = $state<File | null>(null);
  let importOpen = $state(false);
  let importMode = $state<'merge' | 'replace'>('merge');
  let importBackup = $state(true);
  let importing = $state(false);
  let importError = $state<DescribedError | null>(null);
  let fileInput: HTMLInputElement | undefined = $state();

  let job = $state<DatabaseJob | null>(null);
  let jobError = $state<DescribedError | null>(null);
  let jobTimer: ReturnType<typeof setInterval> | undefined;

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
      job = next;
      jobError = null;
      if (!jobRunning(next)) {
        stopJobPolling();
        onRefresh();
      }
    } catch (caught) {
      jobError = describeError(caught);
      stopJobPolling();
    }
  }

  function watchJob(next: DatabaseJob): void {
    job = next;
    jobError = null;
    if (!jobRunning(next)) {
      addToast({
        kind: jobToastKind(next.status),
        title: `${next.kind} ${next.status}`,
        message: next.message || `The ${next.kind} job for ${next.database} finished.`,
      });
      onRefresh();
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
        message: 'The database may be partially modified; check the target before retrying.',
      });
      onRefresh();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not cancel the job', `${described.code}: ${described.message}`);
    }
  }

  onDestroy(stopJobPolling);

  async function submitCreate(name: string): Promise<void> {
    if (formBusy) return;
    formBusy = true;
    formError = '';
    try {
      await dbApi.createServiceDatabase(service.id, { name });
      createOpen = false;
      addToast({
        kind: 'success',
        title: `Database ${name} created`,
        message: 'The database belongs to the service and is ready to link to a site.',
      });
      onRefresh();
    } catch (caught) {
      formError = describeError(caught).message;
    } finally {
      formBusy = false;
    }
  }

  async function submitRename(name: string): Promise<void> {
    const target = renameTarget;
    if (!target || formBusy) return;
    formBusy = true;
    formError = '';
    try {
      await dbApi.renameServiceDatabase(service.id, target.name, { name });
      renameTarget = null;
      addToast({
        kind: 'success',
        title: `Database renamed to ${name}`,
        message: 'Restart linked sites so they reconnect to the renamed database.',
      });
      onRefresh();
    } catch (caught) {
      formError = describeError(caught).message;
    } finally {
      formBusy = false;
    }
  }

  function openRename(database: Database): void {
    formError = '';
    renameTarget = database;
  }

  function openDrop(database: Database): void {
    dropError = null;
    backupFirst = true;
    dropTarget = database;
  }

  async function confirmDrop(): Promise<void> {
    const target = dropTarget;
    if (!target || dropping) return;
    dropping = true;
    dropError = null;
    try {
      await dbApi.dropServiceDatabase(service.id, target.name, {
        confirm: target.name,
        backup_first: backupFirst,
      });
      dropTarget = null;
      addToast({
        kind: 'success',
        title: `Database ${target.name} dropped`,
        message: backupFirst
          ? 'A pre-drop backup was requested before the database was removed.'
          : 'The database was removed without a backup.',
      });
      onRefresh();
    } catch (caught) {
      dropError = describeError(caught);
    } finally {
      dropping = false;
    }
  }

  async function backupDatabase(database: Database): Promise<void> {
    if (!active) {
      actionError = 'Activate the service to back up databases.';
      return;
    }
    actionError = '';
    try {
      const next = await dbApi.createServiceBackup(service.id, database.name, { kind: 'manual' });
      watchJob(next);
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not back up ${database.name}`, `${described.code}: ${described.message}`);
    }
  }

  async function exportDatabase(database: Database): Promise<void> {
    if (!active) {
      actionError = 'Activate the service to export databases.';
      return;
    }
    actionError = '';
    try {
      const blob = await dbApi.exportServiceDatabase(service.id, database.name);
      const stamp = new Date().toISOString().replace(/[:.]/g, '-');
      saveBlob(blob, `${service.id}-${database.name}-${stamp}.sql.gz`);
      addToast({
        kind: 'success',
        title: `Exporting ${database.name}`,
        message: 'The SQL dump is downloading to this browser.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not export ${database.name}`, `${described.code}: ${described.message}`);
    }
  }

  function beginImport(database: Database): void {
    if (!active) {
      actionError = 'Activate the service to import databases.';
      return;
    }
    actionError = '';
    importTarget = database;
    importError = null;
    fileInput?.click();
  }

  function onImportFileSelected(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    input.value = '';
    if (!file || !importTarget) return;
    importFile = file;
    importMode = 'merge';
    importBackup = true;
    importError = null;
    importOpen = true;
  }

  async function confirmImport(): Promise<void> {
    const target = importTarget;
    const file = importFile;
    if (!target || !file || importing) return;
    importing = true;
    importError = null;
    try {
      const next = await dbApi.importServiceDatabase(service.id, target.name, file, {
        mode: importMode,
        confirm: target.name,
        pre_import_backup: importBackup,
      });
      importOpen = false;
      importTarget = null;
      importFile = null;
      addToast({
        kind: 'success',
        title: `Import queued for ${target.name}`,
        message:
          importMode === 'replace'
            ? 'Replace mode drops and recreates the target after the pre-import backup.'
            : 'Merge mode pipes the dump into the existing schema.',
      });
      watchJob(next);
    } catch (caught) {
      importError = describeError(caught);
    } finally {
      importing = false;
    }
  }

  function closeImport(): void {
    if (importing) return;
    importOpen = false;
    importFile = null;
    importError = null;
  }
</script>

<div class="database-tab">
  {#if !active}
    <p class="inactive-note" role="status">
      The service is stopped. Activate the service to create, rename, back up, import or export
      databases. Existing databases and backups remain visible.
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
    title="Databases"
    subtitle="Service-wide databases. Deleting a site removes only its links, never a database."
    headerWrap
  >
    {#snippet actions()}
      <Button
        size="sm"
        variant="primary"
        disabled={!active}
        title={active ? 'Create a database' : 'Activate the service first'}
        onclick={() => {
          formError = '';
          createOpen = true;
        }}
      >
        <Plus size={13} strokeWidth={2} aria-hidden="true" />
        Create database
      </Button>
    {/snippet}

    {#if loading && databases.length === 0}
      <p class="loading-copy" aria-busy="true">Loading databases…</p>
    {:else if databases.length === 0}
      <EmptyState
        icon={DatabaseIcon}
        title="No databases yet"
        description="Create a database here, then link it to one or more sites."
        compact
      >
        <Button
          size="sm"
          variant="primary"
          disabled={!active}
          onclick={() => {
            formError = '';
            createOpen = true;
          }}
        >
          Create database
        </Button>
      </EmptyState>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">Database</th>
              <th scope="col">Size</th>
              <th scope="col">Tables</th>
              <th scope="col">Linked sites</th>
              <th scope="col">Created</th>
              <th scope="col"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each databases as database (database.name)}
              <tr class:missing={database.missing}>
                <td>
                  <span class="db-name mono">{database.name}</span>
                  {#if database.missing}
                    <Badge variant="warning">Missing on engine</Badge>
                  {/if}
                  {#if !database.managed}
                    <Badge>External</Badge>
                  {/if}
                </td>
                <td class="mono">{formatBytes(database.size_bytes)}</td>
                <td class="mono">{database.table_count}</td>
                <td>
                  {#if database.linked_sites?.length}
                    <div class="site-links">
                      {#each database.linked_sites as site (site.id)}
                        <a
                          class="site-link"
                          href={`#/sites/${site.id}`}
                          aria-label={`Open ${site.name}`}
                        >
                          {site.name}
                        </a>
                      {/each}
                    </div>
                    {#if database.missing}
                      <Button
                        size="sm"
                        variant="ghost"
                        ariaLabel={`Unlink ${database.name}`}
                        onclick={() => (unlinkTarget = database)}
                      >
                        Unlink
                      </Button>
                    {/if}
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                <td>{formatDate(database.created_at)}</td>
                <td>
                  <div class="row-actions">
                    {#if canRename}
                      <Button
                        size="sm"
                        variant="ghost"
                        ariaLabel={`Rename ${database.name}`}
                        disabled={!active || database.missing}
                        onclick={() => openRename(database)}
                      >
                        <Pencil size={12} strokeWidth={2} aria-hidden="true" />
                        Rename
                      </Button>
                    {/if}
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Back up ${database.name}`}
                      disabled={database.missing}
                      onclick={() => backupDatabase(database)}
                    >
                      <Archive size={12} strokeWidth={2} aria-hidden="true" />
                      Backup
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Export ${database.name}`}
                      disabled={database.missing}
                      onclick={() => exportDatabase(database)}
                    >
                      <Download size={12} strokeWidth={2} aria-hidden="true" />
                      Export
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Import ${database.name}`}
                      disabled={database.missing}
                      onclick={() => beginImport(database)}
                    >
                      <FileUp size={12} strokeWidth={2} aria-hidden="true" />
                      Import
                    </Button>
                    <Button
                      size="sm"
                      variant="danger-ghost"
                      ariaLabel={`Drop ${database.name}`}
                      disabled={!active || database.missing}
                      onclick={() => openDrop(database)}
                    >
                      <Trash2 size={12} strokeWidth={2} aria-hidden="true" />
                      Drop
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

  <input
    class="file-input"
    type="file"
    accept=".sql,.sql.gz"
    tabindex="-1"
    aria-hidden="true"
    bind:this={fileInput}
    onchange={onImportFileSelected}
  />
</div>

<DatabaseFormDialog
  open={createOpen}
  mode="create"
  busy={formBusy}
  error={formError}
  onsubmit={submitCreate}
  onclose={() => {
    if (!formBusy) createOpen = false;
  }}
/>

<DatabaseFormDialog
  open={renameTarget !== null}
  mode="rename"
  database={renameTarget}
  busy={formBusy}
  error={formError}
  onsubmit={submitRename}
  onclose={() => {
    if (!formBusy) renameTarget = null;
  }}
/>

<ConfirmDialog
  open={dropTarget !== null}
  title={dropTarget ? `Drop database ${dropTarget.name}?` : 'Drop database?'}
  description="The database and its tables are permanently removed. Users, grants and existing backups are not deleted."
  confirmLabel="Drop database"
  danger
  busy={dropping}
  requireText={dropTarget?.name}
  onconfirm={confirmDrop}
  oncancel={() => {
    if (!dropping) dropTarget = null;
  }}
>
  {#snippet details()}
    <div class="drop-details">
      {#if dropTarget?.linked_sites?.length}
        <p class="drop-warning" role="alert">
          Linked sites must be stopped before dropping this database:
          {dropTarget.linked_sites.map((site) => site.name).join(', ')}. Stop them from each site's
          detail page; devstack never stops sites silently.
        </p>
      {/if}
      <Toggle
        bind:checked={backupFirst}
        label="Back up first"
        description="Create a pre-drop backup before removing the database."
      />
      {#if dropError}
        <p class="drop-error" role="alert">
          <span class="mono">{dropError.code}</span>
          {dropError.message}
        </p>
      {/if}
    </div>
  {/snippet}
</ConfirmDialog>

<Modal
  open={unlinkTarget !== null}
  title={unlinkTarget ? `Unlink sites from ${unlinkTarget.name}` : 'Unlink sites'}
  description="Remove the link from each site's Databases card. A database that is missing on the engine is never recreated automatically."
  size="sm"
  onclose={() => (unlinkTarget = null)}
>
  {#if unlinkTarget}
    <ul class="unlink-list">
      {#each unlinkTarget.linked_sites ?? [] as site (site.id)}
        <li>
          <a class="site-link" href={`#/sites/${site.id}`}>Open {site.name}</a>
        </li>
      {/each}
    </ul>
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={() => (unlinkTarget = null)}>Close</Button>
  {/snippet}
</Modal>

<Modal
  open={importOpen}
  title={importTarget ? `Import into ${importTarget.name}` : 'Import database'}
  description="Upload a .sql or .sql.gz dump. Files are staged on the agent and deleted after the job."
  size="md"
  onclose={closeImport}
>
  <div class="import-body">
    <p class="import-file mono">{importFile?.name ?? 'No file selected'}</p>

    <fieldset class="import-modes">
      <legend>Import mode</legend>
      <label class="radio-row">
        <input
          type="radio"
          name="import-mode"
          value="merge"
          checked={importMode === 'merge'}
          onchange={() => (importMode = 'merge')}
        />
        <span>
          <strong>Merge (default)</strong>
          <span class="radio-copy">Pipe the dump into the existing schema.</span>
        </span>
      </label>
      <label class="radio-row">
        <input
          type="radio"
          name="import-mode"
          value="replace"
          checked={importMode === 'replace'}
          onchange={() => (importMode = 'replace')}
        />
        <span>
          <strong>Replace contents</strong>
          <span class="radio-copy">
            Drop and recreate the target after a pre-import backup. Linked sites must be stopped.
          </span>
        </span>
      </label>
    </fieldset>

    <Toggle
      bind:checked={importBackup}
      label="Back up first"
      description="Create a pre-import backup before the dump is applied."
    />

    {#if importMode === 'replace'}
      <p class="drop-warning" role="alert">
        Replace mode removes the current schema. The pre-import backup is offered as a one-click
        recovery if the job fails.
      </p>
    {/if}

    {#if importError}
      <p class="drop-error" role="alert">
        <span class="mono">{importError.code}</span>
        {importError.message}
      </p>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={closeImport} disabled={importing}>Cancel</Button>
    <Button variant="primary" onclick={confirmImport} loading={importing}>
      Import database
    </Button>
  {/snippet}
</Modal>

<style>
  .database-tab {
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

  tr.missing td {
    background: color-mix(in srgb, var(--warning-soft) 55%, transparent);
  }

  .db-name {
    margin-right: 6px;
    font-weight: 600;
  }

  .site-links {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .site-link {
    padding: 1px 6px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--fg);
    font-size: 10.5px;
    text-decoration: none;
  }

  .site-link:hover {
    border-color: var(--control-border);
    background: var(--surface-3);
  }

  .muted {
    color: var(--muted-2);
  }

  .row-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 2px;
  }

  .file-input {
    display: none;
  }

  .unlink-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .drop-details,
  .import-body {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .drop-warning {
    padding: 8px 9px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .drop-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .drop-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }

  .import-file {
    padding: 7px 9px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    font-size: 11px;
    overflow-wrap: anywhere;
  }

  .import-modes {
    display: flex;
    flex-direction: column;
    gap: 7px;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .import-modes legend {
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
