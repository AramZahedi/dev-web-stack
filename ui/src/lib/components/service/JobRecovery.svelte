<script lang="ts">
  import * as dbApi from '../../databaseApi';
  import { describeError, type DescribedError } from '../../errors';
  import type { DatabaseJob } from '../../databaseTypes';
  import type { ServiceView } from '../../types';
  import Button from '../Button.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import { isRecoveryJob, recoveryDatabaseName } from './recovery';

  interface Props {
    service: ServiceView;
    job: DatabaseJob;
    onrecovered: (job: DatabaseJob) => void;
    ondismiss?: () => void;
  }

  let { service, job, onrecovered, ondismiss }: Props = $props();

  let open = $state(false);
  let busy = $state(false);
  let error = $state<DescribedError | null>(null);

  const eligible = $derived(isRecoveryJob(job));
  const targetName = $derived(recoveryDatabaseName(job.database));

  async function confirmRecovery(): Promise<void> {
    const backupID = job.backup_id;
    if (!backupID || busy) return;
    busy = true;
    error = null;
    try {
      const next = await dbApi.restoreServiceBackup(service.id, backupID, {
        target: targetName,
        overwrite: false,
        confirm: job.database,
        pre_restore_backup: false,
      });
      open = false;
      onrecovered(next);
    } catch (caught) {
      error = describeError(caught);
    } finally {
      busy = false;
    }
  }
</script>

{#if eligible}
  <div class="recovery" role="alert">
    <p>
      <strong>Restore incomplete.</strong>
      {job.database} may be partially modified. The pre-operation backup is available as a
      one-click recovery into a new database.
    </p>
    <Button
      size="sm"
      variant="secondary"
      onclick={() => {
        error = null;
        open = true;
      }}
    >
      Restore pre-operation backup
    </Button>
    {#if ondismiss}
      <Button size="sm" variant="ghost" onclick={ondismiss}>Dismiss recovery</Button>
    {/if}
  </div>

  <ConfirmDialog
    {open}
    title={`Restore pre-operation backup for ${job.database}?`}
    description={`Restores the pre-operation backup into ${targetName}. The incomplete target is not overwritten.`}
    confirmLabel="Restore backup"
    busy={busy}
    requireText={job.database}
    onconfirm={confirmRecovery}
    oncancel={() => {
      if (!busy) open = false;
    }}
  >
    {#snippet details()}
      {#if error}
        <p class="recovery-error" role="alert">
          <span class="mono">{error.code}</span>
          {error.message}
        </p>
      {/if}
    {/snippet}
  </ConfirmDialog>
{/if}

<style>
  .recovery {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 7px;
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--danger) 40%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .recovery strong {
    font-weight: 600;
  }

  .recovery-error {
    padding: 7px 9px;
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .recovery-error .mono {
    margin-right: 5px;
    font-size: 9.5px;
  }
</style>
