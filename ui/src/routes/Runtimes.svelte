<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Boxes,
    Code,
    Download,
    HardDrive,
    ShieldCheck,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { describeError } from '../lib/errors';
  import { checksumPrefix, formatBytes, formatDate } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type { CatalogEntry, RuntimeBuild, Site } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  let runtimes = $state<RuntimeBuild[]>([]);
  let catalog = $state<CatalogEntry[]>([]);
  let sites = $state<Site[]>([]);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let installTarget = $state<CatalogEntry | null>(null);
  let installingId = $state<string | null>(null);
  let removeTarget = $state<RuntimeBuild | null>(null);
  let removing = $state(false);

  const available = $derived(
    catalog
      .filter((entry) => entry.kind === 'node')
      .slice()
      .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true })),
  );

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      const [builds, entries, siteList] = await Promise.all([
        api.listRuntimes(),
        api.availableRuntimes(),
        api.listSites(),
      ]);
      runtimes = builds;
      catalog = entries;
      sites = siteList;
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

  function usedBy(runtime: RuntimeBuild): string[] {
    return sites
      .filter((site) => site.kind === 'node' && site.node_version === runtime.version)
      .map((site) => site.name);
  }

  function isInstalled(entry: CatalogEntry): boolean {
    return runtimes.some(
      (runtime) => runtime.kind === entry.kind && runtime.version === entry.version,
    );
  }

  async function install(): Promise<void> {
    const target = installTarget;
    if (!target) return;
    installTarget = null;
    installingId = target.id;
    try {
      await api.installRuntime(target.id);
      addToast({
        kind: 'success',
        title: `Node ${target.version} installed`,
        message: 'Archive verified against the catalog SHA-256. Available for new Node sites.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not install runtime', `${described.code}: ${described.message}`);
    } finally {
      installingId = null;
    }
  }

  async function remove(): Promise<void> {
    if (!removeTarget) return;
    const target = removeTarget;
    removing = true;
    try {
      await api.removeRuntime(target.id);
      addToast({
        kind: 'success',
        title: `Node ${target.version} removed`,
        message: 'The extracted runtime was deleted from the devstack home.',
      });
      removeTarget = null;
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not remove runtime', `${described.code}: ${described.message}`);
    } finally {
      removing = false;
    }
  }

  function scrollToAvailable(): void {
    document.getElementById('available')?.scrollIntoView({ block: 'start' });
  }
</script>

<div class="page">
  <section class="intro" aria-labelledby="runtime-intro">
    <span class="intro-icon" aria-hidden="true"><ShieldCheck size={19} strokeWidth={1.8} /></span>
    <div class="intro-copy">
      <h2 id="runtime-intro">Verified Node runtimes</h2>
      <p>
        Node 24 is the current LTS line; Node 22 remains in maintenance. Choose 24 for new
        projects. Every archive is checked against its published SHA-256 before extraction, and
        installs stay inside the devstack home.
      </p>
    </div>
    <div class="intro-chips">
      <Badge variant="accent">Checksum verified</Badge>
      <Badge>Local only</Badge>
    </div>
  </section>

  {#if error}
    <div class="banner">
      <ErrorBanner
        title="Could not load runtimes"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
        ondismiss={runtimes.length > 0 ? () => (error = null) : undefined}
      />
    </div>
  {/if}

  <section class="section" aria-labelledby="installed-heading">
    <div class="section-head">
      <div>
        <p class="eyebrow">On this machine</p>
        <h2 id="installed-heading">Installed</h2>
      </div>
      <span class="section-count mono">{runtimes.length}</span>
    </div>

    {#if loading}
      <div class="runtime-list" aria-busy="true" aria-label="Loading installed runtimes">
        {#each Array(2) as _, index (index)}
          <div class="runtime-row skeleton">
            <Skeleton width="40px" height="40px" radius="8px" />
            <div class="skeleton-copy">
              <Skeleton width="150px" height="15px" />
              <Skeleton width={`${240 + index * 50}px`} height="10px" />
              <Skeleton width="280px" height="9px" />
            </div>
            <Skeleton width="96px" height="30px" radius="8px" />
          </div>
        {/each}
      </div>
    {:else if runtimes.length === 0 && !error}
      <div class="empty-box">
        <EmptyState
          icon={Boxes}
          title="No runtimes installed"
          description="Install a Node build from the catalog below before adding a Node site."
        >
          <Button variant="secondary" onclick={scrollToAvailable}>Browse available builds</Button>
        </EmptyState>
      </div>
    {:else}
      <div class="runtime-list">
        {#each runtimes as runtime (runtime.id)}
          {@const users = usedBy(runtime)}
          <article class="runtime-row" class:in-use={users.length > 0}>
            <span class="runtime-icon" aria-hidden="true"><Code size={18} strokeWidth={1.8} /></span>
            <div class="runtime-main">
              <div class="runtime-title">
                <h3 class="mono">Node {runtime.version}</h3>
                <Badge>{runtime.kind}</Badge>
                {#if users.length > 0}
                  <span class="usage-pill"><span class="dot" aria-hidden="true"></span> In use</span>
                {/if}
              </div>
              <p class="runtime-path mono" title={runtime.path}>{runtime.path}</p>
              <div class="runtime-meta">
                <span class="checksum" title={runtime.checksum}>
                  sha256 <span class="mono">{checksumPrefix(runtime.checksum, 12)}</span>
                </span>
                <span>Installed {formatDate(runtime.installed_at)}</span>
                <span>{runtime.size ? formatBytes(runtime.size) : 'Size unavailable'}</span>
                <span>{runtime.backend}</span>
              </div>
              {#if users.length > 0}
                <p class="usage-note">
                  Used by {users.join(', ')}. Remove is blocked while a site references this build.
                </p>
              {/if}
            </div>
            <div class="runtime-actions">
              <Button
                size="sm"
                variant="ghost"
                disabled={users.length > 0 || Boolean(installingId)}
                title={users.length > 0 ? `In use by ${users.join(', ')}` : `Remove Node ${runtime.version}`}
                onclick={() => (removeTarget = runtime)}
              >
                <Trash2 size={13} strokeWidth={2} aria-hidden="true" />
                Remove
              </Button>
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </section>

  <section class="section" id="available" aria-labelledby="available-heading">
    <div class="section-head">
      <div>
        <p class="eyebrow">Catalog</p>
        <h2 id="available-heading">Available</h2>
      </div>
      <span class="section-count mono">{available.length}</span>
    </div>

    {#if loading}
      <div class="catalog-list" aria-busy="true" aria-label="Loading runtime catalog">
        {#each Array(3) as _, index (index)}
          <div class="catalog-row skeleton">
            <Skeleton width="36px" height="36px" radius="7px" />
            <div class="skeleton-copy">
              <Skeleton width="140px" height="14px" />
              <Skeleton width={`${220 + index * 30}px`} height="9px" />
            </div>
            <Skeleton width="80px" height="30px" radius="8px" />
          </div>
        {/each}
      </div>
    {:else if available.length === 0 && !error}
      <div class="empty-box">
        <EmptyState
          icon={Download}
          title="Catalog unavailable"
          description="The daemon returned no runtime catalog entries. Run Doctor to check connectivity and try again."
          compact
        />
      </div>
    {:else}
      <div class="catalog-list">
        {#each available as entry (entry.id)}
          {@const installed = isInstalled(entry)}
          <article class="catalog-row" class:installed class:busy={installingId === entry.id}>
            <span class="catalog-icon" aria-hidden="true">
              {#if installed}<Boxes size={17} strokeWidth={1.8} />{:else}<Download size={17} strokeWidth={1.8} />{/if}
            </span>
            <div class="catalog-main">
              <div class="catalog-title">
                <h3 class="mono">Node {entry.version}</h3>
                <Badge>{entry.build}</Badge>
                {#if installed}<Badge variant="accent">Installed</Badge>{/if}
              </div>
              <div class="catalog-meta">
                <span><HardDrive size={11} strokeWidth={1.8} aria-hidden="true" /> {formatBytes(entry.size)}</span>
                <span class="checksum" title={entry.sha256}>
                  sha256 <span class="mono">{checksumPrefix(entry.sha256, 16)}</span>
                </span>
              </div>
              {#if installingId === entry.id}
                <div
                  class="install-progress"
                  role="progressbar"
                  aria-label="Installing Node {entry.version}"
                  aria-valuetext="Downloading and verifying"
                >
                  <span></span>
                </div>
                <p class="install-copy">Downloading, verifying checksum and extracting…</p>
              {/if}
            </div>
            <div class="catalog-actions">
              {#if installed}
                <span class="installed-note">Already installed</span>
              {:else}
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={Boolean(installingId)}
                  onclick={() => (installTarget = entry)}
                >
                  <Download size={13} strokeWidth={2} aria-hidden="true" />
                  Install
                </Button>
              {/if}
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </section>

  <p class="foot-note">
    <ShieldCheck size={13} strokeWidth={1.9} aria-hidden="true" />
    Builds are downloaded over HTTPS from the official Node distribution server and never executed
    during installation.
  </p>
</div>

<ConfirmDialog
  open={Boolean(installTarget)}
  title={installTarget ? `Install Node ${installTarget.version}?` : 'Install runtime?'}
  description="devstack downloads the archive once, verifies its SHA-256, then extracts it under the devstack home. Existing sites are not restarted."
  confirmLabel={installTarget ? `Install Node ${installTarget.version}` : 'Install'}
  busy={Boolean(installingId)}
  onconfirm={install}
  oncancel={() => {
    if (!installingId) installTarget = null;
  }}
>
  {#snippet details()}
    {#if installTarget}
      <dl class="install-details">
        <div>
          <dt>Version</dt>
          <dd class="mono">Node {installTarget.version}</dd>
        </div>
        <div>
          <dt>Build</dt>
          <dd class="mono">{installTarget.build}</dd>
        </div>
        <div>
          <dt>Download</dt>
          <dd class="mono">{formatBytes(installTarget.size)}</dd>
        </div>
        <div class="checksum-row">
          <dt>SHA-256</dt>
          <dd class="mono full-hash">{installTarget.sha256}</dd>
        </div>
      </dl>
    {/if}
  {/snippet}
</ConfirmDialog>

<ConfirmDialog
  open={Boolean(removeTarget)}
  title={removeTarget ? `Remove Node ${removeTarget.version}?` : 'Remove runtime?'}
  description={removeTarget
    ? `The extracted build at ${removeTarget.path} will be deleted. This cannot be undone.`
    : ''}
  confirmLabel="Remove build"
  danger
  busy={removing}
  onconfirm={remove}
  oncancel={() => {
    if (!removing) removeTarget = null;
  }}
/>

<style>
  .intro {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px 16px;
    margin-bottom: 22px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .intro-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 34px;
    height: 34px;
    border: 1px solid var(--accent-border);
    border-radius: 8px;
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .intro-copy {
    flex: 1;
    min-width: 0;
  }

  .intro h2 {
    font-size: 13.5px;
    font-weight: 600;
  }

  .intro p {
    margin-top: 3px;
    max-width: 760px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .intro-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    flex: none;
  }

  .banner {
    margin-bottom: 14px;
  }

  .section {
    margin-bottom: 28px;
    scroll-margin-top: calc(var(--topbar-h) + 12px);
  }

  .section-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 9px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
  }

  .eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .section h2 {
    margin-top: 1px;
    font-size: 17px;
    font-weight: 600;
    letter-spacing: -0.015em;
  }

  .section-count {
    color: var(--muted-2);
    font-size: 11px;
  }

  .runtime-list,
  .catalog-list {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .runtime-row {
    display: grid;
    grid-template-columns: 40px minmax(0, 1fr) auto;
    align-items: start;
    gap: 12px;
    padding: 13px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-sm);
  }

  .runtime-row.in-use {
    border-color: color-mix(in srgb, var(--accent) 24%, var(--border));
  }

  .runtime-icon,
  .catalog-icon {
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .runtime-row.in-use .runtime-icon {
    color: var(--success-fg);
    border-color: var(--accent-border);
    background: var(--accent-soft);
  }

  .runtime-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  h3 {
    font-size: 14.5px;
    font-weight: 600;
    letter-spacing: -0.02em;
  }

  .usage-pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--success-fg);
    font-size: 10px;
    font-weight: 500;
  }

  .usage-pill .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  .runtime-path {
    margin-top: 2px;
    color: var(--muted);
    font-size: 10px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .runtime-meta,
  .catalog-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-top: 5px;
    color: var(--muted-2);
    font-size: 9.5px;
  }

  .checksum {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    min-width: 0;
  }

  .runtime-actions {
    display: flex;
    align-items: center;
    padding-top: 2px;
  }

  .usage-note {
    margin-top: 6px;
    color: var(--warning-fg);
    font-size: 10px;
    line-height: 1.45;
  }

  .catalog-row {
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr) auto;
    align-items: center;
    gap: 12px;
    padding: 11px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .catalog-row.installed {
    opacity: 0.76;
  }

  .catalog-icon {
    width: 36px;
    height: 36px;
    border-radius: 7px;
  }

  .catalog-row.busy {
    border-color: var(--accent-border);
  }

  .catalog-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .catalog-title h3 {
    font-size: 13.5px;
  }

  .catalog-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: 96px;
  }

  .installed-note {
    color: var(--muted-2);
    font-size: 10px;
    white-space: nowrap;
  }

  .install-progress {
    position: relative;
    height: 3px;
    margin-top: 8px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--surface-3);
  }

  .install-progress span {
    position: absolute;
    inset: 0 auto 0 0;
    width: 40%;
    border-radius: inherit;
    background: var(--accent);
    animation: indeterminate 1.2s var(--ease) infinite;
  }

  .install-copy {
    margin-top: 5px;
    color: var(--muted);
    font-size: 10px;
  }

  @keyframes indeterminate {
    0% {
      left: -40%;
    }
    100% {
      left: 100%;
    }
  }

  .empty-box {
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .foot-note {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    max-width: 760px;
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.5;
  }

  .foot-note :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .skeleton {
    align-items: center;
  }

  .skeleton-copy {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .install-details {
    display: flex;
    flex-direction: column;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .install-details > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 10px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
  }

  .install-details > div:first-child {
    border-top: 0;
  }

  .install-details dt {
    color: var(--muted);
    font-size: 10.5px;
  }

  .install-details dd {
    margin: 0;
    color: var(--fg);
    font-size: 10.5px;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .checksum-row dd {
    font-size: 9px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  @media (max-width: 900px) {
    .intro {
      flex-wrap: wrap;
    }

    .intro-chips {
      width: 100%;
      padding-left: 46px;
    }
  }

  @media (max-width: 640px) {
    .runtime-row {
      grid-template-columns: 36px minmax(0, 1fr);
    }

    .runtime-icon {
      width: 36px;
      height: 36px;
    }

    .runtime-actions {
      grid-column: 1 / -1;
      justify-content: stretch;
      padding-top: 0;
    }

    .runtime-actions :global(.btn) {
      width: 100%;
    }

    .catalog-row {
      grid-template-columns: 32px minmax(0, 1fr);
    }

    .catalog-icon {
      width: 32px;
      height: 32px;
    }

    .catalog-actions {
      grid-column: 1 / -1;
      justify-content: stretch;
      min-width: 0;
    }

    .catalog-actions :global(.btn) {
      width: 100%;
    }

    .installed-note {
      padding: 4px 0;
    }

    .runtime-path {
      white-space: normal;
      overflow-wrap: anywhere;
    }

    .intro-chips {
      padding-left: 0;
    }
  }
</style>
