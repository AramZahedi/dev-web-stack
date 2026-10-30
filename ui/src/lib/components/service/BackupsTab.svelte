<script lang="ts">
  import type { ServiceView } from '../../types';
  import type { Database, DatabaseBackup } from '../../databaseTypes';

  interface Props {
    service: ServiceView;
    backups: DatabaseBackup[];
    databases?: Database[];
    loading?: boolean;
    onRefresh: () => void;
    onjobchange?: (active: boolean) => void;
  }

  let { backups }: Props = $props();
</script>

<section aria-label="Backups">
  <h2>Backups</h2>
  <ul>
    {#each backups as backup (backup.id)}
      <li>{backup.database}</li>
    {/each}
  </ul>
</section>
