<script lang="ts">
  import { onMount } from 'svelte';
  import { push } from 'svelte-spa-router';
  import {
    ArrowLeft,
    Check,
    ChevronDown,
    Copy,
    ExternalLink,
    Eye,
    FileCode,
    Globe,
    KeyRound,
    LockOpen,
    Play,
    RotateCw,
    ShieldCheck,
    Square,
    TriangleAlert,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { copyText } from '../lib/clipboard';
  import {
    describeError,
    describePHPError,
    type PHPErrorDescription,
  } from '../lib/errors';
  import {
    checksumPrefix,
    daysUntil,
    formatDate,
    isSecretKey,
    runtimeLabel,
    siteUrl,
  } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type { PHPChange, PHPInfo, PHPRuntimeBuild, RuntimeBuild, Site } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import Card from '../lib/components/Card.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import ExtensionToggles from '../lib/components/ExtensionToggles.svelte';
  import Field from '../lib/components/Field.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import LogsPanel from '../lib/components/LogsPanel.svelte';
  import PHPIniEditor from '../lib/components/PHPIniEditor.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import StatusPill from '../lib/components/StatusPill.svelte';
  import VhostPreviewDrawer from '../lib/components/VhostPreviewDrawer.svelte';

  interface Props {
    params: { ref?: string };
  }

  let { params }: Props = $props();

  type Action = 'start' | 'stop' | 'restart';

  let site = $state<Site | null>(null);
  let loading = $state(true);
  let notFound = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);
  let busyAction = $state<Action | null>(null);
  let copied = $state(false);
  let revealed = $state<Record<string, boolean>>({});
  let deleteOpen = $state(false);
  let deleting = $state(false);
  let previewOpen = $state(false);
  let previewServer = $state<'nginx' | 'apache'>('nginx');
  let previewLoading = $state(false);
  let previewReturnFocus = $state<HTMLElement | null>(null);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  let phpInfo = $state<PHPInfo | null>(null);
  let phpLoading = $state(false);
  let phpError = $state<{ code: string; message: string } | null>(null);
  let phpApplying = $state(false);
  let phpApplyError = $state<PHPErrorDescription | null>(null);
  let phpSwitchError = $state<PHPErrorDescription | null>(null);
  let phpRuntimes = $state<RuntimeBuild[]>([]);
  let switchOpen = $state(false);
  let switchBuild = $state('');

  const ref = $derived(params?.ref ? decodeURIComponent(params.ref) : '');

  const active = $derived(
    site ? ['running', 'starting', 'stopping'].includes(site.status) : false,
  );
  const url = $derived(site ? siteUrl(site) : '');
  const certDays = $derived(site?.certificate ? daysUntil(site.certificate.not_after) : null);
  const envEntries = $derived(Object.entries(site?.env ?? {}));
  const domains = $derived(
    site?.certificate?.domains?.length
      ? site.certificate.domains
      : site
        ? [site.domain, ...(site.www ? [`www.${site.domain}`] : [])]
        : [],
  );

  const stopped = $derived(
    Boolean(site && (site.desired_state === 'stopped' || site.status === 'stopped')),
  );

  function phpLine(version: string): string {
    const parts = version.split('.');
    return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
  }

  function phpRevision(id: string): number {
    const match = /-r([1-9][0-9]*)$/.exec(id);
    return match ? Number.parseInt(match[1], 10) : 0;
  }

  const phpBuilds = $derived<PHPRuntimeBuild[]>(
    phpRuntimes
      .filter((runtime) => runtime.kind === 'php')
      .map((runtime) => ({
        ...runtime,
        line: phpLine(runtime.version),
        revision: phpRevision(runtime.id),
      }))
      .sort(
        (a, b) =>
          b.version.localeCompare(a.version, undefined, { numeric: true }) ||
          b.revision - a.revision,
      ),
  );

  const switchCandidates = $derived(
    phpBuilds.filter((runtime) => runtime.id !== (site?.php_build || phpInfo?.build.id)),
  );

  const switchTarget = $derived(
    phpBuilds.find((runtime) => runtime.id === switchBuild) ?? switchCandidates[0] ?? null,
  );

  async function loadPHP(): Promise<void> {
    phpLoading = true;
    phpError = null;
    try {
      const [info, builds] = await Promise.all([
        api.getPHPInfo(ref),
        api.listRuntimes().catch(() => [] as RuntimeBuild[]),
      ]);
      phpInfo = info;
      phpRuntimes = builds;
    } catch (caught) {
      phpError = describeError(caught);
    } finally {
      phpLoading = false;
    }
  }

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    notFound = false;
    if (!ref) {
      notFound = true;
      loading = false;
      return;
    }
    try {
      site = await api.getSite(ref);
      if (site.kind === 'php') await loadPHP();
    } catch (caught) {
      const described = describeError(caught);
      if (described.code === 'NOT_FOUND') {
        notFound = true;
      } else {
        error = described;
      }
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load(true);
    const refresh = () => void load();
    window.addEventListener('devstack:refresh', refresh);
    return () => {
      window.removeEventListener('devstack:refresh', refresh);
      if (copyTimer) clearTimeout(copyTimer);
    };
  });

  let pollAttempts = 0;
  $effect(() => {
    const transient = site?.status === 'starting' || site?.status === 'stopping';
    if (!transient) return;
    pollAttempts = 0;
    const timer = setInterval(() => {
      pollAttempts += 1;
      if (pollAttempts > 30) {
        clearInterval(timer);
        return;
      }
      void load();
    }, 1200);
    return () => clearInterval(timer);
  });

  async function act(action: Action): Promise<void> {
    if (!site || busyAction) return;
    const siteRef = site.id;
    busyAction = action;
    try {
      if (action === 'start') await api.startSite(siteRef);
      if (action === 'stop') await api.stopSite(siteRef);
      if (action === 'restart') await api.restartSite(siteRef);
      addToast({
        kind: 'success',
        title: `${site.name} ${action === 'stop' ? 'stopped' : action === 'start' ? 'started' : 'restarted'}`,
        message: action === 'stop' ? 'The route remains configured.' : 'The site is being probed for readiness.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not ${action} ${site.name}`, `${described.code}: ${described.message}`);
    } finally {
      busyAction = null;
    }
  }

  async function applyPHP(change: PHPChange): Promise<void> {
    if (!site || phpApplying) return;
    const current = site;
    phpApplying = true;
    phpApplyError = null;
    try {
      await api.applyPHP(current.id, change);
      addToast({
        kind: 'success',
        title: 'PHP settings applied',
        message: stopped
          ? 'Configuration validated. Changes apply on next start.'
          : 'Validated, staged, and swapped onto the new FPM master.',
      });
      await load();
    } finally {
      phpApplying = false;
    }
  }

  async function onExtensionsChange(value: Record<string, boolean>): Promise<void> {
    phpSwitchError = null;
    try {
      await applyPHP({ extensions: value });
    } catch (caught) {
      phpApplyError = describePHPError(caught);
    }
  }

  function openSwitch(): void {
    phpSwitchError = null;
    switchBuild = switchCandidates[0]?.id ?? '';
    switchOpen = true;
  }

  async function switchPHP(): Promise<void> {
    const target = switchTarget;
    if (!site || !target) return;
    switchOpen = false;
    phpSwitchError = null;
    try {
      await applyPHP({ version: target.line, build: target.id });
    } catch (caught) {
      phpSwitchError = describePHPError(caught);
    }
  }

  async function copyUrl(): Promise<void> {
    const ok = await copyText(url);
    if (!ok) {
      toastError('Could not copy the URL', 'Select the address and copy it manually.');
      return;
    }
    copied = true;
    addToast({ kind: 'success', title: 'URL copied', message: url });
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copied = false), 1800);
  }

  async function confirmDelete(): Promise<void> {
    if (!site) return;
    deleting = true;
    try {
      await api.deleteSite(site.id);
      addToast({
        kind: 'success',
        title: `${site.name} removed`,
        message: 'Domain, certificate and generated configs were removed. Project files are untouched.',
      });
      push('/sites');
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not remove site', `${described.code}: ${described.message}`);
    } finally {
      deleting = false;
    }
  }

  async function previewVhost(event?: MouseEvent): Promise<void> {
    if (!site || previewLoading) return;
    previewReturnFocus = event?.currentTarget instanceof HTMLElement ? event.currentTarget : null;
    previewLoading = true;
    try {
      const status = await api.getVhostStatus();
      previewServer =
        status.mode === 'apache' || status.server === 'apache' ? 'apache' : 'nginx';
    } catch {
      // The drawer reports the real preview error; builtin defaults to nginx.
      previewServer = 'nginx';
    } finally {
      previewLoading = false;
      previewOpen = true;
    }
  }

  function toggleReveal(key: string): void {
    revealed = { ...revealed, [key]: !revealed[key] };
  }
