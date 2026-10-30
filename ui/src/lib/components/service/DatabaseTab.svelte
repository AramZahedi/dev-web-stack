<script lang="ts">
  import type { ServiceView } from '../../types';
  import type { Database } from '../../databaseTypes';

  interface Props {
    service: ServiceView;
    databases: Database[];
    loading?: boolean;
    onRefresh: () => void;
    onjobchange?: (active: boolean) => void;
  }

  let { databases }: Props = $props();
</script>

<section aria-label="Databases">
  <h2>Databases</h2>
  <ul>
    {#each databases as database (database.name)}
      <li>{database.name}</li>
    {/each}
  </ul>
</section>
