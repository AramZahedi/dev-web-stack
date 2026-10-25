<script lang="ts">
  import { onMount } from 'svelte';
  import { Server } from 'lucide-svelte';
  import * as api from '../lib/api';
  import type { ServiceView, ServicePort } from '../lib/types';
  import { describeError, type DescribedError } from '../lib/errors';
  import Badge from '../lib/components/Badge.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  let services = $state<ServiceView[]>([]);
  let loading = $state(true);
  let error = $state<DescribedError | null>(null);

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

  function formatPorts(ports?: ServicePort[]): string {
    if (!ports || ports.length === 0) return '—';
    return ports
      .map((p) => {
        if (p.host && p.container) {
          return `${p.host}:${p.container}`;
        }
        return `${p.host || p.container}`;
      })
      .join(', ');
  }

  function stateVariant(state: string, active: boolean): 'accent' | 'neutral' | 'danger' | 'warning' | 'info' {
    if (active) return 'accent';
    switch (state) {
      case 'active':
        return 'accent';
      case 'installed':
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

  onMount(() => {
    void load(true);
    const refresh = () => void load();
    window.addEventListener('devstack:refresh', refresh);
    return () => window.removeEventListener('devstack:refresh', refresh);
  });
</script>

<div class="page">
  <section class="intro" aria-labelledby="services-intro">
    <span class="intro-icon" aria-hidden="true"><Server size={19} strokeWidth={1.8} /></span>
    <div class="intro-copy">
      <h2 id="services-intro">Infrastructure Services</h2>
      <p>
        Docker Compose-managed containers for web servers, databases, and caches.
        Services are configured with loopback isolation and fixed catalog specifications.
      </p>
    </div>
    <div class="intro-chips">
      <Badge variant="accent">Loopback isolated</Badge>
      <Badge>Read-only skeleton</Badge>
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
    <div class="loading-state" aria-busy="true" aria-label="Loading services">
      <div class="table-card">
        <div class="table-skeleton">
          {#each Array(3) as _, index (index)}
            <div class="skeleton-row">
              <Skeleton width="20%" height="16px" />
              <Skeleton width="15%" height="16px" />
              <Skeleton width="12%" height="18px" radius="6px" />
              <Skeleton width="15%" height="16px" />
              <Skeleton width="25%" height="16px" />
            </div>
          {/each}
        </div>
      </div>
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
    <div class="table-card">
      <table class="data-table">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Group</th>
            <th scope="col">Status</th>
            <th scope="col">Ports</th>
            <th scope="col">Image</th>
          </tr>
        </thead>
        <tbody>
          {#each services as svc (svc.id)}
            <tr>
              <td class="cell-name">
                <span class="name">{svc.display_name || svc.id}</span>
                <span class="id">{svc.id}</span>
              </td>
              <td>
                <span class="group-tag">{svc.group}</span>
              </td>
              <td>
                <span class="status-cell">
                  <Badge variant={stateVariant(svc.state, svc.active)}>
                    {svc.state}
                  </Badge>
                </span>
              </td>
              <td class="cell-ports">
                <span class="ports">{formatPorts(svc.ports)}</span>
              </td>
              <td class="cell-image">
                <span class="image-text" title={svc.image}>{svc.image || '—'}</span>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

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

  .table-card {
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    overflow: hidden;
  }

  .data-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
    text-align: left;
  }

  .data-table th {
    background: var(--surface-2);
    color: var(--muted);
    font-weight: 600;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }

  .data-table td {
    padding: 14px 16px;
    border-bottom: 1px solid var(--border-subtle);
    color: var(--text);
    vertical-align: middle;
  }

  .data-table tr:last-child td {
    border-bottom: none;
  }

  .cell-name {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .cell-name .name {
    font-weight: 600;
    color: var(--text);
  }

  .cell-name .id {
    font-size: 11px;
    color: var(--muted);
    font-family: var(--font-mono);
  }

  .group-tag {
    font-size: 12px;
    color: var(--muted);
    background: var(--surface-2);
    padding: 2px 6px;
    border-radius: var(--radius-sm);
  }

  .status-cell {
    display: inline-flex;
    align-items: center;
  }

  .cell-ports .ports {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text);
  }

  .cell-image .image-text {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--muted);
    max-width: 250px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: inline-block;
  }

  .state-card {
    background: var(--surface-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .table-skeleton {
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .skeleton-row {
    display: flex;
    gap: 16px;
    align-items: center;
  }
</style>
