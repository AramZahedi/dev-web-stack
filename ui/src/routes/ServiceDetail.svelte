<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Archive,
    ArrowLeft,
    Database,
    KeyRound,
    Power,
    PowerOff,
    RefreshCw,
    RotateCw,
    Server,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import * as dbApi from '../lib/databaseApi';
  import { describeError, type DescribedError } from '../lib/errors';
  import { formatBytes } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type {
    Database as DatabaseRecord,
    DatabaseBackup,
    DatabaseUser,
  } from '../lib/databaseTypes';
  import type { ServiceView } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import BackupsTab from '../lib/components/service/BackupsTab.svelte';
  import DatabaseTab from '../lib/components/service/DatabaseTab.svelte';
  import RedisTab from '../lib/components/service/RedisTab.svelte';
  import UsersTab from '../lib/components/service/UsersTab.svelte';

  interface Props {
    params: { id?: string };
  }

  let { params }: Props = $props();

  type TabID = 'databases' | 'users' | 'backups' | 'password';
  type ServiceAction = 'install' | 'activate' | 'deactivate' | 'restart';

  interface TabDef {
    id: TabID;
    label: string;
    icon: typeof Database;
  }

  const serviceID = $derived(params?.id ?? '');

  let service = $state<ServiceView | null>(null);
  let loading = $state(true);
  let notFound = $state(false);
  let error = $state<DescribedError | null>(null);
  let busy = $state<ServiceAction | null>(null);

  let databases = $state<DatabaseRecord[]>([]);
  let databasesLoading = $state(false);
  let databasesError = $state<DescribedError | null>(null);
  let users = $state<DatabaseUser[]>([]);
  let usersLoading = $state(false);
  let usersError = $state<DescribedError | null>(null);
  let backups = $state<DatabaseBackup[]>([]);
  let backupsLoading = $state(false);
  let backupsError = $state<DescribedError | null>(null);

  let tab = $state<TabID>('databases');
  let selectedUserId = $state('');
  let jobActive = $state(false);
  let deactivateOpen = $state(false);
  let deactivating = $state(false);
  let deactivateError = $state<DescribedError | null>(null);

  let loadGeneration = 0;
  let loadedID = '';
  let queryApplied = false;
  let pollAttempts = 0;

  function isRedis(view: ServiceView | null): boolean {
    return view?.id === 'redis';
  }

  function isSQL(view: ServiceView | null): boolean {
    if (!view) return false;
    return view.group === 'mysql-family' || view.id === 'postgresql' || view.id === 'mariadb';
  }

  function defaultTab(view: ServiceView): TabID {
    return isRedis(view) ? 'users' : 'databases';
  }

  function tabsFor(view: ServiceView | null): TabDef[] {
    if (!view) return [];
    if (isRedis(view)) {
      return [
        { id: 'users', label: 'Users', icon: KeyRound },
        { id: 'password', label: 'Password', icon: KeyRound },
      ];
    }
    if (isSQL(view)) {
      return [
        { id: 'databases', label: 'Databases', icon: Database },
        { id: 'users', label: 'Users', icon: KeyRound },
        { id: 'backups', label: 'Backups', icon: Archive },
      ];
    }
    return [];
  }

  const tabs = $derived(tabsFor(service));
  const activeTabLabel = $derived(
    tabs.find((entry) => entry.id === tab)?.label ?? 'Database management',
  );
  const transient = $derived(
    Boolean(service && (service.state === 'starting' || service.state === 'stopping')) || jobActive,
  );
  const affectedSites = $derived(
    Array.from(
      new Map(
        databases
          .flatMap((database) => database.linked_sites ?? [])
          .map((site) => [site.id, site] as const),
      ).values(),
    ),
  );
  const backupBytes = $derived(
    backups.reduce((total, backup) => total + backup.size_bytes, 0),
  );
  const runningBackups = $derived(
    backups.filter((backup) => backup.status === 'running').length,
  );

  function routeQuery(): { tab: string; user: string } {
    const hash = window.location.hash || '';
    const index = hash.indexOf('?');
    if (index < 0) return { tab: '', user: '' };
    const query = new URLSearchParams(hash.slice(index + 1));
    return { tab: query.get('tab') ?? '', user: query.get('user') ?? '' };
  }

  function applyRouteQuery(view: ServiceView): void {
    if (queryApplied) return;
    queryApplied = true;
    const query = routeQuery();
    const allowed = tabsFor(view).map((entry) => entry.id);
    tab = allowed.includes(query.tab as TabID) ? (query.tab as TabID) : defaultTab(view);
    selectedUserId = query.user;
  }

  async function loadDatabases(id: string, generation: number): Promise<void> {
    databasesLoading = true;
    databasesError = null;
    try {
      const result = await dbApi.listServiceDatabases(id);
      if (generation !== loadGeneration) return;
      databases = result.databases ?? [];
    } catch (caught) {
      if (generation !== loadGeneration) return;
      databasesError = describeError(caught);
    } finally {
      if (generation === loadGeneration) databasesLoading = false;
    }
  }

  async function loadUsers(id: string, generation: number): Promise<void> {
    usersLoading = true;
    usersError = null;
    try {
      const result = await dbApi.listServiceUsers(id);
      if (generation !== loadGeneration) return;
      users = result.users ?? [];
    } catch (caught) {
      if (generation !== loadGeneration) return;
      usersError = describeError(caught);
    } finally {
      if (generation === loadGeneration) usersLoading = false;
    }
  }

  async function loadBackups(id: string, generation: number): Promise<void> {
    backupsLoading = true;
    backupsError = null;
    try {
      const result = await dbApi.listServiceBackups(id);
      if (generation !== loadGeneration) return;
      backups = result.backups ?? [];
    } catch (caught) {
      if (generation !== loadGeneration) return;
      backupsError = describeError(caught);
    } finally {
      if (generation === loadGeneration) backupsLoading = false;
    }
  }

  async function load(initial = false): Promise<void> {
    const id = serviceID;
    const generation = ++loadGeneration;
    if (initial) {
      loading = true;
      service = null;
      databases = [];
      users = [];
      backups = [];
      databasesError = null;
      usersError = null;
      backupsError = null;
      databasesLoading = false;
      usersLoading = false;
      backupsLoading = false;
      error = null;
      notFound = false;
      busy = null;
      jobActive = false;
      queryApplied = false;
      pollAttempts = 0;
    }
    error = null;
    notFound = false;
    if (!id) {
      notFound = true;
      loading = false;
      return;
    }
    try {
      const next = await api.getService(id);
      if (generation !== loadGeneration || id !== serviceID) return;
      service = next;
      applyRouteQuery(next);
      if (isRedis(next)) {
        await loadUsers(id, generation);
      } else if (isSQL(next)) {
        await Promise.all([
          loadDatabases(id, generation),
          loadUsers(id, generation),
          loadBackups(id, generation),
        ]);
      }
    } catch (caught) {
      if (generation !== loadGeneration || id !== serviceID) return;
      const described = describeError(caught);
      if (described.code === 'NOT_FOUND') {
        notFound = true;
      } else {
        error = described;
      }
    } finally {
      if (generation === loadGeneration) loading = false;
    }
  }

  async function act(action: ServiceAction): Promise<void> {
    if (!service || busy) return;
    const target = service;
    busy = action;
    try {
      if (action === 'install') await api.installService(target.id);
      if (action === 'activate') await api.activateService(target.id);
      if (action === 'deactivate') await api.deactivateService(target.id);
      if (action === 'restart') await api.restartService(target.id);
      addToast({
        kind: 'success',
        title: `${target.display_name || target.id} ${
          action === 'deactivate'
            ? 'deactivated'
            : action === 'install'
              ? 'installed'
              : action === 'restart'
                ? 'restarted'
                : 'activated'
        }`,
        message: 'The service list is refreshing from the daemon.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError(
        `Could not ${action} ${target.display_name || target.id}`,
        `${described.code}: ${described.message}`,
      );
    } finally {
      busy = null;
    }
  }

  function openDeactivate(): void {
    deactivateError = null;
    deactivateOpen = true;
  }

  async function confirmDeactivate(): Promise<void> {
    if (!service || deactivating) return;
    const target = service;
    deactivating = true;
    deactivateError = null;
    try {
      await api.deactivateService(target.id);
      deactivateOpen = false;
      addToast({
        kind: 'success',
        title: `${target.display_name || target.id} deactivated`,
        message: 'Links, databases, users and backups were preserved.',
      });
      await load();
    } catch (caught) {
      deactivateError = describeError(caught);
    } finally {
      deactivating = false;
    }
  }

  function refresh(): void {
    void load();
  }

  function onTabKeydown(event: KeyboardEvent, index: number): void {
    if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft') return;
    event.preventDefault();
    const offset = event.key === 'ArrowRight' ? 1 : -1;
    const next = tabs[(index + offset + tabs.length) % tabs.length];
    if (!next) return;
    tab = next.id;
    document.getElementById(`tab-${next.id}`)?.focus();
  }

  function onJobChange(active: boolean): void {
    jobActive = active;
  }

  $effect(() => {
    if (loadedID === serviceID) return;
    loadedID = serviceID;
    void load(true);
  });

  onMount(() => {
    const listener = () => void load();
    window.addEventListener('devstack:refresh', listener);
    return () => window.removeEventListener('devstack:refresh', listener);
  });

  $effect(() => {
    if (!transient) {
      pollAttempts = 0;
      return;
    }
    pollAttempts = 0;
    const timer = setInterval(() => {
      pollAttempts += 1;
      if (pollAttempts > 60) {
        clearInterval(timer);
        return;
      }
      void load();
    }, 1500);
    return () => clearInterval(timer);
  });
</script>

<div class="page">
  <a class="back" href="#/services">
    <ArrowLeft size={14} strokeWidth={2} aria-hidden="true" />
    All services
  </a>

  {#if loading}
    <div class="loading" aria-busy="true" aria-label="Loading service">
      <Skeleton width="38%" height="22px" />
      <Skeleton width="62%" height="12px" />
      <Skeleton width="100%" height="72px" radius="10px" />
    </div>
  {:else if notFound}
    <div class="state-card">
      <EmptyState
        icon={Server}
        title="Service not found"
        description="The service id in this link does not exist in the catalog."
      >
        <Button variant="secondary" onclick={() => (window.location.hash = '#/services')}>
          Back to services
        </Button>
      </EmptyState>
    </div>
  {:else if error}
    <ErrorBanner
      title="Could not load service"
      code={error.code}
      message={error.message}
      onretry={() => load(true)}
    />
  {:else if service}
    <header class="detail-head">
      <div class="title-block">
        <div class="title-row">
          <span class="service-icon" class:active={service.active} aria-hidden="true">
            {#if isRedis(service)}
              <KeyRound size={19} strokeWidth={1.8} />
            {:else}
              <Database size={19} strokeWidth={1.8} />
            {/if}
          </span>
          <div class="title-copy">
            <h1>{service.display_name || service.id}</h1>
            <p class="service-id mono">{service.id}</p>
          </div>
        </div>
        <div class="badges">
          <Badge variant={service.installed ? 'accent' : 'neutral'}>
            {service.installed ? 'Installed' : 'Not installed'}
          </Badge>
          {#if service.installed}
            <Badge variant={service.active ? 'accent' : 'neutral'}>
              {service.active ? 'Active' : 'Inactive'}
            </Badge>
          {/if}
          {#if service.health}
            <Badge variant="info">Health: {service.health}</Badge>
          {/if}
        </div>
      </div>
      <div class="head-actions">
        {#if !service.installed}
          <Button
            size="sm"
            variant="info"
            loading={busy === 'install'}
            disabled={Boolean(busy)}
            onclick={() => act('install')}
          >
            Install
          </Button>
        {:else if service.active}
          <Button
            size="sm"
            variant="secondary"
            loading={busy === 'deactivate'}
            disabled={Boolean(busy)}
            onclick={openDeactivate}
          >
            <PowerOff size={13} strokeWidth={2} aria-hidden="true" />
            Deactivate
          </Button>
          <Button
            size="sm"
            variant="ghost"
            loading={busy === 'restart'}
            disabled={Boolean(busy)}
            onclick={() => act('restart')}
          >
            <RotateCw size={13} strokeWidth={2} aria-hidden="true" />
            Restart
          </Button>
        {:else}
          <Button
            size="sm"
            variant="primary"
            loading={busy === 'activate'}
            disabled={Boolean(busy)}
            onclick={() => act('activate')}
          >
            <Power size={13} strokeWidth={2} aria-hidden="true" />
            Activate
          </Button>
        {/if}
        <Button size="sm" variant="ghost" disabled={Boolean(busy)} onclick={refresh}>
          <RefreshCw size={13} strokeWidth={2} aria-hidden="true" />
          Refresh
        </Button>
      </div>
    </header>

    {#if service.installed && !service.active}
      <p class="offline-note" role="status">
        This service is installed but stopped. Database mutations, backups, restores and imports
        require an active service; existing backups remain available to download and delete.
      </p>
    {/if}

    {#if databasesError || usersError || backupsError}
      <div class="inventory-errors">
        {#if databasesError}
          <ErrorBanner
            title="Could not load databases"
            code={databasesError.code}
            message={databasesError.message}
            onretry={refresh}
          />
        {/if}
        {#if usersError}
          <ErrorBanner
            title="Could not load users"
            code={usersError.code}
            message={usersError.message}
            onretry={refresh}
          />
        {/if}
        {#if backupsError}
          <ErrorBanner
            title="Could not load backups"
            code={backupsError.code}
            message={backupsError.message}
            onretry={refresh}
          />
        {/if}
      </div>
    {/if}

    {#if tabs.length === 0}
      <div class="state-card">
        <EmptyState
          icon={Server}
          title="No database management"
          description="This service does not expose a database or user inventory."
        />
      </div>
    {:else}
      <div class="tabs" role="tablist" aria-label="Database management">
        {#each tabs as entry, index (entry.id)}
          <button
            type="button"
            role="tab"
            id={`tab-${entry.id}`}
            class="tab"
            aria-selected={tab === entry.id}
            aria-controls={tab === entry.id ? `panel-${entry.id}` : undefined}
            tabindex={tab === entry.id ? 0 : -1}
            onclick={() => (tab = entry.id)}
            onkeydown={(event) => onTabKeydown(event, index)}
          >
            <entry.icon size={14} strokeWidth={1.9} aria-hidden="true" />
            {entry.label}
          </button>
        {/each}
      </div>

      <div
        class="tab-panel"
        role="tabpanel"
        id={`panel-${tab}`}
        aria-labelledby={`tab-${tab}`}
        aria-label={activeTabLabel}
        tabindex="0"
      >
        {#if tab === 'databases'}
          <DatabaseTab
            {service}
            {databases}
            loading={databasesLoading}
            onRefresh={refresh}
            onjobchange={onJobChange}
          />
        {:else if tab === 'users'}
          {#if isRedis(service)}
            <RedisTab {service} {users} loading={usersLoading} onRefresh={refresh} />
          {:else}
            <UsersTab
              {service}
              {users}
              {databases}
              loading={usersLoading}
              {selectedUserId}
              onRefresh={refresh}
            />
          {/if}
        {:else if tab === 'backups'}
          <BackupsTab
            {service}
            {backups}
            {databases}
            loading={backupsLoading}
            onRefresh={refresh}
            onjobchange={onJobChange}
          />
        {:else if tab === 'password'}
          <RedisTab {service} {users} loading={usersLoading} onRefresh={refresh} section="password" />
        {/if}
      </div>
    {/if}
  {:else}
    <div class="state-card">
      <EmptyState
        icon={Server}
        title="Service unavailable"
        description="The daemon did not return this service."
      />
    </div>
  {/if}
</div>

<ConfirmDialog
  open={deactivateOpen}
  title={service ? `Deactivate ${service.display_name || service.id}?` : 'Deactivate service?'}
  description="Sites linked to this service keep their links but show a database-offline warning until it is activated again."
  confirmLabel="Deactivate service"
  danger
  busy={deactivating}
  onconfirm={confirmDeactivate}
  oncancel={() => {
    if (!deactivating) deactivateOpen = false;
  }}
>
  {#snippet details()}
    <div class="deactivate-details">
      <p>
        <strong>Linked sites:</strong>
        {affectedSites.length > 0
          ? affectedSites.map((site) => site.name).join(', ')
          : 'None'}
      </p>
      <p>
        <strong>Backups:</strong>
        {backups.length} backup{backups.length === 1 ? '' : 's'} ({formatBytes(backupBytes)})
      </p>
      <p>
        <strong>Running backup jobs:</strong>
        {runningBackups}
        {jobActive ? ' plus an active database job' : ''}
      </p>
      <p class="deactivate-note">
        Links are kept. Databases, users, grants, passwords and backups are not deleted.
      </p>
      {#if deactivateError}
        <p class="deactivate-error" role="alert">
          <span class="mono">{deactivateError.code}</span>
          {deactivateError.message}
        </p>
      {/if}
    </div>
  {/snippet}
</ConfirmDialog>

<style>
  .page {
    display: flex;
    flex-direction: column;
    gap: 18px;
    padding: 24px;
    max-width: 1200px;
    margin: 0 auto;
    width: 100%;
  }

  .back {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    align-self: flex-start;
    color: var(--muted);
    font-size: 12px;
    text-decoration: none;
  }

  .back:hover {
    color: var(--fg);
  }

  .loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .detail-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 14px;
    padding: 18px;
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .title-block {
    display: flex;
    flex-direction: column;
    gap: 9px;
    min-width: 0;
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 11px;
  }

  .service-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 40px;
    height: 40px;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--surface-2);
    color: var(--muted);
  }

  .service-icon.active {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .title-copy {
    min-width: 0;
  }

  h1 {
    font-size: 19px;
    font-weight: 600;
    letter-spacing: -0.015em;
  }

  .service-id {
    margin-top: 2px;
    color: var(--muted-2);
    font-size: 10.5px;
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
  }

  .head-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .inventory-errors {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .deactivate-details {
    display: flex;
    flex-direction: column;
    gap: 7px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .deactivate-details strong {
    color: var(--fg);
    font-weight: 600;
  }

  .deactivate-note {
    padding: 8px 9px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .deactivate-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
  }

  .deactivate-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }

  .offline-note {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-left: 3px solid var(--warning);
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .tabs {
    display: flex;
    align-items: center;
    gap: 4px;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
  }

  .tab {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--muted);
    font-family: var(--font-sans);
    font-size: 12.5px;
    font-weight: 500;
    white-space: nowrap;
  }

  .tab:hover {
    color: var(--fg);
  }

  .tab[aria-selected='true'] {
    border-bottom-color: var(--accent);
    color: var(--fg);
  }

  .tab-panel {
    min-width: 0;
  }

  .tab-panel:focus {
    outline: none;
  }

  .tab-panel > :global(*) + :global(*) {
    margin-top: 14px;
  }

  .state-card {
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  @media (max-width: 720px) {
    .page {
      padding: 16px;
    }

    .detail-head {
      flex-direction: column;
    }
  }
</style>
