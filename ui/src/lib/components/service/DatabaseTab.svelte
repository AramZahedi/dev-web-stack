<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import {
    Archive,
    Database as DatabaseIcon,
    Download,
    FileUp,
    Pencil,
    Plus,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../../api';
  import * as dbApi from '../../databaseApi';
  import { describeError, type DescribedError } from '../../errors';
  import { formatBytes, formatDate } from '../../format';
  import { addToast, toastError } from '../../stores';
  import type {
    Database,
    DatabaseEngine,
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
  import Field from '../Field.svelte';
  import Modal from '../Modal.svelte';
  import Toggle from '../Toggle.svelte';
  import DatabaseFormDialog from './DatabaseFormDialog.svelte';
  import JobRecovery from './JobRecovery.svelte';
  import LinkedSitesStop from './LinkedSitesStop.svelte';
  import { isProtectedDatabase } from './databaseProtection';
  import { saveBlob } from './fileDownload';
  import { jobToastKind } from './jobStatus';
  import { isRecoveryJob } from './recovery';
  import {
    clearRecoveryJobID,
    loadRecoveryJobID,
    saveRecoveryJobID,
  } from './recoveryStore';
  import {
    allLinkedSitesStopped,
    readLinkedSiteStates,
    type LinkedSiteState,
  } from './linkedSites';

  interface Props {
    service: ServiceView;
    databases: Database[];
    loading?: boolean;
    onRefresh: () => void;
    onjobchange?: (active: boolean, job?: DatabaseJob | null) => void;
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

  let linkedStates = $state<Record<string, LinkedSiteState>>({});
  let stoppingSites = $state<Record<string, boolean>>({});
  let siteStopError = $state('');
  let restartPrompt = $state<{ label: string; sites: DatabaseSiteRef[] } | null>(null);
  let restartingSite = $state<string | null>(null);

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
  let importConfirm = $state('');
  let importing = $state(false);
  let importError = $state<DescribedError | null>(null);
  let fileInput: HTMLInputElement | undefined = $state();

  let job = $state<DatabaseJob | null>(null);
  let jobError = $state<DescribedError | null>(null);
  let recoveryJob = $state<DatabaseJob | null>(null);
  let jobTimer: ReturnType<typeof setInterval> | undefined;

  function jobRunning(candidate: DatabaseJob | null): boolean {
    return candidate?.status === 'queued' || candidate?.status === 'running';
  }

  function stopJobPolling(): void {
    if (jobTimer) {
      clearInterval(jobTimer);
      jobTimer = undefined;
    }
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
      onjobchange?.(false, null);
    }
  }

  function rememberRecovery(next: DatabaseJob): void {
    if (!isRecoveryJob(next)) return;
    recoveryJob = next;
    saveRecoveryJobID(service.id, next.id);
  }

  function finishJob(next: DatabaseJob): void {
    job = next;
    jobError = null;
    rememberRecovery(next);
    stopJobPolling();
    onjobchange?.(false, null);
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
    onjobchange?.(true, next);
    jobTimer = setInterval(() => void pollJob(next.id), 1200);
  }

  async function cancelJob(): Promise<void> {
    if (!job) return;
    try {
      await dbApi.cancelDatabaseJob(job.id);
      const canceled: DatabaseJob = { ...job, status: 'canceled' };
      job = canceled;
      rememberRecovery(canceled);
      stopJobPolling();
      onjobchange?.(false, null);
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

  onMount(() => {
    const saved = loadRecoveryJobID(service.id);
    if (!saved) return;
    void dbApi
      .getDatabaseJob(saved)
      .then((next) => {
        if (isRecoveryJob(next)) {
          recoveryJob = next;
        } else {
          clearRecoveryJobID(service.id);
        }
      })
      .catch(() => {
        // A missing job simply means there is nothing to recover.
      });
  });

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
    if (!target || formBusy || isProtectedDatabase(engine, target.name)) return;
    if (!sitesReady(target.linked_sites)) return;
    formBusy = true;
    formError = '';
    try {
      await dbApi.renameServiceDatabase(service.id, target.name, {
        name,
        confirm: target.name,
      });
      renameTarget = null;
      addToast({
        kind: 'success',
        title: `Database renamed to ${name}`,
        message: 'Restart linked sites so they reconnect to the renamed database.',
      });
      if (target.linked_sites?.length) {
        restartPrompt = {
          label: `Database renamed to ${name}`,
          sites: [...target.linked_sites],
        };
      }
      onRefresh();
    } catch (caught) {
      formError = describeError(caught).message;
    } finally {
      formBusy = false;
    }
  }

  function openRename(database: Database): void {
    if (isProtectedDatabase(engine, database.name)) return;
    formError = '';
    renameTarget = database;
    void prepareLinkedSites(database.linked_sites);
  }

  function openDrop(database: Database): void {
    if (isProtectedDatabase(engine, database.name)) return;
    dropError = null;
    backupFirst = true;
    dropTarget = database;
    void prepareLinkedSites(database.linked_sites);
  }

  function protectedDatabase(database: Database): boolean {
    return isProtectedDatabase(database.engine, database.name);
  }

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
        message: 'The site is stopped; the destructive operation can proceed.',
      });
    } catch (caught) {
      siteStopError = describeError(caught).message;
    } finally {
      stoppingSites = { ...stoppingSites, [site.id]: false };
    }
  }

  async function restartLinkedSite(site: DatabaseSiteRef): Promise<void> {
    if (restartingSite) return;
    restartingSite = site.id;
    try {
      await api.restartSite(site.id);
      addToast({
        kind: 'success',
        title: `${site.name} restarted`,
        message: 'The site now uses the renamed database.',
      });
      if (restartPrompt) {
        const remaining = restartPrompt.sites.filter((entry) => entry.id !== site.id);
        restartPrompt = remaining.length > 0 ? { ...restartPrompt, sites: remaining } : null;
      }
    } catch (caught) {
      actionError = describeError(caught).message;
    } finally {
      restartingSite = null;
    }
  }

  async function confirmDrop(): Promise<void> {
    const target = dropTarget;
    if (!target || dropping || isProtectedDatabase(engine, target.name)) return;
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
    if (isProtectedDatabase(database.engine, database.name)) {
      actionError = 'Protected system databases cannot be backed up.';
      return;
    }
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
    if (isProtectedDatabase(database.engine, database.name)) {
      actionError = 'Protected system databases cannot be exported.';
      return;
    }
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
    if (isProtectedDatabase(database.engine, database.name)) {
      actionError = 'Protected system databases cannot be imported into.';
      return;
    }
    if (!active) {
      actionError = 'Activate the service to import databases.';
      return;
    }
    actionError = '';
    importTarget = database;
    importError = null;
    void prepareLinkedSites(database.linked_sites);
    fileInput?.click();
  }

  function setImportMode(mode: 'merge' | 'replace'): void {
    importMode = mode;
    importConfirm = '';
    if (mode === 'replace') {
      importBackup = true;
      void prepareLinkedSites(importTarget?.linked_sites);
    }
  }

  const importConfirmationReady = $derived(
    importMode !== 'replace' || importConfirm.trim() === (importTarget?.name ?? ''),
  );

  function onImportFileSelected(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    input.value = '';
    if (!file || !importTarget) return;
    importFile = file;
    importMode = 'merge';
    importBackup = true;
    importConfirm = '';
    importError = null;
    importOpen = true;
  }

  async function confirmImport(): Promise<void> {
    const target = importTarget;
    const file = importFile;
    if (!target || !file || importing) return;
    if (importMode === 'replace' && !importConfirmationReady) return;
    if (importMode === 'replace' && !sitesReady(target.linked_sites)) return;
    importing = true;
    importError = null;
    try {
      const next = await dbApi.importServiceDatabase(service.id, target.name, file, {
        mode: importMode,
        confirm: target.name,
        pre_import_backup: importMode === 'replace' ? true : importBackup,
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

  {#if restartPrompt}
    <div class="restart-prompt" role="status">
      <div class="restart-copy">
        <strong>Restart linked sites to apply.</strong>
        <p>{restartPrompt.label}. Restart each site so it reconnects.</p>
      </div>
      <div class="restart-actions">
        {#each restartPrompt.sites as site (site.id)}
          <Button
            size="sm"
            variant="secondary"
            ariaLabel={`Restart site ${site.name}`}
            loading={restartingSite === site.id}
            onclick={() => restartLinkedSite(site)}
          >
            Restart {site.name}
          </Button>
        {/each}
        <Button size="sm" variant="ghost" onclick={() => (restartPrompt = null)}>
          Dismiss
        </Button>
      </div>
    </div>
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

  {#if recoveryJob}
    <JobRecovery
      {service}
      job={recoveryJob}
      onrecovered={(next) => {
        recoveryJob = null;
        clearRecoveryJobID(service.id);
        watchJob(next);
      }}
      ondismiss={() => {
        recoveryJob = null;
        clearRecoveryJobID(service.id);
      }}
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
                  {#if protectedDatabase(database)}
                    <Badge variant="info">Protected</Badge>
                  {/if}
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
                        disabled={!active || database.missing || protectedDatabase(database)}
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
                      disabled={database.missing || protectedDatabase(database)}
                      onclick={() => backupDatabase(database)}
                    >
                      <Archive size={12} strokeWidth={2} aria-hidden="true" />
                      Backup
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Export ${database.name}`}
                      disabled={database.missing || protectedDatabase(database)}
                      onclick={() => exportDatabase(database)}
                    >
                      <Download size={12} strokeWidth={2} aria-hidden="true" />
                      Export
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaLabel={`Import ${database.name}`}
                      disabled={database.missing || protectedDatabase(database)}
                      onclick={() => beginImport(database)}
                    >
                      <FileUp size={12} strokeWidth={2} aria-hidden="true" />
                      Import
                    </Button>
                    <Button
                      size="sm"
                      variant="danger-ghost"
                      ariaLabel={`Drop ${database.name}`}
                      disabled={!active || database.missing || protectedDatabase(database)}
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
  disabled={!sitesReady(renameTarget?.linked_sites)}
  error={formError}
  onsubmit={submitRename}
  onclose={() => {
    if (!formBusy) renameTarget = null;
  }}
>
  {#snippet details()}
    <LinkedSitesStop
      sites={renameTarget?.linked_sites ?? []}
      states={linkedStates}
      stopping={stoppingSites}
      error={siteStopError}
      onstop={stopLinkedSite}
    />
  {/snippet}
</DatabaseFormDialog>

<ConfirmDialog
  open={dropTarget !== null}
  title={dropTarget ? `Drop database ${dropTarget.name}?` : 'Drop database?'}
  description="The database and its tables are permanently removed. Users, grants and existing backups are not deleted."
  confirmLabel="Drop database"
  danger
  busy={dropping}
  requireText={dropTarget?.name}
  confirmDisabled={!sitesReady(dropTarget?.linked_sites)}
  onconfirm={confirmDrop}
  oncancel={() => {
    if (!dropping) dropTarget = null;
  }}
>
  {#snippet details()}
    <div class="drop-details">
      <LinkedSitesStop
        sites={dropTarget?.linked_sites ?? []}
        states={linkedStates}
        stopping={stoppingSites}
        error={siteStopError}
        onstop={stopLinkedSite}
      />
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
          onchange={() => setImportMode('merge')}
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
          onchange={() => setImportMode('replace')}
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
      disabled={importMode === 'replace'}
      description={importMode === 'replace'
        ? 'Replace mode always creates a pre-import backup before dropping the target.'
        : 'Create a pre-import backup before the dump is applied.'}
    />

    {#if importMode === 'replace'}
      <LinkedSitesStop
        sites={importTarget?.linked_sites ?? []}
        states={linkedStates}
        stopping={stoppingSites}
        error={siteStopError}
        onstop={stopLinkedSite}
      />
      <Field
        id="import-confirm"
        label={`Type "${importTarget?.name ?? ''}" to confirm`}
        hint="Replace mode drops and recreates the target."
      >
        <input
          id="import-confirm"
          class="input mono"
          type="text"
          bind:value={importConfirm}
          autocomplete="off"
          spellcheck="false"
        />
      </Field>
      <p class="drop-warning" role="alert">
        Replace mode removes the current schema. A pre-import backup is always created and offered
        as a one-click recovery if the job fails.
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
    <Button
      variant="primary"
      onclick={confirmImport}
      loading={importing}
      disabled={importMode === 'replace' &&
        (!importConfirmationReady || !sitesReady(importTarget?.linked_sites))}
    >
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

  .restart-prompt {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--info) 35%, var(--border));
    border-left: 3px solid var(--info);
    border-radius: var(--radius);
    background: var(--info-soft);
    color: var(--info-fg);
  }

  .restart-copy {
    min-width: 0;
    font-size: 11.5px;
    line-height: 1.45;
  }

  .restart-copy strong {
    font-weight: 600;
  }

  .restart-copy p {
    margin-top: 2px;
  }

  .restart-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
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
