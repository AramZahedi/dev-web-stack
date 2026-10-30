<script lang="ts">
  import type {
    Database,
    DatabaseEngine,
    DatabaseGrant,
    DatabaseUser,
  } from '../../databaseTypes';
  import Button from '../Button.svelte';
  import Field from '../Field.svelte';
  import Modal from '../Modal.svelte';
  import Toggle from '../Toggle.svelte';
  import GrantEditor from './GrantEditor.svelte';
  import { isProtectedDatabase } from './databaseProtection';
  import type { UserFormValue } from './userGrants';

  interface Props {
    open: boolean;
    mode: 'create' | 'edit';
    engine: DatabaseEngine;
    user?: DatabaseUser | null;
    databases: Database[];
    busy?: boolean;
    error?: string;
    onsubmit: (value: UserFormValue) => void;
    onclose: () => void;
  }

  let {
    open,
    mode,
    engine,
    user = null,
    databases,
    busy = false,
    error,
    onsubmit,
    onclose,
  }: Props = $props();

  let username = $state('');
  let host = $state('%');
  let enabled = $state(true);
  let accessLevel = $state('read-only');
  let grants = $state<DatabaseGrant[]>([]);
  let validation = $state<Record<string, string>>({});
  let wasOpen = false;

  const isRedis = $derived(engine === 'redis');
  const isMySQLFamily = $derived(engine === 'mysql' || engine === 'mariadb');
  const grantTargets = $derived(
    databases.filter(
      (database) => !database.missing && !isProtectedDatabase(engine, database.name),
    ),
  );

  $effect(() => {
    if (!open) {
      wasOpen = false;
      return;
    }
    if (wasOpen) return;
    wasOpen = true;
    username = mode === 'edit' ? (user?.username ?? '') : '';
    host = user?.host || (isMySQLFamily ? '%' : '');
    enabled = user?.enabled ?? true;
    accessLevel = user?.access_level === 'full' ? 'full' : 'read-only';
    grants =
      mode === 'edit'
        ? (user?.grants ?? []).map((grant) => ({
            ...grant,
            privileges: grant.privileges ? [...grant.privileges] : undefined,
          }))
        : !isRedis && grantTargets.length > 0
          ? [{ database: grantTargets[0].name, preset: 'read_write_schema' }]
          : [];
    validation = {};
  });

  const title = $derived(
    mode === 'edit' ? `Edit ${user?.username ?? 'user'}` : 'Create database user',
  );
  const description = $derived(
    isRedis
      ? 'Redis ACL users authenticate with a password and a fixed access level.'
      : 'Application users are ordinary engine users. Devstack never creates global or superuser accounts.',
  );

  function submit(): void {
    const name = username.trim();
    const errors: Record<string, string> = {};
    if (!name) {
      errors.username = 'Username is required.';
    } else if (/\s/.test(name)) {
      errors.username = 'Usernames cannot contain spaces.';
    } else if (!/^[A-Za-z0-9_.-]+$/.test(name)) {
      errors.username = 'Use letters, numbers, dots, dashes or underscores.';
    }
    if (isMySQLFamily && !host.trim()) {
      errors.host = 'A host scope is required for MySQL and MariaDB users.';
    }
    if (Object.keys(errors).length > 0) {
      validation = errors;
      return;
    }
    validation = {};
    onsubmit({
      username: name,
      host: host.trim(),
      enabled,
      accessLevel,
      grants: isRedis ? [] : grants,
    });
  }
</script>

<Modal {open} {title} {description} size="md" onclose={onclose}>
  <div class="user-form">
    <div class="row">
      <Field
        id="user-username"
        label="Username"
        error={validation.username}
        hint={mode === 'edit' ? 'Usernames cannot be renamed. Create a new user instead.' : undefined}
        required
      >
        <input
          id="user-username"
          class="input mono"
          type="text"
          bind:value={username}
          autocomplete="off"
          spellcheck="false"
          data-autofocus
          disabled={mode === 'edit'}
        />
      </Field>
      {#if isMySQLFamily}
        <Field
          id="user-host"
          label="Host"
          error={validation.host}
          hint={mode === 'edit' ? 'Host scopes cannot be changed.' : 'Devstack-managed users use %.'}
        >
          <input
            id="user-host"
            class="input mono"
            type="text"
            bind:value={host}
            autocomplete="off"
            spellcheck="false"
            disabled={mode === 'edit'}
          />
        </Field>
      {/if}
    </div>

    {#if isRedis}
      <Field id="user-access" label="Access level">
        <select id="user-access" class="input" bind:value={accessLevel}>
          <option value="read-only">Read-only</option>
          <option value="full">Full data access (admin and dangerous commands excluded)</option>
        </select>
      </Field>
    {:else}
      <GrantEditor {databases} {grants} {engine} onchange={(next) => (grants = next)} />
    {/if}

    {#if mode === 'edit'}
      <Toggle
        bind:checked={enabled}
        label="Enabled"
        description="Disabled users keep their grants but cannot connect."
      />
    {/if}

    {#if error}
      <p class="form-error" role="alert">{error}</p>
    {/if}
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose} disabled={busy}>Cancel</Button>
    <Button variant="primary" onclick={submit} loading={busy}>
      {mode === 'edit' ? 'Save changes' : 'Create user'}
    </Button>
  {/snippet}
</Modal>

<style>
  .user-form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .row {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }

  .form-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  @media (max-width: 560px) {
    .row {
      grid-template-columns: 1fr;
    }
  }
</style>
