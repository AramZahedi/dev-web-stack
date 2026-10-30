<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Copy,
    Database,
    ExternalLink,
    Globe,
    KeyRound,
    PackagePlus,
    Power,
    PowerOff,
    RefreshCw,
    RotateCw,
    ScrollText,
    Server,
    Settings2,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { copyText } from '../lib/clipboard';
  import { describeError, type DescribedError } from '../lib/errors';
  import { addToast, toastError } from '../lib/stores';
  import type { ServiceCredentials, ServicePort, ServiceView } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Modal from '../lib/components/Modal.svelte';
  import PerformancePanel from '../lib/components/PerformancePanel.svelte';
  import ServiceConfigModal from '../lib/components/ServiceConfigModal.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  type ServiceAction = 'install' | 'activate' | 'deactivate' | 'restart' | 'uninstall';

  const groups: { id: ServiceView['group']; label: string; description: string }[] = [
    {
      id: 'web',
      label: 'Web servers',
      description: 'One web server owns ports 80 and 443 at a time.',
    },
    {
      id: 'mysql-family',
      label: 'MySQL family',
      description: 'MySQL and MariaDB are mutually exclusive on port 3306.',
    },
    {
      id: 'standalone',
      label: 'Standalone',
      description: 'Independent services that can run alongside each other.',
    },
  ];

  let services = $state<ServiceView[]>([]);
  let loading = $state(true);
  let error = $state<DescribedError | null>(null);
  let busy = $state<string | null>(null);
  let actionErrors = $state<Record<string, DescribedError>>({});
  let logsTarget = $state<ServiceView | null>(null);
  let configTarget = $state<ServiceView | null>(null);
  let logs = $state('');
  let logsLoading = $state(false);
  let logsError = $state<DescribedError | null>(null);
  let logsTail = $state(200);
  let logsGeneration = 0;
  let credentialsTarget = $state<ServiceView | null>(null);
  let credentials = $state<ServiceCredentials | null>(null);
  let credentialsLoading = $state(false);
  let credentialsError = $state<DescribedError | null>(null);
  let revealPassword = $state(false);
  let rotateArmed = $state(false);
  let rotating = $state(false);
  let rotateError = $state<DescribedError | null>(null);
  let uninstallTarget = $state<ServiceView | null>(null);
  let uninstallDeleteData = $state(false);
  let uninstalling = $state(false);
  let uninstallError = $state<DescribedError | null>(null);

  const transient = $derived(
    services.some((service) => service.state === 'starting' || service.state === 'stopping'),
  );
  const installedCount = $derived(services.filter((service) => service.installed).length);
  const activeCount = $derived(services.filter((service) => service.active).length);

  function servicesIn(group: ServiceView['group']): ServiceView[] {
    return services.filter((service) => service.group === group);
  }

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      services = await api.listServices();
    } catch (caught) {
      error = describeError(caught);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load(true);
    const refresh = () => void load();
    window.addEventListener('devstack:refresh', refresh);
    return () => window.removeEventListener('devstack:refresh', refresh);
  });

  $effect(() => {
    if (!transient) return;
    const timer = setInterval(() => void load(), 1500);
    return () => clearInterval(timer);
  });

  function formatPorts(ports?: ServicePort[]): string {
    if (!ports || ports.length === 0) return '—';
    return ports
      .map((port) => {
        if (port.host && port.container) return `${port.host}:${port.container}`;
        return `${port.host || port.container}`;
      })
      .join(', ');
  }

  function showStateBadge(state: string): boolean {
    if (!state) return false;
    const s = state.toLowerCase().replace(/[-_]/g, '');
    return s !== 'active' && s !== 'installed' && s !== 'notinstalled' && s !== 'inactive';
  }

  function stateVariant(
    state: string,
  ): 'accent' | 'neutral' | 'danger' | 'warning' | 'info' {
    switch (state) {
      case 'active':
        return 'accent';
      case 'installed':
      case 'installed_but_stopped':
        return 'info';
      case 'error':
        return 'danger';
      case 'starting':
      case 'stopping':
        return 'warning';
      default:
        return 'neutral';
    }
  }

  function healthVariant(health: string): 'accent' | 'warning' | 'danger' | 'neutral' {
    const value = health.toLowerCase();
    if (!value) return 'neutral';
    if (['healthy', 'ok', 'running', 'up'].includes(value)) return 'accent';
    if (['starting', 'degraded', 'unhealthy', 'warning'].includes(value)) return 'warning';
    return 'danger';
  }

  function isDatabase(service: ServiceView): boolean {
    return service.group === 'mysql-family' || service.id === 'postgresql';
  }

  function canRestart(service: ServiceView): boolean {
    return service.installed && service.active;
  }

  function phpMyAdminURL(service: ServiceView): string {
    const port =
      service.ports?.find((entry) => entry.host === 8082 || entry.container === 8082)?.host ??
      service.ports?.[0]?.host ??
      8082;
    return `http://127.0.0.1:${port}/`;
  }

  function actionError(service: ServiceView): DescribedError | undefined {
    return actionErrors[service.id];
  }

  function clearActionError(id: string): void {
    if (!actionErrors[id]) return;
    const next = { ...actionErrors };
    delete next[id];
    actionErrors = next;
  }

  async function act(service: ServiceView, action: ServiceAction): Promise<void> {
    if (busy) return;
    const label = service.display_name || service.id;
    busy = `${service.id}:${action}`;
    clearActionError(service.id);
    try {
      if (action === 'install') await api.installService(service.id);
      if (action === 'activate') await api.activateService(service.id);
      if (action === 'deactivate') await api.deactivateService(service.id);
      if (action === 'restart') await api.restartService(service.id);
      addToast({
        kind: 'success',
        title: `${label} ${action === 'deactivate' ? 'deactivated' : action === 'install' ? 'installed' : action === 'restart' ? 'restarted' : 'activated'}`,
        message:
          action === 'activate'
            ? 'The container is starting. Status refreshes from the daemon.'
            : 'The service list is refreshing from the daemon.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      actionErrors = { ...actionErrors, [service.id]: described };
      toastError(
        `Could not ${action} ${label}`,
        `${described.code}: ${described.message}`,
      );
    } finally {
      busy = null;
    }
  }

  async function openLogs(service: ServiceView): Promise<void> {
    logsTarget = service;
    logs = '';
    logsError = null;
    logsTail = 200;
    await fetchLogs(service);
  }

  async function fetchLogs(service = logsTarget): Promise<void> {
    if (!service) return;
    const generation = ++logsGeneration;
    logsLoading = true;
    logsError = null;
    try {
      const content = await api.serviceLogs(service.id, logsTail);
      if (generation !== logsGeneration || logsTarget?.id !== service.id) return;
      logs = content;
    } catch (caught) {
      if (generation !== logsGeneration || logsTarget?.id !== service.id) return;
      logsError = describeError(caught);
    } finally {
      if (generation === logsGeneration) logsLoading = false;
    }
  }

  function closeLogs(): void {
    logsTarget = null;
    logsGeneration += 1;
    logsLoading = false;
  }

  async function openCredentials(service: ServiceView): Promise<void> {
    credentialsTarget = service;
    credentials = null;
    credentialsError = null;
    revealPassword = false;
    rotateArmed = false;
    rotateError = null;
    credentialsLoading = true;
    try {
      const result = await api.revealServiceCredentials(service.id);
      if (credentialsTarget?.id !== service.id) return;
      credentials = result;
    } catch (caught) {
      if (credentialsTarget?.id !== service.id) return;
      credentialsError = describeError(caught);
    } finally {
      if (credentialsTarget?.id === service.id) credentialsLoading = false;
    }
  }

  function closeCredentials(): void {
    if (rotating) return;
    credentialsTarget = null;
    credentials = null;
    credentialsError = null;
    rotateArmed = false;
    rotateError = null;
  }

  async function rotateCredentials(): Promise<void> {
    if (!credentialsTarget || rotating) return;
    rotating = true;
    rotateError = null;
    try {
      credentials = await api.rotateServiceCredentials(credentialsTarget.id);
      rotateArmed = false;
      revealPassword = false;
      addToast({
        kind: 'success',
        title: 'Credentials rotated',
        message: 'The service was updated with the new credentials.',
      });
    } catch (caught) {
      rotateError = describeError(caught);
    } finally {
      rotating = false;
    }
  }

  async function copyCredential(label: string, value: string): Promise<void> {
    if (!(await copyText(value))) {
      toastError(`Could not copy ${label}`);
      return;
    }
    addToast({ kind: 'success', title: `${label} copied`, message: 'Clipboard updated.' });
  }

  function openUninstall(service: ServiceView): void {
    uninstallTarget = service;
    uninstallDeleteData = false;
    uninstallError = null;
  }

  async function confirmUninstall(): Promise<void> {
    if (!uninstallTarget || uninstalling) return;
    const target = uninstallTarget;
    uninstalling = true;
    uninstallError = null;
    try {
      const result = await api.uninstallService(target.id, uninstallDeleteData);
      addToast({
        kind: 'success',
        title: `${target.display_name || target.id} uninstalled`,
        message: uninstallDeleteData
          ? result.backup_path
            ? `Data was backed up to ${result.backup_path} before deletion.`
            : 'The container, configuration and data were removed.'
          : 'The container and configuration were removed. Data was kept.',
      });
      uninstallTarget = null;
      await load();
    } catch (caught) {
      uninstallError = describeError(caught);
    } finally {
      uninstalling = false;
    }
  }
</script>

<div class="page">
  <section class="intro" aria-labelledby="services-intro">
    <span class="intro-icon" aria-hidden="true"><Server size={19} strokeWidth={1.8} /></span>
    <div class="intro-copy">
      <h2 id="services-intro">Infrastructure services</h2>
      <p>
        Docker Compose-managed web servers, databases and caches. Every container is pinned to
        the catalog digest, binds loopback where possible, and reports live state from the
        privileged helper.
      </p>
    </div>
    <div class="intro-chips">
      <Badge variant="accent">Loopback isolated</Badge>
      <Badge>{installedCount} installed</Badge>
      <Badge>{activeCount} active</Badge>
    </div>
  </section>

  {#if error}
    <div class="banner">
      <ErrorBanner
        title="Could not load services"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
        ondismiss={services.length > 0 ? () => (error = null) : undefined}
      />
    </div>
  {/if}

  {#if loading}
    <div class="groups" aria-busy="true" aria-label="Loading services">
      {#each groups as group (group.id)}
        <section class="group" aria-label={`${group.label} loading`}>
          <div class="group-head">
            <div>
              <h2>{group.label}</h2>
              <p>{group.description}</p>
            </div>
            <Skeleton width="42px" height="18px" radius="5px" />
          </div>
          <div class="cards">
            {#each Array(2) as _, index (index)}
              <div class="service-card skeleton-card">
                <Skeleton width="44%" height="16px" />
                <Skeleton width="72%" height="10px" />
                <Skeleton width="100%" height="58px" radius="8px" />
                <Skeleton width="60%" height="10px" />
                <span class="skeleton-actions">
                  <Skeleton width="92px" height="30px" radius="8px" />
                  <Skeleton width="92px" height="30px" radius="8px" />
                </span>
              </div>
            {/each}
          </div>
        </section>
      {/each}
    </div>
  {:else if services.length === 0 && !error}
    <div class="state-card">
      <EmptyState
        icon={Server}
        title="No services configured"
        description="No infrastructure services or containers are defined in the catalog yet."
      />
    </div>
  {:else}
    <div class="groups">
      {#each groups as group (group.id)}
        {@const members = servicesIn(group.id)}
        {#if members.length > 0}
          <section class="group" aria-labelledby={`group-${group.id}`}>
            <div class="group-head">
              <div>
                <h2 id={`group-${group.id}`}>{group.label}</h2>
                <p>{group.description}</p>
              </div>
              <span class="group-count mono">{members.length}</span>
            </div>

            <div class="cards">
              {#each members as service (service.id)}
                {@const installed = service.installed}
                {@const active = service.active}
                {@const inFlight = busy?.startsWith(`${service.id}:`) ?? false}
                <article class="service-card" aria-labelledby={`svc-${service.id}`}>
                  <header class="service-head">
                    <span class="service-icon" class:active aria-hidden="true">
                      {#if service.group === 'mysql-family'}
                        <Database size={18} strokeWidth={1.8} />
                      {:else if service.group === 'web'}
                        <Globe size={18} strokeWidth={1.8} />
                      {:else}
                        <Server size={18} strokeWidth={1.8} />
                      {/if}
                    </span>
                    <div class="service-title">
                      <div class="title-row">
                        <h3 id={`svc-${service.id}`}>{service.display_name || service.id}</h3>
                        <span class="service-id mono">{service.id}</span>
                      </div>
                      <div class="badges">
                        <Badge variant={installed ? 'accent' : 'neutral'}>
                          {installed ? 'Installed' : 'Not installed'}
                        </Badge>
                        {#if installed}
                          <Badge variant={active ? 'accent' : 'neutral'}>
                            {active ? 'Active' : 'Inactive'}
                          </Badge>
                        {/if}
                        {#if showStateBadge(service.state)}
                          <Badge variant={stateVariant(service.state)}>{service.state}</Badge>
                        {/if}
                        {#if service.health}
                          <Badge variant={healthVariant(service.health)}>
                            Health: {service.health}
                          </Badge>
                        {/if}
                      </div>
                    </div>
                  </header>

                  <dl class="meta">
                    <div>
                      <dt>Image</dt>
                      <dd class="mono" title={service.image}>{service.image || '—'}</dd>
                    </div>
                    <div>
                      <dt>Ports</dt>
                      <dd class="mono">{formatPorts(service.ports)}</dd>
                    </div>
                    <div>
                      <dt>Container</dt>
                      <dd class="mono" title={service.container_id}>
                        {service.container_id ? service.container_id.slice(0, 12) : '—'}
                      </dd>
                    </div>
                    <div class="wide">
                      <dt>Config</dt>
                      <dd class="mono" title={service.config_path}>{service.config_path || '—'}</dd>
                    </div>
                    <div class="wide">
                      <dt>Data</dt>
                      <dd class="mono" title={service.data_path}>{service.data_path || '—'}</dd>
                    </div>
                  </dl>

                  {#if service.last_error}
                    <p class="service-last-error" role="status">
                      <span class="error-label">Last error</span>
                      {service.last_error}
                    </p>
                  {/if}

                  {#if actionError(service)}
                    <div class="card-error">
                      <ErrorBanner
                        title={`${service.display_name || service.id} action failed`}
                        code={actionError(service)?.code}
                        message={actionError(service)?.message ?? ''}
                        ondismiss={() => clearActionError(service.id)}
                      />
                    </div>
                  {/if}

                  <footer class="service-actions">
                    {#if !installed}
                      <Button
                        size="sm"
                        variant="info"
                        loading={busy === `${service.id}:install`}
                        disabled={Boolean(busy)}
                        onclick={() => act(service, 'install')}
                      >
                        {#if busy !== `${service.id}:install`}
                          <PackagePlus size={13} strokeWidth={2} aria-hidden="true" />
                        {/if}
                        Install
                      </Button>
                    {:else}
                      {#if active}
                        <Button
                          size="sm"
                          variant="secondary"
                          loading={busy === `${service.id}:deactivate`}
                          disabled={Boolean(busy)}
                          onclick={() => act(service, 'deactivate')}
                        >
                          {#if busy !== `${service.id}:deactivate`}
                            <PowerOff size={13} strokeWidth={2} aria-hidden="true" />
                          {/if}
                          Deactivate
                        </Button>
                      {:else}
                        <Button
                          size="sm"
                          variant="primary"
                          loading={busy === `${service.id}:activate`}
                          disabled={Boolean(busy)}
                          onclick={() => act(service, 'activate')}
                        >
                          {#if busy !== `${service.id}:activate`}
                            <Power size={13} strokeWidth={2} aria-hidden="true" />
                          {/if}
                          Activate
                        </Button>
                      {/if}
                      <Button
                        size="sm"
                        variant="ghost"
                        loading={busy === `${service.id}:restart`}
                        disabled={Boolean(busy) || !canRestart(service)}
                        title={canRestart(service) ? 'Restart the container' : 'Activate the service before restarting'}
                        onclick={() => act(service, 'restart')}
                      >
                        {#if busy !== `${service.id}:restart`}
                          <RotateCw size={13} strokeWidth={2} aria-hidden="true" />
                        {/if}
                        Restart
                      </Button>
                    {/if}

                    {#if installed}
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={inFlight}
                        onclick={() => (configTarget = service)}
                      >
                        <Settings2 size={13} strokeWidth={2} aria-hidden="true" />
                        Configuration
                      </Button>
                    {/if}

                    {#if installed}
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={inFlight}
                        onclick={() => openLogs(service)}
                      >
                        <ScrollText size={13} strokeWidth={2} aria-hidden="true" />
                        Logs
                      </Button>
                    {/if}

                    {#if installed && isDatabase(service)}
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={inFlight}
                        onclick={() => openCredentials(service)}
                      >
                        <KeyRound size={13} strokeWidth={2} aria-hidden="true" />
                        Credentials
                      </Button>
                      <a class="manage-link" href={`#/services/${service.id}`}>
                        <Database size={13} strokeWidth={2} aria-hidden="true" />
                        Manage databases
                      </a>
                    {/if}

                    {#if installed && service.id === 'redis'}
                      <a class="manage-link" href={`#/services/${service.id}`}>
                        <KeyRound size={13} strokeWidth={2} aria-hidden="true" />
                        Manage users
                      </a>
                    {/if}

                    {#if service.id === 'phpmyadmin' && active}
                      <a
                        class="external-link"
                        href={phpMyAdminURL(service)}
                        target="_blank"
                        rel="noreferrer"
                      >
                        <ExternalLink size={13} strokeWidth={2} aria-hidden="true" />
                        Open phpMyAdmin
                      </a>
                    {/if}

                    {#if installed}
                      <Button
                        size="sm"
                        variant="danger-ghost"
                        disabled={Boolean(busy)}
                        title="Uninstall the container; data is kept unless you opt in to deletion"
                        onclick={() => openUninstall(service)}
                      >
                        <Trash2 size={13} strokeWidth={2} aria-hidden="true" />
                        Uninstall
                      </Button>
                    {/if}
                  </footer>
                </article>
              {/each}
            </div>
          </section>
        {/if}
      {/each}
    </div>
  {/if}

  <PerformancePanel />
</div>

{#if configTarget}
  <ServiceConfigModal
    open={true}
    service={configTarget}
    onclose={() => (configTarget = null)}
  />
{/if}

<Modal
  open={logsTarget !== null}
  title={logsTarget ? `${logsTarget.display_name || logsTarget.id} logs` : 'Service logs'}
  description={logsTarget
    ? `Bounded container log tail from ${logsTarget.id}. Refresh to read the latest output.`
    : ''}
  size="lg"
  onclose={closeLogs}
>
  <div class="logs-toolbar">
    <label class="tail-label" for="service-log-tail">Lines</label>
    <select
      id="service-log-tail"
      class="input mono tail-select"
      bind:value={logsTail}
      onchange={() => void fetchLogs()}
    >
      <option value={100}>100</option>
      <option value={200}>200</option>
      <option value={500}>500</option>
      <option value={1000}>1000</option>
    </select>
    <Button size="sm" variant="ghost" loading={logsLoading} onclick={() => void fetchLogs()}>
      {#if !logsLoading}<RefreshCw size={13} strokeWidth={2} aria-hidden="true" />{/if}
      Refresh
    </Button>
  </div>

  {#if logsError}
    <ErrorBanner
      title="Could not load logs"
      code={logsError.code}
      message={logsError.message}
      onretry={() => void fetchLogs()}
    />
  {/if}

  <pre class="service-log" aria-label={logsTarget ? `${logsTarget.id} container logs` : 'Container logs'}>{#if logsLoading && !logs}Loading logs…{:else if logs}{logs}{:else}No log output was returned.{/if}</pre>

  {#snippet footer()}
    <Button variant="ghost" onclick={closeLogs}>Close</Button>
  {/snippet}
</Modal>

<Modal
  open={credentialsTarget !== null}
  title={credentialsTarget
    ? `${credentialsTarget.display_name || credentialsTarget.id} credentials`
    : 'Service credentials'}
  description="Revealing credentials is an explicit, audited action. Values stay in this dialog and are never added to the service list."
  size="md"
  onclose={closeCredentials}
>
  {#if credentialsLoading}
    <div class="credentials-loading" aria-busy="true" aria-label="Loading credentials">
      <Skeleton width="100%" height="12px" />
      <Skeleton width="82%" height="12px" />
      <Skeleton width="64%" height="12px" />
    </div>
  {:else if credentialsError}
    <ErrorBanner
      title="Could not reveal credentials"
      code={credentialsError.code}
      message={credentialsError.message}
      onretry={() => credentialsTarget && void openCredentials(credentialsTarget)}
    />
  {:else if credentials}
    <dl class="credentials">
      <div>
        <dt>Engine</dt>
        <dd class="mono">{credentials.engine}</dd>
      </div>
      <div>
        <dt>Host</dt>
        <dd class="mono">{credentials.host}:{credentials.port}</dd>
      </div>
      <div>
        <dt>Database</dt>
        <dd class="mono">{credentials.database}</dd>
        <button
          type="button"
          class="copy"
          aria-label="Copy database name"
          onclick={() => copyCredential('Database name', credentials?.database ?? '')}
        >
          <Copy size={11} strokeWidth={2} aria-hidden="true" />
        </button>
      </div>
      <div>
        <dt>Username</dt>
        <dd class="mono">{credentials.username}</dd>
        <button
          type="button"
          class="copy"
          aria-label="Copy username"
          onclick={() => copyCredential('Username', credentials?.username ?? '')}
        >
          <Copy size={11} strokeWidth={2} aria-hidden="true" />
        </button>
      </div>
      <div>
        <dt>Password</dt>
        <dd class="mono password">
          {revealPassword ? credentials.password : '••••••••••••'}
        </dd>
        <button
          type="button"
          class="copy"
          aria-label={revealPassword ? 'Hide password' : 'Reveal password'}
          onclick={() => (revealPassword = !revealPassword)}
        >
          {revealPassword ? 'Hide' : 'Reveal'}
        </button>
        <button
          type="button"
          class="copy"
          aria-label="Copy password"
          onclick={() => copyCredential('Password', credentials?.password ?? '')}
        >
          <Copy size={11} strokeWidth={2} aria-hidden="true" />
        </button>
      </div>
    </dl>

    {#if rotateError}
      <div class="rotate-error" role="alert">
        <p class="rotate-error-title">
          <span class="mono">{rotateError.code}</span>
          {rotateError.code === 'CREDENTIAL_ROTATION_UNAVAILABLE'
            ? 'Credential rotation is not available yet'
            : 'Credential rotation failed'}
        </p>
        <p>{rotateError.message}</p>
        {#if rotateError.code === 'CREDENTIAL_ROTATION_UNAVAILABLE'}
          <p class="rotate-note">
            The daemon returned this message verbatim. Rotation needs the Wave 2 database
            credential flow; no credentials were changed.
          </p>
        {:else}
          <p class="rotate-note">
            The daemon returned this message verbatim. No credentials were changed.
          </p>
        {/if}
      </div>
    {:else if rotateArmed}
      <div class="rotate-confirm" role="alert">
        <p class="rotate-error-title">Rotate these credentials now?</p>
        <p>
          The database account and stored secret are replaced. Existing applications using the
          old password will fail until their environment is updated.
        </p>
        <div class="rotate-actions">
          <Button size="sm" variant="ghost" disabled={rotating} onclick={() => (rotateArmed = false)}>
            Cancel
          </Button>
          <Button
            size="sm"
            variant="danger"
            loading={rotating}
            disabled={rotating}
            onclick={rotateCredentials}
          >
            Rotate credentials
          </Button>
        </div>
      </div>
    {/if}
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={closeCredentials} disabled={rotating}>Close</Button>
    {#if credentials && !rotateArmed}
      <Button
        variant="danger-ghost"
        onclick={() => {
          rotateArmed = true;
          rotateError = null;
        }}
      >
        <RotateCw size={13} strokeWidth={2} aria-hidden="true" />
        Rotate credentials
      </Button>
    {/if}
  {/snippet}
</Modal>

<ConfirmDialog
  open={uninstallTarget !== null}
  title={uninstallTarget
    ? `Uninstall ${uninstallTarget.display_name || uninstallTarget.id}?`
    : 'Uninstall service?'}
  description={
    uninstallTarget
      ? `Stops and removes the ${uninstallTarget.id} container and its generated configuration. Data is kept by default.`
      : ''
  }
  confirmLabel="Uninstall service"
  danger
  busy={uninstalling}
  requireText={uninstallDeleteData && uninstallTarget ? uninstallTarget.id : undefined}
  onconfirm={confirmUninstall}
  oncancel={() => {
    if (!uninstalling) uninstallTarget = null;
  }}
>
  {#snippet details()}
    <div class="uninstall-options" role="radiogroup" aria-label="Data handling">
      <label class="radio-row">
        <input
          type="radio"
          name="uninstall-data"
          value="keep"
          checked={!uninstallDeleteData}
          onchange={() => (uninstallDeleteData = false)}
        />
        <span>
          <strong>Keep data (default)</strong>
          <span class="radio-copy">Volumes and secrets stay on disk for a later reinstall.</span>
        </span>
      </label>
      <label class="radio-row danger-option">
        <input
          type="radio"
          name="uninstall-data"
          value="delete"
          checked={uninstallDeleteData}
          onchange={() => (uninstallDeleteData = true)}
        />
        <span>
          <strong>Delete data permanently</strong>
          <span class="radio-copy">
            The daemon may write a backup first, then removes the service data. This cannot be
            undone from the dashboard.
          </span>
        </span>
      </label>
    </div>
    {#if uninstallDeleteData}
      <p class="delete-warning" role="alert">
        Deleting data is irreversible. Type the service id to confirm.
      </p>
    {/if}
    {#if uninstallError}
      <p class="uninstall-error" role="alert">
        <span class="mono">{uninstallError.code}</span>
        {uninstallError.message}
      </p>
    {/if}
  {/snippet}
</ConfirmDialog>

<style>
  .page {
    display: flex;
    flex-direction: column;
    gap: 20px;
    padding: 24px;
    max-width: 1200px;
    margin: 0 auto;
    width: 100%;
  }

  .intro {
    display: flex;
    align-items: flex-start;
    gap: 16px;
    padding: 20px;
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .intro-icon {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    background: var(--surface-2);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-md);
    color: var(--primary);
    flex-shrink: 0;
  }

  .intro-copy {
    flex: 1;
  }

  .intro-copy h2 {
    font-size: 16px;
    font-weight: 600;
    margin: 0 0 4px 0;
    color: var(--text);
  }

  .intro-copy p {
    font-size: 13px;
    color: var(--muted);
    margin: 0;
    line-height: 1.5;
  }

  .intro-chips {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-self: center;
  }

  .banner {
    margin-bottom: 8px;
  }

  .groups {
    display: flex;
    flex-direction: column;
    gap: 22px;
  }

  .group-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    padding-bottom: 8px;
    margin-bottom: 9px;
    border-bottom: 1px solid var(--border);
  }

  .group-head h2 {
    font-size: 15px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .group-head p {
    margin-top: 2px;
    color: var(--muted);
    font-size: 11.5px;
  }

  .group-count {
    color: var(--muted-2);
    font-size: 11px;
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }

  .service-card {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 15px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
    min-width: 0;
  }

  .service-head {
    display: flex;
    align-items: flex-start;
    gap: 10px;
  }

  .service-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 36px;
    height: 36px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .service-icon.active {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .service-title {
    min-width: 0;
    flex: 1;
  }

  .title-row {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 6px;
  }

  .title-row h3 {
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .service-id {
    color: var(--muted-2);
    font-size: 10px;
  }

  .badges {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 6px;
  }

  .meta {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1px;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--border);
    overflow: hidden;
  }

  .meta > div {
    min-width: 0;
    padding: 6px 8px;
    background: var(--surface-2);
  }

  .meta > div.wide {
    grid-column: 1 / -1;
  }

  .meta dt {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 500;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }

  .meta dd {
    margin: 1px 0 0;
    color: var(--fg);
    font-size: 10px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .service-last-error {
    padding: 7px 8px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 10.5px;
    line-height: 1.45;
    overflow-wrap: anywhere;
  }

  .error-label {
    display: block;
    margin-bottom: 1px;
    font-size: 8.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .card-error {
    min-width: 0;
  }

  .service-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
    margin-top: auto;
    padding-top: 2px;
  }

  .external-link,
  .manage-link {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 32px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 500;
    text-decoration: none;
    white-space: nowrap;
  }

  .external-link:hover,
  .manage-link:hover {
    background: var(--surface-3);
    border-color: var(--control-border);
  }

  .state-card {
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .skeleton-card {
    gap: 10px;
  }

  .skeleton-actions {
    display: flex;
    gap: 8px;
  }

  .logs-toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 10px;
  }

  .tail-label {
    color: var(--muted);
    font-size: 11px;
  }

  .tail-select {
    width: auto;
    min-width: 80px;
    height: 30px;
  }

  .service-log {
    min-height: 220px;
    max-height: 420px;
    margin: 0;
    padding: 12px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 10px;
    line-height: 1.55;
    white-space: pre-wrap;
  }

  .credentials-loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .credentials {
    display: flex;
    flex-direction: column;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .credentials > div {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 10px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
  }

  .credentials > div:first-child {
    border-top: 0;
  }

  .credentials dt {
    width: 86px;
    flex: none;
    color: var(--muted);
    font-size: 10.5px;
  }

  .credentials dd {
    margin: 0;
    flex: 1;
    min-width: 0;
    color: var(--fg);
    font-size: 11px;
    overflow-wrap: anywhere;
  }

  .credentials dd.password {
    letter-spacing: 0.06em;
  }

  .copy {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 2px 5px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-family: var(--font-sans);
    font-size: 9.5px;
  }

  .copy:hover {
    background: var(--surface-3);
    color: var(--fg);
  }

  .rotate-confirm,
  .rotate-error {
    margin-top: 12px;
    padding: 10px 11px;
    border: 1px solid color-mix(in srgb, var(--danger) 40%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .rotate-confirm p,
  .rotate-error p {
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .rotate-confirm .rotate-error-title,
  .rotate-error .rotate-error-title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 600;
  }

  .rotate-error .rotate-error-title .mono {
    color: var(--danger-fg);
    font-size: 9.5px;
  }

  .rotate-note {
    margin-top: 3px;
    color: var(--muted-2) !important;
  }

  .rotate-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 9px;
  }

  .uninstall-options {
    display: flex;
    flex-direction: column;
    gap: 8px;
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

  .danger-option {
    border-color: color-mix(in srgb, var(--danger) 30%, var(--border));
  }

  .delete-warning {
    margin-top: 9px;
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .uninstall-error {
    margin-top: 9px;
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .uninstall-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }

  @media (max-width: 900px) {
    .cards {
      grid-template-columns: 1fr;
    }

    .intro {
      flex-wrap: wrap;
    }

    .intro-chips {
      width: 100%;
      padding-left: 52px;
    }
  }

  @media (max-width: 620px) {
    .page {
      padding: 16px;
    }

    .service-actions :global(.btn),
    .external-link,
    .manage-link {
      flex: 1 1 auto;
    }


    .intro-chips {
      padding-left: 0;
    }

    .meta {
      grid-template-columns: 1fr;
    }

    .meta > div.wide {
      grid-column: auto;
    }
  }
</style>
