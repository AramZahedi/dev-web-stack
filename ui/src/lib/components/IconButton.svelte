<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';
  import type { IconComponent } from '../icons';

  interface Props {
    label: string;
    icon: IconComponent;
    variant?: 'ghost' | 'outline' | 'danger';
    size?: 'sm' | 'md';
    disabled?: boolean;
    loading?: boolean;
    pressed?: boolean;
    href?: string;
    external?: boolean;
    title?: string;
    onclick?: (event: MouseEvent) => void;
  }

  let {
    label,
    icon: Icon,
    variant = 'ghost',
    size = 'md',
    disabled = false,
    loading = false,
    pressed,
    href,
    external = false,
    title,
    onclick,
  }: Props = $props();

  const iconSize = $derived(size === 'sm' ? 14 : 16);
</script>

{#if href}
  <a
    class="icon-btn {variant} {size}"
    href={disabled ? undefined : href}
    aria-label={label}
    aria-disabled={disabled || undefined}
    title={title ?? label}
    target={external ? '_blank' : undefined}
    rel={external ? 'noreferrer' : undefined}
    onclick={disabled ? (event) => event.preventDefault() : onclick}
  >
    <Icon size={iconSize} strokeWidth={1.9} aria-hidden="true" />
  </a>
{:else}
  <button
    type="button"
    class="icon-btn {variant} {size}"
    aria-label={label}
    aria-pressed={pressed}
    aria-busy={loading || undefined}
    {title}
    disabled={disabled || loading}
    {onclick}
  >
    {#if loading}
      <LoaderCircle class="spin" size={iconSize} strokeWidth={2} aria-hidden="true" />
    {:else}
      <Icon size={iconSize} strokeWidth={1.9} aria-hidden="true" />
    {/if}
  </button>
{/if}

<style>
  .icon-btn {
    display: inline-grid;
    place-items: center;
    flex: none;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: transparent;
    color: var(--muted);
    transition:
      background-color 150ms var(--ease),
      border-color 150ms var(--ease),
      color 150ms var(--ease),
      filter 150ms var(--ease);
  }

  .icon-btn:active:not(:disabled) {
    filter: brightness(0.94);
  }

  .icon-btn.sm {
    width: 28px;
    height: 28px;
    border-radius: 6px;
  }

  .icon-btn.ghost:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .icon-btn.outline {
    border-color: var(--border-strong);
    background: var(--surface-2);
    color: var(--fg);
  }

  .icon-btn.outline:hover {
    background: var(--surface-3);
    border-color: var(--control-border);
  }

  .icon-btn.danger:hover {
    background: var(--danger-soft);
    color: var(--danger-fg);
  }

  .icon-btn[aria-pressed='true'] {
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .spin {
    animation: spin 700ms linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .icon-btn[aria-disabled='true'],
  .icon-btn:disabled {
    opacity: 0.45;
    pointer-events: none;
    cursor: not-allowed;
  }
</style>
