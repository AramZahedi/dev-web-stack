<script lang="ts">
  import type { CreateSiteDatabaseRequest } from '../../databaseTypes';
  import type { DatabasePickerSummary } from './databasePickerTypes';

  interface Props {
    value: CreateSiteDatabaseRequest;
    summary: DatabasePickerSummary;
    onedit?: () => void;
  }

  let { value, summary, onedit }: Props = $props();

  const newUser = $derived(Boolean(value.new_user));
</script>

<div class="review-row database-review-row">
  <dt>Database</dt>
  <dd>
    <span class="review-main">
      {summary.database || 'Not selected'} on {summary.serviceName}
    </span>
    <span class="review-sub mono">
      {summary.username || 'No user'} · {summary.grantLabel} · {summary.host}:{summary.port}
    </span>
    <span class="review-sub">
      {#if newUser}
        Password will be generated when the site is created.
      {:else if summary.passwordManaged}
        Password managed by devstack.
      {:else}
        Password not managed.
      {/if}
    </span>
  </dd>
  {#if onedit}
    <button type="button" class="edit" onclick={onedit}>Edit</button>
  {/if}
</div>

<style>
  .review-row {
    display: grid;
    grid-template-columns: 92px minmax(0, 1fr) 40px;
    gap: 10px;
    align-items: center;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
  }

  dt {
    color: var(--muted);
    font-size: 11px;
  }

  dd {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }

  .review-main {
    font-size: 12.5px;
    color: var(--fg);
    font-weight: 500;
    overflow-wrap: anywhere;
  }

  .review-sub {
    color: var(--muted-2);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .edit {
    justify-self: end;
    padding: 2px 4px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 500;
  }

  .edit:hover {
    color: var(--fg);
    background: var(--surface-2);
  }

  @media (max-width: 620px) {
    .review-row {
      grid-template-columns: 72px minmax(0, 1fr) 36px;
    }
  }
</style>
