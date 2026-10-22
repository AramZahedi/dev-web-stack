<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { IconComponent } from '../icons';

  interface Props {
    variant?: 'neutral' | 'accent' | 'warning' | 'danger' | 'info';
    icon?: IconComponent;
    children: Snippet;
  }

  let { variant = 'neutral', icon: Icon, children }: Props = $props();
</script>

<span class="badge {variant}">
  {#if Icon}
    <Icon size={11} strokeWidth={2.1} aria-hidden="true" />
  {/if}
  {@render children()}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 20px;
    padding: 0 6px;
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--muted);
    font-family: var(--font-sans);
    font-size: 10.5px;
    font-weight: 600;
    line-height: 1;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    white-space: nowrap;
  }

  .badge.accent {
    background: var(--success-soft);
    color: var(--success-fg);
  }

  .badge.warning {
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .badge.danger {
    background: var(--danger-soft);
    color: var(--danger-fg);
  }

  .badge.info {
    background: var(--info-soft);
    color: var(--info-fg);
  }
</style>
