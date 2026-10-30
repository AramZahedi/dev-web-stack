<script lang="ts">
  import { onMount } from 'svelte';
  import { push } from 'svelte-spa-router';
  import {
    ArrowLeft,
    Check,
    ChevronDown,
    Copy,
    ExternalLink,
    Eye,
    FileCode,
    Globe,
    KeyRound,
    LockOpen,
    Play,
    RotateCw,
    ShieldCheck,
    Square,
    TriangleAlert,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import * as db from '../lib/databaseApi';
  import { copyText } from '../lib/clipboard';
  import {
    describeError,
    describePHPError,
    type PHPErrorDescription,
  } from '../lib/errors';
  import {
    checksumPrefix,
    daysUntil,
    formatDate,
    isSecretKey,
    runtimeLabel,
    siteUrl,
    sourceModeLabel,
  } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type {
    ApacheAllowOverride,
    BuildResult,
    FrontControllerMode,
    NodeDefaultStatus,
    NodeToolchain,
    PHPChange,
    PHPInfo,
    PHPRuntimeBuild,
    RuntimeBuild,
    Site,
    SourceMode,
    UpdateSiteRequest,
  } from '../lib/types';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import Card from '../lib/components/Card.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import ExtensionToggles from '../lib/components/ExtensionToggles.svelte';
  import Field from '../lib/components/Field.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import LogsPanel from '../lib/components/LogsPanel.svelte';
  import NodeVersionPicker from '../lib/components/NodeVersionPicker.svelte';
  import PHPIniEditor from '../lib/components/PHPIniEditor.svelte';
  import ProjectSourceMode from '../lib/components/ProjectSourceMode.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import StatusPill from '../lib/components/StatusPill.svelte';
  import TerminalPanel from '../lib/components/TerminalPanel.svelte';
  import Toggle from '../lib/components/Toggle.svelte';
  import VhostPreviewDrawer from '../lib/components/VhostPreviewDrawer.svelte';
  import SiteDatabaseCard from '../lib/components/site/SiteDatabaseCard.svelte';
  import type { SiteDatabaseLink } from '../lib/databaseTypes';

  interface Props {
    params: { ref?: string };
  }

  let { params }: Props = $props();

  type Action = 'start' | 'stop' | 'restart';

  let site = $state<Site | null>(null);
  let siteLinks = $state<SiteDatabaseLink[]>([]);
  let linksError = $state<{ code: string; message: string } | null>(null);
  let loading = $state(true);
  let notFound = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);
  let busyAction = $state<Action | null>(null);
  let copied = $state(false);
  let revealed = $state<Record<string, boolean>>({});
  let deleteOpen = $state(false);
  let deleting = $state(false);
  let previewOpen = $state(false);
  let previewServer = $state<'nginx' | 'apache'>('nginx');
  let previewLoading = $state(false);
  let previewReturnFocus = $state<HTMLElement | null>(null);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  let phpInfo = $state<PHPInfo | null>(null);
  let phpLoading = $state(false);
  let phpError = $state<{ code: string; message: string } | null>(null);
  let phpApplying = $state(false);
  let phpApplyError = $state<PHPErrorDescription | null>(null);
  let phpSwitchError = $state<PHPErrorDescription | null>(null);
  let phpRuntimes = $state<RuntimeBuild[]>([]);
  let nodeDefault = $state<NodeDefaultStatus | null>(null);
  let switchOpen = $state(false);
  let switchBuild = $state('');

  interface EnvRow {
    key: string;
    value: string;
  }

  let editOpen = $state(false);
  let savingConfig = $state(false);
  let configError = $state<{ code: string; message: string } | null>(null);
  let configValidation = $state<Record<string, string>>({});
  let editDocumentRoot = $state('');
  let editCommand = $state('');
  let editNodeVersion = $state('');
  let editFrontController = $state(false);
  let editFrontControllerFile = $state('index.php');
  let editFrontControllerMode = $state<FrontControllerMode>('auto');
  let editApacheAllowOverride = $state<ApacheAllowOverride>('all');
  let editSpaFallback = $state(false);
  let editSpaFallbackFile = $state('index.html');
  let editBuildEnabled = $state(false);
  let editBuildNodeVersion = $state('');
  let editBuildCommand = $state('');
  let editBuildOutput = $state('');
  let editBuildEnvRows = $state<EnvRow[]>([{ key: '', value: '' }]);

  let sourceOpen = $state(false);
  let sourceMode = $state<SourceMode>('wsl');
  let sourcePath = $state('');
  let sourceSaving = $state(false);
  let sourceError = $state<{ code: string; message: string } | null>(null);

  let buildRunning = $state(false);
  let buildResult = $state<BuildResult | null>(null);
  let buildError = $state<{ code: string; message: string } | null>(null);
  let buildLogsContent = $state('');
  let buildLogsPath = $state('');
  let buildLogsLoading = $state(false);
  let buildLogsError = $state<{ code: string; message: string } | null>(null);
  let buildLogRef = '';

  const ref = $derived(params?.ref ?? '');

  const active = $derived(
    site ? ['running', 'starting', 'stopping'].includes(site.status) : false,
  );
  const url = $derived(site ? siteUrl(site) : '');
  const certDays = $derived(site?.certificate ? daysUntil(site.certificate.not_after) : null);
  const envEntries = $derived(Object.entries(site?.env ?? {}));
  const domains = $derived(
    site?.certificate?.domains?.length
      ? site.certificate.domains
      : site
        ? [site.domain, ...(site.www ? [`www.${site.domain}`] : [])]
        : [],
  );

  const stopped = $derived(
    Boolean(site && (site.desired_state === 'stopped' || site.status === 'stopped')),
  );

  const canSwitchSource = $derived(
    Boolean(site && (site.source_mode === 'windows' || site.source_mode === 'wsl')),
  );

  function phpLine(version: string): string {
    const parts = version.split('.');
    return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
  }

  function phpRevision(id: string): number {
    const match = /-r([1-9][0-9]*)$/.exec(id);
    return match ? Number.parseInt(match[1], 10) : 0;
  }

  const phpBuilds = $derived<PHPRuntimeBuild[]>(
    phpRuntimes
      .filter((runtime) => runtime.kind === 'php')
      .map((runtime) => ({
        ...runtime,
        line: phpLine(runtime.version),
        revision: phpRevision(runtime.id),
      }))
      .sort(
        (a, b) =>
          b.version.localeCompare(a.version, undefined, { numeric: true }) ||
          b.revision - a.revision,
      ),
  );

  const nodeBuilds = $derived(
    phpRuntimes
      .filter((runtime) => runtime.kind === 'node')
      .slice()
      .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true })),
  );

  // The daemon never substitutes another build for a configured default that
  // is no longer installed. Only a resolved build (or an installed global
  // summary reported for this site) may appear in the inherit label.
  const defaultNodeBuild = $derived(
    nodeDefault?.build?.kind === 'node' ? nodeDefault.build : null,
  );
  const configuredNodeDefault = $derived(nodeDefault?.configured?.trim() ?? '');
  const nodeDefaultMissing = $derived(
    Boolean(configuredNodeDefault) && !defaultNodeBuild,
  );
  const globalNodeVersion = $derived(
    defaultNodeBuild?.version ??
      (site?.node_toolchain?.source === 'global' && site.node_toolchain.installed
        ? site.node_toolchain.version
        : ''),
  );

  const nodeInheritLabel = $derived(
    globalNodeVersion
      ? `Inherit global default (v${globalNodeVersion})`
      : 'Inherit global default',
  );

  /**
   * The daemon resolves the exact shell toolchain. Older daemons omitted the
   * summary; fall back to the site pin so the page still explains itself.
   */
  const nodeToolchain = $derived.by<NodeToolchain | null>(() => {
    if (site?.node_toolchain) return site.node_toolchain;
    const pin = site?.node_version?.trim();
    if (!pin) return null;
    return {
      version: pin,
      source: 'site',
      installed: nodeBuilds.some((build) => build.version === pin),
    };
  });

  function nodeSourceLabel(toolchain: NodeToolchain): string {
    switch (toolchain.source) {
      case 'global':
        return 'Inherited from the global default';
      case 'site':
        return 'Pinned for this site';
      case 'build':
        return 'Build profile version';
      default:
        return 'Resolved by the daemon';
    }
  }

  const nodeVersionChanged = $derived(
    Boolean(site) &&
      editNodeVersion !== (site?.node_version ?? '') &&
      (site?.kind !== 'node' || Boolean(editNodeVersion)),
  );

  const switchCandidates = $derived(
    phpBuilds.filter((runtime) => runtime.id !== (site?.php_build || phpInfo?.build.id)),
  );

  const switchTarget = $derived(
    phpBuilds.find((runtime) => runtime.id === switchBuild) ?? switchCandidates[0] ?? null,
  );

  let loadGeneration = 0;
  let loadedRef = '';
  let pollAttempts = 0;

  async function loadPHP(siteRef: string, generation: number): Promise<void> {
    phpLoading = true;
    phpError = null;
    try {
      const info = await api.getPHPInfo(siteRef);
      if (generation !== loadGeneration || siteRef !== ref) return;
      phpInfo = info;
    } catch (caught) {
      if (generation !== loadGeneration || siteRef !== ref) return;
      phpError = describeError(caught);
    } finally {
      if (generation === loadGeneration) phpLoading = false;
    }
  }

  async function load(initial = false): Promise<void> {
    const siteRef = ref;
    const generation = ++loadGeneration;
    const reset = initial || loadedRef !== siteRef;
    loadedRef = siteRef;
    if (reset) {
      loading = true;
      site = null;
      siteLinks = [];
      linksError = null;
      phpInfo = null;
      phpRuntimes = [];
      nodeDefault = null;
      phpLoading = false;
      phpError = null;
      phpApplyError = null;
      phpSwitchError = null;
      error = null;
      notFound = false;
      pollAttempts = 0;
      revealed = {};
      copied = false;
      deleteOpen = false;
      deleting = false;
      busyAction = null;
      switchOpen = false;
      switchBuild = '';
      phpApplying = false;
      previewOpen = false;
      previewLoading = false;
      previewReturnFocus = null;
      editOpen = false;
      savingConfig = false;
      configError = null;
      configValidation = {};
      sourceOpen = false;
      sourceSaving = false;
      sourceError = null;
      sourcePath = '';
      buildRunning = false;
      buildResult = null;
      buildError = null;
      buildLogsContent = '';
      buildLogsPath = '';
      buildLogsLoading = false;
      buildLogsError = null;
      buildLogRef = '';
    }
    error = null;
    notFound = false;
    if (!siteRef) {
      notFound = true;
      loading = false;
      return;
    }
    try {
      const linksPromise = db
        .listSiteDatabases(siteRef)
        .then((result) => ({ ok: true as const, links: result?.links ?? [] }))
        .catch((caught) => ({ ok: false as const, error: describeError(caught) }));
      const [next, builds, defaultStatus, linksResult] = await Promise.all([
        api.getSite(siteRef),
        api.listRuntimes().catch(() => [] as RuntimeBuild[]),
        api.getNodeDefault().catch(() => null),
        linksPromise,
      ]);
      if (generation !== loadGeneration || siteRef !== ref) return;
      site = next;
      if (linksResult.ok) {
        siteLinks = linksResult.links;
        linksError = null;
      } else {
        linksError = linksResult.error;
      }
      phpRuntimes = builds;
      nodeDefault = defaultStatus;
      if (next.kind === 'php') await loadPHP(siteRef, generation);
    } catch (caught) {
      if (generation !== loadGeneration || siteRef !== ref) return;
      const described = describeError(caught);
      if (described.code === 'NOT_FOUND') {
        notFound = true;
      } else {
        error = described;
      }
    } finally {
      if (generation === loadGeneration) loading = false;
    }
  }

  let activeRef = $state<string | null>(null);
  $effect(() => {
    if (activeRef === ref) return;
    activeRef = ref;
    void load(true);
  });

  onMount(() => {
    const refresh = () => void load();
    window.addEventListener('devstack:refresh', refresh);
    return () => {
      window.removeEventListener('devstack:refresh', refresh);
      if (copyTimer) clearTimeout(copyTimer);
    };
  });

  const transient = $derived(site?.status === 'starting' || site?.status === 'stopping');
  $effect(() => {
    if (!transient) {
      pollAttempts = 0;
      return;
    }
    pollAttempts = 0;
    const timer = setInterval(() => {
      pollAttempts += 1;
      if (pollAttempts > 30) {
        clearInterval(timer);
        return;
      }
      void load();
    }, 1200);
    return () => clearInterval(timer);
  });

  async function act(action: Action): Promise<void> {
    if (!site || busyAction) return;
    const siteRef = site.id;
    busyAction = action;
    try {
      if (action === 'start') await api.startSite(siteRef);
      if (action === 'stop') await api.stopSite(siteRef);
      if (action === 'restart') await api.restartSite(siteRef);
      addToast({
        kind: 'success',
        title: `${site.name} ${action === 'stop' ? 'stopped' : action === 'start' ? 'started' : 'restarted'}`,
        message: action === 'stop' ? 'The route remains configured.' : 'The site is being probed for readiness.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError(`Could not ${action} ${site.name}`, `${described.code}: ${described.message}`);
    } finally {
      busyAction = null;
    }
  }

  async function applyPHP(change: PHPChange): Promise<void> {
    if (!site || phpApplying) return;
    const current = site;
    phpApplying = true;
    phpApplyError = null;
    try {
      await api.applyPHP(current.id, change);
      addToast({
        kind: 'success',
        title: 'PHP settings applied',
        message: stopped
          ? 'Configuration validated. Changes apply on next start.'
          : 'Validated, staged, and swapped onto the new FPM master.',
      });
      await load();
    } finally {
      phpApplying = false;
    }
  }

  async function onExtensionsChange(value: Record<string, boolean>): Promise<void> {
    phpSwitchError = null;
    try {
      await applyPHP({ extensions: value });
    } catch (caught) {
      phpApplyError = describePHPError(caught);
    }
  }

  function openSwitch(): void {
    phpSwitchError = null;
    switchBuild = switchCandidates[0]?.id ?? '';
    switchOpen = true;
  }

  async function switchPHP(): Promise<void> {
    const target = switchTarget;
    if (!site || !target) return;
    switchOpen = false;
    phpSwitchError = null;
    try {
      await applyPHP({ version: target.line, build: target.id });
    } catch (caught) {
      phpSwitchError = describePHPError(caught);
    }
  }

  async function copyUrl(): Promise<void> {
    const ok = await copyText(url);
    if (!ok) {
      toastError('Could not copy the URL', 'Select the address and copy it manually.');
      return;
    }
    copied = true;
    addToast({ kind: 'success', title: 'URL copied', message: url });
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copied = false), 1800);
  }

  async function confirmDelete(): Promise<void> {
    if (!site) return;
    deleting = true;
    try {
      await api.deleteSite(site.id);
      addToast({
        kind: 'success',
        title: `${site.name} removed`,
        message: 'Domain, certificate and generated configs were removed. Project files are untouched.',
      });
      push('/sites');
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not remove site', `${described.code}: ${described.message}`);
    } finally {
      deleting = false;
    }
  }

  async function previewVhost(event?: MouseEvent): Promise<void> {
    if (!site || previewLoading) return;
    const previewRef = site.id;
    previewReturnFocus = event?.currentTarget instanceof HTMLElement ? event.currentTarget : null;
    previewLoading = true;
    try {
      const status = await api.getVhostStatus();
      if (site?.id !== previewRef) return;
      previewServer =
        status.mode === 'apache' || status.server === 'apache' ? 'apache' : 'nginx';
    } catch {
      if (site?.id !== previewRef) return;
      // The drawer reports the real preview error; builtin defaults to nginx.
      previewServer = 'nginx';
    } finally {
      if (site?.id === previewRef) {
        previewLoading = false;
        previewOpen = true;
      }
    }
  }

  function toggleReveal(key: string): void {
    revealed = { ...revealed, [key]: !revealed[key] };
  }

  function sourceSlug(): string {
    return (
      (site?.name ?? 'site')
        .trim()
        .toLowerCase()
        .replace(/[^a-z0-9._-]+/g, '-')
        .replace(/^-+|-+$/g, '') || 'site'
    );
  }

  function defaultSourceDestination(mode: SourceMode): string {
    if (mode === 'wsl') return `/data/devstack/projects/${sourceSlug()}`;
    return '';
  }

  function beginSourceEdit(): void {
    if (!site) return;
    sourceMode = site.source_mode === 'windows' ? 'wsl' : 'windows';
    sourcePath = defaultSourceDestination(sourceMode);
    sourceError = null;
    sourceOpen = true;
  }

  function chooseSourceMode(mode: SourceMode): void {
    sourceMode = mode;
    sourcePath = defaultSourceDestination(mode);
  }

  function cancelSourceEdit(): void {
    if (sourceSaving) return;
    sourceOpen = false;
    sourceError = null;
  }

  async function changeSource(): Promise<void> {
    if (!site || sourceSaving) return;
    const destination = sourcePath.trim();
    if (!destination) {
      sourceError = { code: 'INVALID_REQUEST', message: 'Enter an absolute destination path.' };
      return;
    }
    if (sourceMode === 'wsl' && !destination.startsWith('/')) {
      sourceError = {
        code: 'INVALID_REQUEST',
        message: 'Use an absolute Linux path such as /data/devstack/projects/app.',
      };
      return;
    }
    if (sourceMode === 'windows' && !/^[A-Za-z]:[\\/]/.test(destination)) {
      sourceError = {
        code: 'INVALID_REQUEST',
        message: 'Use an absolute Windows path such as C:\\Users\\you\\Projects\\app.',
      };
      return;
    }
    sourceSaving = true;
    sourceError = null;
    try {
      const updated = await api.changeSiteSource(site.id, sourceMode, destination);
      site = updated;
      sourceOpen = false;
      addToast({
        kind: 'success',
        title: 'Source mode changed',
        message: `${updated.name} now uses ${sourceModeLabel(sourceMode)}. The original source was kept.`,
      });
      await load();
    } catch (caught) {
      sourceError = describeError(caught);
    } finally {
      sourceSaving = false;
    }
  }

  function envRows(env: Record<string, string> | undefined): EnvRow[] {
    const rows = Object.entries(env ?? {}).map(([key, value]) => ({ key, value }));
    return rows.length > 0 ? rows : [{ key: '', value: '' }];
  }

  function beginEdit(): void {
    if (!site) return;
    editDocumentRoot = site.document_root || (site.kind === 'static' ? '.' : '');
    editCommand = site.command;
    editNodeVersion = site.node_version || '';
    editFrontController = site.front_controller;
    editFrontControllerFile = site.front_controller_file || 'index.php';
    editFrontControllerMode = site.front_controller_mode || 'auto';
    editApacheAllowOverride = site.apache_allow_override || 'all';
    editSpaFallback = site.spa_fallback;
    editSpaFallbackFile = site.spa_fallback_file || 'index.html';
    editBuildEnabled = Boolean(site.build);
    editBuildNodeVersion = site.build?.node_version ?? nodeBuilds[0]?.version ?? '';
    editBuildCommand = site.build?.command ?? 'npm run build';
    editBuildOutput = site.build?.output ?? 'dist';
    editBuildEnvRows = envRows(site.build?.env);
    configError = null;
    configValidation = {};
    editOpen = true;
  }

  function cancelEdit(): void {
    if (savingConfig) return;
    editOpen = false;
    configError = null;
    configValidation = {};
  }

  function isRelativeFile(value: string): boolean {
    if (value.startsWith('/') || value.startsWith('\\')) return false;
    if (value.includes('\0')) return false;
    const parts = value.split(/[\\/]+/);
    return parts.every(
      (part) => part !== '' && part !== '.' && part !== '..' && !part.startsWith('.'),
    );
  }

  function validateConfig(): Record<string, string> {
    const next: Record<string, string> = {};
    const pin = editNodeVersion.trim();
    if (site?.kind === 'node' && !pin) {
      next.node_version = 'Choose an installed Node version.';
    } else if (pin && !nodeBuilds.some((build) => build.version === pin)) {
      next.node_version = `Node ${pin} is not installed. Install it first.`;
    }
    if (
      site?.kind === 'node' &&
      pin &&
      pin !== (site.node_version || '') &&
      !stopped
    ) {
      next.node_version = 'Stop the site before changing its Node version.';
    }
    if ((site?.kind === 'static' || site?.kind === 'php') && !editDocumentRoot.trim()) {
      next.document_root = 'Enter a document root. Use . to serve the project root.';
    }
    if (site?.kind === 'node' && !editCommand.trim()) {
      next.command = 'Enter the command that starts the dev server.';
    }
    if (site?.kind === 'php' && editFrontController) {
      const file = editFrontControllerFile.trim();
      if (!file) {
        next.front_controller_file = 'Enter the front controller file.';
      } else if (!isRelativeFile(file) || !file.toLowerCase().endsWith('.php')) {
        next.front_controller_file = 'Use a relative .php path inside the document root.';
      }
    }
    if (site?.kind === 'static' && editSpaFallback) {
      const file = editSpaFallbackFile.trim();
      if (!file) {
        next.spa_fallback_file = 'Enter the fallback file.';
      } else if (
        !isRelativeFile(file) ||
        file.toLowerCase().endsWith('.php')
      ) {
        next.spa_fallback_file = 'Use a relative non-PHP path inside the project.';
      }
    }
    if (site?.kind === 'static' && editBuildEnabled) {
      if (!editBuildNodeVersion) next.build_node_version = 'Choose an installed Node build.';
      if (!editBuildCommand.trim()) next.build_command = 'Enter the build command.';
      if (!editBuildOutput.trim()) {
        next.build_output = 'Enter the output directory produced by the build, for example dist.';
      } else if (!isRelativeFile(editBuildOutput.trim())) {
        next.build_output = 'Use a relative path inside the project.';
      }
      const seen = new Set<string>();
      editBuildEnvRows.forEach((row, index) => {
        const key = row.key.trim();
        if (!key) {
          if (row.value.trim()) next[`build_env_${index}`] = 'Add a name for this value.';
          return;
        }
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
          next[`build_env_${index}`] = 'Use letters, numbers and underscores.';
        } else if (seen.has(key)) {
          next[`build_env_${index}`] = 'This variable is already defined.';
        }
        seen.add(key);
      });
    }
    return next;
  }

  async function saveConfig(): Promise<void> {
    if (!site || savingConfig) return;
    const found = validateConfig();
    configValidation = found;
    if (Object.keys(found).length > 0) return;

    const body: UpdateSiteRequest = {};
    const pin = editNodeVersion.trim();
    if (pin !== (site.node_version || '')) {
      // Clearing is an explicit null, and only static/PHP sites may inherit.
      body.node_version = pin || (site.kind === 'node' ? site.node_version : null);
    }
    if (site.kind === 'static' || site.kind === 'php') {
      body.document_root = editDocumentRoot.trim();
    }
    if (site.kind === 'node') body.command = editCommand.trim();
    if (site.kind === 'php') {
      body.front_controller = editFrontController;
      body.front_controller_file = editFrontControllerFile.trim() || 'index.php';
      body.front_controller_mode = editFrontControllerMode;
      body.apache_allow_override = editApacheAllowOverride;
    }
    if (site.kind === 'static') {
      body.spa_fallback = editSpaFallback;
      body.spa_fallback_file = editSpaFallbackFile.trim() || 'index.html';
      if (editBuildEnabled) {
        const env: Record<string, string> = {};
        for (const row of editBuildEnvRows) {
          if (row.key.trim()) env[row.key.trim()] = row.value;
        }
        body.build = {
          node_version: editBuildNodeVersion,
          command: editBuildCommand.trim(),
          output: editBuildOutput.trim(),
          env,
        };
      }
    }

    savingConfig = true;
    configError = null;
    try {
      const saved = await api.updateSite(site.id, body);
      site = saved;
      editOpen = false;
      addToast({
        kind: 'success',
        title: 'Site configuration saved',
        message: 'Routes and generated configuration were refreshed from the daemon.',
      });
      await load();
    } catch (caught) {
      configError = describeError(caught);
    } finally {
      savingConfig = false;
    }
  }

  async function installNodeVersion(
    version: string,
    target: 'site' | 'build',
  ): Promise<void> {
    const preflight = await api.getNodeRelease(version);
    await api.installRuntime(preflight.entry.id);
    const builds = await api.listRuntimes();
    phpRuntimes = builds;
    const installed = preflight.entry.version;
    if (target === 'build') {
      editBuildNodeVersion = installed;
    } else {
      editNodeVersion = installed;
    }
    addToast({
      kind: 'success',
      title: `Node ${installed} installed`,
      message: 'The exact build is ready. Save the configuration to use it.',
    });
  }

  function addBuildEnvRow(): void {
    editBuildEnvRows = [...editBuildEnvRows, { key: '', value: '' }];
  }

  function removeBuildEnvRow(index: number): void {
    editBuildEnvRows = editBuildEnvRows.filter((_, position) => position !== index);
  }

  async function loadBuildLogs(siteRef = site?.id): Promise<void> {
    if (!siteRef) return;
    buildLogsLoading = true;
    buildLogsError = null;
    try {
      const logs = await api.getBuildLogs(siteRef, 200);
      if (site?.id !== siteRef) return;
      buildLogsContent = logs.content;
      buildLogsPath = logs.path ?? '';
    } catch (caught) {
      if (site?.id !== siteRef) return;
      buildLogsError = describeError(caught);
    } finally {
      if (site?.id === siteRef) buildLogsLoading = false;
    }
  }

  async function runBuild(): Promise<void> {
    if (!site || buildRunning) return;
    const target = site;
    buildRunning = true;
    buildError = null;
    buildResult = null;
    try {
      const result = await api.buildSite(target.id);
      if (site?.id !== target.id) return;
      buildResult = result;
      if (result.success) {
        addToast({
          kind: 'success',
          title: `${target.name} build completed`,
          message: `${formatDuration(result.duration_ms)}${result.output_path ? ` · ${result.output_path}` : ''}`,
        });
      } else {
        toastError(
          `${target.name} build failed`,
          result.error ?? 'The build command returned a failure status.',
        );
      }
      await loadBuildLogs(target.id);
      await load();
    } catch (caught) {
      if (site?.id !== target.id) return;
      buildError = describeError(caught);
    } finally {
      if (site?.id === target.id) buildRunning = false;
      else buildRunning = false;
    }
  }

  function formatDuration(ms: number): string {
    if (!Number.isFinite(ms) || ms < 0) return '—';
    if (ms < 1000) return `${ms} ms`;
    return `${(ms / 1000).toFixed(1)} s`;
  }

  $effect(() => {
    const target = site;
    if (!target || target.kind !== 'static' || !target.build) {
      buildLogRef = '';
      return;
    }
    if (buildLogRef === target.id) return;
    buildLogRef = target.id;
    void loadBuildLogs(target.id);
  });
