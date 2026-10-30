<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Check,
    CircleCheck,
    CircleX,
    Copy,
    Download,
    Eye,
    FileCode,
    Package,
    RefreshCw,
    Save,
    Server,
    ShieldCheck,
    Terminal,
    Trash2,
    TriangleAlert,
    Unplug,
    Wrench,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { copyText } from '../lib/clipboard';
  import { describeError } from '../lib/errors';
  import { formatDateTime, formatRelative } from '../lib/format';
  import { addToast, daemon, session, toastError } from '../lib/stores';
  import type {
    GlobalIni,
    NodeDefaultStatus,
    PHPDefaultStatus,
    RuntimeBuild,
    Settings,
    ServingMode,
    ToolStatus,
    TrustTarget,
    VhostStatus,
  } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import Card from '../lib/components/Card.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import VhostPreviewDrawer from '../lib/components/VhostPreviewDrawer.svelte';

  let settings = $state<Settings | null>(null);
  let trust = $state<TrustTarget[]>([]);
  let trustError = $state<{ code: string; message: string } | null>(null);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let vhostStatus = $state<VhostStatus | null>(null);
  let vhostLoading = $state(true);
  let vhostError = $state<{ code: string; message: string } | null>(null);
  let vhostSyncing = $state(false);
  let pendingMode = $state<ServingMode | null>(null);
  let modeBusy = $state(false);
  let modeError = $state<{ code: string; message: string } | null>(null);
  let previewOpen = $state(false);
  let previewServer = $state<'nginx' | 'apache'>('nginx');
  let previewSite = $state('');
  let previewLoading = $state(false);
  let previewReturnFocus = $state<HTMLElement | null>(null);
  let trustAction = $state<'repair' | 'remove' | null>(null);
  let removeTrustOpen = $state(false);
  let uninstallOpen = $state(false);
  let uninstallArmed = $state(false);
  let commandCopied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  let phpDefault = $state<PHPDefaultStatus | null>(null);
  let phpDefaultError = $state<{ code: string; message: string } | null>(null);
  let phpDefaultLoading = $state(true);
  let phpDefaultChoice = $state('');
  let phpDefaultBusy = $state<'save' | 'clear' | null>(null);
  let phpDefaultActionError = $state<{ code: string; message: string } | null>(null);
  let nodeDefault = $state<NodeDefaultStatus | null>(null);
  let nodeDefaultError = $state<{ code: string; message: string } | null>(null);
  let nodeDefaultLoading = $state(true);
  let nodeDefaultChoice = $state('');
  let nodeDefaultBusy = $state<'save' | 'clear' | null>(null);
  let nodeDefaultActionError = $state<{ code: string; message: string } | null>(null);
  let globalIni = $state<GlobalIni | null>(null);
  let globalIniContent = $state('');
  let globalIniError = $state<{ code: string; message: string } | null>(null);
  let globalIniLoading = $state(true);
  let globalIniSaving = $state(false);
  let globalIniSaved = $state(false);
  let toolingRuntimes = $state<RuntimeBuild[]>([]);
  let tools = $state<ToolStatus[]>([]);
  let toolsError = $state<{ code: string; message: string } | null>(null);
  let toolsLoading = $state(true);
  let toolBusy = $state<string | null>(null);
  let toolActionError = $state<{ code: string; message: string } | null>(null);
  let removeToolTarget = $state<ToolStatus | null>(null);
  let removeToolError = $state<{ code: string; message: string } | null>(null);
  let removingTool = $state(false);

  const servingModes: { value: ServingMode; label: string; hint: string }[] = [
    { value: 'builtin', label: 'Built-in', hint: 'Go proxy + forwarders' },
    { value: 'nginx', label: 'nginx', hint: 'Native vhosts' },
    { value: 'apache', label: 'Apache', hint: 'Native vhosts' },
  ];

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
  const isWindowsHost = $derived(Boolean(settings?.home && /^[a-z]:[\\/]/i.test(settings.home)));
  const failingTargets = $derived(trust.filter((target) => !target.ok).length);
  const activeMode = $derived(vhostStatus?.mode ?? settings?.serving_mode ?? 'builtin');
  const activeModeLabel = $derived(modeLabel(activeMode));
  const activeModeDescription = $derived(modeDescription(activeMode));
  const modeBadgeVariant = $derived(
    activeMode === 'builtin' ? 'neutral' : vhostStatus?.config_ok === false ? 'danger' : 'accent',
  );
  const serverDisplay = $derived(
    activeMode === 'builtin'
      ? 'Built-in proxy'
      : vhostStatus?.server
        ? `${vhostStatus.server}${vhostStatus.server_version ? ` ${vhostStatus.server_version}` : ''}`
        : activeMode,
  );
  const includeLabel = $derived(
    activeMode === 'builtin' ? 'Not required' : vhostStatus?.include_present ? 'Present' : 'Missing',
  );
  const configLabel = $derived(
    activeMode === 'builtin'
      ? 'Not applicable'
      : vhostStatus?.config_ok === false
        ? 'Failed'
        : 'Passed',
  );
  const lastSyncLabel = $derived(
    usableTimestamp(vhostStatus?.last_sync) ? formatRelative(vhostStatus?.last_sync) : 'Never',
  );
  const lastSyncTitle = $derived(
    usableTimestamp(vhostStatus?.last_sync)
      ? formatDateTime(vhostStatus?.last_sync)
      : 'No sync recorded',
  );
  const impactItems = $derived(pendingMode ? modeImpact(pendingMode) : []);
  const pendingModeLabel = $derived(
    pendingMode ? (pendingMode === 'builtin' ? 'Built-in' : modeLabel(pendingMode)) : '',
  );
  const phpBuilds = $derived(
    toolingRuntimes
      .filter((runtime) => runtime.kind === 'php')
      .slice()
      .sort(
        (a, b) =>
          b.version.localeCompare(a.version, undefined, { numeric: true }) ||
          phpRevision(b.id) - phpRevision(a.id),
      ),
  );
  const nodeBuilds = $derived(
    toolingRuntimes
      .filter((runtime) => runtime.kind === 'node')
      .slice()
      .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true })),
  );
  const configuredNodeDefault = $derived(nodeDefault?.configured?.trim() ?? '');
  const configuredNodeDefaultDisplay = $derived(nodeVersionLabel(configuredNodeDefault));
  const nodeDefaultMissing = $derived(
    Boolean(configuredNodeDefault) && !nodeDefault?.build,
  );
  const nodeDefaultLabel = $derived.by(() => {
    if (nodeDefault?.build) {
      return configuredNodeDefault
        ? `Configured · Node ${nodeDefault.build.version}`
        : `Newest installed (Node ${nodeDefault.build.version})`;
    }
    if (configuredNodeDefault) {
      return `Configured · Node ${configuredNodeDefaultDisplay} (not installed)`;
    }
    return 'Not configured';
  });
  const nodeDefaultBadge = $derived.by(
    (): { label: string; variant: 'neutral' | 'accent' | 'warning' | 'info' } => {
      if (nodeDefaultLoading && !nodeDefault) return { label: 'Loading', variant: 'neutral' };
      if (nodeDefaultMissing) return { label: 'Missing', variant: 'warning' };
      if (nodeDefault?.configured) return { label: 'Explicit', variant: 'accent' };
      if (nodeDefault?.build) return { label: 'Fallback', variant: 'info' };
      return { label: 'Not set', variant: 'neutral' };
    },
  );
  const composerTool = $derived(tools.find((tool) => tool.id === 'composer'));
  const phpDefaultLabel = $derived(
    phpDefault?.configured
      ? phpDefault.configured
      : phpDefault?.build
        ? `Newest installed (${phpDefault.build.version})`
        : 'Not configured',
  );
  const globalIniPath = $derived(
    globalIni?.path ?? phpDefault?.global_ini ?? '—',
  );
  let portDrafts = $state({ http_port: 0, https_port: 0, ui_port: 0 });
  let portsReady = $state(false);
  let portSaving = $state(false);
  let portError = $state<{ code: string; message: string } | null>(null);
  let portWarnings = $state<string[]>([]);
  let portsSaved = $state(false);

  const restartRequired = $derived(Boolean(settings?.restart_required));
  const publicPortsFixed = $derived(Boolean(settings?.public_ports_fixed));
  const portsDirty = $derived(
    Boolean(settings) &&
      (portDrafts.http_port !== settings?.http_port ||
        portDrafts.https_port !== settings?.https_port ||
        portDrafts.ui_port !== settings?.ui_port),
  );

  function setPortDrafts(config: Settings): void {
    portDrafts = {
      http_port: config.http_port,
      https_port: config.https_port,
      ui_port: config.ui_port,
    };
  }

  function usableTimestamp(value: string | undefined): boolean {
    return Boolean(value) && !value?.startsWith('0001-01-01');
  }

  function phpRevision(id: string): number {
    const match = /-r([1-9][0-9]*)$/.exec(id);
    return match ? Number.parseInt(match[1], 10) : 0;
  }

  function nodeVersionLabel(value: string): string {
    return value.trim().replace(/^node@/i, '').replace(/-linux-x64$/i, '');
  }

  function modeLabel(mode: ServingMode): string {
    if (mode === 'nginx') return 'nginx';
    if (mode === 'apache') return 'Apache';
    return 'Built-in proxy';
  }

  function modeDescription(mode: ServingMode): string {
    if (mode === 'nginx') {
      return 'Native nginx vhosts own ports 80 and 443. The built-in proxy and helper forwarders are stopped.';
    }
    if (mode === 'apache') {
      return 'Native Apache vhosts own ports 80 and 443. The built-in proxy and helper forwarders are stopped.';
    }
    return 'The built-in Go proxy and helper forwarders publish every site on ports 80 and 443.';
  }

  function modeImpact(target: ServingMode): string[] {
    const current = vhostStatus?.mode ?? 'builtin';
    if (target === 'builtin') {
      return [
        current === 'builtin'
          ? 'The built-in proxy already serves every site.'
          : `The ${modeLabel(current)} vhosts and bridge include are removed.`,
        'The built-in proxy returns and the helper owns ports 80 and 443 again.',
        current === 'builtin'
          ? 'No restart is needed.'
          : `${modeLabel(current)} reloads after the vhosts are removed.`,
        'Brief downtime is possible for every site while the switch completes.',
      ];
    }
    const items = [
      `${modeLabel(target)} takes ownership of ports 80 and 443.`,
      'The built-in proxy stops and its 80/443 forwarders are released.',
      `${modeLabel(target)} reloads with the generated vhosts.`,
      'Brief downtime is possible for every site while the switch completes.',
    ];
    if (current !== 'builtin' && current !== target) {
      items.unshift(`The ${modeLabel(current)} bridge and release are removed first.`);
    }
    return items;
  }

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      const config = await api.getSettings();
      settings = config;
      if (initial || !portsReady) {
        setPortDrafts(config);
        portsReady = true;
      }
    } catch (caught) {
      error = describeError(caught);
      loading = false;
      return;
    }
    // Trust depends on the privileged helper. A stopped helper must not turn
    // the whole settings page into an error: the remaining settings are local
    // to the daemon and still actionable.
    await refreshTrust();
    loading = false;
  }

  async function refreshTrust(): Promise<void> {
    try {
      trust = await api.getTrust();
      trustError = null;
    } catch (caught) {
      trust = [];
      trustError = describeError(caught);
    }
  }

  async function loadVhosts(initial = false): Promise<void> {
    if (initial) vhostLoading = true;
    vhostError = null;
    try {
      vhostStatus = await api.getVhostStatus();
    } catch (caught) {
      vhostError = describeError(caught);
    } finally {
      vhostLoading = false;
    }
  }

  async function loadPHPDefault(initial = false): Promise<void> {
    if (initial) phpDefaultLoading = true;
    phpDefaultError = null;
    try {
      const [status, builds] = await Promise.all([api.getPHPDefault(), api.listRuntimes()]);
      phpDefault = status;
      toolingRuntimes = builds;
      const available = builds
        .filter((runtime) => runtime.kind === 'php')
        .map((runtime) => runtime.id);
      if (!phpDefaultChoice || !available.includes(phpDefaultChoice)) {
        phpDefaultChoice = status.configured ?? status.build?.id ?? available[0] ?? '';
      }
    } catch (caught) {
      phpDefaultError = describeError(caught);
    } finally {
      phpDefaultLoading = false;
    }
  }

  async function loadNodeDefault(initial = false): Promise<void> {
    if (initial) nodeDefaultLoading = true;
    nodeDefaultError = null;
    try {
      const [status, builds] = await Promise.all([api.getNodeDefault(), api.listRuntimes()]);
      nodeDefault = status;
      toolingRuntimes = builds;
      const available = builds
        .filter((runtime) => runtime.kind === 'node')
        .map((runtime) => runtime.id);
      if (!nodeDefaultChoice || !available.includes(nodeDefaultChoice)) {
        nodeDefaultChoice = status.build?.id ?? '';
      }
    } catch (caught) {
      nodeDefaultError = describeError(caught);
    } finally {
      nodeDefaultLoading = false;
    }
  }

  async function loadGlobalIni(initial = false): Promise<void> {
    if (initial) globalIniLoading = true;
    globalIniError = null;
    try {
      globalIni = await api.getGlobalIni();
      globalIniContent = globalIni.content;
    } catch (caught) {
      globalIniError = describeError(caught);
    } finally {
      globalIniLoading = false;
    }
  }

  async function loadTools(initial = false): Promise<void> {
    if (initial) toolsLoading = true;
    toolsError = null;
    try {
      tools = await api.listTools();
    } catch (caught) {
      toolsError = describeError(caught);
    } finally {
      toolsLoading = false;
    }
  }

  onMount(() => {
    void load(true);
    void loadVhosts(true);
    void loadPHPDefault(true);
    void loadNodeDefault(true);
    void loadGlobalIni(true);
    void loadTools(true);
    const refresh = () => {
      void load();
      void loadVhosts();
      void loadPHPDefault();
      void loadNodeDefault();
      void loadGlobalIni();
      void loadTools();
    };
    window.addEventListener('devstack:refresh', refresh);
    return () => {
      window.removeEventListener('devstack:refresh', refresh);
      if (copyTimer) clearTimeout(copyTimer);
    };
  });

  async function savePorts(): Promise<void> {
    if (portSaving) return;
    const httpPort = Number(portDrafts.http_port);
    const httpsPort = Number(portDrafts.https_port);
    const uiPort = Number(portDrafts.ui_port);
    if (![httpPort, httpsPort, uiPort].every((port) => Number.isInteger(port) && port >= 1 && port <= 65535)) {
      portError = { code: 'INVALID_REQUEST', message: 'Ports must be whole numbers between 1 and 65535.' };
      return;
    }
    if (new Set([httpPort, httpsPort, uiPort]).size !== 3) {
      portError = { code: 'INVALID_REQUEST', message: 'HTTP, HTTPS, and dashboard ports must differ.' };
      return;
    }
    portSaving = true;
    portError = null;
    portsSaved = false;
    try {
      const updated = await api.updateSettings({
        http_port: httpPort,
        https_port: httpsPort,
        ui_port: uiPort,
      });
      settings = updated;
      setPortDrafts(updated);
      portWarnings = updated.warnings ?? [];
      portsSaved = true;
      addToast({
        kind: 'success',
        title: updated.restart_required ? 'Ports saved — restart required' : 'Ports saved',
        message: updated.restart_required
          ? 'Run devstack daemon restart to bind the new listeners.'
          : 'The running daemon already uses these ports.',
      });
    } catch (caught) {
      const described = describeError(caught);
      portError = described;
      toastError('Could not save ports', `${described.code}: ${described.message}`);
    } finally {
      portSaving = false;
    }
  }

  function requestServingMode(mode: ServingMode): void {
    if (!vhostStatus || mode === vhostStatus.mode || modeBusy) return;
    modeError = null;
    pendingMode = mode;
  }

  async function confirmServingMode(): Promise<void> {
    if (!pendingMode || modeBusy) return;
    const target = pendingMode;
    modeBusy = true;
    modeError = null;
    try {
      const status = await api.setServingMode(target);
      vhostStatus = status;
      pendingMode = null;
      if (settings) settings = { ...settings, serving_mode: status.mode };
      addToast({
        kind: 'success',
        title:
          target === 'builtin'
            ? 'Built-in proxy restored'
            : `${modeLabel(target)} is now serving sites`,
        message:
          target === 'builtin'
            ? 'The devstack vhosts were removed and the helper forwarders own ports 80 and 443 again.'
            : `${modeLabel(target)} owns ports 80 and 443. The built-in proxy has stopped.`,
      });
    } catch (caught) {
      const described = describeError(caught);
      modeError = described;
      toastError('Could not switch serving mode', `${described.code}: ${described.message}`);
    } finally {
      modeBusy = false;
    }
  }

  async function syncVhosts(): Promise<void> {
    if (vhostSyncing || !vhostStatus) return;
    vhostSyncing = true;
    try {
      vhostStatus = await api.syncVhosts();
      vhostError = null;
      addToast({
        kind: 'success',
        title: 'Vhosts synced',
        message:
          vhostStatus.mode === 'builtin'
            ? 'Built-in mode has no external vhosts to install.'
            : `${modeLabel(vhostStatus.mode)} reloaded the current release.`,
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not sync vhosts', `${described.code}: ${described.message}`);
    } finally {
      vhostSyncing = false;
    }
  }

  async function openVhostPreview(event?: MouseEvent): Promise<void> {
    if (!vhostStatus || previewLoading) return;
    previewReturnFocus = event?.currentTarget instanceof HTMLElement ? event.currentTarget : null;
    previewServer =
      vhostStatus.mode === 'apache' || vhostStatus.server === 'apache' ? 'apache' : 'nginx';
    if (!previewSite) {
      previewLoading = true;
      try {
        const sites = await api.listSites();
        previewSite = sites[0]?.name ?? '';
      } catch (caught) {
        const described = describeError(caught);
        toastError('Could not load sites for preview', `${described.code}: ${described.message}`);
        return;
      } finally {
        previewLoading = false;
      }
    }
    if (!previewSite) {
      addToast({
        kind: 'info',
        title: 'No sites to preview',
        message: 'Add a site first, then preview its generated nginx or Apache config.',
      });
      return;
    }
    previewOpen = true;
  }

  async function repairTrust(): Promise<void> {
    if (trustAction) return;
    trustAction = 'repair';
    try {
      trust = await api.repairTrust();
      trustError = null;
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
      // The helper's remove response reports that the operation ran, not the
      // resulting trust state. Reload status so the panel shows Not trusted.
      await api.removeTrust();
      await refreshTrust();
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

  async function savePHPDefault(): Promise<void> {
    if (!phpDefaultChoice || phpDefaultBusy) return;
    phpDefaultBusy = 'save';
    phpDefaultActionError = null;
    try {
      phpDefault = await api.setPHPDefault(phpDefaultChoice);
      phpDefaultChoice = phpDefault.configured ?? phpDefault.build?.id ?? phpDefaultChoice;
      addToast({
        kind: 'success',
        title: 'PHP CLI default saved',
        message: phpDefault.build
          ? `php and composer now use PHP ${phpDefault.build.version} (${phpDefault.build.id}).`
          : 'The default was saved. Install a PHP build to activate the shims.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      phpDefaultActionError = described;
      toastError('Could not set the PHP CLI default', `${described.code}: ${described.message}`);
    } finally {
      phpDefaultBusy = null;
    }
  }

  async function clearPHPDefault(): Promise<void> {
    if (phpDefaultBusy) return;
    phpDefaultBusy = 'clear';
    phpDefaultActionError = null;
    try {
      phpDefault = await api.clearPHPDefault();
      phpDefaultChoice = phpDefault.build?.id ?? '';
      addToast({
        kind: 'success',
        title: 'PHP CLI default cleared',
        message: phpDefault.build
          ? `The shims fall back to the newest installed build, PHP ${phpDefault.build.version}.`
          : 'No PHP build is installed, so the managed shims were removed.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      phpDefaultActionError = described;
      toastError('Could not clear the PHP CLI default', `${described.code}: ${described.message}`);
    } finally {
      phpDefaultBusy = null;
    }
  }

  async function saveNodeDefault(): Promise<void> {
    if (!nodeDefaultChoice || nodeDefaultBusy) return;
    if (!nodeBuilds.some((build) => build.id === nodeDefaultChoice)) {
      nodeDefaultActionError = {
        code: 'NODE_DEFAULT_NOT_INSTALLED',
        message: 'Choose an installed Node build before saving the default.',
      };
      return;
    }
    nodeDefaultBusy = 'save';
    nodeDefaultActionError = null;
    try {
      nodeDefault = await api.setNodeDefault(nodeDefaultChoice);
      nodeDefaultError = null;
      nodeDefaultChoice = nodeDefault.build?.id ?? nodeDefaultChoice;
      addToast({
        kind: 'success',
        title: 'Node CLI default saved',
        message: nodeDefault.build
          ? `node, npm and npx now use Node ${nodeDefault.build.version} (${nodeDefault.build.id}).`
          : 'The default was saved. Install a Node build to activate the shims.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      nodeDefaultActionError = described;
      toastError('Could not set the Node CLI default', `${described.code}: ${described.message}`);
    } finally {
      nodeDefaultBusy = null;
    }
  }

  async function clearNodeDefault(): Promise<void> {
    if (nodeDefaultBusy) return;
    nodeDefaultBusy = 'clear';
    nodeDefaultActionError = null;
    try {
      nodeDefault = await api.clearNodeDefault();
      nodeDefaultError = null;
      nodeDefaultChoice = nodeDefault.build?.id ?? '';
      addToast({
        kind: 'success',
        title: 'Node CLI default cleared',
        message: nodeDefault.build
          ? `The shims fall back to the newest installed build, Node ${nodeDefault.build.version}.`
          : 'No Node build is installed, so the managed shims were removed.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      nodeDefaultActionError = described;
      toastError('Could not clear the Node CLI default', `${described.code}: ${described.message}`);
    } finally {
      nodeDefaultBusy = null;
    }
  }

  async function saveGlobalIni(): Promise<void> {
    if (globalIniSaving) return;
    globalIniSaving = true;
    globalIniError = null;
    globalIniSaved = false;
    try {
      globalIni = await api.saveGlobalIni(globalIniContent);
      globalIniContent = globalIni.content;
      globalIniSaved = true;
      addToast({
        kind: 'success',
        title: 'Global php.ini saved',
        message: `${globalIni.path} was written for the CLI default build.`,
      });
    } catch (caught) {
      const described = describeError(caught);
      globalIniError = described;
      toastError('Could not save the global php.ini', `${described.code}: ${described.message}`);
    } finally {
      globalIniSaving = false;
    }
  }

  async function runToolAction(tool: ToolStatus, action: 'install' | 'update'): Promise<void> {
    if (toolBusy) return;
    toolBusy = `${tool.id}:${action}`;
    toolActionError = null;
    try {
      await api.installTool(tool.id, false);
      addToast({
        kind: 'success',
        title: action === 'install' ? `${tool.id} installed` : `${tool.id} updated`,
        message: 'The helper refreshed the managed system link.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      toolActionError = described;
      toastError(
        `Could not ${action} ${tool.id}`,
        `${described.code}: ${described.message}`,
      );
    } finally {
      toolBusy = null;
    }
  }

  async function removeTool(): Promise<void> {
    if (!removeToolTarget || removingTool) return;
    const target = removeToolTarget;
    removingTool = true;
    toolActionError = null;
    removeToolError = null;
    try {
      await api.removeTool(target.id);
      removeToolTarget = null;
      addToast({
        kind: 'success',
        title: `${target.id} removed`,
        message: 'The managed wrapper and downloaded files were removed. System tools are untouched.',
      });
      await loadTools();
    } catch (caught) {
      const described = describeError(caught);
      toolActionError = described;
      removeToolError = described;
      toastError('Could not remove tool', `${described.code}: ${described.message}`);
    } finally {
      removingTool = false;
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
              <Skeleton width={`${55 + index * 7}%`} height="13px" />
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
        {#if restartRequired}
          <Badge variant="warning">Restart required</Badge>
        {/if}
        <Button size="sm" variant="ghost" onclick={copyConfigPath}>
          <Copy size={12} strokeWidth={2} aria-hidden="true" />
          Copy path
        </Button>
      {/snippet}

      <div class="config-note">
        <FileCode size={15} strokeWidth={1.8} aria-hidden="true" />
        <p>
          Listener ports are editable below and saved to
          <span class="mono">{configPath}</span>. Other machine settings stay read-only.
        </p>
      </div>

      {#if restartRequired}
        <div class="restart-banner" role="status">
          <TriangleAlert size={16} strokeWidth={2} aria-hidden="true" />
          <div>
            <p class="restart-title">Restart required</p>
            <p>
              The new ports are saved but the running daemon still owns the old listeners.
              Run <span class="mono">devstack daemon restart</span> to apply them.
            </p>
            {#if portWarnings.length > 0}
              <ul class="restart-warnings">
                {#each portWarnings as warning (warning)}
                  <li>{warning}</li>
                {/each}
              </ul>
            {/if}
          </div>
        </div>
      {/if}

      <dl class="config-grid">
        <div class="config-item wide">
          <dt>Home</dt>
          <dd class="mono">{settings.home}</dd>
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

      {#if isWindowsHost}
        <section class="windows-wsl-note" aria-labelledby="windows-wsl-heading">
          <div class="windows-wsl-copy">
            <p class="eyebrow">Execution environment</p>
            <h3 id="windows-wsl-heading">Windows + WSL</h3>
            <p>Site backends and PHP/Node runtimes run in WSL. Windows-drive static projects are read through <code>/mnt/c</code> and can be slower for file watching.</p>
          </div>
          <div class="windows-wsl-distro">
            <span>Managed distro</span>
            <strong class="mono">{settings.wsl_distro || '—'}</strong>
          </div>
        </section>
      {/if}

      <form
        class="port-form"
        onsubmit={(event) => {
          event.preventDefault();
          void savePorts();
        }}
      >
        <div class="port-form-head">
          <div>
            <h3>Listener ports</h3>
            <p>Loopback ports for the proxy and dashboard. Changes require a daemon restart.</p>
          </div>
          {#if portsSaved}<Badge variant="accent">Saved</Badge>{/if}
        </div>
        <div class="port-grid">
          <label class="port-field" for="http-port">
            <span>HTTP proxy port</span>
            <input
              id="http-port"
              class="input mono"
              type="number"
              min="1"
              max="65535"
              bind:value={portDrafts.http_port}
              disabled={publicPortsFixed}
              oninput={() => {
                portsSaved = false;
                portError = null;
              }}
            />
          </label>
          <label class="port-field" for="https-port">
            <span>HTTPS proxy port</span>
            <input
              id="https-port"
              class="input mono"
              type="number"
              min="1"
              max="65535"
              bind:value={portDrafts.https_port}
              disabled={publicPortsFixed}
              oninput={() => {
                portsSaved = false;
                portError = null;
              }}
            />
          </label>
          <label class="port-field" for="ui-port">
            <span>Dashboard/API port</span>
            <input
              id="ui-port"
              class="input mono"
              type="number"
              min="1"
              max="65535"
              bind:value={portDrafts.ui_port}
              oninput={() => {
                portsSaved = false;
                portError = null;
              }}
            />
          </label>
        </div>
        <p class="port-hint">
          Ports 41000–43999 are reserved for site backends. Public 80/443 remain owned by
          the helper or the active web-server service.
        </p>
        {#if publicPortsFixed}
          <p class="port-hint">
            On Windows the HTTP and HTTPS proxy ports are fixed because the helper
            forwarders and firewall rule target them. The dashboard port can still change.
          </p>
        {/if}
        {#if portError}
          <div class="port-error">
            <ErrorBanner
              title="Could not save ports"
              code={portError.code}
              message={portError.message}
              ondismiss={() => (portError = null)}
            />
          </div>
        {/if}
        <div class="port-actions">
          <Button
            type="submit"
            size="sm"
            variant="primary"
            loading={portSaving}
            disabled={portSaving || !portsDirty}
          >
            {#if !portSaving}<Save size={12} strokeWidth={2} aria-hidden="true" />{/if}
            Save ports
          </Button>
        </div>
      </form>
    </Card>

    <Card
      title="Serving"
      subtitle="Choose how devstack publishes sites on ports 80 and 443"
      headerWrap
      className="serving-card"
    >
      {#snippet actions()}
        <Button
          size="sm"
          variant="secondary"
          onclick={openVhostPreview}
          loading={previewLoading}
          disabled={!vhostStatus || Boolean(vhostLoading)}
        >
          {#if !previewLoading}<Eye size={12} strokeWidth={2} aria-hidden="true" />{/if}
          Preview vhost
        </Button>
        <Button
          size="sm"
          variant="secondary"
          onclick={syncVhosts}
          loading={vhostSyncing}
          disabled={!vhostStatus || Boolean(vhostLoading)}
        >
          {#if !vhostSyncing}<RefreshCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
          Sync now
        </Button>
      {/snippet}

      {#if vhostLoading && !vhostStatus}
        <div class="serving-loading" aria-busy="true" aria-label="Loading serving status">
          <Skeleton width="90px" height="12px" />
          <Skeleton width="100%" height="46px" />
          <div class="serving-loading-grid">
            {#each Array(4) as _, index (index)}
              <div>
                <Skeleton width="66px" height="9px" />
                <Skeleton width={`${64 + index * 8}%`} height="11px" />
              </div>
            {/each}
          </div>
        </div>
      {:else if vhostError && !vhostStatus}
        <ErrorBanner
          title="Could not load serving status"
          code={vhostError.code}
          message={vhostError.message}
          onretry={() => loadVhosts(true)}
        />
      {:else if vhostStatus}
        {#if vhostError}
          <div class="serving-inline-error">
            <ErrorBanner
              title="Serving status is out of date"
              code={vhostError.code}
              message={vhostError.message}
              onretry={() => loadVhosts()}
              ondismiss={() => (vhostError = null)}
            />
          </div>
        {/if}

        <div class="serving-top">
          <div class="serving-summary">
            <div class="serving-mode-line">
              <span class="serving-mode-name">{activeModeLabel}</span>
              <Badge variant={modeBadgeVariant}>{activeMode}</Badge>
              {#if activeMode !== 'builtin' && vhostStatus.last_error}
                <Badge variant="warning">Attention</Badge>
              {/if}
            </div>
            <p>{activeModeDescription}</p>
          </div>

          <div class="mode-picker" role="group" aria-label="Serving mode">
            {#each servingModes as option (option.value)}
              <button
                type="button"
                class="mode-option"
                class:active={activeMode === option.value}
                aria-pressed={activeMode === option.value}
                aria-label={`Use ${option.label} serving`}
                disabled={modeBusy}
                onclick={() => requestServingMode(option.value)}
              >
                <span class="mode-option-head">
                  {#if activeMode === option.value}
                    <Check size={12} strokeWidth={2.4} aria-hidden="true" />
                  {/if}
                  {option.label}
                </span>
                <span class="mode-option-hint">{option.hint}</span>
              </button>
            {/each}
          </div>
        </div>

        <dl class="serving-grid">
          <div class="serving-item">
            <dt>Server</dt>
            <dd class="mono">{serverDisplay}</dd>
          </div>
          <div class="serving-item">
            <dt>Bridge include</dt>
            <dd>
              <span
                class="serving-state"
                class:ok={activeMode !== 'builtin' && vhostStatus.include_present}
                class:warn={activeMode !== 'builtin' && !vhostStatus.include_present}
              >
                {#if activeMode !== 'builtin'}
                  {#if vhostStatus.include_present}
                    <CircleCheck size={12} strokeWidth={2} aria-hidden="true" />
                  {:else}
                    <CircleX size={12} strokeWidth={2} aria-hidden="true" />
                  {/if}
                {/if}
                {includeLabel}
              </span>
            </dd>
          </div>
          <div class="serving-item">
            <dt>Config test</dt>
            <dd>
              <span
                class="serving-state"
                class:ok={activeMode !== 'builtin' && vhostStatus.config_ok}
                class:danger={activeMode !== 'builtin' && !vhostStatus.config_ok}
              >
                {#if activeMode !== 'builtin'}
                  {#if vhostStatus.config_ok}
                    <CircleCheck size={12} strokeWidth={2} aria-hidden="true" />
                  {:else}
                    <CircleX size={12} strokeWidth={2} aria-hidden="true" />
                  {/if}
                {/if}
                {configLabel}
              </span>
            </dd>
          </div>
          <div class="serving-item">
            <dt>Last sync</dt>
            <dd title={lastSyncTitle}>{lastSyncLabel}</dd>
          </div>
          <div class="serving-item wide">
            <dt>Current release</dt>
            <dd class="mono">{vhostStatus.current_release || '—'}</dd>
          </div>
          {#if vhostStatus.bridge_path}
            <div class="serving-item wide">
              <dt>Bridge path</dt>
              <dd class="mono">{vhostStatus.bridge_path}</dd>
            </div>
          {/if}
        </dl>

        {#if vhostStatus.last_error}
          <div class="serving-last-error" role="status">
            <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
            <div>
              <p class="last-error-title">Last error</p>
              <p>{vhostStatus.last_error}</p>
            </div>
          </div>
        {/if}
      {/if}
    </Card>

    <div class="tooling-grid">
      <Card
        title="PHP CLI default"
        subtitle="Machine-wide php and composer shims outside a site shell"
      >
        {#snippet actions()}
          <Badge variant={phpDefault?.configured ? 'accent' : phpDefault?.build ? 'info' : 'neutral'}>
            {phpDefault?.configured ? 'Explicit' : phpDefault?.build ? 'Fallback' : 'Not set'}
          </Badge>
        {/snippet}

        {#if phpDefaultLoading && !phpDefault}
          <div class="tooling-loading" aria-busy="true" aria-label="Loading PHP CLI default">
            <Skeleton width="64%" height="13px" />
            <Skeleton width="100%" height="36px" />
            <Skeleton width="46%" height="11px" />
          </div>
        {:else if phpDefaultError && !phpDefault}
          <ErrorBanner
            title="Could not load the PHP CLI default"
            code={phpDefaultError.code}
            message={phpDefaultError.message}
            onretry={() => loadPHPDefault(true)}
          />
        {:else if phpDefault}
          {#if phpDefaultError}
            <div class="tooling-inline-error">
              <ErrorBanner
                title="PHP CLI state is out of date"
                code={phpDefaultError.code}
                message={phpDefaultError.message}
                onretry={() => loadPHPDefault()}
                ondismiss={() => (phpDefaultError = null)}
              />
            </div>
          {/if}

          <dl class="tooling-list">
            <div>
              <dt>Status</dt>
              <dd>
                <span class="tool-state" class:ok={Boolean(phpDefault.build)}>
                  {phpDefaultLabel}
                </span>
              </dd>
            </div>
            <div>
              <dt>Resolved build</dt>
              <dd class="mono break">
                {phpDefault.build
                  ? `PHP ${phpDefault.build.version} · ${phpDefault.build.id}`
                  : 'No PHP build is installed'}
              </dd>
            </div>
            <div>
              <dt>Shim directory</dt>
              <dd class="mono break">{phpDefault.shim_dir || '—'}</dd>
            </div>
            <div>
              <dt>Managed shims</dt>
              <dd>
                <span class="tool-state" class:ok={phpDefault.shim_installed}>
                  {phpDefault.shim_installed
                    ? `Installed${phpDefault.shim_dir ? ` in ${phpDefault.shim_dir}` : ''}`
                    : 'Not installed'}
                </span>
              </dd>
            </div>
            <div>
              <dt>Global ini</dt>
              <dd class="mono break">{phpDefault.global_ini || '—'}</dd>
            </div>
          </dl>

          {#if phpDefaultActionError}
            <div class="tooling-inline-error">
              <ErrorBanner
                title="Could not update the PHP CLI default"
                code={phpDefaultActionError.code}
                message={phpDefaultActionError.message}
                ondismiss={() => (phpDefaultActionError = null)}
              />
            </div>
          {/if}

          <div class="php-default-form">
            <label class="tooling-label" for="php-default-build">Default PHP build</label>
            <select
              id="php-default-build"
              class="input mono"
              bind:value={phpDefaultChoice}
              disabled={phpDefaultBusy !== null || phpBuilds.length === 0}
            >
              {#if phpBuilds.length === 0}
                <option value="">No PHP build installed</option>
              {/if}
              {#each phpBuilds as build (build.id)}
                <option value={build.id}>
                  PHP {build.version} (r{phpRevision(build.id)}) · {build.backend}
                </option>
              {/each}
            </select>
            <p class="tooling-hint">
              Choosing a build points <span class="mono">php</span> and
              <span class="mono">composer</span> at it for new shells. Clearing keeps the
              newest installed build as the documented fallback.
            </p>
            <div class="tooling-actions">
              <Button
                size="sm"
                variant="primary"
                loading={phpDefaultBusy === 'save'}
                disabled={phpDefaultBusy !== null || !phpDefaultChoice}
                onclick={savePHPDefault}
              >
                Save default
              </Button>
              <Button
                size="sm"
                variant="ghost"
                loading={phpDefaultBusy === 'clear'}
                disabled={phpDefaultBusy !== null || !phpDefault.configured}
                onclick={clearPHPDefault}
              >
                Clear default
              </Button>
            </div>
          </div>
        {/if}
      </Card>

      <Card
        title="Node CLI default"
        subtitle="Machine-wide node, npm and npx shims outside a site shell"
      >
        {#snippet actions()}
          <Badge variant={nodeDefaultBadge.variant}>{nodeDefaultBadge.label}</Badge>
        {/snippet}

        {#if nodeDefaultLoading && !nodeDefault}
          <div class="tooling-loading" aria-busy="true" aria-label="Loading Node CLI default">
            <Skeleton width="64%" height="13px" />
            <Skeleton width="100%" height="36px" />
            <Skeleton width="46%" height="11px" />
          </div>
        {:else if nodeDefaultError && !nodeDefault}
          <ErrorBanner
            title="Could not load the Node CLI default"
            code={nodeDefaultError.code}
            message={nodeDefaultError.message}
            onretry={() => loadNodeDefault(true)}
          />
        {:else if nodeDefault}
          {#if nodeDefaultError}
            <div class="tooling-inline-error">
              <ErrorBanner
                title="Node CLI state is out of date"
                code={nodeDefaultError.code}
                message={nodeDefaultError.message}
                onretry={() => loadNodeDefault()}
                ondismiss={() => (nodeDefaultError = null)}
              />
            </div>
          {/if}

          {#if nodeDefaultMissing}
            <p class="node-default-warning" role="status">
              Configured Node {configuredNodeDefaultDisplay} is not installed. Choose another
              installed build, clear the default, or install Node {configuredNodeDefaultDisplay}.
            </p>
          {/if}

          <dl class="tooling-list">
            <div>
              <dt>Status</dt>
              <dd>
                <span class="tool-state" class:ok={Boolean(nodeDefault.build)} class:warn={nodeDefaultMissing}>
                  {nodeDefaultLabel}
                </span>
              </dd>
            </div>
            <div>
              <dt>Resolved build</dt>
              <dd class="mono break">
                {nodeDefault.build
                  ? `Node ${nodeDefault.build.version} · ${nodeDefault.build.id}`
                  : nodeDefaultMissing
                    ? `Node ${configuredNodeDefaultDisplay} is not installed`
                    : 'No Node build is installed'}
              </dd>
            </div>
            <div>
              <dt>Shim directory</dt>
              <dd class="mono break">{nodeDefault.shim_dir || '—'}</dd>
            </div>
            <div>
              <dt>Managed shims</dt>
              <dd>
                <span class="tool-state" class:ok={nodeDefault.shim_installed}>
                  {nodeDefault.shim_installed
                    ? `Installed${nodeDefault.shim_dir ? ` in ${nodeDefault.shim_dir}` : ''}`
                    : 'Not installed'}
                </span>
              </dd>
            </div>
          </dl>

          {#if nodeDefaultActionError}
            <div class="tooling-inline-error">
              <ErrorBanner
                title="Could not update the Node CLI default"
                code={nodeDefaultActionError.code}
                message={nodeDefaultActionError.message}
                ondismiss={() => (nodeDefaultActionError = null)}
              />
            </div>
          {/if}

          <div class="php-default-form">
            <label class="tooling-label" for="node-default-build">Default Node build</label>
            <select
              id="node-default-build"
              class="input mono"
              bind:value={nodeDefaultChoice}
              disabled={nodeDefaultBusy !== null || nodeBuilds.length === 0}
            >
              {#if nodeDefaultMissing}
                <option value="" disabled>Node {configuredNodeDefaultDisplay} (not installed)</option>
              {:else if nodeBuilds.length === 0}
                <option value="">No Node build installed</option>
              {/if}
              {#each nodeBuilds as build (build.id)}
                <option value={build.id}>
                  Node {build.version} · {build.backend}
                </option>
              {/each}
            </select>
            <p class="tooling-hint">
              Choosing a build points <span class="mono">node</span>,
              <span class="mono">npm</span> and <span class="mono">npx</span> at it for new shells.
              Clearing keeps the newest installed build as the documented fallback.
            </p>
            <div class="tooling-actions">
              <Button
                size="sm"
                variant="primary"
                ariaLabel="Save Node default"
                loading={nodeDefaultBusy === 'save'}
                disabled={nodeDefaultBusy !== null || !nodeDefaultChoice}
                onclick={saveNodeDefault}
              >
                Save default
              </Button>
              <Button
                size="sm"
                variant="ghost"
                ariaLabel="Clear Node default"
                loading={nodeDefaultBusy === 'clear'}
                disabled={nodeDefaultBusy !== null || !nodeDefault.configured}
                onclick={clearNodeDefault}
              >
                Clear default
              </Button>
            </div>
            <a class="install-more" href="#/runtimes">
              <Download size={12} strokeWidth={2} aria-hidden="true" />
              Install more Node versions
            </a>
          </div>
        {/if}
      </Card>

      <Card
        title="Global CLI php.ini"
        subtitle="Editable machine-wide configuration for the default PHP build"
      >
        {#snippet actions()}
          <Button
            size="sm"
            variant="ghost"
            loading={globalIniLoading}
            disabled={globalIniLoading || globalIniSaving}
            onclick={() => loadGlobalIni()}
          >
            {#if !globalIniLoading}<RefreshCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
            Reload
          </Button>
        {/snippet}

        {#if globalIniLoading && !globalIni}
          <div class="tooling-loading" aria-busy="true" aria-label="Loading global php.ini">
            <Skeleton width="54%" height="12px" />
            <Skeleton width="100%" height="150px" radius="8px" />
          </div>
        {:else if globalIniError && !globalIni}
          <ErrorBanner
            title="Could not load the global php.ini"
            code={globalIniError.code}
            message={globalIniError.message}
            onretry={() => loadGlobalIni(true)}
          />
        {:else}
          <div class="ini-path">
            <FileCode size={14} strokeWidth={1.8} aria-hidden="true" />
            <span class="mono break">{globalIniPath}</span>
            {#if globalIni?.build_id}<Badge>{globalIni.build_id}</Badge>{/if}
            {#if globalIniSaved}<Badge variant="accent">Saved</Badge>{/if}
          </div>

          {#if globalIniError}
            <div class="tooling-inline-error">
              <ErrorBanner
                title="Could not reload the global php.ini"
                code={globalIniError.code}
                message={globalIniError.message}
                onretry={() => loadGlobalIni()}
                ondismiss={() => (globalIniError = null)}
              />
            </div>
          {/if}

          <label class="tooling-label" for="global-ini">Global php.ini content</label>
          <textarea
            id="global-ini"
            class="input mono global-ini"
            rows="10"
            spellcheck="false"
            bind:value={globalIniContent}
            oninput={() => (globalIniSaved = false)}
          ></textarea>
          <p class="tooling-hint">
            This file is used by the machine-wide CLI shims. Site shells keep their own PHP
            build and site ini, so editing here never changes a running site.
          </p>
          <div class="tooling-actions">
            <Button
              size="sm"
              variant="primary"
              loading={globalIniSaving}
              disabled={globalIniSaving || !globalIni}
              onclick={saveGlobalIni}
            >
              Save php.ini
            </Button>
          </div>
        {/if}
      </Card>
    </div>

    <Card
      title="Native tools"
      subtitle="Managed Composer wrapper installed by the privileged helper"
      headerWrap
      className="native-tools-card"
    >
      {#snippet actions()}
        <Badge variant={composerTool?.installed ? 'accent' : 'info'}>
          {composerTool?.installed ? 'Composer managed' : 'Composer available'}
        </Badge>
      {/snippet}

      {#if toolsLoading && tools.length === 0}
        <div class="tooling-loading" aria-busy="true" aria-label="Loading native tools">
          <Skeleton width="36%" height="14px" />
          <Skeleton width="100%" height="82px" radius="8px" />
        </div>
      {:else}
        {#if toolsError}
          <div class="tooling-inline-error">
            <ErrorBanner
              title="Could not load native tools"
              code={toolsError.code}
              message={toolsError.message}
              onretry={() => loadTools(true)}
            />
          </div>
        {/if}

        {#if toolActionError}
          <div class="tooling-inline-error">
            <ErrorBanner
              title="Tool action failed"
              code={toolActionError.code}
              message={toolActionError.message}
              ondismiss={() => (toolActionError = null)}
            />
          </div>
        {/if}

        <div class="tool-list">
          <article class="tool-row">
            <span class="tool-icon" aria-hidden="true"><Package size={18} strokeWidth={1.8} /></span>
            <div class="tool-copy">
              <div class="tool-title">
                <h3>Composer</h3>
                {#if composerTool?.installed}
                  <Badge variant="accent">Installed</Badge>
                {:else}
                  <Badge>Not installed</Badge>
                {/if}
                {#if composerTool?.managed}<Badge>Managed</Badge>{/if}
                {#if composerTool?.conflict}<Badge variant="danger">Conflict</Badge>{/if}
                {#if composerTool?.version}
                  <span class="tool-version mono">{composerTool.version}</span>
                {/if}
              </div>
              {#if composerTool}
                <p class="tool-path mono" title={composerTool.wrapper_path}>
                  {composerTool.wrapper_path || 'No system wrapper reported'}
                </p>
                {#if composerTool.target}
                  <p class="tool-path mono" title={composerTool.target}>
                    → {composerTool.target}
                  </p>
                {/if}
                {#if composerTool.conflict}
                  <p class="tool-conflict" role="status">{composerTool.conflict}</p>
                {/if}
                {#if composerTool.backup}
                  <p class="tool-note">Previous wrapper backed up to <span class="mono">{composerTool.backup}</span>.</p>
                {/if}
              {:else}
                <p class="tool-note">
                  The helper did not report a Composer tool. Check the helper status in Doctor
                  before installing.
                </p>
              {/if}
            </div>
            <div class="tool-actions">
              {#if composerTool}
                {#if composerTool.installed}
                  <Button
                    size="sm"
                    variant="secondary"
                    loading={toolBusy === 'composer:update'}
                    disabled={Boolean(toolBusy)}
                    onclick={() => runToolAction(composerTool, 'update')}
                  >
                    Update
                  </Button>
                {:else}
                  <Button
                    size="sm"
                    variant="secondary"
                    loading={toolBusy === 'composer:install'}
                    disabled={Boolean(toolBusy)}
                    onclick={() => runToolAction(composerTool, 'install')}
                  >
                    Install
                  </Button>
                {/if}
                {#if composerTool.installed || composerTool.managed}
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={Boolean(toolBusy)}
                    onclick={() => {
                      removeToolError = null;
                      removeToolTarget = composerTool ?? null;
                    }}
                  >
                    <Trash2 size={13} strokeWidth={2} aria-hidden="true" />
                    Remove
                  </Button>
                {/if}
              {/if}
            </div>
          </article>

        </div>
      {/if}
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

        {#if trustError}
          <ErrorBanner
            title="Could not read trust status"
            code={trustError.code}
            message={trustError.message}
            onretry={() => void load()}
          />
        {:else}
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
        {/if}
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
  open={removeToolTarget !== null}
  title={removeToolTarget ? `Remove ${removeToolTarget.id}?` : 'Remove tool?'}
  description="Removes the managed wrapper and downloaded tool files. A foreign system command is never deleted. You can install the managed tool again at any time."
  confirmLabel="Remove tool"
  danger
  busy={removingTool}
  onconfirm={removeTool}
  oncancel={() => {
    if (!removingTool) removeToolTarget = null;
  }}
>
  {#snippet details()}
    {#if removeToolError}
      <p class="dialog-error" role="alert">
        <span class="mono">{removeToolError.code}</span>
        {removeToolError.message}
      </p>
    {/if}
  {/snippet}
</ConfirmDialog>

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

<ConfirmDialog
  open={pendingMode !== null}
  title={`Switch serving to ${pendingModeLabel}?`}
  description="This changes which process owns ports 80 and 443 for every devstack site."
  confirmLabel={`Switch to ${pendingModeLabel}`}
  busy={modeBusy}
  onconfirm={confirmServingMode}
  oncancel={() => {
    if (!modeBusy) {
      pendingMode = null;
      modeError = null;
    }
  }}
>
  {#snippet details()}
    <ul class="impact-list">
      {#each impactItems as item (item)}
        <li>{item}</li>
      {/each}
    </ul>
    {#if modeError}
      <p class="dialog-error" role="alert">
        <span class="mono">{modeError.code}</span>
        {modeError.message}
      </p>
    {/if}
  {/snippet}
</ConfirmDialog>

<VhostPreviewDrawer
  open={previewOpen}
  site={previewSite}
  initialServer={previewServer}
  returnFocus={previewReturnFocus}
  onclose={() => (previewOpen = false)}
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
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 16px;
  }

  .loading-item {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .loading-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 4px 0;
  }

  :global(.native-tools-card) {
    margin-top: 18px;
  }

  .port-form {
    margin-top: 14px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }

  .port-form-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
  }

  .port-form-head h3 {
    font-size: 12px;
    font-weight: 600;
  }

  .port-form-head p {
    margin-top: 2px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .port-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 10px;
  }

  .port-field {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }

  .port-field > span {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .port-hint {
    margin-top: 8px;
    color: var(--muted-2);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .port-error {
    margin-top: 10px;
  }

  .port-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 10px;
  }

  .restart-banner {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    margin-bottom: 14px;
    padding: 10px 11px;
    border: 1px solid color-mix(in oklab, var(--warning-fg) 35%, var(--border));
    border-radius: var(--radius);
    background: color-mix(in oklab, var(--warning-bg) 55%, var(--surface));
  }

  .restart-banner :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--warning-fg);
  }

  .restart-title {
    font-size: 11.5px;
    font-weight: 600;
    color: var(--warning-fg);
  }

  .restart-banner p {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .restart-warnings {
    margin: 5px 0 0 16px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
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

  .windows-wsl-note {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 18px;
    margin-top: 12px;
    padding: 13px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .windows-wsl-copy {
    display: grid;
    gap: 4px;
    min-width: 0;
  }

  .windows-wsl-copy .eyebrow {
    margin: 0;
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .windows-wsl-copy h3 {
    margin: 0;
    color: var(--fg);
    font-size: 13px;
    font-weight: 650;
  }

  .windows-wsl-copy p:last-child {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .windows-wsl-copy code {
    color: var(--fg);
    font-family: var(--font-mono);
  }

  .windows-wsl-distro {
    display: grid;
    flex: none;
    gap: 4px;
    color: var(--muted);
    font-size: 10px;
  }

  .windows-wsl-distro strong {
    color: var(--fg);
    font-size: 11px;
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

  :global(.serving-card) {
    margin-top: 14px;
  }

  .serving-loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .serving-loading-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 14px;
  }

  .serving-loading-grid > div {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }

  .serving-inline-error {
    margin-bottom: 12px;
  }

  .serving-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 14px;
  }

  .serving-summary {
    flex: 1;
    min-width: min(280px, 100%);
  }

  .serving-mode-line {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
  }

  .serving-mode-name {
    color: var(--fg);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .serving-summary > p {
    max-width: 620px;
    margin-top: 3px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .mode-picker {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 4px;
    flex: none;
    width: min(430px, 100%);
    padding: 3px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .mode-option {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    min-height: 48px;
    padding: 6px 9px;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--muted);
    text-align: left;
    transition:
      background-color 150ms var(--ease),
      border-color 150ms var(--ease),
      color 150ms var(--ease);
  }

  .mode-option:hover:not(:disabled) {
    background: var(--surface-3);
    color: var(--fg);
  }

  .mode-option.active {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--fg);
  }

  .mode-option:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .mode-option-head {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 600;
    line-height: 1.2;
  }

  .mode-option-hint {
    color: var(--muted-2);
    font-size: 9px;
    line-height: 1.3;
  }

  .serving-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    margin: 14px 0 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--border);
    overflow: hidden;
  }

  .serving-item {
    min-width: 0;
    padding: 9px 12px;
    background: var(--surface);
  }

  .serving-item.wide {
    grid-column: 1 / -1;
    background: var(--surface-2);
  }

  .serving-item dt {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.02em;
  }

  .serving-item dd {
    margin: 2px 0 0;
    color: var(--fg);
    font-size: 11px;
    font-weight: 500;
    overflow-wrap: anywhere;
  }

  .serving-state {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--muted);
    font-size: 11px;
  }

  .serving-state :global(svg) {
    flex: none;
  }

  .serving-state.ok {
    color: var(--success-fg);
  }

  .serving-state.warn {
    color: var(--warning-fg);
  }

  .serving-state.danger {
    color: var(--danger-fg);
  }

  .serving-last-error {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    margin-top: 12px;
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .serving-last-error :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .last-error-title {
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .serving-last-error p:not(.last-error-title) {
    margin-top: 1px;
    font-size: 10.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .impact-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding-left: 17px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .impact-list li::marker {
    color: var(--warning-fg);
  }

  .dialog-error {
    margin-top: 12px;
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--danger) 38%, var(--border));
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .dialog-error .mono {
    margin-right: 5px;
    font-size: 10px;
  }

  .tooling-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
    gap: 14px;
    margin-top: 14px;
  }

  .tooling-loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .tooling-inline-error {
    margin-bottom: 10px;
  }

  .tooling-list {
    display: flex;
    flex-direction: column;
    margin: 0;
    border-top: 1px solid var(--border);
  }

  .tooling-list > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    padding: 6px 0;
    border-bottom: 1px solid var(--border);
  }

  .tooling-list dt {
    flex: none;
    color: var(--muted);
    font-size: 10px;
  }

  .tooling-list dd {
    margin: 0;
    min-width: 0;
    color: var(--fg);
    font-size: 10.5px;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .tool-state {
    color: var(--muted);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .tool-state.ok {
    color: var(--success-fg);
  }

  .tool-state.warn {
    color: var(--warning-fg);
  }

  .node-default-warning {
    margin-bottom: 10px;
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 34%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .install-more {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    margin-top: 2px;
    color: var(--info-fg);
    font-size: 10.5px;
    font-weight: 500;
    text-decoration: underline;
    text-underline-offset: 2px;
    align-self: flex-start;
  }

  .install-more:hover {
    color: var(--fg);
  }

  .php-default-form {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 12px;
  }

  .tooling-label {
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .tooling-hint {
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.5;
  }

  .tooling-hint .mono {
    color: var(--muted);
  }

  .tooling-actions {
    display: flex;
    gap: 6px;
    margin-top: 4px;
  }

  .ini-path {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 10px;
    padding: 7px 9px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10px;
  }

  .ini-path :global(svg) {
    flex: none;
    color: var(--muted-2);
  }

  .global-ini {
    width: 100%;
    min-height: 150px;
    margin-top: 3px;
    padding: 10px;
    resize: vertical;
    font-family: var(--font-mono);
    font-size: 10.5px;
    line-height: 1.55;
  }

  .tool-list {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  .tool-row {
    display: grid;
    grid-template-columns: 38px minmax(0, 1fr) auto;
    align-items: start;
    gap: 11px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .tool-row.pending {
    border-style: dashed;
    background: var(--surface);
  }

  .tool-icon {
    display: grid;
    place-items: center;
    width: 38px;
    height: 38px;
    border: 1px solid var(--info);
    border-radius: 8px;
    background: var(--info-soft);
    color: var(--info-fg);
  }

  .tool-icon.pending {
    border-color: var(--border);
    background: var(--surface-2);
    color: var(--muted-2);
  }

  .tool-copy {
    min-width: 0;
  }

  .tool-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .tool-title h3 {
    font-size: 13px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .tool-version {
    color: var(--muted-2);
    font-size: 9.5px;
  }

  .tool-path {
    margin-top: 2px;
    color: var(--muted);
    font-size: 9.5px;
    overflow-wrap: anywhere;
  }

  .tool-note {
    margin-top: 4px;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .tool-note .mono {
    color: var(--fg);
  }

  .tool-conflict {
    margin-top: 5px;
    padding: 5px 7px;
    border-radius: var(--radius-sm);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 10px;
    line-height: 1.45;
    overflow-wrap: anywhere;
  }

  .tool-actions {
    display: flex;
    align-items: center;
    gap: 5px;
  }

  .tool-pending-label {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--muted-2);
    font-size: 10px;
    white-space: nowrap;
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
    .two-col,
    .tooling-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .port-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .config-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .serving-grid,
    .serving-loading-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .mode-picker {
      width: 100%;
    }
  }

  @media (max-width: 700px) {
    .loading-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 620px) {
    .config-grid,
    .serving-grid {
      grid-template-columns: 1fr;
    }

    .mode-option {
      min-height: 52px;
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

    .tool-row {
      grid-template-columns: 32px minmax(0, 1fr);
    }

    .tool-icon {
      width: 32px;
      height: 32px;
    }

    .tool-actions {
      grid-column: 1 / -1;
      justify-content: flex-end;
    }
  }
</style>
