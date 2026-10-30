<script lang="ts">
  import type { DatabaseSiteRef } from '../../databaseTypes';
  import Badge from '../Badge.svelte';
  import Button from '../Button.svelte';
  import type { LinkedSiteState } from './linkedSites';

  interface Props {
    sites: DatabaseSiteRef[];
    states: Record<string, LinkedSiteState>;
    stopping: Record<string, boolean>;
    error?: string;
    onstop: (site: DatabaseSiteRef) => void;
  }

  let { sites, states, stopping, error, onstop }: Props = $props();

  function stateLabel(state: LinkedSiteState | undefined): string {
    if (state === 'stopped') return 'Stopped';
    if (state === 'stopping') return 'Stopping';
    if (state === 'running') return 'Running';
    return 'Unknown';
  }

  function stateVariant(state: LinkedSiteState | undefined): 'accent' | 'warning' | 'neutral' {
    if (state === 'stopped') return 'accent';
    if (state === 'running' || state === 'stopping') return 'warning';
    return 'neutral';
  }
</script>

{#if sites.length > 0}
  <div class="linked-sites">
    <p class="warning" role="alert">
      Linked sites must be stopped before this operation. Devstack never stops them silently.
    </p>
    {#each sites as site (site.id)}
      <div class="linked-site">
        <span class="site-name mono">{site.name}</span>
        <Badge variant={stateVariant(states[site.id])}>{stateLabel(states[site.id])}</Badge>
        <Button
          size="sm"
          variant="secondary"
          ariaLabel={`Stop site ${site.name}`}
          loading={stopping[site.id] ?? false}
          disabled={states[site.id] === 'stopped' || states[site.id] === 'stopping'}
          onclick={() => onstop(site)}
        >
          Stop site
        </Button>
      </div>
    {/each}
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}
  </div>
{/if}

<style>
  .linked-sites {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .warning {
    padding: 8px 9px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .linked-site {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
  }

  .site-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    color: var(--fg);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }
</style>
