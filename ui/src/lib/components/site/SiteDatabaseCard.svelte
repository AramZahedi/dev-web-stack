<script lang="ts">
  import { onMount } from 'svelte';
  import { Copy, Database, Link2, RotateCw, Trash2 } from 'lucide-svelte';
  import { copyText } from '../../clipboard';
  import { describeError } from '../../errors';
  import { addToast, toastError } from '../../stores';
  import type { Site } from '../../types';
  import {
    listServiceUsers,
    revealServiceUserPassword,
    setServiceUserPassword,
    unlinkSiteDatabase,
  } from '../../databaseApi';
  import type { DatabaseUser, SiteDatabaseLink } from '../../databaseTypes';
  import Badge from '../Badge.svelte';
  import Button from '../Button.svelte';
  import Card from '../Card.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import DatabaseConnectionCard from '../DatabaseConnectionCard.svelte';
  import EmptyState from '../EmptyState.svelte';
  import ErrorBanner from '../ErrorBanner.svelte';
  import SecretField from '../SecretField.svelte';
  import LinkDatabaseDialog from './LinkDatabaseDialog.svelte';
  import {
    isDatabaseUrlEnvKey,
    isSecretEnvKey,
    sanitizeDatabaseUrl,
  } from './databasePickerTypes';

  interface Props {
    site: Site;
    links: SiteDatabaseLink[];
    linksError?: { code: string; message: string } | null;
    onRefresh: () => void;
    onRetryLinks?: () => void;
    onRestart: () => void;
  }

  let { site, links, linksError = null, onRefresh, onRetryLinks, onRestart }: Props = $props();

  let linkOpen = $state(false);
  let unlinkTarget = $state<SiteDatabaseLink | null>(null);
  let unlinking = $state(false);
  let secrets = $state<Record<string, string>>({});
  let revealLoading = $state<Record<string, boolean>>({});
  let revealError = $state<Record<string, string>>({});
  let resettingPassword = $state<Record<string, boolean>>({});
  let managedOverrides = $state<Record<string, boolean>>({});
  let sharedUsers = $state<Record<string, boolean>>({});
  let managedUsers = $state<Record<string, boolean>>({});
  let restartNeeded = $state(false);
  let userGeneration = 0;
  let revealGeneration = 0;

  const userCounts = $derived.by(() => {
    const counts: Record<string, number> = {};
    for (const link of links) {
      counts[link.user_id] = (counts[link.user_id] ?? 0) + 1;
    }
    return counts;
  });

  function userKey(link: SiteDatabaseLink): string {
    return `${link.service_id}:${link.user_id}`;
  }

  function isSharedUser(link: SiteDatabaseLink): boolean {
    return sharedUsers[userKey(link)] ?? ((userCounts[link.user_id] ?? 0) > 1);
  }

  function isManagedUser(link: SiteDatabaseLink): boolean {
    return managedUsers[userKey(link)] === true;
  }

  $effect(() => {
    const serviceIDs = [...new Set(links.map((link) => link.service_id))];
    const generation = ++userGeneration;
    if (serviceIDs.length === 0) {
      sharedUsers = {};
      managedUsers = {};
      return;
    }
    void Promise.all(
      serviceIDs.map(async (serviceID) => {
        try {
          const result = await listServiceUsers(serviceID);
          return { serviceID, users: result?.users ?? [] };
        } catch {
          return { serviceID, users: [] as DatabaseUser[] };
        }
      }),
    ).then((results) => {
      if (generation !== userGeneration) return;
      const nextShared: Record<string, boolean> = {};
      const nextManaged: Record<string, boolean> = {};
      for (const { serviceID, users } of results) {
        for (const user of users) {
          const key = `${serviceID}:${user.id}`;
          nextManaged[key] = user.managed;
          if (Array.isArray(user.linked_sites)) {
            nextShared[key] = user.linked_sites.length > 1;
          }
        }
      }
      sharedUsers = nextShared;
      managedUsers = nextManaged;
    });
  });

  let lastSiteID = '';
  $effect(() => {
    if (site.id === lastSiteID) return;
    lastSiteID = site.id;
    revealGeneration += 1;
    secrets = {};
    revealError = {};
    managedOverrides = {};
    restartNeeded = false;
  });

  let lastLinkKey = '';
  $effect(() => {
    const key = links
      .map((link) => `${link.id}:${link.user_id}:${link.password_managed}`)
      .join(',');
    if (key === lastLinkKey) return;
    lastLinkKey = key;
    revealGeneration += 1;
    secrets = {};
    revealError = {};
  });

  onMount(() => {
    const clearSecrets = () => {
      revealGeneration += 1;
      secrets = {};
      revealError = {};
      revealLoading = {};
    };
    window.addEventListener('devstack:refresh', clearSecrets);
    return () => window.removeEventListener('devstack:refresh', clearSecrets);
  });

  function linkError(link: SiteDatabaseLink): { code: string; message: string } {
    const raw = link.error ?? '';
    if (!raw) return { code: '', message: '' };
    try {
      const parsed = JSON.parse(raw) as { code?: unknown; message?: unknown };
      if (parsed && typeof parsed === 'object') {
        return {
          code: typeof parsed.code === 'string' ? parsed.code : '',
          message: typeof parsed.message === 'string' ? parsed.message : raw,
        };
      }
    } catch {
      // Plain-text errors are the common case.
    }
    return { code: '', message: raw };
  }

  function conflictMessage(link: SiteDatabaseLink): string {
    const { code, message } = linkError(link);
    if (
      code === 'DATABASE_EXCLUSIVE_ENGINE' ||
      code === 'EXCLUSIVE_ENGINE_CONFLICT' ||
      code === 'SERVICE_EXCLUSIVE_CONFLICT'
    ) {
      return 'Another MySQL-family database engine is active on port 3306. Stop it before starting this site.';
    }
    if (/exclusive|mysql-family|already active/i.test(message)) {
      return 'Another MySQL-family database engine is active on port 3306. Stop it before starting this site.';
    }
    return '';
  }

  function linkErrorText(link: SiteDatabaseLink): string {
    return linkError(link).message;
  }

  async function copy(label: string, value: string, secret = false): Promise<void> {
    const ok = await copyText(value);
    if (!ok) {
      toastError(`Could not copy ${label}`, 'Select the value and copy it manually.');
      return;
    }
    addToast({
      kind: 'success',
      title: `${label} copied`,
      message: secret ? 'The value is on your clipboard.' : value,
    });
  }

  function envSnippet(link: SiteDatabaseLink): string {
    return Object.entries(link.connection.env ?? {})
      .filter(([key]) => !isSecretEnvKey(key))
      .map(([key, value]) => `${key}=${isDatabaseUrlEnvKey(key) ? sanitizeDatabaseUrl(value) : value}`)
      .join('\n');
  }

  async function reveal(link: SiteDatabaseLink): Promise<void> {
    const generation = ++revealGeneration;
    const cleared = { ...secrets };
    delete cleared[link.id];
    secrets = cleared;
    revealLoading = { ...revealLoading, [link.id]: true };
    revealError = { ...revealError, [link.id]: '' };
    try {
      const secret = await revealServiceUserPassword(link.service_id, link.user_id);
      if (generation !== revealGeneration) return;
      secrets = { ...secrets, [link.id]: secret.value };
    } catch (caught) {
      if (generation !== revealGeneration) return;
      revealError = { ...revealError, [link.id]: describeError(caught).message };
    } finally {
      if (generation === revealGeneration) {
        revealLoading = { ...revealLoading, [link.id]: false };
      }
    }
  }

  function hide(link: SiteDatabaseLink): void {
    revealGeneration += 1;
    const next = { ...secrets };
    delete next[link.id];
    secrets = next;
  }

  async function resetPassword(link: SiteDatabaseLink): Promise<void> {
    if (resettingPassword[link.id]) return;
    resettingPassword = { ...resettingPassword, [link.id]: true };
    try {
      const secret = await setServiceUserPassword(link.service_id, link.user_id);
      secrets = { ...secrets, [link.id]: secret.value };
      managedOverrides = { ...managedOverrides, [link.id]: true };
      restartNeeded = true;
      addToast({
        kind: 'success',
        title: 'Database password reset',
        message: 'Restart the site to apply the new password.',
      });
      onRefresh();
    } catch (caught) {
      toastError('Could not reset the database password', describeError(caught).message);
    } finally {
      resettingPassword = { ...resettingPassword, [link.id]: false };
    }
  }

  async function confirmUnlink(): Promise<void> {
    if (!unlinkTarget) return;
    unlinking = true;
    try {
      await unlinkSiteDatabase(site.id, unlinkTarget.id);
      addToast({
        kind: 'success',
        title: 'Database unlinked',
        message:
          'The database, user, grants, password, backups, and data remain. Only this site link was removed.',
      });
      unlinkTarget = null;
      restartNeeded = true;
      onRefresh();
    } catch (caught) {
      toastError('Could not unlink the database', describeError(caught).message);
    } finally {
      unlinking = false;
    }
  }

  function handleLinked(): void {
    linkOpen = false;
    restartNeeded = true;
    onRefresh();
  }
