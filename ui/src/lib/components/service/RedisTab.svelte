<script lang="ts">
  import * as dbApi from '../../databaseApi';
  import { copyText } from '../../clipboard';
  import { describeError, type DescribedError } from '../../errors';
  import { addToast, toastError } from '../../stores';
  import type { DatabaseUser } from '../../databaseTypes';
  import type { ServiceView } from '../../types';
  import Button from '../Button.svelte';
  import Card from '../Card.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import ErrorBanner from '../ErrorBanner.svelte';
  import SecretField from '../SecretField.svelte';
  import UsersTab from './UsersTab.svelte';

  interface Props {
    service: ServiceView;
    users: DatabaseUser[];
    loading?: boolean;
    onRefresh: () => void;
    section?: 'all' | 'password';
  }

  let {
    service,
    users,
    loading = false,
    onRefresh,
    section = 'all',
  }: Props = $props();

  let revealed = $state('');
  let revealedVisible = $state(false);
  let revealLoading = $state(false);
  let revealError = $state('');
  let resetLoading = $state(false);
  let actionError = $state<DescribedError | null>(null);
  let aclOpen = $state(false);
  let aclBusy = $state(false);
  let aclError = $state('');
  let keepRevealedOnce = false;

  const defaultUser = $derived(users.find((user) => user.username === 'default') ?? null);

  $effect(() => {
    void users;
    if (keepRevealedOnce) {
      keepRevealedOnce = false;
      return;
    }
    revealed = '';
    revealedVisible = false;
    revealError = '';
  });

  async function revealDefault(): Promise<void> {
    if (!defaultUser) return;
    revealLoading = true;
    revealError = '';
    actionError = null;
    try {
      const secret = await dbApi.revealServiceUserPassword(service.id, defaultUser.id);
      revealed = secret.value;
      revealedVisible = true;
    } catch (caught) {
      revealError = describeError(caught).message;
    } finally {
      revealLoading = false;
    }
  }

  function hideDefault(): void {
    revealedVisible = false;
  }

  async function resetDefault(): Promise<void> {
    if (resetLoading || !defaultUser) return;
    resetLoading = true;
    actionError = null;
    revealError = '';
    try {
      const secret = await dbApi.setServiceUserPassword(service.id, defaultUser.id);
      revealed = secret.value;
      revealedVisible = true;
      addToast({
        kind: 'success',
        title: 'Redis default password reset',
        message: 'Copy the new password now; the platform stored it outside SQLite.',
      });
      // Keep the freshly reset secret visible through the refresh it triggers.
      keepRevealedOnce = true;
      onRefresh();
    } catch (caught) {
      const described = describeError(caught);
      actionError =
        described.code === 'CONFIRMATION_REQUIRED'
          ? {
              ...described,
              message:
                'Redis ACL persistence is not enabled. Enable it on the Users tab, then retry the password reset.',
            }
          : described;
    } finally {
      resetLoading = false;
    }
  }

  async function confirmEnableACL(): Promise<void> {
    if (aclBusy) return;
    aclBusy = true;
    aclError = '';
    try {
      await dbApi.enableRedisACLFile(service.id, true);
      aclOpen = false;
      addToast({
        kind: 'success',
        title: 'Redis ACL persistence enabled',
        message: 'Redis restarted once. User and password changes are now durable.',
      });
      onRefresh();
    } catch (caught) {
      aclError = describeError(caught).message;
    } finally {
      aclBusy = false;
    }
  }

  async function copyDefault(): Promise<void> {
    if (!revealed) return;
    if (!(await copyText(revealed))) {
      toastError('Could not copy the default password');
      return;
    }
    addToast({ kind: 'success', title: 'Password copied', message: 'Clipboard updated.' });
  }
</script>

<div class="redis-tab">
  {#if actionError}
    <ErrorBanner
      title="Redis password action failed"
      code={actionError.code}
      message={actionError.message}
      ondismiss={() => (actionError = null)}
    />
  {/if}

  {#if section === 'all'}
    <UsersTab
      {service}
      {users}
      databases={[]}
      {loading}
      onRefresh={onRefresh}
    />
    <Card
      title="ACL persistence"
      subtitle="Redis user changes are durable only after a one-time ACL-file migration. Enabling it restarts Redis once."
    >
      <div class="acl-actions">
        <Button
          size="sm"
          variant="secondary"
          disabled={!service.active}
          onclick={() => {
            aclError = '';
            aclOpen = true;
          }}
        >
          Enable ACL persistence
        </Button>
        <span class="muted">Idempotent; safe if already enabled.</span>
      </div>
    </Card>
    <p class="default-hint">The protected default password is managed on the Password tab.</p>
  {:else}
    <p class="password-intro">
      Redis has no logical database management. This tab manages the protected default user's
      password only.
    </p>

    <Card
      title="Default password"
      subtitle="The Redis default user is protected. Full access means all data commands; admin and dangerous commands stay blocked."
    >
      {#if !defaultUser}
        <p class="unavailable" role="status">
          The default ACL user was not discovered on this service. Password management is
          unavailable.
        </p>
      {:else}
        <div class="default-password">
          <SecretField
            label="default password"
            value={revealed}
            revealed={revealedVisible}
            loading={revealLoading || resetLoading}
            error={revealError}
            onreveal={revealDefault}
            onhide={hideDefault}
            oncopy={copyDefault}
          />
          <div class="default-actions">
            <Button
              size="sm"
              variant="secondary"
              loading={resetLoading}
              disabled={!service.active || revealLoading}
              onclick={resetDefault}
            >
              Reset default password
            </Button>
            {#if !defaultUser.password_managed}
              <span class="muted">No password is stored for the default user yet.</span>
            {/if}
          </div>
        </div>
      {/if}
    </Card>
  {/if}
</div>

<ConfirmDialog
  open={aclOpen}
  title="Enable Redis ACL persistence?"
  description="This updates redis.conf, seeds users.acl, and restarts Redis once. Existing users and passwords are preserved."
  confirmLabel="Enable and restart"
  busy={aclBusy}
  onconfirm={confirmEnableACL}
  oncancel={() => {
    if (!aclBusy) aclOpen = false;
  }}
>
  {#snippet details()}
    {#if aclError}
      <p class="acl-error" role="alert">{aclError}</p>
    {/if}
  {/snippet}
</ConfirmDialog>

<style>
  .redis-tab {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .password-intro {
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .default-hint {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .unavailable {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .default-password {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .default-actions,
  .acl-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }

  .acl-error {
    margin: 8px 0 0;
    color: var(--danger);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .muted {
    color: var(--muted);
    font-size: 11.5px;
  }
</style>
