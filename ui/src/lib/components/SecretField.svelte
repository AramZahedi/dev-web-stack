<script lang="ts">
  import Button from './Button.svelte';

  interface Props {
    label: string;
    value?: string;
    revealed: boolean;
    loading?: boolean;
    error?: string;
    onreveal: () => void;
    onhide: () => void;
    oncopy?: () => void;
  }

  let { label, value = '', revealed, loading = false, error, onreveal, onhide, oncopy }: Props = $props();
</script>

<div class="secret-field">
  <span class="label">{label}</span>
  <code class="value">{revealed ? value : '••••••••'}</code>
  <div class="actions">
    {#if revealed}
      <Button variant="ghost" size="sm" ariaLabel={`Hide ${label}`} onclick={onhide}>Hide</Button>
      {#if oncopy}
        <Button variant="ghost" size="sm" ariaLabel={`Copy ${label}`} onclick={oncopy}>Copy</Button>
      {/if}
    {:else}
      <Button
        variant="secondary"
        size="sm"
        ariaLabel={`Reveal ${label}`}
        loading={loading}
        onclick={onreveal}
      >
        Reveal
      </Button>
    {/if}
  </div>
  {#if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</div>

<style>
  .secret-field {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 8px;
    padding: 6px 0;
  }

  .label {
    color: var(--muted);
    font-size: 12px;
  }

  .value {
    overflow: hidden;
    padding: 4px 7px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .actions {
    display: flex;
    gap: 4px;
  }

  .error {
    grid-column: 1 / -1;
    margin: 0;
    color: var(--danger-fg);
    font-size: 12px;
  }
</style>
