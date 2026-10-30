<script lang="ts">
  import type {
    Database,
    DatabaseEngine,
    DatabaseGrant,
    GrantPreset,
  } from '../../databaseTypes';
  import Button from '../Button.svelte';
  import { CUSTOM_PRIVILEGES, GRANT_PRESETS } from './userGrants';

  interface Props {
    databases: Database[];
    grants: DatabaseGrant[];
    engine: DatabaseEngine;
    disabled?: boolean;
    onchange: (grants: DatabaseGrant[]) => void;
  }

  let { databases, grants, engine, disabled = false, onchange }: Props = $props();

  function update(next: DatabaseGrant[]): void {
    onchange(next);
  }

  function updateDatabase(index: number, name: string): void {
    update(grants.map((grant, position) => (position === index ? { ...grant, database: name } : grant)));
  }

  function updatePreset(index: number, preset: GrantPreset): void {
    update(
      grants.map((grant, position) =>
        position === index
          ? { ...grant, preset, privileges: preset === 'custom' ? (grant.privileges ?? []) : undefined }
          : grant,
      ),
    );
  }

  function togglePrivilege(index: number, privilege: string): void {
    update(
      grants.map((grant, position) => {
        if (position !== index) return grant;
        const current = grant.privileges ?? [];
        const privileges = current.includes(privilege)
          ? current.filter((entry) => entry !== privilege)
          : [...current, privilege];
        return { ...grant, privileges };
      }),
    );
  }

  function remove(index: number): void {
    update(grants.filter((_grant, position) => position !== index));
  }

  function add(): void {
    const used = new Set(grants.map((grant) => grant.database));
    const available = databases.find((database) => !used.has(database.name));
    if (!available) return;
    update([...grants, { database: available.name, preset: 'read_write_schema' }]);
  }
</script>

<div class="grant-editor">
  <div class="grant-head">
    <span class="label">Database grants</span>
    <span class="hint">Grants stay scoped to one database; global privileges are never changed.</span>
  </div>

  {#if databases.length === 0}
    <p class="empty">Create a database before granting access.</p>
  {:else if grants.length === 0}
    <p class="empty">No grants. The user can connect but cannot read or write any database.</p>
  {:else}
    {#each grants as grant, index (index)}
      <div class="grant-row">
        <select
          class="input"
          aria-label={`Grant ${index + 1} database`}
          value={grant.database}
          {disabled}
          onchange={(event) => updateDatabase(index, event.currentTarget.value)}
        >
          {#each databases as database (database.name)}
            <option value={database.name}>{database.name}</option>
          {/each}
        </select>
        <select
          class="input"
          aria-label={`Grant ${index + 1} preset`}
          value={grant.preset}
          {disabled}
          onchange={(event) => updatePreset(index, event.currentTarget.value as GrantPreset)}
        >
          {#each GRANT_PRESETS as preset (preset.value)}
            <option value={preset.value}>{preset.label}</option>
          {/each}
        </select>
        <Button
          size="sm"
          variant="ghost"
          ariaLabel={`Remove grant ${index + 1}`}
          {disabled}
          onclick={() => remove(index)}
        >
          Remove
        </Button>
      </div>
      {#if grant.preset === 'custom'}
        <div class="privileges" aria-label={`Grant ${index + 1} privileges`}>
          {#each CUSTOM_PRIVILEGES[engine] as privilege (privilege)}
            <label class="privilege">
              <input
                type="checkbox"
                checked={grant.privileges?.includes(privilege) ?? false}
                {disabled}
                onchange={() => togglePrivilege(index, privilege)}
              />
              {privilege}
            </label>
          {/each}
        </div>
      {/if}
    {/each}
  {/if}

  <Button
    size="sm"
    variant="ghost"
    disabled={disabled || grants.length >= databases.length || databases.length === 0}
    onclick={add}
  >
    Add grant
  </Button>
</div>

<style>
  .grant-editor {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .grant-head {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .label {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .hint,
  .empty {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .grant-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.3fr) auto;
    gap: 6px;
    align-items: center;
  }

  .privileges {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 10px;
    padding: 7px 9px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
  }

  .privilege {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .privilege input {
    accent-color: var(--accent);
  }

  @media (max-width: 560px) {
    .grant-row {
      grid-template-columns: 1fr;
    }
  }
</style>
