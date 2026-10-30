<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';
  import type { Snippet } from 'svelte';

  interface Props {
    variant?: 'primary' | 'secondary' | 'info' | 'ghost' | 'danger' | 'danger-ghost';
    size?: 'sm' | 'md' | 'lg';
    type?: 'button' | 'submit' | 'reset';
    disabled?: boolean;
    loading?: boolean;
    full?: boolean;
    title?: string;
    ariaLabel?: string;
    ariaExpanded?: boolean;
    ariaControls?: string;
    onclick?: (event: MouseEvent) => void;
    children: Snippet;
  }

  let {
    variant = 'secondary',
    size = 'md',
    type = 'button',
    disabled = false,
    loading = false,
    full = false,
    title,
    ariaLabel,
    ariaExpanded,
    ariaControls,
    onclick,
    children,
  }: Props = $props();
</script>

<button
  {type}
  class="btn {variant} {size}"
  class:full
  class:loading
  disabled={disabled || loading}
  aria-busy={loading || undefined}
  aria-label={ariaLabel}
  aria-expanded={ariaExpanded}
  aria-controls={ariaControls}
  {title}
  {onclick}
>
  {#if loading}
    <LoaderCircle class="spin" size={size === 'sm' ? 13 : 14} strokeWidth={2} aria-hidden="true" />
  {/if}
  {@render children()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    height: 36px;
    padding: 0 14px;
    border: 1px solid transparent;
    border-radius: var(--radius);
    font-family: var(--font-sans);
    font-size: 13px;
    font-weight: 500;
    line-height: 1;
    white-space: nowrap;
    transition:
      background-color 150ms var(--ease),
      border-color 150ms var(--ease),
      color 150ms var(--ease),
      filter 150ms var(--ease);
  }

  .btn:active:not(:disabled) {
    filter: brightness(0.94);
  }

  .btn.sm {
    height: 32px;
    padding: 0 11px;
    font-size: 12.5px;
  }

  .btn.lg {
    height: 40px;
    padding: 0 18px;
    font-size: 13.5px;
  }

  .btn.full {
    width: 100%;
  }

  .btn.primary {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-fg);
    font-weight: 600;
  }

  .btn.primary:hover:not(:disabled) {
    background: var(--accent-hover);
    border-color: var(--accent-hover);
  }

  .btn.info {
    background: var(--info);
    border-color: var(--info);
    color: #ffffff;
    font-weight: 600;
  }

  .btn.info:hover:not(:disabled) {
    filter: brightness(0.92);
  }

  .btn.secondary {
    background: var(--surface-2);
    border-color: var(--border-strong);
    color: var(--fg);
  }

  .btn.secondary:hover:not(:disabled) {
    background: var(--surface-3);
    border-color: var(--control-border);
  }

  .btn.ghost {
    background: transparent;
    color: var(--muted);
  }

  .btn.ghost:hover:not(:disabled) {
    background: var(--surface-2);
    color: var(--fg);
  }

  .btn.danger {
    background: var(--danger);
    border-color: var(--danger);
    color: #ffffff;
    font-weight: 600;
  }

  .btn.danger:hover:not(:disabled) {
    background: var(--danger-hover);
    border-color: var(--danger-hover);
  }

  .btn.danger-ghost {
    background: transparent;
    border-color: var(--danger);
    color: var(--danger-fg);
  }

  .btn.danger-ghost:hover:not(:disabled) {
    background: var(--danger-soft);
  }

  .btn:disabled {
    opacity: 0.48;
    cursor: not-allowed;
  }

  .spin {
    animation: spin 700ms linear infinite;
    flex: none;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
