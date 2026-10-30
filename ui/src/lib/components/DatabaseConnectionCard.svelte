<script lang="ts">
  import Badge from './Badge.svelte';
  import type { DatabaseConnection } from '../databaseTypes';

  interface Props {
    connection: DatabaseConnection;
    title?: string;
    showEnv?: boolean;
  }

  let { connection, title = 'Connection', showEnv = false }: Props = $props();
  const envLines = $derived(
    Object.entries(connection.env ?? {}).map(([key, value]) => `${key}=${value}`),
  );
</script>

<section class="connection" aria-label={title}>
  <header>
    <h3>{title}</h3>
    <Badge variant="neutral">{connection.engine}</Badge>
  </header>
  <dl>
    <div><dt>Host</dt><dd><code>{connection.host}:{connection.port}</code></dd></div>
    <div><dt>Database</dt><dd><code>{connection.database}</code></dd></div>
    <div><dt>Username</dt><dd><code>{connection.username}</code></dd></div>
    <div>
      <dt>Password</dt>
      <dd>{connection.password_managed ? 'Password managed' : 'Password not managed'}</dd>
    </div>
  </dl>
  {#if connection.url}
    <p class="url"><code>{connection.url}</code></p>
  {/if}
  {#if showEnv && envLines.length > 0}
    <pre aria-label="Environment variables">{envLines.join('\n')}</pre>
  {/if}
</section>

<style>
  .connection {
    display: grid;
    gap: 10px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-1);
  }

  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  h3 {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
  }

  dl {
    display: grid;
    gap: 6px;
    margin: 0;
  }

  dl div {
    display: grid;
    grid-template-columns: 78px minmax(0, 1fr);
    gap: 8px;
  }

  dt {
    color: var(--muted);
    font-size: 12px;
  }

  dd {
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  code {
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .url {
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  pre {
    margin: 0;
    overflow: auto;
    padding: 8px;
    border-radius: var(--radius-sm);
    background: var(--surface-2);
    font-family: var(--font-mono);
    font-size: 11.5px;
  }
</style>
