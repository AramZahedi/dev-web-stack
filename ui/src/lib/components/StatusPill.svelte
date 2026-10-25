<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';
  import type { SiteStatus } from '../types';

  interface Props {
    status: SiteStatus;
    compact?: boolean;
  }

  let { status, compact = false }: Props = $props();

  const labels: Record<SiteStatus, string> = {
    stopped: 'Stopped',
    starting: 'Starting',
    running: 'Running',
    stopping: 'Stopping',
    crashed: 'Crashed',
    degraded: 'Degraded',
    error: 'Error',
  };

  const transient = $derived(status === 'starting' || status === 'stopping');
  const label = $derived(labels[status] ?? status);
</script>

<span class="pill {status}" class:compact aria-label="Status: {label}">
  {#if transient}
    <LoaderCircle class="spin" size={11} strokeWidth={2.2} aria-hidden="true" />
  {:else}
    <span class="dot" aria-hidden="true"></span>
  {/if}
  <span class="label">{label}</span>
</span>

<style>
  .pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 22px;
    padding: 0 8px;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 11.5px;
    font-weight: 500;
    line-height: 1;
    white-space: nowrap;
  }

  .pill.compact {
    height: 20px;
    padding: 0 7px;
    font-size: 11px;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
    flex: none;
  }

  .running {
    background: var(--success-soft);
    color: var(--success-fg);
  }

  .running .dot {
    box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 14%, transparent);
  }

  .stopped .dot {
    background: transparent;
    border: 1.5px solid currentColor;
  }

  .starting,
  .stopping {
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .crashed,
  .error {
    background: var(--danger-soft);
    color: var(--danger-fg);
  }

  .degraded {
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .spin {
    animation: spin 800ms linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
