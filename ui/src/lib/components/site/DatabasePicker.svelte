<script lang="ts">
  import { onMount } from 'svelte';
  import { ChevronDown } from 'lucide-svelte';
  import { listServices } from '../../api';
  import { listServiceDatabases, listServiceUsers } from '../../databaseApi';
  import type {
    CreateSiteDatabaseRequest,
    Database,
    DatabaseUser,
    GrantPreset,
  } from '../../databaseTypes';
  import { describeError } from '../../errors';
  import type { ServiceView } from '../../types';
  import ErrorBanner from '../ErrorBanner.svelte';
  import Field from '../Field.svelte';
  import Toggle from '../Toggle.svelte';
  import { grantLabel, type DatabasePickerSummary } from './databasePickerTypes';
  import { isSelectableDatabase, isSelectableUser } from './databaseIdentifiers';

  interface Props {
    value?: CreateSiteDatabaseRequest | null;
    summary?: DatabasePickerSummary | null;
    siteName?: string;
    errors?: Record<string, string>;
    alwaysOn?: boolean;
    primary?: boolean;
    linkedDatabases?: Array<{ service_id: string; database: string }>;
    onmanageuser?: (serviceID: string, userID: string) => void;
    onclearerror?: (key: string) => void;
  }

  let {
    value = $bindable(null),
    summary = $bindable(null),
    siteName = '',
    errors = {},
    alwaysOn = false,
    primary = true,
    linkedDatabases = [],
    onmanageuser,
    onclearerror,
  }: Props = $props();

  let enabled = $state(false);
  let services = $state<ServiceView[]>([]);
  let servicesLoading = $state(false);
  let servicesLoaded = $state(false);
  let servicesError = $state<{ code: string; message: string } | null>(null);
  let databases = $state<Database[]>([]);
  let users = $state<DatabaseUser[]>([]);
  let dataLoading = $state(false);
  let dataError = $state<{ code: string; message: string } | null>(null);
  let serviceID = $state('');
  let databaseMode = $state<'new' | 'existing'>('new');
  let databaseName = $state('');
  let userMode = $state<'new' | 'existing'>('new');
  let userID = $state('');
  let username = $state('');
  let grant = $state<GrantPreset>('read_write_schema');
  let dataGeneration = 0;

  const SQL_SERVICES = ['mysql', 'mariadb', 'postgres', 'postgresql'];

  const selectable = $derived(
    services.filter(
      (service) =>
        service.installed && service.active && SQL_SERVICES.includes(service.id),
    ),
  );
  const linkedDatabaseKeys = $derived(
    new Set(linkedDatabases.map((link) => `${link.service_id}:${link.database}`)),
  );
  const availableDatabases = $derived(
    databases.filter(
      (database) =>
        isSelectableDatabase(database) &&
        !linkedDatabaseKeys.has(`${serviceID}:${database.name}`),
    ),
  );
  const availableUsers = $derived(users.filter(isSelectableUser));
  const selectedService = $derived(services.find((service) => service.id === serviceID) ?? null);
  const selectedUser = $derived(availableUsers.find((user) => user.id === userID) ?? null);
  const selectedDatabase = $derived(
    databaseMode === 'new' ? databaseName.trim() : databaseName,
  );
  const selectedUserGrant = $derived(
    selectedUser?.grants?.find((candidate) => candidate.database === selectedDatabase) ?? null,
  );
  const userGrants = $derived(selectedUser?.grants ?? []);
  const accessMissing = $derived(
    userMode === 'existing' &&
      Boolean(selectedUser) &&
      Boolean(selectedDatabase) &&
      !selectedUserGrant,
  );

  function slugify(input: string): string {
    return input
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '');
  }

  function defaultName(): string {
    const slug = (slugify(siteName) || 'app').replace(/-/g, '_');
    const safe = /^[A-Za-z_]/.test(slug) ? slug : `db_${slug}`;
    return safe.slice(0, 63);
  }

  function defaultPort(id: string | undefined): number {
    if (id === 'postgres' || id === 'postgresql') return 5432;
    if (id === 'redis') return 6379;
    return 3306;
  }

  function buildSummary(): DatabasePickerSummary | null {
    if (!serviceID) return null;
    const grantPreset: GrantPreset | '' =
      userMode === 'new' ? grant : (selectedUserGrant?.preset ?? '');
    return {
      serviceName: selectedService?.display_name ?? serviceID,
      database: selectedDatabase,
      username: userMode === 'new' ? username.trim() : (selectedUser?.username ?? ''),
      grantLabel: grantPreset ? grantLabel(grantPreset) : 'No access yet',
      host: '127.0.0.1',
      port:
        selectedService?.ports?.find((port) => port.loopback)?.host ??
        defaultPort(selectedService?.id),
      passwordManaged:
        userMode === 'new' ? true : Boolean(selectedUser?.password_managed),
      userManaged: userMode === 'new' ? true : Boolean(selectedUser?.managed),
    };
  }

  function sync(): void {
    if (!enabled) {
      value = null;
      summary = null;
      return;
    }
    const request: CreateSiteDatabaseRequest = {
      service_id: serviceID,
      primary,
    };
    if (databaseMode === 'new') {
      request.new_database = { name: databaseName.trim() };
    } else {
      request.database = databaseName;
    }
    if (userMode === 'new') {
      request.new_user = {
        username: username.trim(),
        grants: [{ database: selectedDatabase, preset: grant }],
      };
      request.grant = grant;
    } else {
      if (userID) request.user_id = userID;
    }
    value = request;
    summary = buildSummary();
  }

  function invalidateData(): void {
    dataGeneration += 1;
    dataLoading = false;
  }

  async function selectService(id: string, reset = true): Promise<void> {
    serviceID = id;
    if (reset) {
      databaseName = databaseMode === 'new' ? defaultName() : '';
      userID = '';
      username = userMode === 'new' ? defaultName() : '';
    }
    databases = [];
    users = [];
    dataError = null;
    sync();

    const generation = ++dataGeneration;
    dataLoading = true;
    try {
      const [databaseResult, userResult] = await Promise.all([
        listServiceDatabases(id),
        listServiceUsers(id),
      ]);
      if (generation !== dataGeneration) return;
      databases = databaseResult?.databases ?? [];
      users = userResult?.users ?? [];
      const visibleDatabases = databases.filter(
        (database) =>
          isSelectableDatabase(database) &&
          !linkedDatabaseKeys.has(`${id}:${database.name}`),
      );
      const visibleUsers = users.filter(isSelectableUser);
      if (
        databaseMode === 'existing' &&
        !visibleDatabases.some((database) => database.name === databaseName)
      ) {
        databaseName = visibleDatabases[0]?.name ?? '';
      }
      if (userMode === 'existing' && !visibleUsers.some((user) => user.id === userID)) {
        userID = visibleUsers[0]?.id ?? '';
      }
      sync();
    } catch (caught) {
      if (generation !== dataGeneration) return;
      dataError = describeError(caught);
      sync();
    } finally {
      if (generation === dataGeneration) dataLoading = false;
    }
  }

  async function loadServices(): Promise<void> {
    servicesLoading = true;
    servicesError = null;
    try {
      services = (await listServices()) ?? [];
      const available = services.filter(
        (service) =>
          service.installed && service.active && SQL_SERVICES.includes(service.id),
      );
      if (available.length === 0) {
        if (!alwaysOn && enabled) enabled = false;
        sync();
      } else if ((alwaysOn || enabled) && serviceID) {
        if (available.some((service) => service.id === serviceID)) {
          await selectService(serviceID, false);
        } else {
          await selectService(available[0].id);
        }
      } else if ((alwaysOn || enabled) && !serviceID) {
        await selectService(available[0].id);
      } else {
        sync();
      }
    } catch (caught) {
      services = [];
      servicesError = describeError(caught);
      if (!alwaysOn) enabled = false;
      invalidateData();
      sync();
    } finally {
      servicesLoading = false;
      servicesLoaded = true;
    }
  }

  function setEnabled(next: boolean): void {
    enabled = next;
    if (!next) {
      invalidateData();
      sync();
      return;
    }
    if (!databaseName) databaseName = defaultName();
    if (!username) username = defaultName();
    if (serviceID) {
      void selectService(serviceID);
    } else if (selectable.length > 0) {
      void selectService(selectable[0].id);
    } else {
      sync();
    }
  }

  function setDatabaseMode(mode: 'new' | 'existing'): void {
    databaseMode = mode;
    databaseName = mode === 'new' ? defaultName() : (availableDatabases[0]?.name ?? '');
    onclearerror?.('database_name');
    sync();
  }

  function setUserMode(mode: 'new' | 'existing'): void {
    userMode = mode;
    username = mode === 'new' ? defaultName() : '';
    userID = mode === 'existing' ? (availableUsers[0]?.id ?? '') : '';
    onclearerror?.('database_user');
    onclearerror?.('database_username');
    sync();
  }

  function handleServiceChange(event: Event): void {
    const id = (event.currentTarget as HTMLSelectElement).value;
    onclearerror?.('database_service');
    if (!id) {
      invalidateData();
      serviceID = '';
      databases = [];
      users = [];
      sync();
      return;
    }
    void selectService(id);
  }

  onMount(() => {
    if (value) {
      enabled = true;
      serviceID = value.service_id;
      databaseMode = value.new_database ? 'new' : 'existing';
      databaseName = value.new_database?.name ?? value.database ?? '';
      userMode = value.new_user ? 'new' : 'existing';
      username = value.new_user?.username ?? '';
      userID = value.user_id ?? '';
      grant = value.grant ?? value.new_user?.grants?.[0]?.preset ?? 'read_write_schema';
    } else if (alwaysOn) {
      enabled = true;
      databaseName = defaultName();
      username = defaultName();
    }
    void loadServices();
  });
