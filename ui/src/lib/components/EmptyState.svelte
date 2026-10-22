<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { IconComponent } from '../icons';

  interface Props {
    icon: IconComponent;
    title: string;
    description?: string;
    children?: Snippet;
    compact?: boolean;
  }

  let { icon: Icon, title, description, children, compact = false }: Props = $props();
</script>

<div class="empty" class:compact>
  <span class="icon-box" aria-hidden="true">
    <Icon size={20} strokeWidth={1.7} />
  </span>
  <h3>{title}</h3>
  {#if description}<p>{description}</p>{/if}
  {#if children}
    <div class="actions">{@render children()}</div>
  {/if}
</div>

<style>
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    padding: 44px 24px;
  }

  .empty.compact {
    padding: 28px 20px;
  }

  .icon-box {
    display: grid;
    place-items: center;
    width: 42px;
    height: 42px;
    margin-bottom: 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
    color: var(--muted);
  }

  h3 {
    font-size: 14px;
    font-weight: 600;
  }

  p {
    max-width: 360px;
    margin-top: 5px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.55;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 8px;
    margin-top: 16px;
  }
</style>
