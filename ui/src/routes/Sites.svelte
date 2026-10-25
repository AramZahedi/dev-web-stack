<script lang="ts">
  import { onMount } from 'svelte';
  import { push } from 'svelte-spa-router';
  import {
    Activity,
    Boxes,
    Code,
    ExternalLink,
    FileCode,
    Globe,
    Play,
    Plus,
    RotateCw,
    Search,
    Square,
    Trash2,
    TriangleAlert,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { describeError } from '../lib/errors';
  import { runtimeLabel, siteUrl } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type { RuntimeBuild, Site, SiteKind } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import StatusPill from '../lib/components/StatusPill.svelte';

  type Filter = 'all' | 'running' | 'stopped' | 'crashed';
  type Action = 'start' | 'stop' | 'restart';

  let sites = $state<Site[]>([]);
  let runtimes = $state<RuntimeBuild[]>([]);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let query = $state('');
  let filter = $state<Filter>('all');
  let busy = $state<Record<string, Action>>({});
  let deleteTarget = $state<Site | null>(null);
  let deleting = $state(false);

  const kindIcons: Record<SiteKind, typeof Globe> = {
    static: Globe,
    node: Code,
    php: FileCode,
  };

  const isActive = (site: Site) =>
    site.status === 'running' || site.status === 'starting' || site.status === 'stopping';
  const isCrashed = (site: Site) => site.status === 'crashed' || site.status === 'degraded' || site.status === 'error';

  const counts = $derived.by(() => ({
    all: sites.length,
    running: sites.filter(isActive).length,
    stopped: sites.filter((site) => site.status === 'stopped').length,
    crashed: sites.filter(isCrashed).length,
  }));

  const filtered = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    return sites.filter((site) => {
      if (filter === 'running' && !isActive(site)) return false;
      if (filter === 'stopped' && site.status !== 'stopped') return false;
      if (filter === 'crashed' && !isCrashed(site)) return false;
      if (!needle) return true;
      return (
        site.name.toLowerCase().includes(needle) ||
        site.domain.toLowerCase().includes(needle) ||
        site.host_path.toLowerCase().includes(needle)
      );
    });
  });

  const nodeBuilds = $derived(runtimes.filter((runtime) => runtime.kind === 'node').length);
  const phpBuilds = $derived(runtimes.filter((runtime) => runtime.kind === 'php').length);
  const unavailable = $derived(Boolean(error) && sites.length === 0);

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      const [siteList, runtimeList] = await Promise.all([api.listSites(), api.listRuntimes()]);
      sites = siteList;
      runtimes = runtimeList;
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
    if (loading) return;
    if (!sites.some((site) => site.status === 'starting' || site.status === 'stopping')) return;
    const timer = setInterval(() => void load(), 2000);
    return () => clearInterval(timer);
  });

  function openSite(site: Site): void {
    push(`/sites/${encodeURIComponent(site.id)}`);
  }

  async function act(site: Site, action: Action): Promise<void> {
    if (busy[site.id]) return;
    busy = { ...busy, [site.id]: action };
    try {
      if (action === 'start') await api.startSite(site.id);
      if (action === 'stop') await api.stopSite(site.id);
      if (action === 'restart') await api.restartSite(site.id);
      addToast({
        kind: 'success',
        title: `${site.name} ${action === 'stop' ? 'stopped' : action === 'start' ? 'started' : 'restarted'}`,
        message: `${site.domain} is ${action === 'stop' ? 'stopped' : 'running'}.`,
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not ${action} ${site.name}`, `${described.code}: ${described.message}`);
    } finally {
      const next = { ...busy };
      delete next[site.id];
      busy = next;
    }
  }

  async function confirmDelete(): Promise<void> {
    if (!deleteTarget) return;
    deleting = true;
    try {
      await api.deleteSite(deleteTarget.id);
      addToast({
        kind: 'success',
        title: `${deleteTarget.name} removed`,
        message: 'The domain and certificate were removed. Project files are untouched.',
      });
      deleteTarget = null;
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not remove site', `${described.code}: ${described.message}`);
    } finally {
      deleting = false;
    }
  }

  function clearFilters(): void {
    query = '';
    filter = 'all';
  }

  const filterOptions: { value: Filter; label: string; count: keyof typeof counts }[] = [
    { value: 'all', label: 'All', count: 'all' },
    { value: 'running', label: 'Running', count: 'running' },
    { value: 'stopped', label: 'Stopped', count: 'stopped' },
    { value: 'crashed', label: 'Crashed', count: 'crashed' },
  ];
</script>

<div class="page">
  <section class="stats" aria-label="Site summary">
    <div class="tile">
      <div class="tile-head">
        <span class="tile-label">Sites</span>
        <span class="tile-icon" aria-hidden="true"><Globe size={15} strokeWidth={1.8} /></span>
      </div>
      {#if loading}
        <Skeleton width="42px" height="30px" radius="5px" />
        <Skeleton width="90px" height="10px" />
      {:else}
        <p class="tile-value mono">{unavailable ? '—' : sites.length}</p>
        <p class="tile-context">{unavailable ? 'Unavailable' : sites.length === 1 ? '1 local project' : 'Local projects'}</p>
      {/if}
    </div>

    <div class="tile">
      <div class="tile-head">
        <span class="tile-label">Running</span>
        <span class="tile-icon running" aria-hidden="true">
          <Activity size={15} strokeWidth={1.9} />
        </span>
      </div>
      {#if loading}
        <Skeleton width="42px" height="30px" radius="5px" />
        <Skeleton width="90px" height="10px" />
      {:else}
        <p class="tile-value mono">{unavailable ? '—' : counts.running}</p>
        <p class="tile-context">
          {unavailable ? 'Unavailable' : `${counts.running} of ${sites.length} ${sites.length === 1 ? 'project' : 'projects'} active`}
        </p>
      {/if}
    </div>

    <div class="tile" class:has-failure={counts.crashed > 0}>
      <div class="tile-head">
        <span class="tile-label">Crashed</span>
        <span class="tile-icon failure" aria-hidden="true">
          <TriangleAlert size={15} strokeWidth={1.9} />
        </span>
      </div>
      {#if loading}
        <Skeleton width="42px" height="30px" radius="5px" />
        <Skeleton width="90px" height="10px" />
      {:else}
        <p class="tile-value mono">{unavailable ? '—' : counts.crashed}</p>
        <p class="tile-context">{unavailable ? 'Unavailable' : counts.crashed > 0 ? 'Needs attention' : 'No failures'}</p>
      {/if}
    </div>

    <div class="tile">
      <div class="tile-head">
        <span class="tile-label">Runtimes</span>
        <span class="tile-icon" aria-hidden="true"><Boxes size={15} strokeWidth={1.8} /></span>
      </div>
      {#if loading}
        <Skeleton width="42px" height="30px" radius="5px" />
        <Skeleton width="90px" height="10px" />
      {:else}
        <p class="tile-value mono">{unavailable ? '—' : runtimes.length}</p>
        <p class="tile-context">
          {unavailable
            ? 'Unavailable'
            : `${nodeBuilds} Node · ${phpBuilds} PHP ${nodeBuilds + phpBuilds === 1 ? 'build' : 'builds'}`}
        </p>
      {/if}
    </div>
  </section>

  <div class="toolbar">
    <div class="search">
      <Search size={14} strokeWidth={2} aria-hidden="true" />
      <label class="sr-only" for="site-search">Search sites</label>
      <input
        id="site-search"
        class="input"
        type="search"
        placeholder="Search sites or domains"
        bind:value={query}
      />
    </div>

    <div class="filters" role="group" aria-label="Filter by status">
      {#each filterOptions as option (option.value)}
        <button
          type="button"
          class="filter"
          class:selected={filter === option.value}
          aria-pressed={filter === option.value}
          onclick={() => (filter = option.value)}
        >
          {option.label}
          <span class="count">{counts[option.count]}</span>
        </button>
      {/each}
    </div>

    <div class="add">
      <Button variant="primary" onclick={() => push('/sites/new')}>
        <Plus size={15} strokeWidth={2.2} aria-hidden="true" />
        Add site
      </Button>
    </div>
  </div>

  {#if error}
    <div class="banner-slot">
      <ErrorBanner
        title="Could not load sites"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
        ondismiss={sites.length > 0 ? () => (error = null) : undefined}
      />
    </div>
  {/if}

  {#if loading}
    <div class="loading-state" aria-busy="true" aria-label="Loading sites">
      <div class="table-card">
        <div class="table-skeleton">
          {#each Array(5) as _, index (index)}
            <div class="skeleton-row">
              <Skeleton width="9%" height="20px" radius="999px" />
              <Skeleton width="28%" height="13px" />
              <Skeleton width="9%" height="18px" radius="6px" />
              <Skeleton width="11%" height="11px" />
              <Skeleton width="17%" height="11px" />
            </div>
          {/each}
        </div>
      </div>
      <div class="mobile-list loading-cards" aria-hidden="true">
        {#each Array(3) as _, index (index)}
          <div class="site-card skeleton-card">
            <div class="skeleton-card-top">
              <div class="skeleton-card-copy">
                <Skeleton width="46%" height="15px" />
                <Skeleton width="32%" height="10px" />
              </div>
              <Skeleton width="72px" height="20px" radius="999px" />
            </div>
            <Skeleton width="58%" height="10px" />
            <Skeleton width="42%" height="10px" />
            <div class="skeleton-card-actions">
              <Skeleton width="32%" height="32px" radius="8px" />
              <Skeleton width="27%" height="32px" radius="8px" />
              <Skeleton width="27%" height="32px" radius="8px" />
            </div>
          </div>
        {/each}
      </div>
    </div>
  {:else if sites.length === 0 && !error}
    <div class="state-card">
      <EmptyState
        icon={Globe}
        title="No local sites yet"
        description="Add a project folder and devstack will give it a trusted .test domain, a certificate and a supervised runtime."
      >
        <Button variant="primary" onclick={() => push('/sites/new')}>
          <Plus size={15} strokeWidth={2.2} aria-hidden="true" />
          Add your first site
        </Button>
      </EmptyState>
    </div>
  {:else if sites.length > 0 && filtered.length === 0}
    <div class="state-card">
      <EmptyState icon={Search} title="No sites match" description="Try another search or clear the status filter.">
        <Button variant="secondary" onclick={clearFilters}>Clear filters</Button>
      </EmptyState>
    </div>
  {:else}
    <div class="table-card">
      <table>
        <caption class="sr-only">Local sites and their status</caption>
        <thead>
          <tr>
            <th scope="col" class="col-status">Status</th>
            <th scope="col">Site</th>
            <th scope="col" class="col-kind">Kind</th>
            <th scope="col" class="col-runtime">Runtime</th>
            <th scope="col" class="col-url">URL</th>
            <th scope="col" class="col-actions"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {#each filtered as site (site.id)}
            {@const KindIcon = kindIcons[site.kind]}
            <tr onclick={() => openSite(site)}>
              <td class="col-status"><StatusPill status={site.status} compact /></td>
              <td>
                <a
                  class="site-link"
                  href={`#/sites/${encodeURIComponent(site.id)}`}
                  onclick={(event) => event.stopPropagation()}
                >
                  {site.name}
                </a>
                <span class="domain mono">{site.domain}</span>
              </td>
              <td class="col-kind">
                <Badge icon={KindIcon}>{site.kind}</Badge>
              </td>
              <td class="col-runtime mono runtime">{runtimeLabel(site)}</td>
              <td class="col-url">
                <a
                  class="url-link mono"
                  href={siteUrl(site)}
                  target="_blank"
                  rel="noreferrer"
                  title="Open {siteUrl(site)}"
                  onclick={(event) => event.stopPropagation()}
                >
                  {siteUrl(site)}
                  <ExternalLink size={11} strokeWidth={2} aria-hidden="true" />
                </a>
              </td>
              <td class="col-actions" onclick={(event) => event.stopPropagation()}>
                <div class="row-actions">
                  {#if isActive(site)}
                    <IconButton
                      label="Stop {site.name}"
                      icon={Square}
                      size="sm"
                      loading={busy[site.id] === 'stop'}
                      disabled={Boolean(busy[site.id])}
                      onclick={() => act(site, 'stop')}
                    />
                  {:else}
                    <IconButton
                      label="Start {site.name}"
                      icon={Play}
                      size="sm"
                      loading={busy[site.id] === 'start'}
                      disabled={Boolean(busy[site.id])}
                      onclick={() => act(site, 'start')}
                    />
                  {/if}
                  <IconButton
                    label="Restart {site.name}"
                    icon={RotateCw}
                    size="sm"
                    loading={busy[site.id] === 'restart'}
                    disabled={Boolean(busy[site.id])}
                    onclick={() => act(site, 'restart')}
                  />
                  <IconButton
                    label="Remove {site.name}"
                    icon={Trash2}
                    size="sm"
                    variant="danger"
                    disabled={Boolean(busy[site.id])}
                    onclick={() => (deleteTarget = site)}
                  />
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      <div class="table-foot">
        <span>Showing {filtered.length} of {sites.length}</span>
        <span class="foot-hint">Select a row to open</span>
      </div>
    </div>

    <div class="mobile-list">
      {#each filtered as site (site.id)}
        {@const KindIcon = kindIcons[site.kind]}
        <article class="site-card">
          <div class="card-top">
            <div class="card-name">
              <a href={`#/sites/${encodeURIComponent(site.id)}`}>{site.name}</a>
              <span class="domain mono">{site.domain}</span>
            </div>
            <StatusPill status={site.status} compact />
          </div>
          <div class="card-meta">
            <Badge icon={KindIcon}>{site.kind}</Badge>
            <span class="mono runtime">{runtimeLabel(site)}</span>
          </div>
          <a
            class="card-url mono"
            href={siteUrl(site)}
            target="_blank"
            rel="noreferrer"
            onclick={(event) => event.stopPropagation()}
          >
            {siteUrl(site)}
            <ExternalLink size={11} strokeWidth={2} aria-hidden="true" />
          </a>
          <div class="card-actions">
            {#if isActive(site)}
              <Button size="sm" variant="secondary" disabled={Boolean(busy[site.id])} onclick={() => act(site, 'stop')}>
                <Square size={12} strokeWidth={2} aria-hidden="true" />
                Stop
              </Button>
            {:else}
              <Button size="sm" variant="secondary" disabled={Boolean(busy[site.id])} onclick={() => act(site, 'start')}>
                <Play size={12} strokeWidth={2} aria-hidden="true" />
                Start
              </Button>
            {/if}
            <Button size="sm" variant="ghost" disabled={Boolean(busy[site.id])} onclick={() => act(site, 'restart')}>
              <RotateCw size={12} strokeWidth={2} aria-hidden="true" />
              Restart
            </Button>
            <Button size="sm" variant="danger-ghost" onclick={() => (deleteTarget = site)}>
              <Trash2 size={12} strokeWidth={2} aria-hidden="true" />
              Remove
            </Button>
          </div>
        </article>
      {/each}
    </div>
  {/if}
</div>

<ConfirmDialog
  open={Boolean(deleteTarget)}
  title={deleteTarget ? `Remove ${deleteTarget.name}?` : 'Remove site?'}
  description={deleteTarget
    ? `This stops the site and removes its ${deleteTarget.domain} domain, certificate and generated configs. Files in ${deleteTarget.host_path} stay on disk.`
    : ''}
  confirmLabel="Remove site"
  requireText={deleteTarget?.name}
  danger
  busy={deleting}
  onconfirm={confirmDelete}
  oncancel={() => {
    if (!deleting) deleteTarget = null;
  }}
/>

<style>
  .stats {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 12px;
    margin-bottom: 18px;
  }

  .tile {
    display: flex;
    flex-direction: column;
    gap: 7px;
    min-height: 106px;
    padding: 12px 14px 13px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
  }

  .tile.has-failure {
    border-color: color-mix(in srgb, var(--danger) 36%, var(--border));
  }

  .tile-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .tile-label {
    color: var(--muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .tile-icon {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .tile-icon.running {
    color: var(--success-fg);
    border-color: var(--accent-border);
    background: var(--accent-soft);
  }

  .tile-icon.failure {
    color: var(--danger-fg);
  }

  .has-failure .tile-icon.failure {
    border-color: color-mix(in srgb, var(--danger) 36%, var(--border));
    background: var(--danger-soft);
  }

  .tile-value {
    margin-top: 2px;
    color: var(--fg);
    font-size: 27px;
    font-weight: 600;
    line-height: 1;
    letter-spacing: -0.04em;
  }

  .tile-context {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }

  .search {
    position: relative;
    flex: 1 1 240px;
    max-width: 340px;
  }

  .search > :global(svg) {
    position: absolute;
    left: 10px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .search :global(.input) {
    padding-left: 32px;
    background: var(--surface);
  }

  .filters {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 3px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
  }

  .filter {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 28px;
    padding: 0 8px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--muted);
    font-size: 11.5px;
    font-weight: 500;
    white-space: nowrap;
    transition:
      background-color 150ms var(--ease),
      color 150ms var(--ease);
  }

  .filter:hover {
    color: var(--fg);
    background: var(--surface-2);
  }

  .filter.selected {
    background: var(--surface-2);
    color: var(--fg);
    box-shadow: inset 0 0 0 1px var(--border);
  }

  .count {
    display: inline-grid;
    place-items: center;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border-radius: 4px;
    background: var(--surface-3);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 9.5px;
    line-height: 1;
  }

  .filter.selected .count {
    color: var(--fg);
  }

  .add {
    margin-left: auto;
  }

  .banner-slot {
    margin-bottom: 12px;
  }

  .table-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    overflow: hidden;
    box-shadow: var(--shadow-sm);
  }

  table {
    width: 100%;
    border-collapse: separate;
    border-spacing: 0;
    table-layout: fixed;
  }

  th {
    height: 36px;
    padding: 0 12px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-align: left;
    text-transform: uppercase;
    white-space: nowrap;
  }

  th:first-child {
    padding-left: 16px;
  }

  td {
    height: 54px;
    padding: 8px 12px;
    border-top: 1px solid var(--border);
    vertical-align: middle;
  }

  td:first-child {
    padding-left: 16px;
  }

  tbody tr {
    cursor: pointer;
    transition: background-color 150ms var(--ease);
  }

  tbody tr:hover {
    background: var(--surface-2);
  }

  .col-status {
    width: 118px;
  }

  .col-kind {
    width: 86px;
  }

  .col-runtime {
    width: 112px;
  }

  .col-url {
    width: 200px;
  }

  .col-actions {
    width: 122px;
  }

  .site-link {
    display: block;
    width: fit-content;
    max-width: 100%;
    color: var(--fg);
    font-size: 13.5px;
    font-weight: 500;
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .site-link:hover {
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .domain {
    display: block;
    margin-top: 1px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.4;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .runtime {
    color: var(--muted);
    font-size: 10.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .url-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 100%;
    color: var(--muted);
    font-size: 10.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .url-link :global(svg) {
    flex: none;
    opacity: 0.7;
  }

  .url-link:hover {
    color: var(--fg);
  }

  .row-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 1px;
  }

  .table-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    height: 34px;
    padding: 0 16px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--muted-2);
    font-size: 10.5px;
  }

  .foot-hint {
    color: var(--muted-2);
  }

  .table-skeleton {
    display: flex;
    flex-direction: column;
    padding: 0 16px;
  }

  .skeleton-row {
    display: flex;
    align-items: center;
    gap: 24px;
    height: 54px;
    border-bottom: 1px solid var(--border);
  }

  .skeleton-row:last-child {
    border-bottom: 0;
  }

  .state-card {
    padding: 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
    overflow: hidden;
  }

  .loading-state {
    min-width: 0;
  }

  .skeleton-card-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }

  .skeleton-card-copy {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
    min-width: 0;
  }

  .skeleton-card-actions {
    display: flex;
    gap: 6px;
    margin-top: 1px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
  }

  .mobile-list {
    display: none;
  }

  @media (max-width: 1120px) {
    .col-url {
      display: none;
    }
  }

  @media (max-width: 900px) {
    .stats {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .tile {
      min-height: 98px;
    }
  }

  @media (max-width: 860px) and (min-width: 768px) {
    .search {
      flex-basis: 100%;
      max-width: none;
    }
  }

  @media (max-width: 767px) {
    .search {
      flex-basis: 100%;
    }

    .filters {
      flex: 1;
    }

    .filter {
      flex: 1;
      justify-content: center;
      padding: 0 6px;
    }

    .add {
      width: 100%;
      margin-left: 0;
    }

    .table-card {
      display: none;
    }

    .mobile-list {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }

    .site-card {
      display: flex;
      flex-direction: column;
      gap: 9px;
      padding: 13px 14px;
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius-lg);
      box-shadow: var(--shadow-sm);
    }

    .card-top {
      display: flex;
      align-items: flex-start;
      justify-content: space-between;
      gap: 10px;
    }

    .card-name a {
      display: block;
      color: var(--fg);
      font-size: 14.5px;
      font-weight: 600;
      line-height: 1.35;
    }

    .card-name .domain {
      font-size: 10.5px;
    }

    .card-meta {
      display: flex;
      align-items: center;
      gap: 8px;
    }

    .card-url {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      width: fit-content;
      max-width: 100%;
      color: var(--muted);
      font-size: 10.5px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .card-url:hover {
      color: var(--fg);
    }

    .card-actions {
      display: flex;
      align-items: center;
      gap: 6px;
      padding-top: 9px;
      border-top: 1px solid var(--border);
      margin-top: 1px;
    }

    .card-actions :global(.btn) {
      flex: 1;
    }
  }

  @media (max-width: 390px) {
    .card-actions {
      flex-wrap: wrap;
    }

    .card-actions :global(.btn) {
      flex: 1 1 30%;
      padding: 0 8px;
    }
  }
</style>
