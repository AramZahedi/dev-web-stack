<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { Database } from '../../databaseTypes';
  import Button from '../Button.svelte';
  import Field from '../Field.svelte';
  import Modal from '../Modal.svelte';

  interface Props {
    open: boolean;
    mode: 'create' | 'rename';
    database?: Database | null;
    busy?: boolean;
    disabled?: boolean;
    error?: string;
    onsubmit: (name: string) => void;
    onclose: () => void;
    details?: Snippet;
  }

  let {
    open,
    mode,
    database = null,
    busy = false,
    disabled = false,
    error,
    onsubmit,
    onclose,
    details,
  }: Props = $props();

  let name = $state('');
  let confirmName = $state('');
  let validation = $state('');

  $effect(() => {
    if (!open) return;
    name = mode === 'rename' ? (database?.name ?? '') : '';
    confirmName = '';
    validation = '';
  });

  const renameConfirmed = $derived(
    mode !== 'rename' || confirmName === (database?.name ?? ''),
  );
  const confirmMismatch = $derived(
    mode === 'rename' && confirmName.length > 0 && confirmName !== (database?.name ?? ''),
  );

  const title = $derived(
    mode === 'rename' ? `Rename ${database?.name ?? 'database'}` : 'Create database',
  );
  const description = $derived(
    mode === 'rename'
      ? 'PostgreSQL renames the database in place. Linked sites must be stopped and restarted after the rename.'
      : 'Creates an empty database on this service. It belongs to the service, not to a site.',
  );

  function submit(): void {
    if (disabled) return;
    if (!renameConfirmed) {
      validation = 'Type the current database name to confirm the rename.';
      return;
    }
    const value = name.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(value)) {
      validation = 'Use letters, numbers and underscores; start with a letter or underscore.';
      return;
    }
    if (value.length > 63) {
      validation = 'Database names are limited to 63 characters.';
      return;
    }
    if (mode === 'rename' && value === database?.name) {
      validation = 'Enter a different name.';
      return;
    }
    validation = '';
    onsubmit(value);
  }
</script>

<Modal {open} {title} {description} size="sm" onclose={onclose}>
  {#if details}
    <div class="details">{@render details()}</div>
  {/if}
  <div class="fields">
    <Field
      id="database-name"
      label="Database name"
      error={validation || error}
      hint="Letters, numbers and underscores only."
    >
      <input
        id="database-name"
        class="input mono"
        type="text"
        bind:value={name}
        autocomplete="off"
        spellcheck="false"
        data-autofocus
        onkeydown={(event) => {
          if (event.key === 'Enter') submit();
        }}
      />
    </Field>

    {#if mode === 'rename'}
      <Field
        id="database-rename-confirm"
        label="Type the current name to confirm"
        error={confirmMismatch
          ? 'The typed name does not match the current database name.'
          : undefined}
        hint={`Enter “${database?.name ?? ''}” exactly.`}
      >
        <input
          id="database-rename-confirm"
          class="input mono"
          type="text"
          bind:value={confirmName}
          autocomplete="off"
          spellcheck="false"
          onkeydown={(event) => {
            if (event.key === 'Enter') submit();
          }}
        />
      </Field>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose} disabled={busy}>Cancel</Button>
    <Button
      variant="primary"
      onclick={submit}
      loading={busy}
      disabled={disabled || !renameConfirmed}
    >
      {mode === 'rename' ? 'Rename database' : 'Create database'}
    </Button>
  {/snippet}
</Modal>

<style>
  .fields {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .details {
    margin-bottom: 12px;
  }
</style>
