<script lang="ts">
  import { onMount } from 'svelte';
  import { push } from 'svelte-spa-router';
  import {
    ArrowLeft,
    Check,
    Copy,
    ExternalLink,
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
  import { describeError } from '../lib/errors';
  import {
    checksumPrefix,
    daysUntil,
    formatDate,
    isSecretKey,
    runtimeLabel,
    siteUrl,
  } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type { Site } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import Card from '../lib/components/Card.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import LogsPanel from '../lib/components/LogsPanel.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import StatusPill from '../lib/components/StatusPill.svelte';

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
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

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
