<script lang="ts">
  import { KeyRound, UserPlus } from 'lucide-svelte';
  import * as api from '../../api';
  import * as dbApi from '../../databaseApi';
  import { copyText } from '../../clipboard';
  import { describeError, type DescribedError } from '../../errors';
  import { addToast, toastError } from '../../stores';
  import type {
    CreateDatabaseUserRequest,
    Database,
    DatabaseEngine,
    DatabaseSiteRef,
    DatabaseUser,
    UpdateDatabaseUserRequest,
  } from '../../databaseTypes';
  import type { ServiceView } from '../../types';
  import Badge from '../Badge.svelte';
  import Button from '../Button.svelte';
  import Card from '../Card.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import EmptyState from '../EmptyState.svelte';
  import ErrorBanner from '../ErrorBanner.svelte';
  import Modal from '../Modal.svelte';
  import SecretField from '../SecretField.svelte';
  import UserFormDialog from './UserFormDialog.svelte';
  import {
    accessLevelDescription,
    accessLevelLabel,
    grantSummary,
    isProtectedUsername,
    type UserFormValue,
  } from './userGrants';

  interface Props {
    service: ServiceView;
    users: DatabaseUser[];
    databases: Database[];
    loading?: boolean;
    selectedUserId?: string;
    onRefresh: () => void;
  }

  let {
    service,
    users,
    databases,
    loading = false,
    selectedUserId = '',
    onRefresh,
  }: Props = $props();

  function engineFor(id: string): DatabaseEngine {
    if (id === 'postgresql' || id === 'postgres') return 'postgres';
    if (id === 'mariadb') return 'mariadb';
    if (id === 'redis') return 'redis';
    return 'mysql';
  }

  const engine = $derived<DatabaseEngine>(users[0]?.engine ?? engineFor(service.id));
  const isRedis = $derived(engine === 'redis');
  const isPostgres = $derived(engine === 'postgres');
  const active = $derived(service.active);

  let formOpen = $state(false);
  let formMode = $state<'create' | 'edit'>('create');
  let editTarget = $state<DatabaseUser | null>(null);
  let formBusy = $state(false);
  let formError = $state('');

  let deleteTarget = $state<DatabaseUser | null>(null);
  let reassignOwned = $state(false);
  let deleting = $state(false);
  let deleteError = $state<DescribedError | null>(null);

  let unlinkTarget = $state<DatabaseUser | null>(null);

  let revealed = $state<Record<string, string>>({});
  let revealing = $state<Record<string, boolean>>({});
  let revealErrors = $state<Record<string, string>>({});
  let secretBusy = $state<string | null>(null);
  let actionError = $state<DescribedError | null>(null);
  let restartPrompt = $state<{ label: string; sites: DatabaseSiteRef[] } | null>(null);
  let restartingSite = $state<string | null>(null);

  let secretDialog = $state<{ user: DatabaseUser; value: string; revealed: boolean } | null>(null);

  const deleteLabel = $derived(
    deleteTarget
      ? `${deleteTarget.username}${deleteTarget.host ? `@${deleteTarget.host}` : ''}`
      : 'user',
  );

  // A refreshed inventory never keeps a previously revealed secret on screen.
  $effect(() => {
    void users;
    revealed = {};
    revealErrors = {};
  });

  function linked(user: DatabaseUser): boolean {
    return (user.linked_sites?.length ?? 0) > 0;
  }

  function protectedUser(user: DatabaseUser): boolean {
    return isProtectedUsername(engine, user.username);
  }

  function promptRestart(label: string, sites: DatabaseSiteRef[] | undefined): void {
    if (!sites?.length) return;
    restartPrompt = { label, sites: [...sites] };
  }

  async function restartLinkedSite(site: DatabaseSiteRef): Promise<void> {
    if (restartingSite) return;
    restartingSite = site.id;
    try {
      await api.restartSite(site.id);
      addToast({
        kind: 'success',
        title: `${site.name} restarted`,
        message: 'The site now uses the updated database credentials or grants.',
      });
      if (restartPrompt) {
        const remaining = restartPrompt.sites.filter((entry) => entry.id !== site.id);
        restartPrompt = remaining.length > 0 ? { ...restartPrompt, sites: remaining } : null;
      }
    } catch (caught) {
      actionError = describeError(caught);
    } finally {
      restartingSite = null;
    }
  }

  function openCreate(): void {
    formMode = 'create';
    editTarget = null;
    formError = '';
    formOpen = true;
  }

  function openEdit(user: DatabaseUser): void {
    formMode = 'edit';
    editTarget = user;
    formError = '';
    formOpen = true;
  }

  async function submitForm(value: UserFormValue): Promise<void> {
    if (formBusy) return;
    formBusy = true;
    formError = '';
    try {
      if (formMode === 'create') {
        const body: CreateDatabaseUserRequest = { username: value.username };
        if (engine === 'mysql' || engine === 'mariadb') body.host = value.host;
        if (isRedis) body.access_level = value.accessLevel;
        else body.grants = value.grants;
        await dbApi.createServiceUser(service.id, body);
        addToast({
          kind: 'success',
          title: `User ${value.username} created`,
          message: 'The password was generated by the platform and stored outside SQLite.',
        });
      } else {
        const target = editTarget;
        if (!target) return;
        const body: UpdateDatabaseUserRequest = { enabled: value.enabled };
        if (isRedis) body.access_level = value.accessLevel;
        else body.grants = value.grants;
        await dbApi.updateServiceUser(service.id, target.id, body);
        addToast({
          kind: 'success',
          title: `User ${target.username} updated`,
          message: 'Restart linked sites to apply the new grants or state.',
        });
        promptRestart(`Grants for ${target.username}`, target.linked_sites);
      }
      formOpen = false;
      onRefresh();
    } catch (caught) {
      formError = describeError(caught).message;
    } finally {
      formBusy = false;
    }
  }

  async function reveal(user: DatabaseUser): Promise<void> {
    revealing = { ...revealing, [user.id]: true };
    const nextErrors = { ...revealErrors };
    delete nextErrors[user.id];
    revealErrors = nextErrors;
    try {
      const secret = await dbApi.revealServiceUserPassword(service.id, user.id);
      revealed = { ...revealed, [user.id]: secret.value };
    } catch (caught) {
      revealErrors = { ...revealErrors, [user.id]: describeError(caught).message };
    } finally {
      revealing = { ...revealing, [user.id]: false };
    }
  }

  function hide(user: DatabaseUser): void {
    const next = { ...revealed };
    delete next[user.id];
    revealed = next;
  }

  async function copySecret(user: DatabaseUser): Promise<void> {
    const value = revealed[user.id];
    if (!value) return;
    if (!(await copyText(value))) {
      toastError(`Could not copy the password for ${user.username}`);
      return;
    }
    addToast({
      kind: 'success',
      title: 'Password copied',
      message: 'Clipboard updated. Keep the value out of logs and screenshots.',
    });
  }

  async function resetPassword(user: DatabaseUser): Promise<void> {
    if (secretBusy) return;
    secretBusy = user.id;
    actionError = null;
    try {
      const secret = await dbApi.setServiceUserPassword(service.id, user.id);
      secretDialog = { user, value: secret.value, revealed: true };
      addToast({
        kind: 'success',
        title: `Password ${user.password_managed ? 'reset' : 'set'} for ${user.username}`,
        message: 'The platform stored the new password; copy it before closing the dialog.',
      });
      promptRestart(`Password for ${user.username}`, user.linked_sites);
      onRefresh();
    } catch (caught) {
      actionError = describeError(caught);
    } finally {
      secretBusy = null;
    }
  }

  async function copyDialogSecret(): Promise<void> {
    if (!secretDialog) return;
    if (!(await copyText(secretDialog.value))) {
      toastError('Could not copy the new password');
      return;
    }
    addToast({ kind: 'success', title: 'Password copied', message: 'Clipboard updated.' });
  }

  function openDelete(user: DatabaseUser): void {
    deleteTarget = user;
    reassignOwned = false;
    deleteError = null;
  }

  async function confirmDelete(): Promise<void> {
    const target = deleteTarget;
    if (!target || deleting) return;
    deleting = true;
    deleteError = null;
    try {
      await dbApi.deleteServiceUser(service.id, target.id, {
        confirm: target.username,
        reassign_owned: isPostgres ? reassignOwned : false,
      });
      deleteTarget = null;
      addToast({
        kind: 'success',
        title: `User ${target.username} deleted`,
        message: 'Databases, backups and site links were not deleted with the user.',
      });
      onRefresh();
    } catch (caught) {
      deleteError = describeError(caught);
    } finally {
      deleting = false;
    }
  }