</script>

<div class="page">
  <a class="back" href="#/sites">
    <ArrowLeft size={14} strokeWidth={2} aria-hidden="true" />
    All sites
  </a>

  {#if loading}
    <div class="head-skeleton">
      <Skeleton width="180px" height="26px" radius="6px" />
      <Skeleton width="240px" height="14px" radius="4px" />
      <Skeleton width="160px" height="12px" radius="4px" />
    </div>
    <div class="detail-grid">
      <div class="skeleton-logs">
        <Skeleton width="100%" height="470px" radius="12px" />
      </div>
      <div class="skeleton-side">
        <Skeleton width="100%" height="190px" radius="12px" />
        <Skeleton width="100%" height="160px" radius="12px" />
      </div>
    </div>
  {:else if notFound}
    <div class="state-card">
      <EmptyState
        icon={Globe}
        title="Site not found"
        description="It may have been removed, or the link is stale."
      >
        <Button variant="primary" onclick={() => push('/sites')}>Back to sites</Button>
      </EmptyState>
    </div>
  {:else if !site}
    <div class="state-card">
      <ErrorBanner
        title="Could not load site"
        code={error?.code}
        message={error?.message ?? 'The daemon returned no site data.'}
        onretry={() => load(true)}
      />
    </div>
  {:else}
    <header class="detail-head">
      <div class="head-main">
        <div class="title-row">
          <h2>{site.name}</h2>
          <StatusPill status={site.status} />
        </div>
        <div class="url-row">
          <span class="url mono">{url}</span>
          <IconButton
            label={copied ? 'URL copied' : 'Copy site URL'}
            icon={copied ? Check : Copy}
            size="sm"
            onclick={copyUrl}
          />
          <IconButton
            label="Open site in a new tab"
            icon={ExternalLink}
            size="sm"
            href={url}
            external
          />
        </div>
        <p class="path mono" title={site.host_path}>{site.host_path}</p>
      </div>
      <div class="head-actions">
        {#if active}
          <Button
            variant="secondary"
            onclick={() => act('stop')}
            loading={busyAction === 'stop'}
            disabled={Boolean(busyAction)}
          >
            {#if busyAction !== 'stop'}<Square size={13} strokeWidth={2} aria-hidden="true" />{/if}
            Stop
          </Button>
        {:else}
          <Button
            variant="primary"
            onclick={() => act('start')}
            loading={busyAction === 'start'}
            disabled={Boolean(busyAction)}
          >
            {#if busyAction !== 'start'}<Play size={13} strokeWidth={2} aria-hidden="true" />{/if}
            Start
          </Button>
        {/if}
        <Button
          variant="secondary"
          onclick={() => act('restart')}
          loading={busyAction === 'restart'}
          disabled={Boolean(busyAction)}
        >
          {#if busyAction !== 'restart'}<RotateCw size={13} strokeWidth={2} aria-hidden="true" />{/if}
          Restart
        </Button>
      </div>
    </header>

    {#if error}
      <div class="inline-error">
        <ErrorBanner
          title="Could not refresh site"
          code={error.code}
          message={error.message}
          onretry={() => load()}
          ondismiss={() => (error = null)}
        />
      </div>
    {/if}

    <div class="detail-grid">
      <div class="main-col">
        <LogsPanel {site} />
      </div>

      <aside class="side-col" aria-label="Site configuration">
        <Card title="Overview">
          <dl class="overview">
            <div class="row">
              <dt>Kind</dt>
              <dd>
                <Badge icon={site.kind === 'node' ? FileCode : Globe}>{site.kind}</Badge>
              </dd>
            </div>
            <div class="row">
              <dt>Runtime</dt>
              <dd class="mono">{runtimeLabel(site)}</dd>
            </div>
            {#if site.kind === 'php'}
              <div class="row">
                <dt>PHP build</dt>
                <dd class="mono break">{site.php_build || '—'}</dd>
              </div>
            {/if}
            <div class="row">
              <dt>Backend</dt>
              <dd class="capitalize">{site.backend}</dd>
            </div>
            <div class="row">
              <dt>HTTP port</dt>
              <dd class="mono">{site.http_port || '—'}</dd>
            </div>
            <div class="row">
              <dt>FastCGI port</dt>
              <dd class="mono">{site.fastcgi_port || '—'}</dd>
            </div>
            {#if site.kind === 'static'}
              <div class="row">
                <dt>Document root</dt>
                <dd class="mono break">{site.document_root || '.'}</dd>
              </div>
            {:else if site.kind === 'node'}
              <div class="row">
                <dt>Command</dt>
                <dd class="mono break">{site.command || '—'}</dd>
              </div>
            {/if}
            <div class="row">
              <dt>Host path</dt>
              <dd class="mono break" title={site.host_path}>{site.host_path}</dd>
            </div>
            {#if site.backend_path && site.backend_path !== site.host_path}
              <div class="row">
                <dt>Backend path</dt>
                <dd class="mono break" title={site.backend_path}>{site.backend_path}</dd>
              </div>
            {/if}
          </dl>
        </Card>

        <Card title="Certificate">
          {#snippet actions()}
            <Button
              size="sm"
              variant="ghost"
              onclick={previewVhost}
              loading={previewLoading}
              title="Preview the generated nginx or Apache vhost"
            >
              {#if !previewLoading}<Eye size={12} strokeWidth={2} aria-hidden="true" />{/if}
              Preview vhost
            </Button>
          {/snippet}
          {#if !site.tls}
            <div class="cert-off">
              <span class="cert-icon" aria-hidden="true"><LockOpen size={18} strokeWidth={1.8} /></span>
              <div>
                <p class="cert-title">TLS disabled</p>
                <p class="cert-copy">Served over plain HTTP. Enable TLS when adding a site to use HTTPS.</p>
              </div>
            </div>
          {:else}
            <div class="cert-head">
              <span class="cert-icon ok" aria-hidden="true"><ShieldCheck size={18} strokeWidth={1.8} /></span>
              <div class="cert-copy-wrap">
                <p class="cert-title">Local CA certificate</p>
                <p class="cert-copy">
                  {site.certificate ? `Expires ${formatDate(site.certificate.not_after)}` : 'Issued and managed by devstack'}
                </p>
              </div>
              {#if certDays !== null}
                <Badge variant={certDays <= 0 ? 'danger' : certDays < 30 ? 'warning' : 'accent'}>
                  {certDays <= 0 ? 'Expired' : `${certDays}d`}
                </Badge>
              {/if}
            </div>

            {#if site.certificate && certDays !== null && certDays < 30}
              <div class="cert-warning" role="status">
                <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
                <p>
                  {certDays <= 0
                    ? 'This certificate has expired. Browsers will block the site until it is renewed.'
                    : `Expires in ${certDays} days. Renew it before browsers start warning.`}
                </p>
              </div>
            {/if}

            <div class="cert-domains">
              <p class="label">Subject alternative names</p>
              <div class="chips">
                {#each domains as domain (domain)}
                  <span class="chip mono">{domain}</span>
                {/each}
              </div>
            </div>

            {#if site.certificate?.serial}
              <div class="row cert-row">
                <dt>Serial</dt>
                <dd class="mono">{checksumPrefix(site.certificate.serial, 18)}</dd>
              </div>
            {/if}
            <p class="cert-foot">
              {site.certificate
                ? `The certificate is managed locally; no external CA is involved.`
                : `This daemon does not expose certificate expiry; the domain still serves over HTTPS.`}
            </p>
          {/if}
        </Card>

        <Card title="Environment">
          {#snippet actions()}
            <span class="env-count">{envEntries.length}</span>
          {/snippet}
          {#if envEntries.length === 0}
            <div class="env-empty">
              <KeyRound size={16} strokeWidth={1.8} aria-hidden="true" />
              <p>No environment variables set for this site.</p>
            </div>
          {:else}
            <dl class="env">
              {#each envEntries as [key, value] (key)}
                {@const secret = isSecretKey(key)}
                <div class="env-row">
                  <dt class="mono" title={key}>{key}</dt>
                  <dd class="mono">
                    <span class="env-value" title={secret && !revealed[key] ? 'Redacted — use Reveal to show' : value}>
                      {secret && !revealed[key] ? '••••••••' : value}
                    </span>
                    {#if secret}
                      <button
                        type="button"
                        class="reveal"
                        aria-label={revealed[key] ? `Hide ${key}` : `Reveal ${key}`}
                        onclick={() => toggleReveal(key)}
                      >
                        {revealed[key] ? 'Hide' : 'Reveal'}
                      </button>
                    {/if}
                  </dd>
                </div>
              {/each}
            </dl>
          {/if}
        </Card>
      </aside>
    </div>

    {#if site.kind === 'php'}
      <section class="php-section" aria-labelledby="php-heading">
        <div class="php-head">
          <div class="php-head-copy">
            <p class="php-eyebrow">PHP</p>
            <h3 id="php-heading">Runtime configuration</h3>
            <p class="php-sub">
              <span>PHP {site.php_version || phpInfo?.build.version}</span>
              <span class="mono">{site.php_build || phpInfo?.build.id || 'No build pinned'}</span>
              <span class="mono">
                FPM
                {site.fastcgi_port ? `127.0.0.1:${site.fastcgi_port}` : 'not allocated while stopped'}
              </span>
            </p>
          </div>
          <Button
            size="sm"
            variant="secondary"
            onclick={openSwitch}
            disabled={switchCandidates.length === 0 || phpApplying}
            title={switchCandidates.length === 0 ? 'No other PHP build is installed' : 'Switch PHP version or build'}
          >
            <RotateCw size={12} strokeWidth={2} aria-hidden="true" />
            Switch build
          </Button>
        </div>

        {#if stopped}
          <p class="php-note" role="status">
            This site is stopped. Configuration changes are validated now and apply on next start.
          </p>
        {/if}

        {#if phpSwitchError}
          <div class="php-error" role="alert">
            <div class="php-error-title">
              <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
              <span>PHP switch failed. The previous build was kept; validate-then-swap rolled back.</span>
            </div>
            <p class="php-error-meta">
              <span class="mono">{phpSwitchError.code}</span>
              <span>{phpSwitchError.message}</span>
            </p>
            {#if phpSwitchError.details?.message}
              <p class="php-error-line">{phpSwitchError.details.message}</p>
            {/if}
            {#if phpSwitchError.details?.line !== undefined}
              <p class="php-error-line">Line {phpSwitchError.details.line}</p>
            {/if}
            {#if phpSwitchError.details?.output}
              <pre class="php-output" aria-label="PHP switch validation output">{phpSwitchError.details.output}</pre>
            {/if}
          </div>
        {/if}

        {#if phpApplyError && !phpSwitchError}
          <div class="php-error" role="alert">
            <div class="php-error-title">
              <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
              <span>PHP extension change failed. The previous configuration was kept.</span>
            </div>
            <p class="php-error-meta">
              <span class="mono">{phpApplyError.code}</span>
              <span>{phpApplyError.message}</span>
            </p>
            {#if phpApplyError.details?.message}
              <p class="php-error-line">{phpApplyError.details.message}</p>
            {/if}
            {#if phpApplyError.details?.line !== undefined}
              <p class="php-error-line">Line {phpApplyError.details.line}</p>
            {/if}
            {#if phpApplyError.details?.output}
              <pre class="php-output" aria-label="PHP validation output">{phpApplyError.details.output}</pre>
            {/if}
          </div>
        {/if}

        {#if phpLoading && !phpInfo}
          <div class="php-loading" aria-busy="true" aria-label="Loading PHP configuration">
            <Skeleton width="160px" height="12px" />
            <Skeleton width="100%" height="120px" radius="8px" />
            <Skeleton width="72%" height="10px" />
          </div>
        {:else if phpError}
          <ErrorBanner
            title="Could not load PHP configuration"
            code={phpError.code}
            message={phpError.message}
            onretry={loadPHP}
          />
        {:else if phpInfo}
          <div class="php-grid">
            <div class="php-editor">
              <PHPIniEditor
                ini={phpInfo.ini}
                raw={phpInfo.ini_raw}
                generated={phpInfo.generated_ini}
                onApply={applyPHP}
                disabled={phpApplying}
              />
            </div>
            <div class="php-side">
              <h4>Extensions</h4>
              <p class="php-side-copy">
                Inherit follows the PHP {site.php_version} defaults. On and Off are site-specific
                overrides; required dependencies are added automatically.
              </p>
              <ExtensionToggles
                extensions={phpInfo.available_extensions}
                builtins={phpInfo.builtins}
                value={site.php_extensions ?? {}}
                onchange={onExtensionsChange}
                disabled={phpApplying}
              />
              <dl class="php-meta">
                <div>
                  <dt>Config hash</dt>
                  <dd class="mono">{phpInfo.config_hash ? `${phpInfo.config_hash.slice(0, 12)}…` : '—'}</dd>
                </div>
                <div>
                  <dt>Resolved</dt>
                  <dd>{phpInfo.extensions.length} extension{phpInfo.extensions.length === 1 ? '' : 's'}</dd>
                </div>
                <div>
                  <dt>Defaults line</dt>
                  <dd class="mono">PHP {phpInfo.defaults.version}</dd>
                </div>
              </dl>
            </div>
          </div>
        {/if}
      </section>
    {/if}

    <section class="danger" aria-labelledby="danger-title">
      <div class="danger-copy">
        <p class="danger-eyebrow">Danger zone</p>
        <h3 id="danger-title">Remove this site</h3>
        <p>
          Stops the site and removes its domain, certificate and generated configuration. Project
          files remain at <span class="mono">{site.host_path}</span>.
        </p>
      </div>
      <Button variant="danger-ghost" onclick={() => (deleteOpen = true)}>
        <Trash2 size={14} strokeWidth={2} aria-hidden="true" />
        Remove site
      </Button>
    </section>
  {/if}
</div>

<ConfirmDialog
  open={deleteOpen}
  title={site ? `Remove ${site.name}?` : 'Remove site?'}
  description={site
    ? `This stops the site and removes its ${site.domain} domain, certificate and generated configs. Files in ${site.host_path} are never deleted.`
    : ''}
  confirmLabel="Remove site"
  requireText={site?.name}
  danger
  busy={deleting}
  onconfirm={confirmDelete}
  oncancel={() => {
    if (!deleting) deleteOpen = false;
  }}
/>

<ConfirmDialog
  open={switchOpen}
  title={site ? `Switch ${site.name} to another PHP build?` : 'Switch PHP build?'}
  description="devstack writes and validates the new php.ini and FPM configuration, stages a new master, then swaps traffic. If validation or readiness fails, the previous build stays active and its master is kept — this is the rollback path."
  confirmLabel="Switch build"
  busy={phpApplying}
  onconfirm={switchPHP}
  oncancel={() => {
    if (!phpApplying) switchOpen = false;
  }}
>
  {#snippet details()}
    {#if switchCandidates.length === 0}
      <p class="switch-empty">No other PHP build is installed. Install one from Runtimes first.</p>
    {:else}
      <Field id="switch-php-build" label="Target build">
        <div class="select-wrap">
          <select id="switch-php-build" class="input mono" bind:value={switchBuild}>
            {#each switchCandidates as runtime (runtime.id)}
              <option value={runtime.id}>
                PHP {runtime.version} (r{runtime.revision}) · line {runtime.line}
              </option>
            {/each}
          </select>
          <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
        </div>
      </Field>
    {/if}
  {/snippet}
</ConfirmDialog>

{#if site}
  <VhostPreviewDrawer
    open={previewOpen}
    site={site.name}
    initialServer={previewServer}
    returnFocus={previewReturnFocus}
    onclose={() => (previewOpen = false)}
  />
{/if}

<style>
  .page {
    padding-top: 18px;
  }

  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 12px;
    color: var(--muted);
    font-size: 12px;
    font-weight: 500;
  }

  .back:hover {
    color: var(--fg);
  }

  .detail-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    padding-bottom: 16px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }

  .head-main {
    min-width: 0;
  }

  .title-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 9px;
  }

  h2 {
    font-size: 23px;
    font-weight: 600;
    letter-spacing: -0.025em;
    line-height: 1.2;
  }

  .url-row {
    display: flex;
    align-items: center;
    gap: 3px;
    margin-top: 5px;
  }

  .url {
    color: var(--muted);
    font-size: 12.5px;
    overflow-wrap: anywhere;
  }

  .path {
    margin-top: 2px;
    color: var(--muted-2);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .head-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
    padding-top: 3px;
  }

  .detail-grid {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(286px, 1fr);
    align-items: stretch;
    gap: 14px;
  }

  .main-col,
  .side-col {
    min-width: 0;
  }

  .side-col {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .overview {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    min-height: 28px;
    padding: 4px 0;
    border-top: 1px solid var(--border);
  }

  .row:first-child {
    border-top: 0;
    padding-top: 0;
  }

  .row:last-child {
    padding-bottom: 0;
  }

  dt {
    flex: none;
    color: var(--muted);
    font-size: 11px;
  }

  dd {
    margin: 0;
    min-width: 0;
    color: var(--fg);
    font-size: 11.5px;
    text-align: right;
  }

  .capitalize {
    text-transform: capitalize;
  }

  .break {
    overflow-wrap: anywhere;
    font-size: 10.5px;
    line-height: 1.5;
  }

  .cert-head {
    display: flex;
    align-items: flex-start;
    gap: 9px;
  }

  .cert-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 32px;
    height: 32px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .cert-icon.ok {
    color: var(--success-fg);
    border-color: var(--accent-border);
    background: var(--accent-soft);
  }

  .cert-copy-wrap {
    flex: 1;
    min-width: 0;
  }

  .cert-title {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .cert-copy {
    margin-top: 1px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .cert-warning {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 10px;
    padding: 7px 8px;
    border-radius: 6px;
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .cert-warning :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .cert-warning p {
    font-size: 10.5px;
    line-height: 1.45;
    color: var(--warning-fg);
  }

  .cert-domains {
    margin-top: 12px;
  }

  .label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 5px;
  }

  .chip {
    padding: 2px 5px;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
  }

  .cert-row {
    margin-top: 10px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
  }

  .cert-foot {
    margin-top: 10px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.5;
  }

  .cert-off {
    display: flex;
    align-items: flex-start;
    gap: 9px;
  }

  .env {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .env-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 5px 0;
    border-top: 1px solid var(--border);
  }

  .env-row:first-child {
    border-top: 0;
    padding-top: 0;
  }

  .env-row dt {
    color: var(--muted);
    font-size: 9.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 48%;
  }

  .env-row dd {
    display: flex;
    align-items: baseline;
    gap: 5px;
    font-size: 10px;
    min-width: 0;
  }

  .env-value {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 170px;
  }

  .reveal {
    padding: 1px 3px;
    border: 0;
    border-radius: 3px;
    background: transparent;
    color: var(--muted-2);
    font-family: var(--font-sans);
    font-size: 9px;
    white-space: nowrap;
  }

  .reveal:hover {
    color: var(--fg);
    background: var(--surface-2);
  }

  .env-empty {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .env-empty :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--muted-2);
  }

  .env-count {
    display: inline-grid;
    place-items: center;
    min-width: 20px;
    height: 20px;
    padding: 0 5px;
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 9.5px;
  }

  .danger {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    margin-top: 16px;
    padding: 14px 16px;
    border: 1px solid color-mix(in srgb, var(--danger) 32%, var(--border));
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .danger-copy {
    min-width: 0;
  }

  .danger-eyebrow {
    color: var(--danger-fg);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .danger h3 {
    margin-top: 2px;
    font-size: 13px;
    font-weight: 600;
  }

  .danger p:not(.danger-eyebrow) {
    margin-top: 3px;
    max-width: 680px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .danger .mono {
    color: var(--fg);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .php-section {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 16px;
    padding: 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .php-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
    padding-bottom: 11px;
    border-bottom: 1px solid var(--border);
  }

  .php-head-copy {
    min-width: 0;
  }

  .php-eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .php-head h3 {
    margin-top: 1px;
    font-size: 14px;
    font-weight: 600;
  }

  .php-sub {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .php-note {
    padding: 7px 9px;
    border: 1px solid color-mix(in srgb, var(--info) 28%, var(--border));
    border-radius: var(--radius);
    background: var(--info-soft);
    color: var(--info-fg);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-error {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--danger) 42%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .php-error-title {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 600;
    line-height: 1.45;
  }

  .php-error-title :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--danger-fg);
  }

  .php-error-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 8px;
    margin-top: 4px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-error-meta .mono {
    color: var(--danger-fg);
  }

  .php-error-line {
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-output {
    margin: 6px 0 0;
    padding: 9px;
    max-height: 160px;
    overflow: auto;
    border: 1px solid color-mix(in srgb, var(--danger) 22%, var(--border));
    border-radius: var(--radius);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 9.5px;
    line-height: 1.55;
    white-space: pre-wrap;
  }

  .php-loading {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 4px 0;
  }

  .php-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(280px, 1fr);
    align-items: start;
    gap: 18px;
  }

  .php-editor,
  .php-side {
    min-width: 0;
  }

  .php-side h4 {
    font-size: 12.5px;
    font-weight: 600;
  }

  .php-side-copy {
    margin: 2px 0 9px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .php-meta {
    display: flex;
    flex-direction: column;
    gap: 0;
    margin: 12px 0 0;
    border-top: 1px solid var(--border);
  }

  .php-meta > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 5px 0;
    border-top: 1px solid var(--border);
  }

  .php-meta > div:first-child {
    border-top: 0;
  }

  .php-meta dt {
    color: var(--muted);
    font-size: 9.5px;
  }

  .php-meta dd {
    margin: 0;
    color: var(--fg);
    font-size: 9.5px;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .select-wrap {
    position: relative;
  }

  .select-wrap :global(select) {
    appearance: none;
    width: 100%;
    padding-right: 30px;
  }

  .select-wrap :global(svg) {
    position: absolute;
    top: 50%;
    right: 9px;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .switch-empty {
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 35%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11px;
    line-height: 1.5;
  }

  .state-card {
    max-width: 640px;
    margin: 40px auto;
    padding: 0 16px;
  }

  .inline-error {
    margin-bottom: 12px;
  }

  .head-skeleton {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-bottom: 18px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }

  .skeleton-logs {
    min-width: 0;
  }

  .skeleton-side {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  @media (max-width: 900px) {
    .detail-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .php-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .side-col {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      align-items: start;
    }

    .side-col > :nth-child(3) {
      grid-column: 1 / -1;
    }
  }

  @media (max-width: 700px) {
    .detail-head {
      flex-direction: column;
      gap: 12px;
    }

    .head-actions {
      width: 100%;
    }

    .head-actions :global(.btn) {
      flex: 1;
    }

    .side-col {
      display: flex;
      flex-direction: column;
      align-items: stretch;
    }

    .danger {
      flex-direction: column;
      align-items: flex-start;
    }

    .danger :global(.btn) {
      width: 100%;
    }

    h2 {
      font-size: 21px;
    }
  }
</style>
