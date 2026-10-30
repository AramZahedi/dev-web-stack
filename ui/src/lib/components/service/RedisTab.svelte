<script lang="ts">
  import type { ServiceView } from '../../types';
  import type { DatabaseUser } from '../../databaseTypes';

  interface Props {
    service: ServiceView;
    users: DatabaseUser[];
    loading?: boolean;
    onRefresh: () => void;
    section?: 'all' | 'password';
  }

  let { users }: Props = $props();
</script>

<section aria-label="Redis users">
  <h2>Redis users</h2>
  <ul>
    {#each users as user (user.id)}
      <li>{user.username}</li>
    {/each}
  </ul>
</section>