</script>

<div class="users-tab">
  {#if !active}
    <p class="inactive-note" role="status">
      The service is stopped. Activate the service to create, edit or delete users and to manage
      passwords.
    </p>
  {/if}

  {#if actionError}
    <ErrorBanner
      title="User action failed"
      code={actionError.code}
      message={actionError.message}
      ondismiss={() => (actionError = null)}
    />
  {/if}

  {#if restartPrompt}
    <div class="restart-prompt" role="status">
      <div class="restart-copy">
        <strong>Restart linked sites to apply.</strong>
        <p>{restartPrompt.label} changed. Restart each site so it reconnects.</p>
      </div>
      <div class="restart-actions">
        {#each restartPrompt.sites as site (site.id)}
          <Button
            size="sm"
            variant="secondary"
            ariaLabel={`Restart site ${site.name}`}
            loading={restartingSite === site.id}
            onclick={() => restartLinkedSite(site)}
          >
            Restart {site.name}
          </Button>
        {/each}
        <Button size="sm" variant="ghost" onclick={() => (restartPrompt = null)}>
          Dismiss
        </Button>
      </div>
    </div>
  {/if}

  <Card
    title={isRedis ? 'ACL users' : 'Users'}
    subtitle={isRedis
      ? 'ACL users control Redis access. Full means all data commands; admin and dangerous commands stay blocked.'
      : 'Application users are ordinary engine users. Devstack never creates global or superuser accounts.'}
    headerWrap
  >
    {#snippet actions()}
      <Button
        size="sm"
        variant="primary"
        disabled={!active}
        title={active ? 'Create a database user' : 'Activate the service first'}
        onclick={openCreate}
      >
        <UserPlus size={13} strokeWidth={2} aria-hidden="true" />
        Create user
      </Button>
    {/snippet}

    {#if loading && users.length === 0}
      <p class="loading-copy" aria-busy="true">Loading users…</p>
    {:else if users.length === 0}
      <EmptyState
        icon={KeyRound}
        title="No users yet"
        description="Create an application user and grant it access to a database."
        compact
      >
        <Button size="sm" variant="primary" disabled={!active} onclick={openCreate}>
          Create user
        </Button>
      </EmptyState>
    {:else if isRedis}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">User</th>
              <th scope="col">Engine</th>
              <th scope="col">State</th>
              <th scope="col">Access</th>
              <th scope="col">Password</th>
              <th scope="col"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each users as user (user.id)}
              <tr class:selected={selectedUserId === user.id}>
                <td>
                  <span class="username mono">{user.username}</span>
                  {#if protectedUser(user)}
                    <Badge>Protected</Badge>
                  {/if}
                  {#if selectedUserId === user.id}
                    <Badge variant="info">Selected</Badge>
                  {/if}
                </td>
                <td class="mono">{user.engine}</td>
                <td>
                  <Badge variant={user.enabled ? 'accent' : 'neutral'}>
                    {user.enabled ? 'Enabled' : 'Disabled'}
                  </Badge>
                </td>
                <td>
                  <Badge variant={accessLevelLabel(user.access_level) === 'full' ? 'info' : 'neutral'}>
                    {accessLevelLabel(user.access_level)}
                  </Badge>
                  <p class="access-copy">{accessLevelDescription(user.access_level)}</p>
                </td>
                <td>{@render passwordCell(user)}</td>
                <td>
                  <div class="row-actions">{@render redisActions(user)}</div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">User</th>
              <th scope="col">Engine</th>
              <th scope="col">Host</th>
              <th scope="col">State</th>
              <th scope="col">Password</th>
              <th scope="col">Grants</th>
              <th scope="col">Linked sites</th>
              <th scope="col"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each users as user (user.id)}
              <tr class:selected={selectedUserId === user.id}>
                <td>
                  <span class="username mono">{user.username}</span>
                  <Badge variant={user.managed ? 'accent' : 'neutral'}>
                    {user.managed ? 'Managed' : 'External'}
                  </Badge>
                  {#if protectedUser(user)}
                    <Badge>Protected</Badge>
                  {/if}
                  {#if selectedUserId === user.id}
                    <Badge variant="info">Selected</Badge>
                  {/if}
                </td>
                <td class="mono">{user.engine}</td>
                <td class="mono">{user.host || '—'}</td>
                <td>
                  <Badge variant={user.enabled ? 'accent' : 'neutral'}>
                    {user.enabled ? 'Enabled' : 'Disabled'}
                  </Badge>
                </td>
                <td>{@render passwordCell(user)}</td>
                <td class="grants">{grantSummary(user.grants)}</td>
                <td>
                  {#if user.linked_sites?.length}
                    <div class="site-links">
                      {#each user.linked_sites as site (site.id)}
                        <a class="site-link" href={`#/sites/${site.id}`}>{site.name}</a>
                      {/each}
                    </div>
                    <Button
                      size="sm"
                      variant="ghost"
                      onclick={() => (unlinkTarget = user)}
                    >
                      Unlink sites
                    </Button>
                  {:else}
                    <span class="muted">—</span>
                  {/if}
                </td>
                <td>
                  <div class="row-actions">{@render sqlActions(user)}</div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</div>

{#snippet passwordCell(user: DatabaseUser)}
  {#if user.password_managed}
    <SecretField
      label={`Password for ${user.username}`}
      value={revealed[user.id] ?? ''}
      revealed={Boolean(revealed[user.id])}
      loading={revealing[user.id] ?? false}
      error={revealErrors[user.id]}
      onreveal={() => reveal(user)}
      onhide={() => hide(user)}
      oncopy={() => copySecret(user)}
    />
  {:else}
    <div class="password-empty">
      <span class="muted">Not managed</span>
      <Button
        size="sm"
        variant="ghost"
        ariaLabel={`Set password for ${user.username}`}
        disabled={!active || protectedUser(user)}
        title={protectedUser(user) ? 'Protected engine user' : 'Set a stored password'}
        loading={secretBusy === user.id}
        onclick={() => resetPassword(user)}
      >
        Set password
      </Button>
    </div>
  {/if}
{/snippet}

{#snippet sqlActions(user: DatabaseUser)}
  <Button
    size="sm"
    variant="ghost"
    ariaLabel={`Edit ${user.username}`}
    disabled={!active || protectedUser(user)}
    title={protectedUser(user) ? 'Protected engine user' : 'Edit grants and enabled state'}
    onclick={() => openEdit(user)}
  >
    Edit
  </Button>
  {#if user.password_managed}
    <Button
      size="sm"
      variant="ghost"
      ariaLabel={`Reset password for ${user.username}`}
      disabled={!active || protectedUser(user)}
      title={protectedUser(user) ? 'Protected engine user' : 'Rotate the stored password'}
      loading={secretBusy === user.id}
      onclick={() => resetPassword(user)}
    >
      Reset password
    </Button>
  {/if}
  <Button
    size="sm"
    variant="danger-ghost"
    ariaLabel={`Delete ${user.username}`}
    disabled={!active || linked(user) || protectedUser(user)}
    title={linked(user)
      ? 'Unlink the user from its sites first'
      : protectedUser(user)
        ? 'Protected engine user'
        : 'Delete the user'}
    onclick={() => openDelete(user)}
  >
    Delete
  </Button>
{/snippet}

{#snippet redisActions(user: DatabaseUser)}
  <Button
    size="sm"
    variant="ghost"
    ariaLabel={`Edit ${user.username}`}
    disabled={!active || protectedUser(user)}
    title={protectedUser(user) ? 'The Redis default user is protected' : 'Edit the access level'}
    onclick={() => openEdit(user)}
  >
    Edit
  </Button>
  {#if user.password_managed}
    <Button
      size="sm"
      variant="ghost"
      ariaLabel={`Reset password for ${user.username}`}
      disabled={!active || protectedUser(user)}
      title={protectedUser(user)
        ? 'Use the Default password card to rotate this password'
        : 'Rotate the stored password'}
      loading={secretBusy === user.id}
      onclick={() => resetPassword(user)}
    >
      Reset password
    </Button>
  {/if}
  <Button
    size="sm"
    variant="danger-ghost"
    ariaLabel={`Delete ${user.username}`}
    disabled={!active || protectedUser(user)}
    title={protectedUser(user) ? 'The Redis default user is protected' : 'Delete the ACL user'}
    onclick={() => openDelete(user)}
  >
    Delete
  </Button>
{/snippet}

<UserFormDialog
  open={formOpen}
  mode={formMode}
  {engine}
  user={editTarget}
  {databases}
  busy={formBusy}
  error={formError}
  onsubmit={submitForm}
  onclose={() => {
    if (!formBusy) formOpen = false;
  }}
/>

<ConfirmDialog
  open={deleteTarget !== null}
  title={deleteTarget ? `Delete user ${deleteLabel}?` : 'Delete user?'}
  description={`The engine user ${deleteLabel} and its grants are removed. Databases, backups and site links are not deleted.`}
  confirmLabel="Delete user"
  danger
  busy={deleting}
  requireText={deleteTarget?.username}
  onconfirm={confirmDelete}
  oncancel={() => {
    if (!deleting) deleteTarget = null;
  }}
>
  {#snippet details()}
    <div class="delete-details">
      {#if isPostgres}
        <label class="checkbox-row">
          <input
            type="checkbox"
            aria-label="Reassign owned objects to devstack admin"
            bind:checked={reassignOwned}
          />
          <span>
            <strong>Reassign owned objects to devstack admin</strong>
            <span class="checkbox-copy">
              PostgreSQL refuses to drop a role that owns objects. This reassigns ownership; it
              never drops objects implicitly.
            </span>
          </span>
        </label>
      {/if}
      {#if deleteError}
        <p class="delete-error" role="alert">
          <span class="mono">{deleteError.code}</span>
          {deleteError.message}
        </p>
      {/if}
    </div>
  {/snippet}
</ConfirmDialog>

<Modal
  open={unlinkTarget !== null}
  title={unlinkTarget ? `Unlink sites from ${unlinkTarget.username}` : 'Unlink sites'}
  description="Remove the database link from each site's Databases card. Deleting a link never deletes the user or its password."
  size="sm"
  onclose={() => (unlinkTarget = null)}
>
  {#if unlinkTarget}
    <ul class="unlink-list">
      {#each unlinkTarget.linked_sites ?? [] as site (site.id)}
        <li>
          <a class="site-link" href={`#/sites/${site.id}`}>Open {site.name}</a>
        </li>
      {/each}
    </ul>
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={() => (unlinkTarget = null)}>Close</Button>
  {/snippet}
</Modal>

<Modal
  open={secretDialog !== null}
  title={secretDialog ? `Password for ${secretDialog.user.username}` : 'Password'}
  description="The platform returns this password once. Copy it now; it is never stored in SQLite."
  size="sm"
  onclose={() => (secretDialog = null)}
>
  {#if secretDialog}
    <SecretField
      label="New password"
      value={secretDialog.value}
      revealed={secretDialog.revealed}
      onreveal={() => {
        if (secretDialog) secretDialog.revealed = true;
      }}
      onhide={() => {
        if (secretDialog) secretDialog.revealed = false;
      }}
      oncopy={copyDialogSecret}
    />
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={() => (secretDialog = null)}>Close</Button>
  {/snippet}
</Modal>

<style>
  .users-tab {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .inactive-note {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-left: 3px solid var(--warning);
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .restart-prompt {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--info) 35%, var(--border));
    border-left: 3px solid var(--info);
    border-radius: var(--radius);
    background: var(--info-soft);
    color: var(--info-fg);
  }

  .restart-copy {
    min-width: 0;
    font-size: 11.5px;
    line-height: 1.45;
  }

  .restart-copy strong {
    font-weight: 600;
  }

  .restart-copy p {
    margin-top: 2px;
  }

  .restart-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
  }

  .loading-copy {
    color: var(--muted);
    font-size: 12px;
  }

  .table-wrap {
    overflow-x: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }

  th {
    padding: 0 8px 7px;
    border-bottom: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-align: left;
    text-transform: uppercase;
    white-space: nowrap;
  }

  td {
    padding: 9px 8px;
    border-bottom: 1px solid var(--border);
    color: var(--fg);
    vertical-align: middle;
  }

  tbody tr:last-child td {
    border-bottom: 0;
  }

  tr.selected td {
    background: var(--info-soft);
  }

  .username {
    margin-right: 5px;
    font-weight: 600;
  }

  .grants {
    max-width: 240px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .access-copy {
    margin-top: 3px;
    max-width: 260px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.4;
  }

  .password-empty {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
  }

  .muted {
    color: var(--muted-2);
  }

  .site-links {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .site-link {
    display: inline-block;
    padding: 1px 6px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--fg);
    font-size: 10.5px;
    text-decoration: none;
  }

  .site-link:hover {
    border-color: var(--control-border);
    background: var(--surface-3);
  }

  .row-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 2px;
  }

  .delete-details {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .checkbox-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--fg);
    font-size: 11.5px;
    line-height: 1.45;
    cursor: pointer;
  }

  .checkbox-row input {
    margin-top: 2px;
    accent-color: var(--accent);
  }

  .checkbox-row strong {
    display: block;
    font-size: 11.5px;
    font-weight: 600;
  }

  .checkbox-copy {
    display: block;
    margin-top: 1px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .delete-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .delete-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }

  .unlink-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  @media (max-width: 720px) {
    .row-actions {
      justify-content: flex-start;
    }
  }
</style>
