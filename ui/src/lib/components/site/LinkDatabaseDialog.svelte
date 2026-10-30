<script lang="ts">
  import { describeError } from '../../errors';
  import { addToast } from '../../stores';
  import { createSiteDatabase, linkSiteDatabase } from '../../databaseApi';
  import type { CreateSiteDatabaseRequest } from '../../databaseTypes';
  import Button from '../Button.svelte';
  import ErrorBanner from '../ErrorBanner.svelte';
  import Modal from '../Modal.svelte';
  import DatabasePicker from './DatabasePicker.svelte';

  interface Props {
    open: boolean;
    siteRef: string;
    siteName?: string;
    primary?: boolean;
    linkedDatabases?: Array<{ service_id: string; database: string }>;
    onclose: () => void;
    onlinked: () => void;
  }

  let {
    open,
    siteRef,
    siteName = '',
    primary = true,
    linkedDatabases = [],
    onclose,
    onlinked,
  }: Props = $props();

  let value = $state<CreateSiteDatabaseRequest | null>(null);
  let submitting = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);
  let validation = $state('');

  function close(): void {
    if (submitting) return;
    value = null;
    error = null;
    validation = '';
    onclose();
  }

  function isDatabaseIdentifier(value: string): boolean {
    return /^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(value);
  }

  async function submit(): Promise<void> {
    if (!value) {
      validation = 'Choose a database and user.';
      return;
    }
    if (!value.service_id) {
      validation = 'Choose a database service.';
      return;
    }
    const database = value.new_database?.name?.trim() ?? value.database ?? '';
    if (!database) {
      validation = value.new_database ? 'Enter a database name.' : 'Choose an existing database.';
      return;
    }
    if (value.new_database && !isDatabaseIdentifier(database)) {
      validation = 'Use letters, numbers and underscores; start with a letter or underscore.';
      return;
    }
    if (value.new_user) {
      const username = value.new_user.username.trim();
      if (!username) {
        validation = 'Enter a database username.';
        return;
      }
      if (!isDatabaseIdentifier(username)) {
        validation = 'Use letters, numbers and underscores; start with a letter or underscore.';
        return;
      }
    } else if (!value.user_id) {
      validation = 'Choose an existing user.';
      return;
    }

    validation = '';
    error = null;
    submitting = true;
    try {
      if (value.new_database || value.new_user) {
        const result = await createSiteDatabase(siteRef, value);
        if (result?.warning) {
          addToast({
            kind: 'info',
            title: 'Database linked with a warning',
            message: result.warning,
          });
        }
      } else {
        await linkSiteDatabase(siteRef, {
          service_id: value.service_id,
          database,
          user_id: value.user_id ?? '',
          ...(value.password ? { password: value.password } : {}),
          primary: value.primary ?? primary,
        });
      }
      addToast({
        kind: 'success',
        title: 'Database linked',
        message: 'Restart the site to apply the new connection variables.',
      });
      value = null;
      error = null;
      validation = '';
      onlinked();
    } catch (caught) {
      error = describeError(caught);
    } finally {
      submitting = false;
    }
  }
</script>

<Modal
  {open}
  title="Link database"
  description="Create a database and user, or attach an existing database to this site."
  size="lg"
  onclose={close}
>
  {#if error}
    <div class="dialog-error">
      <ErrorBanner
        title="Could not link the database"
        code={error.code}
        message={error.message}
        ondismiss={() => (error = null)}
      />
    </div>
  {/if}
  {#if validation}
    <p class="validation" role="alert">{validation}</p>
  {/if}

  <DatabasePicker
    bind:value={value}
    alwaysOn
    {siteName}
    {primary}
    {linkedDatabases}
  />

  {#snippet footer()}
    <Button variant="ghost" onclick={close} disabled={submitting}>Cancel</Button>
    <Button variant="primary" onclick={submit} loading={submitting}>Link database</Button>
  {/snippet}
</Modal>

<style>
  .dialog-error {
    margin-bottom: 14px;
  }

  .validation {
    margin: 0 0 12px;
    color: var(--danger-fg);
    font-size: 12px;
  }
</style>
