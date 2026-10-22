<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Check,
    CircleCheck,
    CircleX,
    Copy,
    FileCode,
    Lock,
    Server,
    ShieldCheck,
    Terminal,
    Trash2,
    Unplug,
    Wrench,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { copyText } from '../lib/clipboard';
  import { describeError } from '../lib/errors';
  import { addToast, daemon, session, toastError } from '../lib/stores';
  import type { Settings, TrustTarget } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import Card from '../lib/components/Card.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  let settings = $state<Settings | null>(null);
  let trust = $state<TrustTarget[]>([]);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let trustAction = $state<'repair' | 'remove' | null>(null);
  let removeTrustOpen = $state(false);
  let uninstallOpen = $state(false);
  let uninstallArmed = $state(false);
  let commandCopied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  const configPath = $derived(
    settings ? `${settings.home.replace(/\/$/, '')}/config.yaml` : '~/.devstack/config.yaml',
  );
  const socketPath = $derived(
    settings?.socket_path ?? (settings ? `${settings.home}/devstack.sock` : '—'),
  );
  const pidPath = $derived(
    settings?.pid_path ?? (settings ? `${settings.home}/devstack.pid` : '—'),
  );
  const version = $derived(settings?.version ?? $daemon.version ?? '0.1.0');
  const failingTargets = $derived(trust.filter((target) => !target.ok).length);

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      const [config, targets] = await Promise.all([api.getSettings(), api.getTrust()]);
      settings = config;
      trust = targets;
    } catch (caught) {
      error = describeError(caught);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load(true);
    const refresh = () => void load();
    window.addEventListener('devstack:refresh', refresh);
    return () => {
      window.removeEventListener('devstack:refresh', refresh);
      if (copyTimer) clearTimeout(copyTimer);
    };
  });

  async function repairTrust(): Promise<void> {
    if (trustAction) return;
    trustAction = 'repair';
    try {
      trust = await api.repairTrust();
      addToast({
        kind: 'success',
        title: 'Trust repaired',
        message: 'Available system and browser stores were updated with the devstack CA.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not repair trust', `${described.code}: ${described.message}`);
    } finally {
      trustAction = null;
    }
  }

  async function removeTrust(): Promise<void> {
    if (trustAction) return;
    trustAction = 'remove';
    try {
      trust = await api.removeTrust();
      removeTrustOpen = false;
      addToast({
        kind: 'success',
        title: 'CA removed from trust stores',
        message: 'HTTPS sites will show certificate warnings until trust is repaired.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not remove trust', `${described.code}: ${described.message}`);
    } finally {
      trustAction = null;
    }
  }

  async function copyConfigPath(): Promise<void> {
    if (!(await copyText(configPath))) {
      toastError('Could not copy the path');
      return;
    }
    addToast({ kind: 'success', title: 'Config path copied', message: configPath });
  }

  async function copyUninstallCommand(): Promise<void> {
    if (!(await copyText('devstack uninstall --yes'))) {
      toastError('Could not copy the command');
      return;
    }
    commandCopied = true;
    addToast({ kind: 'info', title: 'Uninstall command copied', message: 'Run it in a terminal to remove the integration.' });
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (commandCopied = false), 1800);
  }

  function confirmUninstall(): void {
    uninstallOpen = false;
    uninstallArmed = true;
    addToast({
      kind: 'info',
      title: 'Uninstall command ready',
      message: 'Run it outside the dashboard. Project files are never touched.',
    });
  }
</script>

<div class="page">
  {#if error}
    <div class="banner">
      <ErrorBanner
        title="Could not load settings"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
        ondismiss={settings ? () => (error = null) : undefined}
      />
    </div>
  {/if}

  {#if loading}
    <div class="loading-stack" aria-busy="true" aria-label="Loading settings">
      <div class="loading-card">
        <Skeleton width="100px" height="14px" />
        <Skeleton width="100%" height="52px" />
        <div class="loading-grid">
          {#each Array(6) as _, index (index)}
            <div class="loading-item">
              <Skeleton width="70px" height="9px" />
              <Skeleton width={`${80 + index * 12}px`} height="13px" />
            </div>
          {/each}
        </div>
      </div>
      <div class="loading-card">
        <Skeleton width="80px" height="14px" />
        {#each Array(3) as _, index (index)}
          <div class="loading-row">
            <Skeleton width="22px" height="22px" radius="50%" />
            <Skeleton width={`${180 + index * 40}px`} height="11px" />
          </div>
        {/each}
      </div>
    </div>
  {:else if settings}
    <Card title="Machine" subtitle="Configuration loaded from the daemon's config.yaml" headerWrap>
      {#snippet actions()}
        <Badge icon={Lock}>Read only</Badge>
        <Button size="sm" variant="ghost" onclick={copyConfigPath}>
          <Copy size={12} strokeWidth={2} aria-hidden="true" />
          Copy path
        </Button>
      {/snippet}

      <div class="config-note">
        <FileCode size={15} strokeWidth={1.8} aria-hidden="true" />
        <p>
          Machine settings are read-only in the dashboard. Edit
          <span class="mono">{configPath}</span> and restart the daemon to apply changes.
        </p>
      </div>

      <dl class="config-grid">
        <div class="config-item wide">
          <dt>Home</dt>
          <dd class="mono">{settings.home}</dd>
        </div>
        <div class="config-item">
          <dt>HTTP port</dt>
          <dd class="mono">{settings.http_port}</dd>
        </div>
        <div class="config-item">
          <dt>HTTPS port</dt>
          <dd class="mono">{settings.https_port}</dd>
        </div>
        <div class="config-item">
          <dt>UI port</dt>
          <dd class="mono">{settings.ui_port}</dd>
        </div>
        <div class="config-item">
          <dt>Auto-start sites</dt>
          <dd>
            <span class="bool" class:on={settings.auto_start_sites}>
              {#if settings.auto_start_sites}<Check size={12} strokeWidth={2.2} aria-hidden="true" />{/if}
              {settings.auto_start_sites ? 'Enabled' : 'Disabled'}
            </span>
          </dd>
        </div>
        <div class="config-item">
          <dt>Graceful stop</dt>
          <dd class="mono">{settings.graceful_stop_seconds}s</dd>
        </div>
        <div class="config-item">
          <dt>Log retention</dt>
          <dd class="mono">{settings.log_max_mb} MB × {settings.log_keep}</dd>
        </div>
        <div class="config-item">
          <dt>Max request body</dt>
          <dd class="mono">{settings.max_request_body_mb} MB</dd>
        </div>
        <div class="config-item">
          <dt>WSL distro</dt>
          <dd class="mono">{settings.wsl_distro || '—'}</dd>
        </div>
      </dl>
    </Card>

    <div class="two-col">
      <Card
        title="Trust"
        subtitle="Local certificate authority in system and browser stores"
      >
        {#snippet actions()}
          <Button
            size="sm"
            variant="secondary"
            onclick={repairTrust}
            loading={trustAction === 'repair'}
            disabled={Boolean(trustAction)}
          >
            {#if trustAction !== 'repair'}<Wrench size={12} strokeWidth={2} aria-hidden="true" />{/if}
            Repair all
          </Button>
        {/snippet}

        <div class="trust-summary">
          <span class="trust-icon" class:ok={failingTargets === 0}>
            <ShieldCheck size={16} strokeWidth={1.9} aria-hidden="true" />
          </span>
          <p>
            {#if trust.length === 0}
              No trust targets were reported by the helper.
            {:else if failingTargets === 0}
              All {trust.length} available targets trust the devstack CA.
            {:else if failingTargets === 1}
              1 of {trust.length} targets needs attention.
            {:else}
              {failingTargets} of {trust.length} targets need attention.
            {/if}
          </p>
        </div>

        <div class="trust-list">
          {#each trust as target (target.name)}
            <div class="trust-row">
              <span class="trust-state" class:ok={target.ok} aria-hidden="true">
                {#if target.ok}
                  <CircleCheck size={16} strokeWidth={1.9} />
                {:else}
                  <CircleX size={16} strokeWidth={1.9} />
                {/if}
              </span>
              <div class="trust-copy">
                <div class="trust-title">
                  <span>{target.name}</span>
                  <span class="trust-label" class:ok={target.ok}>{target.ok ? 'Trusted' : 'Not trusted'}</span>
                </div>
                <p>{target.detail}</p>
              </div>
              <div class="trust-actions">
                <Button
                  size="sm"
                  variant="ghost"
                  onclick={repairTrust}
                  disabled={Boolean(trustAction)}
                  title="Repair the CA in all trust stores"
                >
                  Repair
                </Button>
                <IconButton
                  label="Remove CA from all trust stores"
                  icon={Trash2}
                  size="sm"
                  variant="danger"
                  disabled={Boolean(trustAction)}
                  onclick={() => (removeTrustOpen = true)}
                />
              </div>
            </div>
          {/each}
          {#if trust.length === 0}
            <p class="empty-trust">No trust targets were reported by the helper.</p>
          {/if}
        </div>
        <p class="trust-foot">Trust actions apply to every target. A missing browser is reported as not installed.</p>
      </Card>

      <Card title="Daemon" subtitle="Loopback control plane">
        <dl class="daemon-list">
          <div>
            <dt>Socket</dt>
            <dd class="mono break">{socketPath}</dd>
          </div>
          <div>
            <dt>PID file</dt>
            <dd class="mono break">{pidPath}</dd>
          </div>
          <div>
            <dt>Version</dt>
            <dd class="mono">{version}</dd>
          </div>
          <div>
            <dt>Data source</dt>
            <dd>
              <Badge variant={$session.mode === 'mock' ? 'warning' : 'accent'}>
                {$session.mode === 'mock' ? 'Mock data' : 'Live daemon'}
              </Badge>
            </dd>
          </div>
          <div>
            <dt>Status</dt>
            <dd>
              <span class="daemon-state {$daemon.status}">
                <span class="dot" aria-hidden="true"></span>
                {$daemon.status === 'online' ? 'Online' : $daemon.status === 'checking' ? 'Connecting' : 'Offline'}
              </span>
            </dd>
          </div>
        </dl>
        <div class="daemon-note">
          <Server size={14} strokeWidth={1.8} aria-hidden="true" />
          <p>Binds loopback only. The dashboard has no remote access and no CORS.</p>
        </div>
      </Card>
    </div>

    <section class="danger" aria-labelledby="uninstall-title">
      <div class="danger-copy">
        <p class="danger-eyebrow">Danger zone</p>
        <h2 id="uninstall-title">Uninstall devstack</h2>
        <p>
          Removes the privileged helper, local CA, hosts entries and daemon integration. Your
          project files, logs in project folders, and browser data are untouched.
        </p>
        {#if uninstallArmed}
          <div class="command">
            <span class="command-label">Run in a terminal</span>
            <div class="command-row">
              <Terminal size={14} strokeWidth={1.9} aria-hidden="true" />
              <code>devstack uninstall --yes</code>
              <IconButton
                label={commandCopied ? 'Command copied' : 'Copy uninstall command'}
                icon={commandCopied ? Check : Copy}
                size="sm"
                onclick={copyUninstallCommand}
              />
            </div>
            <p class="command-note">
              The dashboard can’t stop the process serving it. Run the command outside the browser.
              {#if $session.mode === 'mock'} Mock mode changes nothing on disk. {/if}
            </p>
          </div>
        {/if}
      </div>
      <Button variant="danger-ghost" onclick={() => (uninstallOpen = true)}>
        <Unplug size={14} strokeWidth={2} aria-hidden="true" />
        Uninstall devstack
      </Button>
    </section>
  {/if}
</div>

<ConfirmDialog
  open={removeTrustOpen}
  title="Remove the devstack CA?"
  description="Removes the local CA from every system and browser trust store. HTTPS sites keep running but browsers will show certificate warnings. You can repair trust at any time."
  confirmLabel="Remove trust"
  danger
  busy={trustAction === 'remove'}
  onconfirm={removeTrust}
  oncancel={() => {
    if (trustAction !== 'remove') removeTrustOpen = false;
  }}
/>

<ConfirmDialog
  open={uninstallOpen}
  title="Uninstall devstack?"
  description="The dashboard cannot stop the daemon serving it, so confirming prepares the exact CLI command to run in a terminal. This removes the helper, CA, hosts entries and daemon integration — never your project files."
  confirmLabel="Prepare command"
  requireText="uninstall"
  danger
  onconfirm={confirmUninstall}
  oncancel={() => (uninstallOpen = false)}
/>

<style>
  .banner {
    margin-bottom: 14px;
  }

  .loading-stack {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .loading-card {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .loading-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 16px;
  }

  .loading-item {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .loading-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 4px 0;
  }

  .config-note {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 14px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .config-note :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--muted-2);
  }

  .config-note .mono {
    color: var(--fg);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .config-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--border);
    overflow: hidden;
  }

  .config-item {
    min-width: 0;
    padding: 9px 12px;
    background: var(--surface);
  }

  .config-item.wide {
    grid-column: 1 / -1;
    background: var(--surface-2);
  }

  .config-item dt {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.02em;
  }

  .config-item dd {
    margin: 2px 0 0;
    color: var(--fg);
    font-size: 11px;
    font-weight: 500;
    overflow-wrap: anywhere;
  }

  .bool {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--muted);
  }

  .bool.on {
    color: var(--success-fg);
  }

  .two-col {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(270px, 1fr);
    align-items: start;
    gap: 14px;
    margin-top: 14px;
  }

  .two-col > :global(.card) {
    min-width: 0;
  }

  .trust-summary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-bottom: 10px;
    margin-bottom: 2px;
    border-bottom: 1px solid var(--border);
  }

  .trust-icon {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: 6px;
    background: var(--warning-soft);
    color: var(--warning-fg);
    flex: none;
  }

  .trust-icon.ok {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .trust-summary p {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .trust-list {
    display: flex;
    flex-direction: column;
  }

  .trust-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 0;
    border-bottom: 1px solid var(--border);
  }

  .trust-row:last-child {
    border-bottom: 0;
  }

  .trust-state {
    display: grid;
    place-items: center;
    flex: none;
    width: 22px;
    height: 22px;
    color: var(--danger-fg);
  }

  .trust-state.ok {
    color: var(--success-fg);
  }

  .trust-copy {
    flex: 1;
    min-width: 0;
  }

  .trust-title {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 6px;
  }

  .trust-title > span:first-child {
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .trust-label {
    color: var(--danger-fg);
    font-size: 9.5px;
  }

  .trust-label.ok {
    color: var(--success-fg);
  }

  .trust-copy p {
    margin-top: 1px;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.45;
    overflow-wrap: anywhere;
  }

  .trust-actions {
    display: flex;
    align-items: center;
    gap: 1px;
    flex: none;
  }

  .trust-foot {
    margin-top: 10px;
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.45;
  }

  .empty-trust {
    padding: 14px 0;
    color: var(--muted);
    font-size: 11px;
  }

  .daemon-list {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .daemon-list > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 0;
    border-top: 1px solid var(--border);
  }

  .daemon-list > div:first-child {
    border-top: 0;
    padding-top: 0;
  }

  .daemon-list dt {
    flex: none;
    color: var(--muted);
    font-size: 10px;
  }

  .daemon-list dd {
    margin: 0;
    min-width: 0;
    color: var(--fg);
    font-size: 10.5px;
    text-align: right;
  }

  .break {
    overflow-wrap: anywhere;
    font-size: 9.5px;
    line-height: 1.45;
  }

  .daemon-state {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .daemon-state.online {
    color: var(--success-fg);
  }

  .daemon-state.offline {
    color: var(--danger-fg);
  }

  .daemon-state.checking {
    color: var(--warning-fg);
  }

  .daemon-state .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  .daemon-note {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.45;
  }

  .daemon-note :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .danger {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    margin-top: 14px;
    padding: 14px 16px;
    border: 1px solid color-mix(in srgb, var(--danger) 32%, var(--border));
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .danger-copy {
    min-width: 0;
    flex: 1;
  }

  .danger-eyebrow {
    color: var(--danger-fg);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .danger h2 {
    margin-top: 2px;
    font-size: 13px;
    font-weight: 600;
  }

  .danger p:not(.danger-eyebrow) {
    margin-top: 3px;
    max-width: 720px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .command {
    margin-top: 10px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--input-bg);
  }

  .command-label {
    display: block;
    margin-bottom: 4px;
    color: var(--muted-2);
    font-size: 8.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .command-row {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }

  .command-row :global(svg) {
    flex: none;
    color: var(--muted-2);
  }

  .command-row code {
    flex: 1;
    min-width: 0;
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 11px;
    overflow-wrap: anywhere;
  }

  .command-note {
    margin-top: 5px;
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.45;
  }

  @media (max-width: 1000px) {
    .two-col {
      grid-template-columns: minmax(0, 1fr);
    }

    .config-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 620px) {
    .config-grid {
      grid-template-columns: 1fr;
    }

    .danger {
      flex-direction: column;
    }

    .danger > :global(.btn) {
      width: 100%;
    }

    .trust-row {
      flex-wrap: wrap;
    }

    .trust-actions {
      width: 100%;
      justify-content: flex-end;
      padding-left: 30px;
    }
  }
</style>