</script>

<div class="page">
  <a class="back" href="#/sites">
    <ArrowLeft size={14} strokeWidth={2} aria-hidden="true" />
    All sites
  </a>

  {#if loading}
    <div class="head-skeleton">
      <Skeleton width="180px" height="26px" radius="6px" />
      <Skeleton width="240px" height="14px" radius="4px" />
      <Skeleton width="160px" height="12px" radius="4px" />
    </div>
    <div class="detail-grid">
      <div class="skeleton-logs">
        <Skeleton width="100%" height="470px" radius="12px" />
      </div>
      <div class="skeleton-side">
        <Skeleton width="100%" height="190px" radius="12px" />
        <Skeleton width="100%" height="160px" radius="12px" />
      </div>
    </div>
  {:else if notFound}
    <div class="state-card">
      <EmptyState
        icon={Globe}
        title="Site not found"
        description="It may have been removed, or the link is stale."
      >
        <Button variant="primary" onclick={() => push('/sites')}>Back to sites</Button>
      </EmptyState>
    </div>
  {:else if !site}
    <div class="state-card">
      <ErrorBanner
        title="Could not load site"
        code={error?.code}
        message={error?.message ?? 'The daemon returned no site data.'}
        onretry={() => load(true)}
      />
    </div>
  {:else}
    <header class="detail-head">
      <div class="head-main">
        <div class="title-row">
          <h2>{site.name}</h2>
          <StatusPill status={site.status} />
        </div>
        <div class="url-row">
          <span class="url mono">{url}</span>
          <IconButton
            label={copied ? 'URL copied' : 'Copy site URL'}
            icon={copied ? Check : Copy}
            size="sm"
            onclick={copyUrl}
          />
          <IconButton
            label="Open site in a new tab"
            icon={ExternalLink}
            size="sm"
            href={url}
            external
          />
        </div>
        <p class="path mono" title={site.host_path}>{site.host_path}</p>
      </div>
      <div class="head-actions">
        {#if active}
          <Button
            variant="secondary"
            onclick={() => act('stop')}
            loading={busyAction === 'stop'}
            disabled={Boolean(busyAction)}
          >
            {#if busyAction !== 'stop'}<Square size={13} strokeWidth={2} aria-hidden="true" />{/if}
            Stop
          </Button>
        {:else}
          <Button
            variant="primary"
            onclick={() => act('start')}
            loading={busyAction === 'start'}
            disabled={Boolean(busyAction)}
          >
            {#if busyAction !== 'start'}<Play size={13} strokeWidth={2} aria-hidden="true" />{/if}
            Start
          </Button>
        {/if}
        <Button
          variant="secondary"
          onclick={() => act('restart')}
          loading={busyAction === 'restart'}
          disabled={Boolean(busyAction)}
        >
          {#if busyAction !== 'restart'}<RotateCw size={13} strokeWidth={2} aria-hidden="true" />{/if}
          Restart
        </Button>
      </div>
    </header>

    {#if error}
      <div class="inline-error">
        <ErrorBanner
          title="Could not refresh site"
          code={error.code}
          message={error.message}
          onretry={() => load()}
          ondismiss={() => (error = null)}
        />
      </div>
    {/if}

    <div class="detail-grid">
      <div class="main-col">
        <LogsPanel {site} />
      </div>

      <aside class="side-col" aria-label="Site configuration">
        <Card title="Overview">
          <dl class="overview">
            <div class="row">
              <dt>Kind</dt>
              <dd>
                <Badge icon={site.kind === 'node' ? FileCode : Globe}>{site.kind}</Badge>
              </dd>
            </div>
            <div class="row">
              <dt>Runtime</dt>
              <dd class="mono">{runtimeLabel(site)}</dd>
            </div>
            {#if nodeToolchain}
              <div class="row">
                <dt>Node toolchain</dt>
                <dd class="mono">
                  Node {nodeToolchain.version}
                  <span class="toolchain-source">{nodeSourceLabel(nodeToolchain)}</span>
                </dd>
              </div>
              <div class="row">
                <dt>Shell node</dt>
                <dd class="mono">node -v uses v{nodeToolchain.version}</dd>
              </div>
              {#if !nodeToolchain.installed}
                <div class="row">
                  <dt>Node warning</dt>
                  <dd class="toolchain-warning">
                    This build is not installed. Install it before starting the site.
                  </dd>
                </div>
              {/if}
            {/if}
            {#if site.kind === 'php'}
              <div class="row">
                <dt>PHP build</dt>
                <dd class="mono break">{site.php_build || '—'}</dd>
              </div>
            {/if}
            <div class="row">
              <dt>Backend</dt>
              <dd class="capitalize">{site.backend}</dd>
            </div>
            <div class="row">
              <dt>HTTP port</dt>
              <dd class="mono">{site.http_port || '—'}</dd>
            </div>
            <div class="row">
              <dt>FastCGI port</dt>
              <dd class="mono">{site.fastcgi_port || '—'}</dd>
            </div>
            {#if site.kind === 'static'}
              <div class="row">
                <dt>Document root</dt>
                <dd class="mono break">{site.document_root || '.'}</dd>
              </div>
            {:else if site.kind === 'node'}
              <div class="row">
                <dt>Command</dt>
                <dd class="mono break">{site.command || '—'}</dd>
              </div>
            {/if}
            <div class="row">
              <dt>Host path</dt>
              <dd class="mono break" title={site.host_path}>{site.host_path}</dd>
            </div>
            {#if site.backend_path && site.backend_path !== site.host_path}
              <div class="row">
                <dt>Backend path</dt>
                <dd class="mono break" title={site.backend_path}>{site.backend_path}</dd>
              </div>
            {/if}
            {#if site.source_mode}
              <div class="row">
                <dt>Source mode</dt>
                <dd>
                  <Badge variant="neutral">{sourceModeLabel(site.source_mode)}</Badge>
                </dd>
              </div>
            {/if}
          </dl>
        </Card>

        <Card title="Certificate">
          {#snippet actions()}
            <Button
              size="sm"
              variant="ghost"
              onclick={previewVhost}
              loading={previewLoading}
              title="Preview the generated nginx or Apache vhost"
            >
              {#if !previewLoading}<Eye size={12} strokeWidth={2} aria-hidden="true" />{/if}
              Preview vhost
            </Button>
          {/snippet}
          {#if !site.tls}
            <div class="cert-off">
              <span class="cert-icon" aria-hidden="true"><LockOpen size={18} strokeWidth={1.8} /></span>
              <div>
                <p class="cert-title">TLS disabled</p>
                <p class="cert-copy">Served over plain HTTP. Enable TLS when adding a site to use HTTPS.</p>
              </div>
            </div>
          {:else}
            <div class="cert-head">
              <span class="cert-icon ok" aria-hidden="true"><ShieldCheck size={18} strokeWidth={1.8} /></span>
              <div class="cert-copy-wrap">
                <p class="cert-title">Local CA certificate</p>
                <p class="cert-copy">
                  {site.certificate ? `Expires ${formatDate(site.certificate.not_after)}` : 'Issued and managed by devstack'}
                </p>
              </div>
              {#if certDays !== null}
                <Badge variant={certDays <= 0 ? 'danger' : certDays < 30 ? 'warning' : 'accent'}>
                  {certDays <= 0 ? 'Expired' : `${certDays}d`}
                </Badge>
              {/if}
            </div>

            {#if site.certificate && certDays !== null && certDays < 30}
              <div class="cert-warning" role="status">
                <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
                <p>
                  {certDays <= 0
                    ? 'This certificate has expired. Browsers will block the site until it is renewed.'
                    : `Expires in ${certDays} days. Renew it before browsers start warning.`}
                </p>
              </div>
            {/if}

            <div class="cert-domains">
              <p class="label">Subject alternative names</p>
              <div class="chips">
                {#each domains as domain (domain)}
                  <span class="chip mono">{domain}</span>
                {/each}
              </div>
            </div>

            {#if site.certificate?.serial}
              <div class="row cert-row">
                <dt>Serial</dt>
                <dd class="mono">{checksumPrefix(site.certificate.serial, 18)}</dd>
              </div>
            {/if}
            <p class="cert-foot">
              {site.certificate
                ? `The certificate is managed locally; no external CA is involved.`
                : `This daemon does not expose certificate expiry; the domain still serves over HTTPS.`}
            </p>
          {/if}
        </Card>

        <Card title="Environment">
          {#snippet actions()}
            <span class="env-count">{envEntries.length}</span>
          {/snippet}
          {#if envEntries.length === 0}
            <div class="env-empty">
              <KeyRound size={16} strokeWidth={1.8} aria-hidden="true" />
              <p>No environment variables set for this site.</p>
            </div>
          {:else}
            <dl class="env">
              {#each envEntries as [key, value] (key)}
                {@const secret = isSecretKey(key)}
                <div class="env-row">
                  <dt class="mono" title={key}>{key}</dt>
                  <dd class="mono">
                    <span class="env-value" title={secret && !revealed[key] ? 'Redacted — use Reveal to show' : value}>
                      {secret && !revealed[key] ? '••••••••' : value}
                    </span>
                    {#if secret}
                      <button
                        type="button"
                        class="reveal"
                        aria-label={revealed[key] ? `Hide ${key}` : `Reveal ${key}`}
                        onclick={() => toggleReveal(key)}
                      >
                        {revealed[key] ? 'Hide' : 'Reveal'}
                      </button>
                    {/if}
                  </dd>
                </div>
              {/each}
            </dl>
          {/if}
        </Card>
      </aside>
    </div>

    <SiteDatabaseCard
      {site}
      links={siteLinks}
      {linksError}
      onRefresh={() => void load()}
      onRetryLinks={() => void load()}
      onRestart={() => void act('restart')}
    />

    <TerminalPanel {site} />

    <section class="config-section" aria-labelledby="config-heading">
      <div class="section-head">
        <div class="section-head-copy">
          <p class="section-eyebrow">Site</p>
          <h3 id="config-heading">Configuration</h3>
          <p class="section-sub">
            Document root, front controller, SPA fallback and build profile. Changes are
            validated by the daemon before routes are republished.
          </p>
        </div>
        {#if !editOpen}
          <Button size="sm" variant="secondary" onclick={beginEdit}>
            Edit configuration
          </Button>
        {:else}
          <Button size="sm" variant="ghost" onclick={cancelEdit} disabled={savingConfig}>
            Cancel
          </Button>
        {/if}
      </div>

      {#if configError}
        <div class="section-error">
          <ErrorBanner
            title="Could not save the configuration"
            code={configError.code}
            message={configError.message}
            ondismiss={() => (configError = null)}
          />
        </div>
      {/if}

      {#if editOpen}
        <div class="config-form">
          {#if Object.keys(configValidation).length > 0}
            <p class="config-validation" role="alert">
              Fix the highlighted fields before saving.
            </p>
          {/if}

          <div class="node-toolchain-field">
            <NodeVersionPicker
              id="edit-node-version"
              label={site.kind === 'node' ? 'Node version' : 'Node toolchain'}
              required={site.kind === 'node'}
              hint={site.kind === 'node'
                ? 'Node server sites store an exact installed version.'
                : 'Leave this on the global default unless this project needs a specific Node build.'}
              value={editNodeVersion}
              builds={nodeBuilds}
              inheritLabel={site.kind === 'node' ? undefined : nodeInheritLabel}
              error={configValidation.node_version}
              disabled={site.kind === 'node' && !stopped}
              onchange={(value) => {
                editNodeVersion = value;
                const next = { ...configValidation };
                delete next.node_version;
                configValidation = next;
              }}
              oninstall={(version) => installNodeVersion(version, 'site')}
            />
            {#if nodeDefaultMissing && site.kind !== 'node' && !editNodeVersion}
              <p class="profile-note node-default-warning" role="status">
                The configured global Node default (v{configuredNodeDefault}) is not installed.
                This site inherits no Node toolchain until the default is fixed.
              </p>
            {/if}
            {#if nodeVersionChanged}
              <div class="abi-warning" role="status">
                <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
                <div>
                  <p>
                    Native modules are compiled against the Node ABI. Reinstall project
                    dependencies after switching from v{site.node_version || 'the global default'}
                    to v{editNodeVersion || 'the global default'}.
                  </p>
                  <p>
                    Run <code>npm rebuild</code> or a fresh <code>npm install</code> in the
                    project folder{#if site.kind === 'node'}, then restart the site{/if}.
                  </p>
                </div>
              </div>
            {/if}
            {#if site.kind === 'node' && !stopped}
              <p class="profile-note">
                Stop the site before changing its Node version. The shell toolchain is pinned
                for this site.
              </p>
            {/if}
          </div>

          {#if site.kind === 'static' || site.kind === 'php'}
            <Field
              id="edit-document-root"
              label="Document root"
              required
              hint="Relative to the project folder. Use . to serve the project root."
              error={configValidation.document_root}
            >
              <input
                id="edit-document-root"
                class="input mono"
                type="text"
                bind:value={editDocumentRoot}
                autocomplete="off"
                spellcheck="false"
                aria-invalid={Boolean(configValidation.document_root)}
              />
            </Field>
          {:else if site.kind === 'node'}
            <Field
              id="edit-command"
              label="Start command"
              required
              error={configValidation.command}
            >
              <input
                id="edit-command"
                class="input mono"
                type="text"
                bind:value={editCommand}
                autocomplete="off"
                spellcheck="false"
                aria-invalid={Boolean(configValidation.command)}
              />
            </Field>
          {/if}

          {#if site.kind === 'php'}
            <div class="options-box">
              <Toggle
                label="Front controller"
                description="Route requests that do not match a file to the PHP entry point."
                bind:checked={editFrontController}
              />
            </div>
            {#if editFrontController}
              <div class="config-grid">
                <Field
                  id="edit-front-controller-file"
                  label="Front controller file"
                  required
                  hint="Relative to the document root, for example index.php (not public/index.php when the document root is public)."
                  error={configValidation.front_controller_file}
                >
                  <input
                    id="edit-front-controller-file"
                    class="input mono"
                    type="text"
                    bind:value={editFrontControllerFile}
                    autocomplete="off"
                    spellcheck="false"
                    aria-invalid={Boolean(configValidation.front_controller_file)}
                  />
                </Field>
                <Field id="edit-front-controller-mode" label="Front controller mode">
                  <div class="select-wrap">
                    <select
                      id="edit-front-controller-mode"
                      class="input mono"
                      bind:value={editFrontControllerMode}
                    >
                      <option value="auto">Auto</option>
                      <option value="generated">Generated config</option>
                      <option value="htaccess">Project .htaccess</option>
                    </select>
                    <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
                  </div>
                </Field>
              </div>
            {/if}
            <Field
              id="edit-apache-allow-override"
              label="Apache AllowOverride"
              hint="nginx and builtin serving ignore .htaccess; Apache honours this policy."
            >
              <div class="select-wrap">
                <select
                  id="edit-apache-allow-override"
                  class="input mono"
                  bind:value={editApacheAllowOverride}
                >
                  <option value="all">All</option>
                  <option value="fileinfo">FileInfo</option>
                  <option value="none">None</option>
                </select>
                <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
              </div>
            </Field>
          {/if}

          {#if site.kind === 'static'}
            <div class="options-box">
              <Toggle
                label="SPA fallback"
                description="Route unknown paths to the fallback file for client-side routing."
                bind:checked={editSpaFallback}
              />
            </div>
            {#if editSpaFallback}
              <Field
                id="edit-spa-fallback-file"
                label="SPA fallback file"
                required
                error={configValidation.spa_fallback_file}
              >
                <input
                  id="edit-spa-fallback-file"
                  class="input mono"
                  type="text"
                  bind:value={editSpaFallbackFile}
                  autocomplete="off"
                  spellcheck="false"
                  aria-invalid={Boolean(configValidation.spa_fallback_file)}
                />
              </Field>
            {/if}

            {#if site.build}
              <p class="profile-note">
                This site has a build profile. The fields below update it; removing a build
                profile is not supported by the daemon yet.
              </p>
            {:else}
              <div class="options-box">
                <Toggle
                  label="Add build profile"
                  description="Adds a Node build command used by the dashboard Build action and the CLI. A profile cannot be removed from the dashboard yet."
                  bind:checked={editBuildEnabled}
                />
              </div>
            {/if}
            {#if site.build || editBuildEnabled}
              <div class="config-grid">
                <NodeVersionPicker
                  id="edit-build-node"
                  label="Build Node version"
                  required
                  hint="The build runs with this exact installed version."
                  value={editBuildNodeVersion}
                  builds={nodeBuilds}
                  error={configValidation.build_node_version}
                  onchange={(value) => {
                    editBuildNodeVersion = value;
                    const next = { ...configValidation };
                    delete next.build_node_version;
                    configValidation = next;
                  }}
                  oninstall={(version) => installNodeVersion(version, 'build')}
                />
                <Field
                  id="edit-build-output"
                  label="Output directory"
                  required
                  hint="Required. Relative to the project folder, for example dist."
                  error={configValidation.build_output}
                >
                  <input
                    id="edit-build-output"
                    class="input mono"
                    type="text"
                    placeholder="dist"
                    bind:value={editBuildOutput}
                    autocomplete="off"
                    spellcheck="false"
                    aria-invalid={Boolean(configValidation.build_output)}
                  />
                </Field>
              </div>
              <Field
                id="edit-build-command"
                label="Build command"
                required
                error={configValidation.build_command}
              >
                <input
                  id="edit-build-command"
                  class="input mono"
                  type="text"
                  placeholder="npm run build"
                  bind:value={editBuildCommand}
                  autocomplete="off"
                  spellcheck="false"
                  aria-invalid={Boolean(configValidation.build_command)}
                />
              </Field>

              <div class="build-env">
                <p class="build-env-label">Build environment</p>
                <div class="env-list">
                  {#each editBuildEnvRows as row, index (index)}
                    <div class="env-row">
                      <div class="env-cell">
                        <label class="sr-only" for={`edit-build-env-key-${index}`}>
                          Build variable {index + 1} name
                        </label>
                        <input
                          id={`edit-build-env-key-${index}`}
                          class="input mono"
                          type="text"
                          placeholder="NODE_ENV"
                          bind:value={row.key}
                          autocomplete="off"
                          spellcheck="false"
                          aria-invalid={Boolean(configValidation[`build_env_${index}`])}
                        />
                        {#if configValidation[`build_env_${index}`]}
                          <p class="env-error">{configValidation[`build_env_${index}`]}</p>
                        {/if}
                      </div>
                      <div class="env-cell">
                        <label class="sr-only" for={`edit-build-env-value-${index}`}>
                          Build variable {index + 1} value
                        </label>
                        <input
                          id={`edit-build-env-value-${index}`}
                          class="input mono"
                          type="text"
                          placeholder="production"
                          bind:value={row.value}
                          autocomplete="off"
                          spellcheck="false"
                        />
                      </div>
                      <IconButton
                        label={`Remove build environment variable ${index + 1}`}
                        icon={Trash2}
                        size="sm"
                        onclick={() => removeBuildEnvRow(index)}
                      />
                    </div>
                  {/each}
                </div>
                <div>
                  <Button size="sm" variant="ghost" onclick={addBuildEnvRow}>
                    Add build variable
                  </Button>
                </div>
              </div>
            {/if}
          {/if}

          <div class="config-actions">
            <Button variant="ghost" onclick={cancelEdit} disabled={savingConfig}>Cancel</Button>
            <Button
              variant="primary"
              loading={savingConfig}
              disabled={savingConfig}
              onclick={saveConfig}
            >
              Save configuration
            </Button>
          </div>
        </div>
      {:else}
        <dl class="config-summary">
          <div>
            <dt>Node toolchain</dt>
            <dd class="mono">
              {#if nodeToolchain}
                Node {nodeToolchain.version} · {nodeToolchain.source === 'global'
                  ? 'global default'
                  : 'site pin'}
              {:else}
                Inherit global default
              {/if}
            </dd>
          </div>
          {#if site.kind === 'static' || site.kind === 'php'}
            <div>
              <dt>Document root</dt>
              <dd class="mono">{site.document_root || '.'}</dd>
            </div>
          {:else}
            <div>
              <dt>Start command</dt>
              <dd class="mono">{site.command || '—'}</dd>
            </div>
          {/if}
          {#if site.kind === 'php'}
            <div>
              <dt>Front controller</dt>
              <dd class="mono">
                {site.front_controller
                  ? `${site.front_controller_file || 'index.php'} · ${site.front_controller_mode || 'auto'}`
                  : 'Off'}
              </dd>
            </div>
            <div>
              <dt>Apache AllowOverride</dt>
              <dd class="mono">{site.apache_allow_override || 'all'}</dd>
            </div>
          {/if}
          {#if site.kind === 'static'}
            <div>
              <dt>SPA fallback</dt>
              <dd class="mono">
                {site.spa_fallback ? site.spa_fallback_file || 'index.html' : 'Off'}
              </dd>
            </div>
            <div>
              <dt>Build profile</dt>
              <dd class="mono">
                {site.build
                  ? `Node ${site.build.node_version || '—'} · ${site.build.command || 'no command'}`
                  : 'Not configured'}
              </dd>
            </div>
          {/if}
        </dl>
      {/if}
    </section>

    {#if canSwitchSource}
      <section class="config-section" aria-labelledby="source-heading">
        <div class="section-head">
          <div class="section-head-copy">
            <p class="section-eyebrow">Source</p>
            <h3 id="source-heading">Project source</h3>
            <p class="section-sub">
              Copy a stopped site's files into the other source mode. The agent verifies the copy,
              republishes the route and keeps the original source.
            </p>
          </div>
          {#if !sourceOpen}
            <Button size="sm" variant="secondary" onclick={beginSourceEdit} disabled={!stopped}>
              Change source
            </Button>
          {:else}
            <Button size="sm" variant="ghost" onclick={cancelSourceEdit} disabled={sourceSaving}>
              Cancel
            </Button>
          {/if}
        </div>

        {#if sourceError}
          <div class="section-error">
            <ErrorBanner
              title="Could not change the source mode"
              code={sourceError.code}
              message={sourceError.message}
              ondismiss={() => (sourceError = null)}
            />
          </div>
        {/if}

        {#if !stopped && !sourceOpen}
          <p class="section-sub">Stop the site before changing source modes.</p>
        {/if}

        {#if sourceOpen}
          <div class="config-form">
            <ProjectSourceMode
              mode={sourceMode}
              kind={site.kind}
              isWindows={true}
              onmodechange={chooseSourceMode}
            />
            <Field
              id="source-destination"
              label="Destination path"
              required
              hint={sourceMode === 'wsl'
                ? 'Absolute path inside the managed WSL distro. The folder must not exist yet.'
                : 'Absolute Windows path. The folder must not exist yet.'}
            >
              <input
                id="source-destination"
                class="input mono"
                type="text"
                bind:value={sourcePath}
                placeholder={sourceMode === 'wsl'
                  ? '/data/devstack/projects/app'
                  : 'C:\\Users\\you\\Projects\\app'}
                autocomplete="off"
                spellcheck="false"
              />
            </Field>
            <div class="config-actions">
              <Button variant="ghost" onclick={cancelSourceEdit} disabled={sourceSaving}>
                Cancel
              </Button>
              <Button
                variant="primary"
                loading={sourceSaving}
                disabled={sourceSaving || !sourcePath.trim()}
                onclick={changeSource}
              >
                Copy and switch
              </Button>
            </div>
          </div>
        {/if}
      </section>
    {/if}

    {#if site.kind === 'static'}
      <section class="build-section" aria-labelledby="build-heading">
        <div class="section-head">
          <div class="section-head-copy">
            <p class="section-eyebrow">Build</p>
            <h3 id="build-heading">Static build</h3>
            <p class="section-sub">
              Runs the configured command in the site's build environment (the managed WSL
              agent on Windows), then refreshes the build log. A failed build can restore the
              previous output snapshot.
            </p>
          </div>
          {#if site.build}
            <Button
              size="sm"
              variant="primary"
              loading={buildRunning}
              disabled={buildRunning}
              onclick={runBuild}
            >
              {#if !buildRunning}<Play size={13} strokeWidth={2} aria-hidden="true" />{/if}
              {buildRunning ? 'Building…' : 'Run build'}
            </Button>
          {:else}
            <Badge variant="warning">No build profile</Badge>
          {/if}
        </div>

        {#if site.build}
          <dl class="build-meta">
            <div>
              <dt>Node version</dt>
              <dd class="mono">{site.build.node_version || '—'}</dd>
            </div>
            <div>
              <dt>Command</dt>
              <dd class="mono">{site.build.command || '—'}</dd>
            </div>
            <div>
              <dt>Output</dt>
              <dd class="mono">{site.build.output || 'in place'}</dd>
            </div>
            <div>
              <dt>Environment</dt>
              <dd class="mono">{Object.keys(site.build.env ?? {}).length} variables</dd>
            </div>
          </dl>

          {#if buildError}
            <div class="section-error">
              <ErrorBanner
                title="Could not run the build"
                code={buildError.code}
                message={buildError.message}
                ondismiss={() => (buildError = null)}
              />
            </div>
          {/if}

          {#if buildResult}
            <div class="build-result" class:failed={!buildResult.success} role="status">
              <div class="build-result-head">
                <Badge variant={buildResult.success ? 'accent' : 'danger'}>
                  {buildResult.success ? 'Build succeeded' : 'Build failed'}
                </Badge>
                <span class="build-duration mono">{formatDuration(buildResult.duration_ms)}</span>
                {#if buildResult.restored}
                  <Badge variant="info">Previous output restored</Badge>
                {/if}
                {#if buildResult.snapshot_skipped}
                  <Badge variant="warning">Snapshot skipped</Badge>
                {/if}
              </div>
              <p class="build-command mono">{buildResult.command}</p>
              {#if buildResult.output_path}
                <p class="build-copy">
                  Output <span class="mono">{buildResult.output_path}</span>
                </p>
              {/if}
              {#if buildResult.error}
                <p class="build-error-text" role="alert">{buildResult.error}</p>
              {/if}
              {#if buildResult.restore_note}
                <p class="build-copy">{buildResult.restore_note}</p>
              {/if}
            </div>
          {/if}

          <div class="build-log">
            <div class="build-log-head">
              <div>
                <p class="build-log-title">Build log tail</p>
                {#if buildResult?.log_path || buildLogsPath}
                  <p class="build-log-path mono">{buildResult?.log_path ?? buildLogsPath}</p>
                {:else}
                  <p class="build-log-path">No build log file yet.</p>
                {/if}
              </div>
              <Button
                size="sm"
                variant="ghost"
                loading={buildLogsLoading}
                onclick={() => loadBuildLogs()}
              >
                {#if !buildLogsLoading}<RotateCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
                Refresh log
              </Button>
            </div>
            {#if buildLogsError}
              <ErrorBanner
                title="Could not load the build log"
                code={buildLogsError.code}
                message={buildLogsError.message}
                onretry={() => loadBuildLogs()}
              />
            {/if}
            <pre class="build-log-content" aria-label="Build log tail">{#if buildLogsLoading && !buildLogsContent}Loading build log…{:else if buildLogsContent}{buildLogsContent}{:else}No build log has been written yet.{/if}</pre>
          </div>
        {:else}
          <div class="build-empty">
            <p>No build profile is configured for this site.</p>
            <p class="build-copy">
              Add a Node version, command and output directory from Configuration, then run the
              build here or with <span class="mono">devstack site build {site.name}</span>.
            </p>
          </div>
        {/if}
      </section>
    {/if}

    {#if site.kind === 'php'}
      <section class="php-section" aria-labelledby="php-heading">
        <div class="php-head">
          <div class="php-head-copy">
            <p class="php-eyebrow">PHP</p>
            <h3 id="php-heading">Runtime configuration</h3>
            <p class="php-sub">
              <span>PHP {site.php_version || phpInfo?.build.version}</span>
              <span class="mono">{site.php_build || phpInfo?.build.id || 'No build pinned'}</span>
              <span class="mono">
                FPM
                {site.fastcgi_port ? `127.0.0.1:${site.fastcgi_port}` : 'not allocated while stopped'}
              </span>
            </p>
          </div>
          <Button
            size="sm"
            variant="secondary"
            onclick={openSwitch}
            disabled={switchCandidates.length === 0 || phpApplying}
            title={switchCandidates.length === 0 ? 'No other PHP build is installed' : 'Switch PHP version or build'}
          >
            <RotateCw size={12} strokeWidth={2} aria-hidden="true" />
            Switch build
          </Button>
        </div>

        {#if stopped}
          <p class="php-note" role="status">
            This site is stopped. Configuration changes are validated now and apply on next start.
          </p>
        {/if}

        {#if phpSwitchError}
          <div class="php-error" role="alert">
            <div class="php-error-title">
              <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
              <span>PHP switch failed. The previous build was kept; validate-then-swap rolled back.</span>
            </div>
            <p class="php-error-meta">
              <span class="mono">{phpSwitchError.code}</span>
              <span>{phpSwitchError.message}</span>
            </p>
            {#if phpSwitchError.details?.message}
              <p class="php-error-line">{phpSwitchError.details.message}</p>
            {/if}
            {#if phpSwitchError.details?.line !== undefined && phpSwitchError.details.line > 0}
              <p class="php-error-line">Line {phpSwitchError.details.line}</p>
            {/if}
            {#if phpSwitchError.details?.output}
              <pre class="php-output" aria-label="PHP switch validation output">{phpSwitchError.details.output}</pre>
            {/if}
          </div>
        {/if}

        {#if phpApplyError && !phpSwitchError}
          <div class="php-error" role="alert">
            <div class="php-error-title">
              <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
              <span>PHP extension change failed. The previous configuration was kept.</span>
            </div>
            <p class="php-error-meta">
              <span class="mono">{phpApplyError.code}</span>
              <span>{phpApplyError.message}</span>
            </p>
            {#if phpApplyError.details?.message}
              <p class="php-error-line">{phpApplyError.details.message}</p>
            {/if}
            {#if phpApplyError.details?.line !== undefined && phpApplyError.details.line > 0}
              <p class="php-error-line">Line {phpApplyError.details.line}</p>
            {/if}
            {#if phpApplyError.details?.output}
              <pre class="php-output" aria-label="PHP validation output">{phpApplyError.details.output}</pre>
            {/if}
          </div>
        {/if}

        {#if phpLoading && !phpInfo}
          <div class="php-loading" aria-busy="true" aria-label="Loading PHP configuration">
            <Skeleton width="160px" height="12px" />
            <Skeleton width="100%" height="120px" radius="8px" />
            <Skeleton width="72%" height="10px" />
          </div>
        {:else if phpError}
          <ErrorBanner
            title="Could not load PHP configuration"
            code={phpError.code}
            message={phpError.message}
            onretry={() => void load(true)}
          />
        {:else if phpInfo}
          <div class="php-grid">
            <div class="php-editor">
              <PHPIniEditor
                ini={phpInfo.ini}
                raw={phpInfo.ini_raw}
                generated={phpInfo.generated_ini}
                siteRef={site?.id ?? ''}
                onApply={applyPHP}
                disabled={phpApplying}
              />
            </div>
            <div class="php-side">
              <h4>Extensions</h4>
              <p class="php-side-copy">
                Inherit follows the PHP {site.php_version} defaults. On and Off are site-specific
                overrides; required dependencies are added automatically.
              </p>
              <ExtensionToggles
                extensions={phpInfo.available_extensions}
                builtins={phpInfo.builtins}
                value={site.php_extensions ?? {}}
                defaults={phpInfo.defaults.extensions}
                onchange={onExtensionsChange}
                disabled={phpApplying}
              />
              <dl class="php-meta">
                <div>
                  <dt>Config hash</dt>
                  <dd class="mono">{phpInfo.config_hash ? `${phpInfo.config_hash.slice(0, 12)}…` : '—'}</dd>
                </div>
                <div>
                  <dt>Resolved</dt>
                  <dd>{phpInfo.extensions.length} extension{phpInfo.extensions.length === 1 ? '' : 's'}</dd>
                </div>
                <div>
                  <dt>Defaults line</dt>
                  <dd class="mono">PHP {phpInfo.defaults.version}</dd>
                </div>
              </dl>
            </div>
          </div>
        {/if}
      </section>
    {/if}

    <section class="danger" aria-labelledby="danger-title">
      <div class="danger-copy">
        <p class="danger-eyebrow">Danger zone</p>
        <h3 id="danger-title">Remove this site</h3>
        <p>
          Stops the site and removes its domain, certificate and generated configuration. Project
          files remain at <span class="mono">{site.host_path}</span>.
        </p>
      </div>
      <Button variant="danger-ghost" onclick={() => (deleteOpen = true)}>
        <Trash2 size={14} strokeWidth={2} aria-hidden="true" />
        Remove site
      </Button>
    </section>
  {/if}
</div>

<ConfirmDialog
  open={deleteOpen}
  title={site ? `Remove ${site.name}?` : 'Remove site?'}
  description={site
    ? `This stops the site and removes its ${site.domain} domain, certificate, generated configs, and database links. Files in ${site.host_path} are never deleted. Databases, users, grants, passwords, backups, and data remain.`
    : ''}
  confirmLabel="Remove site"
  requireText={site?.name}
  danger
  busy={deleting}
  onconfirm={confirmDelete}
  oncancel={() => {
    if (!deleting) deleteOpen = false;
  }}
/>

<ConfirmDialog
  open={switchOpen}
  title={site ? `Switch ${site.name} to another PHP build?` : 'Switch PHP build?'}
  description="devstack writes and validates the new php.ini and FPM configuration, stages a new master, then swaps traffic. If validation or readiness fails, the previous build stays active and its master is kept — this is the rollback path."
  confirmLabel="Switch build"
  busy={phpApplying}
  onconfirm={switchPHP}
  oncancel={() => {
    if (!phpApplying) switchOpen = false;
  }}
>
  {#snippet details()}
    {#if switchCandidates.length === 0}
      <p class="switch-empty">No other PHP build is installed. Install one from Runtimes first.</p>
    {:else}
      <Field id="switch-php-build" label="Target build">
        <div class="select-wrap">
          <select id="switch-php-build" class="input mono" bind:value={switchBuild}>
            {#each switchCandidates as runtime (runtime.id)}
              <option value={runtime.id}>
                PHP {runtime.version} (r{runtime.revision}) · line {runtime.line}
              </option>
            {/each}
          </select>
          <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
        </div>
      </Field>
    {/if}
  {/snippet}
</ConfirmDialog>

{#if site}
  <VhostPreviewDrawer
    open={previewOpen}
    site={site.name}
    initialServer={previewServer}
    returnFocus={previewReturnFocus}
    onclose={() => (previewOpen = false)}
  />
{/if}

<style>
  .page {
    padding-top: 18px;
  }

  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 12px;
    color: var(--muted);
    font-size: 12px;
    font-weight: 500;
  }

  .back:hover {
    color: var(--fg);
  }

  .detail-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    padding-bottom: 16px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }

  .head-main {
    min-width: 0;
  }

  .title-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 9px;
  }

  h2 {
    font-size: 23px;
    font-weight: 600;
    letter-spacing: -0.025em;
    line-height: 1.2;
  }

  .url-row {
    display: flex;
    align-items: center;
    gap: 3px;
    margin-top: 5px;
  }

  .url {
    color: var(--muted);
    font-size: 12.5px;
    overflow-wrap: anywhere;
  }

  .path {
    margin-top: 2px;
    color: var(--muted-2);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .head-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
    padding-top: 3px;
  }

  .detail-grid {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(286px, 1fr);
    align-items: stretch;
    gap: 14px;
  }

  .main-col,
  .side-col {
    min-width: 0;
  }

  .side-col {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .overview {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    min-height: 28px;
    padding: 4px 0;
    border-top: 1px solid var(--border);
  }

  .row:first-child {
    border-top: 0;
    padding-top: 0;
  }

  .row:last-child {
    padding-bottom: 0;
  }

  dt {
    flex: none;
    color: var(--muted);
    font-size: 11px;
  }

  dd {
    margin: 0;
    min-width: 0;
    color: var(--fg);
    font-size: 11.5px;
    text-align: right;
  }

  .capitalize {
    text-transform: capitalize;
  }

  .break {
    overflow-wrap: anywhere;
    font-size: 10.5px;
    line-height: 1.5;
  }

  .cert-head {
    display: flex;
    align-items: flex-start;
    gap: 9px;
  }

  .cert-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 32px;
    height: 32px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .cert-icon.ok {
    color: var(--success-fg);
    border-color: var(--accent-border);
    background: var(--accent-soft);
  }

  .cert-copy-wrap {
    flex: 1;
    min-width: 0;
  }

  .cert-title {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .cert-copy {
    margin-top: 1px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
  }

  .cert-warning {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 10px;
    padding: 7px 8px;
    border-radius: 6px;
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .cert-warning :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .cert-warning p {
    font-size: 10.5px;
    line-height: 1.45;
    color: var(--warning-fg);
  }

  .cert-domains {
    margin-top: 12px;
  }

  .label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 5px;
  }

  .chip {
    padding: 2px 5px;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
  }

  .cert-row {
    margin-top: 10px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
  }

  .cert-foot {
    margin-top: 10px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.5;
  }

  .cert-off {
    display: flex;
    align-items: flex-start;
    gap: 9px;
  }

  .env {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .env-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 5px 0;
    border-top: 1px solid var(--border);
  }

  .env-row:first-child {
    border-top: 0;
    padding-top: 0;
  }

  .env-row dt {
    color: var(--muted);
    font-size: 9.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 48%;
  }

  .env-row dd {
    display: flex;
    align-items: baseline;
    gap: 5px;
    font-size: 10px;
    min-width: 0;
  }

  .env-value {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 170px;
  }

  .reveal {
    padding: 1px 3px;
    border: 0;
    border-radius: 3px;
    background: transparent;
    color: var(--muted-2);
    font-family: var(--font-sans);
    font-size: 9px;
    white-space: nowrap;
  }

  .reveal:hover {
    color: var(--fg);
    background: var(--surface-2);
  }

  .env-empty {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .env-empty :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--muted-2);
  }

  .env-count {
    display: inline-grid;
    place-items: center;
    min-width: 20px;
    height: 20px;
    padding: 0 5px;
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 9.5px;
  }

  .danger {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    margin-top: 16px;
    padding: 14px 16px;
    border: 1px solid color-mix(in srgb, var(--danger) 32%, var(--border));
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .danger-copy {
    min-width: 0;
  }

  .danger-eyebrow {
    color: var(--danger-fg);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .danger h3 {
    margin-top: 2px;
    font-size: 13px;
    font-weight: 600;
  }

  .danger p:not(.danger-eyebrow) {
    margin-top: 3px;
    max-width: 680px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .danger .mono {
    color: var(--fg);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .config-section,
  .build-section {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 16px;
    padding: 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .section-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
    padding-bottom: 11px;
    border-bottom: 1px solid var(--border);
  }

  .section-head-copy {
    min-width: 0;
  }

  .section-eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .section-head h3 {
    margin-top: 1px;
    font-size: 14px;
    font-weight: 600;
  }

  .section-sub {
    max-width: 760px;
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .section-error {
    min-width: 0;
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .config-validation {
    padding: 7px 9px;
    border: 1px solid color-mix(in srgb, var(--danger) 38%, var(--border));
    border-radius: var(--radius);
    background: var(--danger-soft);
    color: var(--danger-fg);
    font-size: 10.5px;
  }

  .config-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }

  .config-actions {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    padding-top: 11px;
    border-top: 1px solid var(--border);
  }

  .config-summary {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 1px;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--border);
    overflow: hidden;
  }

  .config-summary > div {
    min-width: 0;
    padding: 9px 11px;
    background: var(--surface-2);
  }

  .config-summary dt {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 500;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }

  .config-summary dd {
    margin: 2px 0 0;
    color: var(--fg);
    font-size: 10px;
    overflow-wrap: anywhere;
  }

  .options-box {
    padding: 2px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--input-bg);
  }

  .profile-note {
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .node-toolchain-field {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .node-default-warning {
    border-color: color-mix(in srgb, var(--warning) 36%, var(--border));
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .abi-warning {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .abi-warning :global(svg) {
    flex: none;
    margin-top: 2px;
    color: var(--warning-fg, var(--fg));
  }

  .abi-warning p {
    margin: 0;
    color: var(--fg);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .abi-warning p + p {
    margin-top: 2px;
    color: var(--muted);
  }

  .abi-warning code {
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .toolchain-source {
    display: block;
    margin-top: 1px;
    color: var(--muted);
    font-family: var(--font-sans);
    font-size: 10px;
    font-weight: 400;
  }

  .toolchain-warning {
    color: var(--danger-fg);
    font-size: 10px;
    line-height: 1.45;
  }

  .build-env {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .build-env-label {
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .build-meta {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--border);
    overflow: hidden;
  }

  .build-meta > div {
    min-width: 0;
    padding: 8px 10px;
    background: var(--surface-2);
  }

  .build-meta dt {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 500;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }

  .build-meta dd {
    margin: 2px 0 0;
    color: var(--fg);
    font-size: 10px;
    overflow-wrap: anywhere;
  }

  .build-result {
    padding: 10px 11px;
    border: 1px solid color-mix(in srgb, var(--accent) 32%, var(--border));
    border-left: 3px solid var(--accent);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .build-result.failed {
    border-color: color-mix(in srgb, var(--danger) 38%, var(--border));
    border-left-color: var(--danger);
    background: var(--danger-soft);
  }

  .build-result-head {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
  }

  .build-duration {
    color: var(--muted);
    font-size: 10.5px;
  }

  .build-command {
    margin-top: 6px;
    color: var(--fg);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .build-copy {
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .build-copy .mono {
    color: var(--fg);
  }

  .build-error-text {
    margin-top: 5px;
    color: var(--danger-fg);
    font-size: 10.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .build-log {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .build-log-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }

  .build-log-title {
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 600;
  }

  .build-log-path {
    margin-top: 1px;
    color: var(--muted-2);
    font-size: 9px;
    overflow-wrap: anywhere;
  }

  .build-log-content {
    min-height: 150px;
    max-height: 320px;
    margin: 0;
    padding: 10px;
    overflow: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 9.5px;
    line-height: 1.55;
    white-space: pre-wrap;
  }

  .build-empty {
    padding: 11px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .build-empty > p:first-child {
    color: var(--warning-fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .build-empty .build-copy {
    color: var(--muted);
  }

  .build-env .env-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .build-env .env-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 28px;
    gap: 6px;
    align-items: start;
  }

  .build-env .env-cell {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .build-env .env-error {
    color: var(--danger-fg);
    font-size: 10px;
  }

  .php-section {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 16px;
    padding: 16px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .php-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 14px;
    padding-bottom: 11px;
    border-bottom: 1px solid var(--border);
  }

  .php-head-copy {
    min-width: 0;
  }

  .php-eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .php-head h3 {
    margin-top: 1px;
    font-size: 14px;
    font-weight: 600;
  }

  .php-sub {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .php-note {
    padding: 7px 9px;
    border: 1px solid color-mix(in srgb, var(--info) 28%, var(--border));
    border-radius: var(--radius);
    background: var(--info-soft);
    color: var(--info-fg);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-error {
    padding: 9px 11px;
    border: 1px solid color-mix(in srgb, var(--danger) 42%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .php-error-title {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 600;
    line-height: 1.45;
  }

  .php-error-title :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--danger-fg);
  }

  .php-error-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 8px;
    margin-top: 4px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-error-meta .mono {
    color: var(--danger-fg);
  }

  .php-error-line {
    margin-top: 3px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .php-output {
    margin: 6px 0 0;
    padding: 9px;
    max-height: 160px;
    overflow: auto;
    border: 1px solid color-mix(in srgb, var(--danger) 22%, var(--border));
    border-radius: var(--radius);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 9.5px;
    line-height: 1.55;
    white-space: pre-wrap;
  }

  .php-loading {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding: 4px 0;
  }

  .php-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(280px, 1fr);
    align-items: start;
    gap: 18px;
  }

  .php-editor,
  .php-side {
    min-width: 0;
  }

  .php-side h4 {
    font-size: 12.5px;
    font-weight: 600;
  }

  .php-side-copy {
    margin: 2px 0 9px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .php-meta {
    display: flex;
    flex-direction: column;
    gap: 0;
    margin: 12px 0 0;
    border-top: 1px solid var(--border);
  }

  .php-meta > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 5px 0;
    border-top: 1px solid var(--border);
  }

  .php-meta > div:first-child {
    border-top: 0;
  }

  .php-meta dt {
    color: var(--muted);
    font-size: 9.5px;
  }

  .php-meta dd {
    margin: 0;
    color: var(--fg);
    font-size: 9.5px;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .select-wrap {
    position: relative;
  }

  .select-wrap :global(select) {
    appearance: none;
    width: 100%;
    padding-right: 30px;
  }

  .select-wrap :global(svg) {
    position: absolute;
    top: 50%;
    right: 9px;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .switch-empty {
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 35%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11px;
    line-height: 1.5;
  }

  .state-card {
    max-width: 640px;
    margin: 40px auto;
    padding: 0 16px;
  }

  .inline-error {
    margin-bottom: 12px;
  }

  .head-skeleton {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-bottom: 18px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }

  .skeleton-logs {
    min-width: 0;
  }

  .skeleton-side {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  @media (max-width: 900px) {
    .detail-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .php-grid {
      grid-template-columns: minmax(0, 1fr);
    }

    .config-grid,
    .config-summary,
    .build-meta {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .side-col {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      align-items: start;
    }

    .side-col > :nth-child(3) {
      grid-column: 1 / -1;
    }
  }

  @media (max-width: 700px) {
    .detail-head {
      flex-direction: column;
      gap: 12px;
    }

    .head-actions {
      width: 100%;
    }

    .head-actions :global(.btn) {
      flex: 1;
    }

    .side-col {
      display: flex;
      flex-direction: column;
      align-items: stretch;
    }

    .danger {
      flex-direction: column;
      align-items: flex-start;
    }

    .danger :global(.btn) {
      width: 100%;
    }

    .config-grid,
    .config-summary,
    .build-meta {
      grid-template-columns: minmax(0, 1fr);
    }

    .section-head {
      flex-direction: column;
    }

    .build-log-head {
      flex-direction: column;
    }

    h2 {
      font-size: 21px;
    }
  }
</style>
