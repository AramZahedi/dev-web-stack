<script lang="ts">
  import type { Snippet } from 'svelte';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';

  interface Props {
    open: boolean;
    title: string;
    description?: string;
    confirmLabel?: string;
    cancelLabel?: string;
    danger?: boolean;
    requireText?: string;
    busy?: boolean;
    onconfirm: () => void;
    oncancel: () => void;
    details?: Snippet;
  }

  let {
    open,
    title,
    description,
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
    danger = false,
    requireText,
    busy = false,
    onconfirm,
    oncancel,
    details,
  }: Props = $props();

  let typed = $state('');
  const inputId = `confirm-${Math.random().toString(36).slice(2, 8)}`;
  const ready = $derived(!requireText || typed.trim() === requireText);

  $effect(() => {
    if (open) typed = '';
  });
</script>

<Modal {open} {title} {description} size="sm" onclose={oncancel}>
  {#if details}
    <div class="details">{@render details()}</div>
  {/if}

  {#if requireText}
    <Field
      id={inputId}
      label={`Type “${requireText}” to confirm`}
      hint="This action can’t be undone from the dashboard."
    >
      <input
        id={inputId}
        class="input mono"
        type="text"
        bind:value={typed}
        autocomplete="off"
        spellcheck="false"
        data-autofocus
        aria-invalid={typed.length > 0 && !ready}
      />
    </Field>
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={oncancel} disabled={busy}>{cancelLabel}</Button>
    <Button
      variant={danger ? 'danger' : 'primary'}
      onclick={onconfirm}
      disabled={!ready}
      loading={busy}
    >
      {confirmLabel}
    </Button>
  {/snippet}
</Modal>

<style>
  .details {
    margin-bottom: 16px;
  }
</style>