</script>

<div class="database-picker" id="database-picker" tabindex="-1">
  <div class="panel-label">
    <h3>Database</h3>
    {#if !alwaysOn}<span>Optional</span>{/if}
  </div>
  {#if !alwaysOn}
    <div class="panel-toggle">
      <Toggle
        label="Create or link a database"
        description="Provision a database and user, or attach an existing one. Nothing is created until you submit."
        bind:checked={enabled}
        onchange={setEnabled}
        disabled={!alwaysOn &&
          (servicesError !== null ||
            (!enabled && servicesLoaded && !servicesError && selectable.length === 0))}
      />
    </div>
  {/if}

  {#if servicesError}
    <ErrorBanner
      title="Could not load database services"
      code={servicesError.code}
      message={servicesError.message}
      onretry={loadServices}
    />
    {#if !alwaysOn}
      <p class="empty-hint">
        No database will be linked. Site creation can continue without a database.
      </p>
    {/if}
  {:else if servicesLoaded && selectable.length === 0}
    <div class="notice">
      <p>No active SQL service is available.</p>
      <p class="notice-copy">
        Install and activate MySQL, MariaDB, or PostgreSQL from
        <a class="link" href="#/services">Services</a>. Site creation can continue without a
        database.
      </p>
    </div>
    {#if !enabled}
      <p class="empty-hint">No database will be linked. You can add one later from Site Detail.</p>
    {/if}
  {:else if !enabled}
    <p class="empty-hint">No database will be linked. You can add one later from Site Detail.</p>
  {:else if servicesLoading}
    <p class="hint">Loading database services…</p>
  {:else}
    <Field
      id="database-service"
      label="Database service"
      required
      error={errors.database_service}
    >
      <div class="select-wrap">
        <select
          id="database-service"
          class="input mono"
          value={serviceID}
          aria-invalid={Boolean(errors.database_service)}
          onchange={handleServiceChange}
        >
          <option value="">Select a service</option>
          {#each selectable as service (service.id)}
            <option value={service.id}>{service.display_name}</option>
          {/each}
        </select>
        <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
      </div>
    </Field>

    {#if dataLoading}
      <p class="hint">Loading databases and users…</p>
    {/if}
    {#if dataError}
      <ErrorBanner
        title="Could not load databases"
        code={dataError.code}
        message={dataError.message}
        onretry={() => void selectService(serviceID)}
      />
    {/if}

    <fieldset class="choice-group">
      <legend>Database source</legend>
      <div class="choice-row">
        <label class="choice">
          <input
            type="radio"
            name="database-mode"
            value="new"
            checked={databaseMode === 'new'}
            onchange={() => setDatabaseMode('new')}
          />
          Create new database
        </label>
        <label class="choice">
          <input
            type="radio"
            name="database-mode"
            value="existing"
            checked={databaseMode === 'existing'}
            onchange={() => setDatabaseMode('existing')}
          />
          Use existing database
        </label>
      </div>
    </fieldset>

    {#if databaseMode === 'new'}
      <Field
        id="database-name"
        label="Database name"
        required
        hint="Letters, numbers and underscores; start with a letter or underscore."
        error={errors.database_name}
      >
        <input
          id="database-name"
          class="input mono"
          type="text"
          autocomplete="off"
          spellcheck="false"
          bind:value={databaseName}
          aria-invalid={Boolean(errors.database_name)}
          oninput={() => {
            onclearerror?.('database_name');
            sync();
          }}
        />
      </Field>
    {:else}
      <Field
        id="database-name"
        label="Existing database"
        required
        error={errors.database_name}
      >
        <div class="select-wrap">
          <select
            id="database-name"
            class="input mono"
            bind:value={databaseName}
            aria-invalid={Boolean(errors.database_name)}
            onchange={() => {
              onclearerror?.('database_name');
              sync();
            }}
          >
            {#if availableDatabases.length === 0}
              <option value="">No databases available</option>
            {/if}
            {#each availableDatabases as database (database.name)}
              <option value={database.name}>
                {database.name}{database.missing ? ' (missing)' : ''}
              </option>
            {/each}
          </select>
          <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
        </div>
      </Field>
    {/if}

    <fieldset class="choice-group">
      <legend>Database user</legend>
      <div class="choice-row">
        <label class="choice">
          <input
            type="radio"
            name="database-user-mode"
            value="new"
            checked={userMode === 'new'}
            onchange={() => setUserMode('new')}
          />
          Create new user
        </label>
        <label class="choice">
          <input
            type="radio"
            name="database-user-mode"
            value="existing"
            checked={userMode === 'existing'}
            onchange={() => setUserMode('existing')}
          />
          Use existing user
        </label>
      </div>
    </fieldset>

    {#if userMode === 'new'}
      <Field
        id="database-username"
        label="Database username"
        required
        hint="The user gets a generated password and a database-scoped grant."
        error={errors.database_username}
      >
        <input
          id="database-username"
          class="input mono"
          type="text"
          autocomplete="off"
          spellcheck="false"
          bind:value={username}
          aria-invalid={Boolean(errors.database_username)}
          oninput={() => {
            onclearerror?.('database_username');
            sync();
          }}
        />
      </Field>

      <Field id="database-grant" label="Grant preset">
        <div class="select-wrap">
          <select id="database-grant" class="input mono" bind:value={grant} onchange={sync}>
            <option value="read_only">Read-only</option>
            <option value="read_write">Read/write</option>
            <option value="read_write_schema">Read/write + schema (recommended)</option>
            <option value="admin">Admin (this database)</option>
          </select>
          <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
        </div>
      </Field>
      <p class="grant-summary">{grantLabel(grant)}</p>
    {:else}
      <Field id="database-user" label="Existing user" required error={errors.database_user}>
        <div class="select-wrap">
          <select
            id="database-user"
            class="input mono"
            bind:value={userID}
            aria-invalid={Boolean(errors.database_user)}
            onchange={() => {
              onclearerror?.('database_user');
              sync();
            }}
          >
            {#if availableUsers.length === 0}
              <option value="">No users available</option>
            {/if}
            {#each availableUsers as user (user.id)}
              <option value={user.id}>
                {user.username}{user.host ? `@${user.host}` : ''}
              </option>
            {/each}
          </select>
          <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
        </div>
      </Field>

      {#if selectedUser}
        <div class="access">
          {#if userGrants.length > 0}
            <table class="grants" aria-label={`Grants for ${selectedUser.username}`}>
              <thead>
                <tr><th>Database</th><th>Grant</th></tr>
              </thead>
              <tbody>
                {#each userGrants as userGrant (userGrant.database)}
                  <tr class:selected={userGrant.database === selectedDatabase}>
                    <td class="mono">{userGrant.database}</td>
                    <td>{grantLabel(userGrant.preset)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
          <p>
            Grant on {selectedDatabase || 'the selected database'}:
            <strong>{selectedUserGrant ? grantLabel(selectedUserGrant.preset) : 'No access'}</strong>
          </p>
          {#if accessMissing}
            <div class="warning" role="status">
              <p>
                {selectedUser.username} does not have access to {selectedDatabase}. devstack will
                not change grants during site creation.
              </p>
              <a
                class="link"
                href={`#/services/${encodeURIComponent(serviceID)}?tab=users&user=${encodeURIComponent(selectedUser.id)}`}
                onclick={() => onmanageuser?.(serviceID, selectedUser.id)}
              >
                Manage database user
              </a>
            </div>
          {/if}
          {#if !selectedUser.password_managed}
            {#if selectedUser.managed}
              <p class="password-note">
                The password is unknown. Reset it after creation from the site's Databases card.
              </p>
            {:else}
              <p class="password-note">
                External password entry is not available in v1. This link will be metadata-only;
                set the password in the engine, then restart the site.
              </p>
            {/if}
          {/if}
        </div>
      {/if}
    {/if}
  {/if}
</div>

<style>
  .database-picker {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--input-bg);
  }

  .panel-label {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: -4px;
  }

  .panel-label h3 {
    margin: 0;
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 600;
  }

  .panel-label span {
    color: var(--muted-2);
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .panel-toggle {
    margin: -2px -2px 0;
  }

  .empty-hint {
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .choice-group {
    margin: 0;
    padding: 0;
    border: 0;
  }

  .choice-group legend {
    margin-bottom: 7px;
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .choice-row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 18px;
  }

  .choice {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--fg);
    font-size: 12px;
  }

  .choice input {
    accent-color: var(--accent);
  }

  .grant-summary {
    margin: -6px 0 0;
    color: var(--muted);
    font-size: 11.5px;
  }

  .access {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .access > p {
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
  }

  .password-note {
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .access strong {
    color: var(--fg);
    font-weight: 600;
  }

  .grants {
    width: 100%;
    border-collapse: collapse;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
    font-size: 11.5px;
  }

  .grants th,
  .grants td {
    padding: 5px 8px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }

  .grants thead th {
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .grants tbody tr:last-child td {
    border-bottom: 0;
  }

  .grants tr.selected td {
    background: var(--accent-soft);
    color: var(--fg);
    font-weight: 500;
  }

  .warning {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .warning p {
    margin: 0;
    color: var(--fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .notice {
    padding: 12px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .notice p {
    margin: 0;
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 500;
  }

  .notice .notice-copy {
    margin-top: 3px;
    color: var(--muted);
    font-size: 11.5px;
    font-weight: 400;
    line-height: 1.5;
  }
</style>
