<script lang="ts">
  import type { ServiceView } from '../../types';
  import type { Database, DatabaseUser } from '../../databaseTypes';

  interface Props {
    service: ServiceView;
    users: DatabaseUser[];
    databases: Database[];
    loading?: boolean;
    selectedUserId?: string;
    onRefresh: () => void;
  }

  let { users }: Props = $props();
</script>

<section aria-label="Users">
  <h2>Users</h2>
  <ul>
    {#each users as user (user.id)}
      <li>{user.username}</li>
    {/each}
  </ul>
</section>
