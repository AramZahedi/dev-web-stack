<script lang="ts">
  import { CircleAlert } from 'lucide-svelte';
  import type { Snippet } from 'svelte';

  interface Props {
    id: string;
    label: string;
    hint?: string;
    error?: string;
    required?: boolean;
    children: Snippet;
  }

  let { id, label, hint, error, required = false, children }: Props = $props();
</script>

<div class="field" class:invalid={Boolean(error)}>
  <label for={id}>
    {label}
    {#if required}<span class="required" aria-hidden="true">*</span>{/if}
  </label>
  {@render children()}
  {#if error}
    <p class="error" id="{id}-error">
      <CircleAlert size={12} strokeWidth={2.1} aria-hidden="true" />
      <span>{error}</span>
    </p>
  {:else if hint}
    <p class="hint" id="{id}-hint">{hint}</p>
  {/if}
</div>

<style>
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  label {
    display: flex;
    align-items: center;
    gap: 3px;
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
    line-height: 1.3;
  }

  .required {
    color: var(--danger-fg);
  }

  .hint,
  .error {
    display: flex;
    align-items: flex-start;
    gap: 5px;
    margin: 0;
    font-size: 11.5px;
    line-height: 1.45;
  }

  .hint {
    color: var(--muted);
  }

  .error {
    color: var(--danger-fg);
  }

  .error :global(svg) {
    flex: none;
    margin-top: 1px;
  }
</style>
