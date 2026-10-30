<script lang="ts">
  import * as dbApi from '../../databaseApi';
  import { copyText } from '../../clipboard';
  import { describeError, type DescribedError } from '../../errors';
  import { addToast, toastError } from '../../stores';
  import type { DatabaseUser } from '../../databaseTypes';
  import type { ServiceView } from '../../types';
  import Button from '../Button.svelte';
  import Card from '../Card.svelte';
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
      actionError = describeError(caught);
    } finally {
      resetLoading = false;
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

  .default-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }

  .muted {
    color: var(--muted);
    font-size: 11.5px;
  }
</style>