</script>

<Card title="Databases" headerWrap>
  {#snippet actions()}
    <Button size="sm" variant="secondary" onclick={() => (linkOpen = true)}>
      <Link2 size={13} strokeWidth={2} aria-hidden="true" />
      Link database
    </Button>
  {/snippet}

  {#if linksError}
    <div class="links-error">
      <ErrorBanner
        title="Could not load databases"
        code={linksError.code}
        message={linksError.message}
        onretry={onRetryLinks}
      />
    </div>
  {/if}

  {#if links.length === 0}
    {#if !linksError}
      <EmptyState
        icon={Database}
        title="No databases linked"
        description="Link a database to inject DB_* connection variables into this site. Databases are service-wide and survive site deletion."
        compact
      />
    {/if}
  {:else}
    <div class="db-list">
      {#each links as link (link.id)}
        {@const passwordStored = managedOverrides[link.id] ?? link.password_managed}
        {@const conflict = conflictMessage(link)}
        {@const displayConnection = {
          ...link.connection,
          password_managed: passwordStored,
          url: link.connection.url ? sanitizeDatabaseUrl(link.connection.url) : undefined,
        }}
        <article class="db-link" aria-label={`Database ${link.database}`}>
          <div class="db-head">
            <div class="badges">
              {#if link.primary}<Badge variant="accent">Primary</Badge>{/if}
              {#if isSharedUser(link)}
                <Badge variant="neutral">shared user</Badge>
              {/if}
              {#if !link.available}<Badge variant="warning">Offline</Badge>{/if}
            </div>
            <div class="db-actions">
              <a
                class="manage"
                href={`#/services/${encodeURIComponent(link.service_id)}?tab=users&user=${encodeURIComponent(link.user_id)}`}
              >
                Manage user
              </a>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel={`Copy host for ${link.database}`}
                onclick={() => copy('host', link.connection.host)}
              >
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                Host
              </Button>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel={`Copy port for ${link.database}`}
                onclick={() => copy('port', String(link.connection.port))}
              >
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                Port
              </Button>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel={`Copy database for ${link.database}`}
                onclick={() => copy('database name', link.connection.database)}
              >
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                Database
              </Button>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel={`Copy username for ${link.database}`}
                onclick={() => copy('username', link.connection.username)}
              >
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                User
              </Button>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel={`Copy .env snippet for ${link.database}`}
                onclick={() => copy('.env snippet', envSnippet(link), true)}
              >
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                .env
              </Button>
              <Button
                size="sm"
                variant="danger-ghost"
                ariaLabel={`Unlink ${link.database}`}
                onclick={() => (unlinkTarget = link)}
              >
                <Trash2 size={12} strokeWidth={2} aria-hidden="true" />
                Unlink
              </Button>
            </div>
          </div>

          <DatabaseConnectionCard connection={displayConnection} />

          {#if !link.available}
            <p class="offline" role="status">
              <strong>Database offline.</strong>
              <a class="manage" href={`#/services/${encodeURIComponent(link.service_id)}`}>
                Activate {link.service_id}
              </a>
            </p>
          {/if}
          {#if link.error}
            <p class="db-error" role="alert">{conflict || linkErrorText(link)}</p>
          {/if}

          <div class="db-credentials">
            {#if passwordStored}
              <SecretField
                label={`password for ${link.database}`}
                value={secrets[link.id] ?? ''}
                revealed={Boolean(secrets[link.id])}
                loading={revealLoading[link.id]}
                error={revealError[link.id] || undefined}
                onreveal={() => reveal(link)}
                onhide={() => hide(link)}
                oncopy={() => copy('password', secrets[link.id] ?? '', true)}
              />
            {/if}
            {#if isManagedUser(link)}
              {#if !passwordStored}
                <p class="password-hint">
                  Password unknown. Reset it to generate a new password.
                </p>
              {/if}
              <Button
                size="sm"
                variant="secondary"
                loading={resettingPassword[link.id]}
                onclick={() => resetPassword(link)}
              >
                Reset password
              </Button>
              <span class="password-hint">Replaces the current password.</span>
            {:else if !passwordStored}
              <p class="password-hint">
                External password entry is not available in v1. Set the password in the engine,
                then restart the site.
              </p>
            {/if}
          </div>
        </article>
      {/each}
    </div>
  {/if}

  {#if restartNeeded}
    <div class="restart-notice" role="status">
      <p>Restart site to apply the database changes.</p>
      <Button
        size="sm"
        variant="primary"
        onclick={() => {
          restartNeeded = false;
          onRestart();
        }}
      >
        <RotateCw size={12} strokeWidth={2} aria-hidden="true" />
        Restart site
      </Button>
    </div>
  {/if}
</Card>

<LinkDatabaseDialog
  open={linkOpen}
  siteRef={site.id}
  siteName={site.name}
  primary={!links.some((link) => link.primary)}
  linkedDatabases={links.map((link) => ({
    service_id: link.service_id,
    database: link.database,
  }))}
  onclose={() => (linkOpen = false)}
  onlinked={handleLinked}
/>

<ConfirmDialog
  open={Boolean(unlinkTarget)}
  title={unlinkTarget ? `Unlink ${unlinkTarget.database}?` : 'Unlink database?'}
  description="The database, user, grants, password, backups, and data remain. Only this site link is removed."
  confirmLabel="Unlink"
  danger
  busy={unlinking}
  onconfirm={confirmUnlink}
  oncancel={() => {
    if (!unlinking) unlinkTarget = null;
  }}
/>

<style>
  .db-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .links-error {
    margin-bottom: 12px;
  }

  .db-link {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-1);
  }

  .db-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
  }

  .badges {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .db-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
  }

  .manage {
    color: var(--info-fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .manage:hover {
    text-decoration: underline;
  }

  .db-credentials {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
  }

  .password-hint {
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .offline,
  .db-error {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
  }

  .offline {
    color: var(--warning-fg);
  }

  .db-error {
    color: var(--danger-fg);
  }

  .restart-notice {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .restart-notice p {
    margin: 0;
    color: var(--fg);
    font-size: 12px;
  }
</style>
