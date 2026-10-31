<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { push } from 'svelte-spa-router';
  import {
    ArrowLeft,
    ArrowRight,
    Check,
    ChevronDown,
    Code,
    FileCode,
    FolderOpen,
    Globe,
    Lock,
    LockOpen,
    Plus,
    ShieldCheck,
    Trash2,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import * as db from '../lib/databaseApi';
  import { copyText } from '../lib/clipboard';
  import { describeError } from '../lib/errors';
  import { isAbsolutePath } from '../lib/format';
  import { addToast } from '../lib/stores';
  import type {
    ApacheAllowOverride,
    BuildProfile,
    CreateSiteRequest,
    FrontControllerMode,
    NodeDefaultStatus,
    RuntimeBuild,
    Site,
    SourceMode,
    Settings,
  } from '../lib/types';
  import Button from '../lib/components/Button.svelte';
  import DatabaseConnectionCard from '../lib/components/DatabaseConnectionCard.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Field from '../lib/components/Field.svelte';
  import IconButton from '../lib/components/IconButton.svelte';
  import Modal from '../lib/components/Modal.svelte';
  import NodeVersionPicker from '../lib/components/NodeVersionPicker.svelte';
  import PathBrowser from '../lib/components/PathBrowser.svelte';
  import ProjectSourceMode from '../lib/components/ProjectSourceMode.svelte';
  import SecretField from '../lib/components/SecretField.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';
  import Toggle from '../lib/components/Toggle.svelte';
  import DatabasePicker from '../lib/components/site/DatabasePicker.svelte';
  import DatabaseReviewRow from '../lib/components/site/DatabaseReviewRow.svelte';
  import {
    isDatabaseIdentifier,
    isProtectedDatabase,
    isProtectedUser,
  } from '../lib/components/site/databaseIdentifiers';
  import {
    isDatabaseLinkEnvKey,
    sanitizeDatabaseUrl,
    withoutPassword,
    type DatabasePickerSummary,
  } from '../lib/components/site/databasePickerTypes';
  import type { CreateSiteDatabaseRequest, SiteDatabaseLink } from '../lib/databaseTypes';

  type Kind = 'static' | 'php' | 'node';
  interface EnvRow {
    key: string;
    value: string;
  }

  const steps = [
    { label: 'Project', title: 'Project details', description: 'Name the site and point devstack at its folder.' },
    { label: 'Runtime', title: 'How should it run?', description: 'Choose static files, PHP-FPM, or a supervised Node process.' },
    { label: 'Options', title: 'Domain and options', description: 'Decide how the site is served, then review everything.' },
  ];

  let step = $state(1);
  let name = $state('');
  let path = $state('');
  let pathTouched = $state(false);
  let kind = $state<Kind>('static');
  let sourceMode = $state<SourceMode>('local');
  let sourceModeTouched = $state(false);
  let nodeVersion = $state('');
  let phpBuild = $state('');
  let command = $state('npm run dev');
  let documentRoot = $state('.');
  let frontController = $state(true);
  let frontControllerFile = $state('index.php');
  let frontControllerMode = $state<FrontControllerMode>('auto');
  let apacheAllowOverride = $state<ApacheAllowOverride>('all');
  let spaFallback = $state(false);
  let spaFallbackFile = $state('index.html');
  let buildEnabled = $state(false);
  let buildNodeVersion = $state('');
  let buildCommand = $state('npm run build');
  let buildOutput = $state('dist');
  let buildEnvRows = $state<EnvRow[]>([{ key: '', value: '' }]);
  let tls = $state(true);
  let forceHttps = $state(false);
  let www = $state(false);
  let envRows = $state<EnvRow[]>([{ key: '', value: '' }]);
  let dbRequest = $state<CreateSiteDatabaseRequest | null>(null);
  let dbSummary = $state<DatabasePickerSummary | null>(null);
  let databaseWarning = $state('');
  let createdSite = $state<Site | null>(null);
  let createdLink = $state<SiteDatabaseLink | null>(null);
  let createdUserManaged = $state(false);
  let createdOpen = $state(false);
  let revealValue = $state('');
  let revealLoading = $state(false);
  let revealError = $state<string | null>(null);
  let errors = $state<Record<string, string>>({});
  let showErrors = $state(false);
  let submitting = $state(false);
  let apiError = $state<{ code: string; message: string } | null>(null);
  let runtimes = $state<RuntimeBuild[]>([]);
  let runtimesLoading = $state(true);
  let runtimesError = $state<{ code: string; message: string } | null>(null);
  let nodeDefault = $state<NodeDefaultStatus | null>(null);
  let settings = $state<Settings | null>(null);
  let pathOpen = $state(false);
  let headingEl: HTMLHeadingElement | undefined = $state();

  function phpLine(version: string): string {
    const parts = version.split('.');
    return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
  }

  function phpRevision(id: string): number {
    const match = /-r([1-9][0-9]*)$/.exec(id);
    return match ? Number.parseInt(match[1], 10) : 0;
  }

  const nodeRuntimes = $derived(
    runtimes
      .filter((runtime) => runtime.kind === 'node')
      .slice()
      .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true })),
  );

  const phpRuntimes = $derived(
    runtimes
      .filter((runtime) => runtime.kind === 'php')
      .slice()
      .sort(
        (a, b) =>
          b.version.localeCompare(a.version, undefined, { numeric: true }) ||
          phpRevision(b.id) - phpRevision(a.id),
      ),
  );

  // Only the resolved build may appear in the inherit label. A configured
  // default that is no longer installed has no build, and the daemon never
  // substitutes another version, so the dashboard must not either.
  const globalNodeBuild = $derived(
    nodeDefault?.build?.kind === 'node' ? nodeDefault.build : null,
  );
  const configuredNodeDefault = $derived(nodeDefault?.configured?.trim() ?? '');
  const nodeDefaultMissing = $derived(
    Boolean(configuredNodeDefault) && !globalNodeBuild,
  );
  const globalNodeVersion = $derived(globalNodeBuild?.version ?? '');
  const nodeInheritLabel = $derived(
    globalNodeVersion
      ? `Inherit global default (v${globalNodeVersion})`
      : 'Inherit global default',
  );

  const phpLines = $derived(
    Array.from(new Set(phpRuntimes.map((runtime) => phpLine(runtime.version)))).sort((a, b) =>
      b.localeCompare(a, undefined, { numeric: true }),
    ),
  );

  const selectedPHPBuild = $derived(phpRuntimes.find((runtime) => runtime.id === phpBuild));

  function buildsOnLine(line: string): RuntimeBuild[] {
    return phpRuntimes.filter((runtime) => phpLine(runtime.version) === line);
  }
  const previewName = $derived(slugify(name) || 'your-site');
  const previewUrl = $derived(`${tls ? 'https' : 'http'}://${previewName}.test`);
  const previewRuntime = $derived(
    kind === 'static'
      ? 'Static files'
      : kind === 'php'
        ? selectedPHPBuild
          ? `PHP ${selectedPHPBuild.version} (r${phpRevision(selectedPHPBuild.id)})`
          : 'PHP - not selected'
        : nodeVersion
          ? `Node ${nodeVersion}`
          : 'Node - not selected',
  );
  const reservedDatabaseEnv = $derived(
    Boolean(dbRequest) && envRows.some((row) => isDatabaseLinkEnvKey(row.key.trim())),
  );
  function slugify(val: string): string {
    return val
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '');
  }

  function defaultPathFor(slug: string, mode: SourceMode): string {
    if (!slug) return '';
    if (mode === 'wsl') {
      return `/data/devstack/projects/${slug}`;
    }
    if (mode === 'windows') {
      const base = settings?.home ? `${settings.home}\\projects` : 'C:\\Users\\User\\.devstack\\projects';
      return `${base}\\${slug}`;
    }
    const base = settings?.user_home ? `${settings.user_home}/projects` : '/home/projects';
    return `${base}/${slug}`;
  }

  const isWindowsHost = $derived(Boolean(settings?.home && /^[a-z]:[\\/]/i.test(settings.home)));
  const initialBrowsePath = $derived(
    pathTouched && path
      ? path
      : sourceMode === 'wsl'
        ? '/data/devstack/projects'
        : sourceMode === 'windows'
          ? (settings?.home ? `${settings.home}\\projects` : (settings?.user_home ? `${settings.user_home}\\.devstack\\projects` : 'C:\\'))
          : (settings?.user_home ? `${settings.user_home}/projects` : '/')
  );
  const pathPlaceholder = $derived(
    sourceMode === 'windows'
      ? `${settings?.home ?? 'C:\\Users\\you\\.devstack'}\\projects\\my-app`
      : sourceMode === 'wsl'
        ? '/data/devstack/projects/my-app'
        : '/home/you/projects/my-app',
  );

  function defaultSourceMode(siteKind: Kind, windowsHost: boolean): SourceMode {
    if (!windowsHost) return 'local';
    return siteKind === 'static' ? 'windows' : 'wsl';
  }

  function sourceModeForPath(value: string, siteKind: Kind): SourceMode {
    const trimmed = value.trim();
    if (/^[a-z]:[\\/]/i.test(trimmed)) return 'windows';
    if (/^\\\\wsl(?:\.localhost|\$)\\/i.test(trimmed)) return 'wsl';
    if (trimmed.startsWith('/')) return isWindowsHost ? 'wsl' : 'local';
    return defaultSourceMode(siteKind, isWindowsHost);
  }

  const errorItems = $derived(
    Object.entries(errors).map(([key, message]) => ({
      key,
      message,
      label: fieldLabel(key),
    })),
  );

  async function loadRuntimeData(): Promise<void> {
    runtimesLoading = true;
    runtimesError = null;
    try {
      // Settings drive Windows source-mode defaults; runtimes drive the PHP and
      // Node pickers. Neither must prevent a static site when the other call
      // fails (for example while the WSL agent is asleep).
      settings = await api.getSettings().catch(() => null);
      if (!sourceModeTouched) sourceMode = defaultSourceMode(kind, Boolean(settings?.home && /^[a-z]:[\\/]/i.test(settings.home)));
      try {
        runtimes = await api.listRuntimes();
      } catch (caught) {
        runtimes = [];
        runtimesError = describeError(caught);
      }
      nodeDefault = await api.getNodeDefault().catch(() => null);
      if (!buildNodeVersion && nodeRuntimes.length > 0) buildNodeVersion = nodeRuntimes[0].version;
      if (!phpBuild && phpRuntimes.length > 0) phpBuild = phpRuntimes[0].id;
    } finally {
      runtimesLoading = false;
    }
  }

  onMount(() => {
    void loadRuntimeData();
  });

  async function installNodeVersion(
    version: string,
    target: 'site' | 'build',
  ): Promise<void> {
    // Preflight pins the exact archive before the existing runtime install
    // endpoint registers it; a site never stores an uninstalled version.
    const preflight = await api.getNodeRelease(version);
    await api.installRuntime(preflight.entry.id);
    const installed = preflight.entry.version;
    if (target === 'build') {
      buildNodeVersion = installed;
    } else {
      nodeVersion = installed;
    }
    await loadRuntimeData();
    addToast({
      kind: 'success',
      title: `Node ${installed} installed`,
      message:
        target === 'build'
          ? 'The build profile now uses this exact version.'
          : 'The site toolchain now uses this exact version.',
    });
  }

  function fieldLabel(key: string): string {
    if (key === 'name') return 'Site name';
    if (key === 'path') return 'Project folder';
    if (key === 'node_version') return 'Node version';
    if (key === 'php_build') return 'PHP build';
    if (key === 'command') return 'Start command';
    if (key === 'document_root') return 'Document root';
    if (key === 'front_controller_file') return 'Front controller file';
    if (key === 'front_controller_mode') return 'Front controller mode';
    if (key === 'apache_allow_override') return 'Apache AllowOverride';
    if (key === 'spa_fallback_file') return 'SPA fallback file';
    if (key === 'build_node_version') return 'Build Node version';
    if (key === 'build_command') return 'Build command';
    if (key === 'build_output') return 'Build output';
    if (key === 'database_service') return 'Database service';
    if (key === 'database_name') return 'Database name';
    if (key === 'database_user') return 'Database user';
    if (key === 'database_username') return 'Database username';
    if (key.startsWith('build-env-')) {
      return `Build environment variable ${Number(key.slice(10)) + 1}`;
    }
    if (key.startsWith('env-')) return `Environment variable ${Number(key.slice(4)) + 1}`;
    return key;
  }

  function fieldStep(key: string): number {
    if (key === 'name' || key === 'path' || key === 'kind') return 1;
    if (
      key === 'node_version' ||
      key === 'php_build' ||
      key === 'command' ||
      key === 'document_root' ||
      key === 'front_controller_file' ||
      key === 'front_controller_mode' ||
      key === 'apache_allow_override' ||
      key === 'spa_fallback_file' ||
      key === 'build_node_version' ||
      key === 'build_command' ||
      key === 'build_output' ||
      key.startsWith('build-env-') ||
      key.startsWith('env-')
    ) {
      return 2;
    }
    return 3;
  }

  function validateAll(): Record<string, string> {
    const next: Record<string, string> = {};
    const finalName = slugify(name);
    if (!finalName) {
      next.name = 'Enter a site name.';
    } else if (finalName.length > 63) {
      next.name = 'Keep the name at 63 characters or fewer.';
    }

    if (!path.trim()) {
      next.path = 'Choose the project folder.';
    } else if (!isAbsolutePath(path.trim())) {
      next.path = 'Use an absolute path, for example /home/you/projects/app.';
    } else if (isWindowsHost && sourceMode === 'windows' && !/^[a-z]:[\\/]/i.test(path.trim())) {
      next.path = 'Windows source mode requires a drive path such as C:\\projects\\app.';
    } else if (
      isWindowsHost &&
      sourceMode === 'wsl' &&
      !path.trim().startsWith('/') &&
      !/^\\\\wsl(?:\.localhost|\$)\\/i.test(path.trim())
    ) {
      next.path = 'WSL source mode requires a Linux path or a \\\\wsl.localhost\\... path.';
    }

    if (kind === 'static' || kind === 'php') {
      if (!documentRoot.trim()) next.document_root = 'Enter a document root. Use . to serve the project root.';
    }
    if (kind === 'php') {
      if (!phpBuild) next.php_build = 'Install a PHP build before adding a PHP site.';
      const fcFile = frontControllerFile.trim();
      if (frontController) {
        if (!fcFile) {
          next.front_controller_file = 'Enter the front controller file, for example index.php.';
        } else if (!isRelativeFile(fcFile)) {
          next.front_controller_file = 'Use a relative path inside the project.';
        } else if (!fcFile.toLowerCase().endsWith('.php')) {
          next.front_controller_file = 'The front controller file must end with .php.';
        }
      }
    }
    if (kind === 'node' && !nodeVersion.trim()) {
      next.node_version = 'Choose an installed Node version.';
    } else if (
      nodeVersion.trim() &&
      !nodeRuntimes.some((runtime) => runtime.version === nodeVersion.trim())
    ) {
      next.node_version = `Node ${nodeVersion.trim()} is not installed. Install it first.`;
    }
    if (kind === 'node' && !command.trim()) {
      next.command = 'Enter the command that starts the dev server.';
    }

    if (kind === 'static') {
      const spaFile = spaFallbackFile.trim();
      if (spaFallback) {
        if (!spaFile) {
          next.spa_fallback_file = 'Enter the file to serve for unknown paths, for example index.html.';
        } else if (!isRelativeFile(spaFile)) {
          next.spa_fallback_file = 'Use a relative path inside the project.';
        } else if (spaFile.toLowerCase().endsWith('.php')) {
          next.spa_fallback_file = 'The SPA fallback file cannot be a .php file.';
        }
      }

      if (buildEnabled) {
        if (!buildNodeVersion) {
          next.build_node_version = 'Install a Node build before enabling a static build.';
        }
        if (!buildCommand.trim()) {
          next.build_command = 'Enter the build command, for example npm run build.';
        }
        const output = buildOutput.trim();
        if (!output) {
          next.build_output = 'Enter the output directory produced by the build, for example dist.';
        } else if (!isRelativeFile(output)) {
          next.build_output = 'Use a relative path inside the project.';
        }
        const seen = new Set<string>();
        buildEnvRows.forEach((row, index) => {
          const key = row.key.trim();
          if (!key) {
            if (row.value.trim()) next[`build-env-${index}`] = 'Add a name for this value.';
            return;
          }
          if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
            next[`build-env-${index}`] =
              'Use letters, numbers and underscores; do not start with a number.';
          } else if (seen.has(key)) {
            next[`build-env-${index}`] = 'This variable is already defined.';
          }
          seen.add(key);
        });
      }
    }

    if (kind === 'node') {
      const seen = new Set<string>();
      envRows.forEach((row, index) => {
        const key = row.key.trim();
        if (!key) {
          if (row.value.trim()) next[`env-${index}`] = 'Add a name for this value.';
          return;
        }
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
          next[`env-${index}`] = 'Use letters, numbers and underscores; do not start with a number.';
        } else if (seen.has(key)) {
          next[`env-${index}`] = 'This variable is already defined.';
        }
        seen.add(key);
      });
    }

    if (dbRequest) {
      if (!dbRequest.service_id) {
        next.database_service = 'Choose a database service.';
      }
      const database = dbRequest.new_database?.name?.trim() ?? dbRequest.database ?? '';
      if (!database) {
        next.database_name = dbRequest.new_database
          ? 'Enter a database name.'
          : 'Choose an existing database.';
      } else if (dbRequest.new_database && !isDatabaseIdentifier(database)) {
        next.database_name =
          'Use letters, numbers and underscores; start with a letter or underscore.';
      } else if (dbRequest.new_database && isProtectedDatabase(database)) {
        next.database_name = 'That database name is reserved.';
      }
      if (dbRequest.new_user) {
        const username = dbRequest.new_user.username.trim();
        if (!username) {
          next.database_username = 'Enter a database username.';
        } else if (!isDatabaseIdentifier(username)) {
          next.database_username =
            'Use letters, numbers and underscores; start with a letter or underscore.';
        } else if (isProtectedUser(username)) {
          next.database_username = 'That username is reserved.';
        }
      } else if (!dbRequest.user_id) {
        next.database_user = 'Choose an existing user.';
      }
    }

    return next;
  }

  function isRelativeFile(value: string): boolean {
    if (value.startsWith('/') || value.startsWith('\\')) return false;
    if (value.includes('\0')) return false;
    const parts = value.split(/[\\/]+/);
    return parts.every((part) => part !== '' && part !== '.' && part !== '..' && !part.startsWith('.'));
  }

  function stepFields(target: number): string[] {
    const fields = [
      'name',
      'path',
      'kind',
      'node_version',
      'php_build',
      'command',
      'document_root',
      'front_controller_file',
      'front_controller_mode',
      'apache_allow_override',
      'spa_fallback_file',
      'build_node_version',
      'build_command',
      'build_output',
    ];
    if (target === 2 && kind === 'node') fields.push(...envRows.map((_, index) => `env-${index}`));
    if (target === 2 && kind === 'static' && buildEnabled) {
      fields.push(...buildEnvRows.map((_, index) => `build-env-${index}`));
    }
    if (target === 3) {
      fields.push(
        'database_service',
        'database_name',
        'database_user',
        'database_username',
      );
    }
    return fields.filter((key) => fieldStep(key) === target);
  }

  function validateStep(target: number): Record<string, string> {
    const all = validateAll();
    const keys = new Set(stepFields(target));
    const scoped: Record<string, string> = {};
    for (const [key, value] of Object.entries(all)) {
      if (keys.has(key)) scoped[key] = value;
    }
    return scoped;
  }

  function clearError(key: string): void {
    if (!errors[key]) return;
    const next = { ...errors };
    delete next[key];
    errors = next;
  }

  function validateField(key: string): void {
    const all = validateAll();
    if (all[key]) {
      errors = { ...errors, [key]: all[key] };
    } else {
      clearError(key);
    }
  }

  function scrollBehavior(): ScrollBehavior {
    if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      return 'auto';
    }
    return 'smooth';
  }

  async function focusField(key: string): Promise<void> {
    if (key.startsWith('database_') && !dbRequest) return;
    const target = fieldStep(key);
    step = target;
    await tick();
    const id =
      key === 'kind'
        ? 'kind-static'
        : key === 'name'
          ? 'site-name'
          : key === 'path'
            ? 'site-path'
            : key === 'php_build'
              ? 'php-build'
              : key === 'front_controller_file'
                ? 'front-controller-file'
                : key === 'front_controller_mode'
                  ? 'front-controller-mode'
                  : key === 'apache_allow_override'
                    ? 'apache-allow-override'
                    : key === 'spa_fallback_file'
                      ? 'spa-fallback-file'
                      : key === 'build_node_version'
                        ? 'build-node-version'
                        : key === 'build_command'
                          ? 'build-command'
                          : key === 'build_output'
                            ? 'build-output'
                            : key.startsWith('build-env-')
                              ? `build-env-key-${key.slice(10)}`
                              : key === 'document_root' && kind === 'php'
                                ? 'php-document-root'
                                : key.startsWith('env-')
                                  ? `env-key-${key.slice(4)}`
                                  : key.replace(/_/g, '-');
    const element = document.getElementById(id);
    element?.focus();
    element?.scrollIntoView({ block: 'center', behavior: scrollBehavior() });
  }

  async function goToStep(target: number): Promise<void> {
    step = Math.min(3, Math.max(1, target));
    await tick();
    headingEl?.focus();
    window.scrollTo({ top: 0, behavior: scrollBehavior() });
  }

  async function next(): Promise<void> {
    if (step === 1 && name.trim()) {
      name = slugify(name);
      if (!pathTouched && !path.trim()) {
        path = defaultPathFor(name, sourceMode);
      }
    }
    const found = validateStep(step);
    const keys = new Set(stepFields(step));
    const nextErrors = { ...errors };
    for (const key of keys) delete nextErrors[key];
    errors = { ...nextErrors, ...found };
    if (Object.keys(found).length > 0) {
      showErrors = true;
      const first = Object.keys(found)[0];
      await focusField(first);
      return;
    }
    await goToStep(step + 1);
  }

  function back(): void {
    if (step === 1) {
      push('/sites');
      return;
    }
    void goToStep(step - 1);
  }

  function addEnv(): void {
    envRows = [...envRows, { key: '', value: '' }];
    void tick().then(() => document.getElementById(`env-key-${envRows.length - 1}`)?.focus());
  }

  function removeEnv(index: number): void {
    envRows = envRows.filter((_, position) => position !== index);
    const next: Record<string, string> = {};
    for (const [key, value] of Object.entries(errors)) {
      if (!key.startsWith('env-')) next[key] = value;
    }
    errors = next;
  }

  function addBuildEnv(): void {
    buildEnvRows = [...buildEnvRows, { key: '', value: '' }];
    void tick().then(() =>
      document.getElementById(`build-env-key-${buildEnvRows.length - 1}`)?.focus(),
    );
  }

  function removeBuildEnv(index: number): void {
    buildEnvRows = buildEnvRows.filter((_, position) => position !== index);
    const next: Record<string, string> = {};
    for (const [key, value] of Object.entries(errors)) {
      if (!key.startsWith('build-env-')) next[key] = value;
    }
    errors = next;
  }

  function setKind(value: Kind): void {
    kind = value;
    if (!sourceModeTouched) {
      sourceMode = pathTouched ? sourceModeForPath(path, value) : defaultSourceMode(value, isWindowsHost);
      if (!pathTouched) {
        path = defaultPathFor(slugify(name), sourceMode);
      }
    }
    const next = { ...errors };
    delete next.kind;
    delete next.node_version;
    delete next.php_build;
    delete next.command;
    delete next.document_root;
    delete next.front_controller_file;
    delete next.front_controller_mode;
    delete next.apache_allow_override;
    delete next.spa_fallback_file;
    delete next.build_node_version;
    delete next.build_command;
    delete next.build_output;
    for (const key of Object.keys(next)) {
      if (key.startsWith('env-') || key.startsWith('build-env-')) delete next[key];
    }
    errors = next;
  }

  function manageDatabaseUser(serviceID: string, userID: string): void {
    push(`/services/${encodeURIComponent(serviceID)}?tab=users&user=${encodeURIComponent(userID)}`);
  }

  async function focusDatabasePanel(): Promise<void> {
    await goToStep(3);
    const target =
      document.getElementById('database-service') ?? document.getElementById('database-picker');
    target?.focus();
    target?.scrollIntoView({ block: 'center', behavior: scrollBehavior() });
  }

  function warningFromDetails(details: unknown): string {
    if (!details || typeof details !== 'object') return '';
    const raw = details as Record<string, unknown>;
    for (const key of ['warning', 'database_warning']) {
      const value = raw[key];
      if (typeof value === 'string' && value.trim()) return value.trim();
    }
    if (Array.isArray(raw.warnings)) {
      const first = raw.warnings.find(
        (warning): warning is string => typeof warning === 'string' && warning.trim().length > 0,
      );
      if (first) return first.trim();
    }
    return '';
  }

  async function revealCreatedPassword(): Promise<void> {
    if (!createdLink || revealLoading) return;
    revealLoading = true;
    revealError = null;
    try {
      const secret = await db.revealServiceUserPassword(
        createdLink.service_id,
        createdLink.user_id,
      );
      revealValue = secret.value;
    } catch (caught) {
      revealError = describeError(caught).message;
    } finally {
      revealLoading = false;
    }
  }

  async function copyCreatedPassword(): Promise<void> {
    const ok = await copyText(revealValue);
    if (!ok) {
      addToast({
        kind: 'error',
        title: 'Could not copy the password',
        message: 'Select the value and copy it manually.',
      });
    }
  }

  function continueAfterCreate(): void {
    const target = createdSite;
    createdOpen = false;
    createdLink = null;
    createdUserManaged = false;
    createdSite = null;
    revealValue = '';
    revealError = null;
    if (target) push(`/sites/${encodeURIComponent(target.id)}`);
  }

  async function create(): Promise<void> {
    const found = validateAll();
    errors = found;
    showErrors = true;
    apiError = null;
    databaseWarning = '';
    if (Object.keys(found).length > 0) {
      const first = Object.keys(found)[0];
      await focusField(first);
      return;
    }

    submitting = true;
    const databaseRequest = dbRequest;
    const databaseSummary = dbSummary;
    try {
      const env: Record<string, string> = {};
      if (kind === 'node') {
        for (const row of envRows) {
          if (row.key.trim()) env[row.key.trim()] = row.value;
        }
      }
      const buildEnv: Record<string, string> = {};
      if (kind === 'static' && buildEnabled) {
        for (const row of buildEnvRows) {
          if (row.key.trim()) buildEnv[row.key.trim()] = row.value;
        }
      }
      const build: BuildProfile | undefined =
        kind === 'static' && buildEnabled
          ? {
              node_version: buildNodeVersion,
              command: buildCommand.trim(),
              output: buildOutput.trim(),
              env: buildEnv,
            }
          : undefined;
      const body: CreateSiteRequest = {
        name: name.trim(),
        path: path.trim(),
        kind,
        source_mode: sourceMode,
        node_version: nodeVersion.trim() || undefined,
        command: kind === 'node' ? command.trim() : undefined,
        document_root: kind === 'static' || kind === 'php' ? documentRoot.trim() : undefined,
        php_version:
          kind === 'php' && selectedPHPBuild ? phpLine(selectedPHPBuild.version) : undefined,
        php_build: kind === 'php' ? phpBuild : undefined,
        front_controller: kind === 'php' ? frontController : false,
        front_controller_file:
          kind === 'php' && frontController ? frontControllerFile.trim() : undefined,
        front_controller_mode: kind === 'php' ? frontControllerMode : undefined,
        apache_allow_override: kind === 'php' ? apacheAllowOverride : undefined,
        www,
        tls,
        force_https: tls ? forceHttps : false,
        spa_fallback: kind === 'static' ? spaFallback : false,
        spa_fallback_file: kind === 'static' && spaFallback ? spaFallbackFile.trim() : undefined,
        build,
        env,
      };
      if (databaseRequest) body.database = withoutPassword(databaseRequest);
      const site = await api.createSite(body);
      const buildOutputMissing =
        site.kind === 'static' && Boolean(site.build) && site.status === 'stopped';
      addToast({
        kind: 'success',
        title: `${site.name} added`,
        message: buildOutputMissing
          ? `${site.domain} was created stopped because ${site.build?.output || 'the build output'} does not exist yet. Run Build on the detail page, then Start the site.`
          : `${site.domain} is starting. Logs are live on the detail page.`,
      });
      if (databaseRequest) {
        const request = databaseRequest;
        let link: SiteDatabaseLink | null = null;
        let linkLoadError = '';
        try {
          const result = await db.listSiteDatabases(site.id);
          const requestedDatabase =
            request.new_database?.name?.trim() ?? request.database ?? '';
          const candidates = result?.links ?? [];
          link =
            candidates.find(
              (candidate) =>
                candidate.service_id === request.service_id &&
                candidate.database === requestedDatabase,
            ) ?? null;
        } catch (caught) {
          linkLoadError = describeError(caught).message;
        }
        if (link) {
          createdSite = site;
          createdLink = link;
          createdUserManaged = Boolean(databaseSummary?.userManaged || request.new_user);
          revealValue = '';
          revealError = null;
          createdOpen = true;
          return;
        }
        addToast({
          kind: 'info',
          title: 'Could not load the created database',
          message:
            linkLoadError ||
            'The site was created, but its database link could not be loaded. Open the site to check the Databases card.',
        });
      }
      push(`/sites/${encodeURIComponent(site.id)}`);
    } catch (caught) {
      const described = describeError(caught);
      apiError = { code: described.code, message: described.message };
      databaseWarning = warningFromDetails(described.details);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="page">
  <a class="back" href="#/sites">
    <ArrowLeft size={14} strokeWidth={2} aria-hidden="true" />
    All sites
  </a>

  <div class="wizard">
    <div class="wizard-main">
      <ol class="stepper" aria-label="Add site steps">
        {#each steps as item, index (item.label)}
          {@const number = index + 1}
          <li class:current={step === number} class:done={step > number}>
            <span class="step-number" aria-hidden="true">
              {#if step > number}
                <Check size={12} strokeWidth={2.6} />
              {:else}
                {number}
              {/if}
            </span>
            <span class="step-name" aria-current={step === number ? 'step' : undefined}>{item.label}</span>
          </li>
        {/each}
      </ol>

      <section class="panel" aria-labelledby="step-heading">
        <header class="panel-head">
          <p class="eyebrow">Step {step} of 3</p>
          <h2 id="step-heading" tabindex="-1" bind:this={headingEl}>{steps[step - 1].title}</h2>
          <p class="step-copy">{steps[step - 1].description}</p>
        </header>

        <div class="panel-body">
          {#if apiError}
            <div class="banner">
              <ErrorBanner
                title="Could not create the site"
                code={apiError.code}
                message={apiError.message}
                ondismiss={() => (apiError = null)}
              />
            </div>
          {/if}

          {#if databaseWarning}
            <div class="banner">
              <div class="notice" role="status">
                <p>{databaseWarning}</p>
              </div>
            </div>
          {/if}

          {#if showErrors && errorItems.length > 0}
            <div class="error-summary" role="alert" aria-labelledby="error-summary-title">
              <p id="error-summary-title">Check {errorItems.length === 1 ? 'one field' : `${errorItems.length} fields`} before continuing</p>
              <ul>
                {#each errorItems as item (item.key)}
                  <li>
                    <button type="button" onclick={() => focusField(item.key)}>
                      <strong>{item.label}:</strong>
                      {item.message}
                    </button>
                  </li>
                {/each}
              </ul>
            </div>
          {/if}

          {#if step === 1}
            <div class="form-stack">
              <Field
                id="site-name"
                label="Site name"
                required
                hint="The site becomes name.test. Lowercase letters, numbers and hyphens only."
                error={errors.name}
              >
                <div class="suffix-wrap">
                  <input
                    id="site-name"
                    class="input mono"
                    type="text"
                    placeholder="my-project"
                    autocomplete="off"
                    spellcheck="false"
                    bind:value={name}
                    aria-invalid={Boolean(errors.name)}
                    aria-describedby={errors.name ? 'site-name-error' : 'site-name-hint'}
                    oninput={() => {
                      clearError('name');
                      if (!pathTouched) {
                        path = defaultPathFor(slugify(name), sourceMode);
                        clearError('path');
                      }
                    }}
                    onblur={() => {
                      const slug = slugify(name);
                      if (slug && slug !== name) {
                        name = slug;
                        if (!pathTouched) {
                          path = defaultPathFor(slug, sourceMode);
                        }
                      }
                      validateField('name');
                    }}
                  />
                  <span class="suffix mono" aria-hidden="true">.test</span>
                </div>
              </Field>

              <Field
                id="site-path"
                label="Project folder"
                required
                hint={isWindowsHost
                  ? 'Choose a Windows drive folder or a folder in the configured WSL distro. devstack never writes to your source files.'
                  : 'An existing absolute path. devstack never writes to your project files.'}
                error={errors.path}
              >
                <div class="path-row">
                  <input
                    id="site-path"
                    class="input mono"
                    type="text"
                    placeholder={pathPlaceholder}
                    autocomplete="off"
                    spellcheck="false"
                    bind:value={path}
                    aria-invalid={Boolean(errors.path)}
                    aria-describedby={errors.path ? 'site-path-error' : 'site-path-hint'}
                    oninput={() => {
                      pathTouched = true;
                      clearError('path');
                    }}
                    onblur={() => validateField('path')}
                  />
                  <Button variant="secondary" onclick={() => (pathOpen = true)} disabled={runtimesLoading}>
                    <FolderOpen size={14} strokeWidth={1.9} aria-hidden="true" />
                    Browse
                  </Button>
                </div>
              </Field>

              <fieldset class="kind-group">
                <legend>Site kind</legend>
                <div class="kind-options">
                  <label class="kind-card" class:checked={kind === 'static'}>
                    <input
                      id="kind-static"
                      type="radio"
                      name="site-kind"
                      value="static"
                      checked={kind === 'static'}
                      onchange={() => setKind('static')}
                    />
                    <span class="kind-icon" aria-hidden="true"><Globe size={17} strokeWidth={1.8} /></span>
                    <span class="kind-copy">
                      <span class="kind-title">Static</span>
                      <span class="kind-desc">Prebuilt HTML, CSS and assets served from a folder.</span>
                    </span>
                    {#if kind === 'static'}<span class="kind-check" aria-hidden="true"><Check size={14} strokeWidth={2.4} /></span>{/if}
                  </label>

                  <label class="kind-card" class:checked={kind === 'node'}>
                    <input
                      id="kind-node"
                      type="radio"
                      name="site-kind"
                      value="node"
                      checked={kind === 'node'}
                      onchange={() => setKind('node')}
                    />
                    <span class="kind-icon" aria-hidden="true"><Code size={17} strokeWidth={1.8} /></span>
                    <span class="kind-copy">
                      <span class="kind-title">Node</span>
                      <span class="kind-desc">A supervised dev server, started with your command.</span>
                    </span>
                    {#if kind === 'node'}<span class="kind-check" aria-hidden="true"><Check size={14} strokeWidth={2.4} /></span>{/if}
                  </label>

                  <label class="kind-card" class:checked={kind === 'php'}>
                    <input
                      id="kind-php"
                      type="radio"
                      name="site-kind"
                      value="php"
                      checked={kind === 'php'}
                      onchange={() => setKind('php')}
                    />
                    <span class="kind-icon" aria-hidden="true"><FileCode size={17} strokeWidth={1.8} /></span>
                    <span class="kind-copy">
                      <span class="kind-title">PHP</span>
                      <span class="kind-desc">A PHP-FPM pool served through the devstack proxy.</span>
                    </span>
                    {#if kind === 'php'}<span class="kind-check" aria-hidden="true"><Check size={14} strokeWidth={2.4} /></span>{/if}
                  </label>
                </div>
              </fieldset>

              <ProjectSourceMode
                mode={sourceMode}
                kind={kind}
                isWindows={isWindowsHost}
                onmodechange={(value) => {
                  sourceMode = value;
                  sourceModeTouched = isWindowsHost;
                  if (!pathTouched) {
                    path = defaultPathFor(slugify(name), value);
                  }
                }}
              />
            </div>
          {:else if step === 2}
            {#if kind === 'static'}
              <div class="form-stack">
                <Field
                  id="document-root"
                  label="Document root"
                  required
                  hint="Relative to the project folder. Use . to serve the folder itself."
                  error={errors.document_root}
                >
                  <input
                    id="document-root"
                    class="input mono"
                    type="text"
                    placeholder="."
                    autocomplete="off"
                    spellcheck="false"
                    bind:value={documentRoot}
                    aria-invalid={Boolean(errors.document_root)}
                    aria-describedby={errors.document_root ? 'document-root-error' : 'document-root-hint'}
                    oninput={() => clearError('document_root')}
                    onblur={() => validateField('document_root')}
                  />
                </Field>

                <div class="build-fields">
                  {#if nodeDefaultMissing}
                    <p class="toolchain-warning" role="status">
                      The configured global Node default (v{configuredNodeDefault}) is not
                      installed. Sites that inherit it will have no Node toolchain until it is
                      installed.
                    </p>
                  {/if}
                  <NodeVersionPicker
                    id="node-version"
                    label="Node toolchain"
                    hint="Shells, terminals and tools use this Node build. Inherit the machine-wide default unless this project needs a pinned version."
                    value={nodeVersion}
                    builds={nodeRuntimes}
                    inheritLabel={nodeInheritLabel}
                    loading={runtimesLoading}
                    error={errors.node_version}
                    onchange={(value) => {
                      nodeVersion = value;
                      clearError('node_version');
                    }}
                    oninstall={(version) => installNodeVersion(version, 'site')}
                  />
                </div>

                <div class="options-box">
                  <Toggle
                    label="SPA fallback"
                    description="Route unknown paths to a single HTML file so client-side routers work on refresh."
                    bind:checked={spaFallback}
                  />
                  {#if spaFallback}
                    <div class="toggle-detail">
                      <Field
                        id="spa-fallback-file"
                        label="SPA fallback file"
                        required
                        hint="Relative to the document root. For example index.html."
                        error={errors.spa_fallback_file}
                      >
                        <input
                          id="spa-fallback-file"
                          class="input mono"
                          type="text"
                          placeholder="index.html"
                          autocomplete="off"
                          spellcheck="false"
                          bind:value={spaFallbackFile}
                          aria-invalid={Boolean(errors.spa_fallback_file)}
                          oninput={() => clearError('spa_fallback_file')}
                          onblur={() => validateField('spa_fallback_file')}
                        />
                      </Field>
                    </div>
                  {/if}
                </div>

                <fieldset class="build-group">
                  <legend>Static build <span class="optional">Optional</span></legend>
                  <p class="build-hint">
                    Builds run natively with the selected Node version before the files are
                    served. devstack snapshots the previous output when practical and restores it
                    if the build fails.
                  </p>
                  <div class="options-box">
                    <Toggle
                      label="Build before serving"
                      description="Adds a build profile the dashboard and CLI can run on demand."
                      bind:checked={buildEnabled}
                    />
                  </div>

                  {#if buildEnabled}
                    <div class="build-fields">
                      {#if runtimesLoading}
                        <div class="loading-field">
                          <Skeleton width="110px" height="10px" />
                          <Skeleton width="100%" height="36px" />
                        </div>
                      {:else if nodeRuntimes.length === 0}
                        <div class="notice">
                          <p>No Node builds are installed yet.</p>
                          <p class="notice-copy">
                            Install a Node runtime from <a class="link" href="#/runtimes">Runtimes</a>
                            before enabling a static build.
                          </p>
                        </div>
                      {:else}
                        <NodeVersionPicker
                          id="build-node-version"
                          label="Build Node version"
                          required
                          hint="Builds run with this exact installed Node version, independent of the site shell toolchain."
                          value={buildNodeVersion}
                          builds={nodeRuntimes}
                          error={errors.build_node_version}
                          onchange={(value) => {
                            buildNodeVersion = value;
                            clearError('build_node_version');
                          }}
                          oninstall={(version) => installNodeVersion(version, 'build')}
                        />
                      {/if}

                      <Field
                        id="build-command"
                        label="Build command"
                        required
                        hint="Runs in the project folder with node, npm and npx from the selected build."
                        error={errors.build_command}
                      >
                        <input
                          id="build-command"
                          class="input mono"
                          type="text"
                          placeholder="npm ci && npm run build"
                          autocomplete="off"
                          spellcheck="false"
                          bind:value={buildCommand}
                          aria-invalid={Boolean(errors.build_command)}
                          oninput={() => clearError('build_command')}
                          onblur={() => validateField('build_command')}
                        />
                      </Field>

                      <Field
                        id="build-output"
                        label="Output directory"
                        required
                        hint="Required. Relative to the project folder, for example dist. The build fails if this directory is missing afterward."
                        error={errors.build_output}
                      >
                        <input
                          id="build-output"
                          class="input mono"
                          type="text"
                          placeholder="dist"
                          autocomplete="off"
                          spellcheck="false"
                          bind:value={buildOutput}
                          aria-invalid={Boolean(errors.build_output)}
                          oninput={() => clearError('build_output')}
                          onblur={() => validateField('build_output')}
                        />
                      </Field>

                      <div class="build-env">
                        <p class="build-env-label">Build environment <span class="optional">Optional</span></p>
                        <p class="env-hint">
                          Applied only to the build process. Values are redacted in the dashboard.
                        </p>
                        <div class="env-list">
                          {#each buildEnvRows as row, index (index)}
                            <div class="env-row">
                              <div class="env-cell">
                                <label class="sr-only" for={`build-env-key-${index}`}>
                                  Build variable {index + 1} name
                                </label>
                                <input
                                  id={`build-env-key-${index}`}
                                  class="input mono"
                                  type="text"
                                  placeholder="NODE_ENV"
                                  autocomplete="off"
                                  spellcheck="false"
                                  bind:value={row.key}
                                  aria-invalid={Boolean(errors[`build-env-${index}`])}
                                  aria-describedby={errors[`build-env-${index}`]
                                    ? `build-env-error-${index}`
                                    : undefined}
                                  oninput={() => clearError(`build-env-${index}`)}
                                  onblur={() => validateField(`build-env-${index}`)}
                                />
                                {#if errors[`build-env-${index}`]}
                                  <p class="env-error" id={`build-env-error-${index}`}>
                                    {errors[`build-env-${index}`]}
                                  </p>
                                {/if}
                              </div>
                              <div class="env-cell">
                                <label class="sr-only" for={`build-env-value-${index}`}>
                                  Build variable {index + 1} value
                                </label>
                                <input
                                  id={`build-env-value-${index}`}
                                  class="input mono"
                                  type="text"
                                  placeholder="production"
                                  autocomplete="off"
                                  spellcheck="false"
                                  bind:value={row.value}
                                />
                              </div>
                              <IconButton
                                label={`Remove build environment variable ${index + 1}`}
                                icon={Trash2}
                                size="sm"
                                onclick={() => removeBuildEnv(index)}
                              />
                            </div>
                          {/each}
                        </div>
                        <div class="env-add">
                          <Button size="sm" variant="ghost" onclick={addBuildEnv}>
                            <Plus size={13} strokeWidth={2.2} aria-hidden="true" />
                            Add build variable
                          </Button>
                        </div>
                      </div>
                    </div>
                  {/if}
                </fieldset>
              </div>
            {:else if kind === 'php'}
              <div class="form-stack">
                {#if runtimesLoading}
                  <div class="loading-field">
                    <Skeleton width="90px" height="10px" />
                    <Skeleton width="100%" height="36px" />
                  </div>
                {:else if runtimesError}
                  <ErrorBanner
                    title="Could not load PHP builds"
                    code={runtimesError.code}
                    message={runtimesError.message}
                    onretry={loadRuntimeData}
                  />
                {:else if phpRuntimes.length === 0}
                  <div class="notice">
                    <p>No PHP builds are installed yet.</p>
                    <p class="notice-copy">
                      Install a PHP runtime from <a class="link" href="#/runtimes">Runtimes</a>, then
                      come back to finish this site.
                    </p>
                  </div>
                {:else}
                  <Field
                    id="php-build"
                    label="PHP build"
                    required
                    hint="Builds are grouped by PHP line. You can switch to another installed build from the site page later."
                    error={errors.php_build}
                  >
                    <div class="select-wrap">
                      <select
                        id="php-build"
                        class="input mono"
                        bind:value={phpBuild}
                        aria-invalid={Boolean(errors.php_build)}
                        aria-describedby={errors.php_build ? 'php-build-error' : 'php-build-hint'}
                        onchange={() => clearError('php_build')}
                      >
                        {#each phpLines as line (line)}
                          <optgroup label={`PHP ${line}`}>
                            {#each buildsOnLine(line) as runtime (runtime.id)}
                              <option value={runtime.id}>
                                PHP {runtime.version} (r{phpRevision(runtime.id)}) · {runtime.backend}
                              </option>
                            {/each}
                          </optgroup>
                        {/each}
                      </select>
                      <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
                    </div>
                  </Field>
                {/if}

                <div class="build-fields">
                  {#if nodeDefaultMissing}
                    <p class="toolchain-warning" role="status">
                      The configured global Node default (v{configuredNodeDefault}) is not
                      installed. Sites that inherit it will have no Node toolchain until it is
                      installed.
                    </p>
                  {/if}
                  <NodeVersionPicker
                    id="node-version"
                    label="Node toolchain"
                    hint="Shells, terminals and tools use this Node build. Inherit the machine-wide default unless this project needs a pinned version."
                    value={nodeVersion}
                    builds={nodeRuntimes}
                    inheritLabel={nodeInheritLabel}
                    loading={runtimesLoading}
                    error={errors.node_version}
                    onchange={(value) => {
                      nodeVersion = value;
                      clearError('node_version');
                    }}
                    oninstall={(version) => installNodeVersion(version, 'site')}
                  />
                </div>

                <Field
                  id="php-document-root"
                  label="Document root"
                  required
                  hint="Relative to the project folder. Use . to serve the folder itself; public for most frameworks."
                  error={errors.document_root}
                >
                  <input
                    id="php-document-root"
                    class="input mono"
                    type="text"
                    placeholder="public"
                    autocomplete="off"
                    spellcheck="false"
                    bind:value={documentRoot}
                    aria-invalid={Boolean(errors.document_root)}
                    aria-describedby={errors.document_root ? 'php-document-root-error' : 'php-document-root-hint'}
                    oninput={() => clearError('document_root')}
                    onblur={() => validateField('document_root')}
                  />
                </Field>

                <div class="options-box">
                  <Toggle
                    label="Front controller"
                    description="Route requests that do not match a file to one PHP entry point, as most frameworks expect."
                    bind:checked={frontController}
                  />
                  {#if frontController}
                    <div class="toggle-detail form-stack">
                      <Field
                        id="front-controller-file"
                        label="Front controller file"
                        required
                        hint="Relative to the document root, for example index.php (not public/index.php when the document root is public)."
                        error={errors.front_controller_file}
                      >
                        <input
                          id="front-controller-file"
                          class="input mono"
                          type="text"
                          placeholder="index.php"
                          autocomplete="off"
                          spellcheck="false"
                          bind:value={frontControllerFile}
                          aria-invalid={Boolean(errors.front_controller_file)}
                          oninput={() => clearError('front_controller_file')}
                          onblur={() => validateField('front_controller_file')}
                        />
                      </Field>
                      <Field
                        id="front-controller-mode"
                        label="Front controller mode"
                        hint="Auto lets devstack choose generated routing or the project's .htaccess."
                      >
                        <div class="select-wrap">
                          <select
                            id="front-controller-mode"
                            class="input mono"
                            bind:value={frontControllerMode}
                            onchange={() => clearError('front_controller_mode')}
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
                </div>

                <Field
                  id="apache-allow-override"
                  label="Apache AllowOverride"
                  hint="Controls whether Apache reads a project .htaccess file. nginx and builtin ignore it."
                >
                  <div class="select-wrap">
                    <select
                      id="apache-allow-override"
                      class="input mono"
                      bind:value={apacheAllowOverride}
                      onchange={() => clearError('apache_allow_override')}
                    >
                      <option value="all">All</option>
                      <option value="fileinfo">FileInfo</option>
                      <option value="none">None</option>
                    </select>
                    <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
                  </div>
                </Field>

                <div class="notice">
                  <p>ini and extensions are editable after creation.</p>
                  <p class="notice-copy">
                    Open the site to edit the site php.ini section and enable or disable
                    extensions. Shared version defaults live on the Runtimes page.
                  </p>
                </div>
              </div>
            {:else}
              <div class="form-stack">
                {#if runtimesLoading}
                  <div class="loading-field">
                    <Skeleton width="90px" height="10px" />
                    <Skeleton width="100%" height="36px" />
                  </div>
                {:else if runtimesError}
                  <ErrorBanner
                    title="Could not load Node builds"
                    code={runtimesError.code}
                    message={runtimesError.message}
                    onretry={loadRuntimeData}
                  />
                {:else if nodeRuntimes.length === 0}
                  <div class="notice">
                    <p>No Node builds are installed yet.</p>
                    <p class="notice-copy">
                      Install one from <a class="link" href="#/runtimes">Runtimes</a>, then come back to finish this site.
                    </p>
                  </div>
                {:else}
                  <NodeVersionPicker
                    id="node-version"
                    label="Node version"
                    required
                    hint="Node server sites store an exact installed version. The global default is not inherited."
                    value={nodeVersion}
                    builds={nodeRuntimes}
                    error={errors.node_version}
                    onchange={(value) => {
                      nodeVersion = value;
                      clearError('node_version');
                    }}
                    oninstall={(version) => installNodeVersion(version, 'site')}
                  />
                {/if}

                <Field
                  id="node-command"
                  label="Start command"
                  required
                  hint="Runs through your shell in the project folder. Use the command you would run locally."
                  error={errors.command}
                >
                  <input
                    id="node-command"
                    class="input mono"
                    type="text"
                    placeholder="npm run dev"
                    autocomplete="off"
                    spellcheck="false"
                    bind:value={command}
                    aria-invalid={Boolean(errors.command)}
                    aria-describedby={errors.command ? 'node-command-error' : 'node-command-hint'}
                    oninput={() => clearError('command')}
                    onblur={() => validateField('command')}
                  />
                </Field>

                <fieldset class="env">
                  <legend>Environment variables <span class="optional">Optional</span></legend>
                  <p class="env-hint">Added to the process environment. Values are redacted in the dashboard.</p>
                  {#if reservedDatabaseEnv}
                    <p class="env-warning" role="status">
                      Database link values override DB_* and DATABASE_URL at runtime. Remove the
                      overlapping variable or expect the link value to win.
                    </p>
                  {/if}
                  <div class="env-list">
                    {#each envRows as row, index (index)}
                      <div class="env-row">
                        <div class="env-cell">
                          <label class="sr-only" for={`env-key-${index}`}>Variable {index + 1} name</label>
                          <input
                            id={`env-key-${index}`}
                            class="input mono"
                            type="text"
                            placeholder="NODE_ENV"
                            autocomplete="off"
                            spellcheck="false"
                            bind:value={row.key}
                            aria-invalid={Boolean(errors[`env-${index}`])}
                            aria-describedby={errors[`env-${index}`] ? `env-error-${index}` : undefined}
                            oninput={() => clearError(`env-${index}`)}
                            onblur={() => validateField(`env-${index}`)}
                          />
                          {#if errors[`env-${index}`]}
                            <p class="env-error" id={`env-error-${index}`}>{errors[`env-${index}`]}</p>
                          {/if}
                        </div>
                        <div class="env-cell">
                          <label class="sr-only" for={`env-value-${index}`}>Variable {index + 1} value</label>
                          <input
                            id={`env-value-${index}`}
                            class="input mono"
                            type="text"
                            placeholder="development"
                            autocomplete="off"
                            spellcheck="false"
                            bind:value={row.value}
                          />
                        </div>
                        <IconButton
                          label={`Remove environment variable ${index + 1}`}
                          icon={Trash2}
                          size="sm"
                          onclick={() => removeEnv(index)}
                        />
                      </div>
                    {/each}
                  </div>
                  <div class="env-add">
                    <Button size="sm" variant="ghost" onclick={addEnv}>
                      <Plus size={13} strokeWidth={2.2} aria-hidden="true" />
                      Add variable
                    </Button>
                  </div>
                </fieldset>
              </div>
            {/if}
          {:else}
            <div class="form-stack">
              <div class="options-box options-list">
                <Toggle
                  label="Serve over HTTPS"
                  description="Issue a certificate from the local devstack CA for this domain."
                  bind:checked={tls}
                  onchange={(checked) => {
                    tls = checked;
                    if (!checked) forceHttps = false;
                  }}
                />
                <Toggle
                  label="Force HTTPS"
                  description="Redirect plain HTTP requests to the HTTPS URL."
                  bind:checked={forceHttps}
                  disabled={!tls}
                />
                <Toggle
                  label="Add www alias"
                  description={`Also respond on www.${previewName}.test with the same certificate.`}
                  bind:checked={www}
                />
                {#if kind === 'static'}
                  <Toggle
                    label="SPA fallback"
                    description="Route unknown paths to index.html for client-side routing."
                    bind:checked={spaFallback}
                  />
                {/if}
              </div>

              <DatabasePicker
                bind:value={dbRequest}
                bind:summary={dbSummary}
                siteName={name}
                {errors}
                onclearerror={clearError}
                onmanageuser={manageDatabaseUser}
              />

              <div class="review">
                <div class="review-head">
                  <h3>Review</h3>
                  <span class="sr-only">Review your choices</span>
                </div>
                <dl>
                  <div class="review-row">
                    <dt>Project</dt>
                    <dd>
                      <span class="review-main">{name || '—'}</span>
                      <span class="review-sub mono">{path || 'No folder selected'}</span>
                    </dd>
                    <button type="button" class="edit" onclick={() => goToStep(1)}>Edit</button>
                  </div>
                  <div class="review-row">
                    <dt>Runtime</dt>
                    <dd>
                      <span class="review-main">{previewRuntime}</span>
                      <span class="review-sub mono">
                        {kind === 'static'
                          ? `root ${documentRoot || '.'}`
                          : kind === 'php'
                            ? `root ${documentRoot || '.'} · ${frontController ? 'front controller' : 'no front controller'}`
                            : command || 'No command'}
                      </span>
                    </dd>
                    <button type="button" class="edit" onclick={() => goToStep(2)}>Edit</button>
                  </div>
                  <div class="review-row">
                    <dt>Domain</dt>
                    <dd>
                      <span class="review-main mono">{previewUrl}</span>
                      <span class="review-sub">
                        {[tls ? 'TLS' : 'HTTP', forceHttps ? 'force HTTPS' : null, www ? 'www alias' : null]
                          .filter(Boolean)
                          .join(' · ')}
                      </span>
                    </dd>
                    <button type="button" class="edit" onclick={() => goToStep(3)}>Edit</button>
                  </div>
                  {#if dbRequest && dbSummary}
                    <DatabaseReviewRow
                      value={dbRequest}
                      summary={dbSummary}
                      onedit={focusDatabasePanel}
                    />
                  {/if}
                  {#if reservedDatabaseEnv}
                    <div class="review-warning" role="status">
                      Database link values override DB_* and DATABASE_URL at runtime. Remove the
                      overlapping variable or expect the link value to win.
                    </div>
                  {/if}
                  {#if kind === 'node'}
                    <div class="review-row">
                      <dt>Environment</dt>
                      <dd>
                        <span class="review-main">
                          {envRows.filter((row) => row.key.trim()).length} variables
                        </span>
                        <span class="review-sub">Stored locally with the site</span>
                      </dd>
                      <button type="button" class="edit" onclick={() => goToStep(2)}>Edit</button>
                    </div>
                  {/if}
                  {#if kind === 'php'}
                    <div class="review-row">
                      <dt>PHP routing</dt>
                      <dd>
                        <span class="review-main">
                          {frontController ? `${frontControllerFile || 'index.php'} · ${frontControllerMode}` : 'Front controller off'}
                        </span>
                        <span class="review-sub">Apache AllowOverride {apacheAllowOverride}</span>
                      </dd>
                      <button type="button" class="edit" onclick={() => goToStep(2)}>Edit</button>
                    </div>
                  {/if}
                  {#if kind === 'static' && buildEnabled}
                    <div class="review-row">
                      <dt>Build</dt>
                      <dd>
                        <span class="review-main mono">{buildCommand || 'No command'}</span>
                        <span class="review-sub">
                          Node {buildNodeVersion || 'not selected'} · output {buildOutput || 'not set'} · {buildEnvRows.filter((row) => row.key.trim()).length} variables
                        </span>
                      </dd>
                      <button type="button" class="edit" onclick={() => goToStep(2)}>Edit</button>
                    </div>
                  {/if}
                </dl>
              </div>

              <p class="protect-note">
                <ShieldCheck size={14} strokeWidth={1.9} aria-hidden="true" />
                Project files are never modified. Removing the site later leaves the folder untouched.
              </p>
            </div>
          {/if}
        </div>

        <footer class="panel-foot">
          <Button variant="ghost" onclick={back}>{step === 1 ? 'Cancel' : 'Back'}</Button>
          <div class="foot-right">
            {#if step < 3}
              <Button variant="primary" onclick={next}>
                Continue
                <ArrowRight size={14} strokeWidth={2.2} aria-hidden="true" />
              </Button>
            {:else}
              <Button variant="primary" onclick={create} loading={submitting} disabled={submitting}>
                {submitting ? 'Creating site…' : 'Create site'}
              </Button>
            {/if}
          </div>
        </footer>
      </section>
    </div>

    <aside class="preview" aria-label="Site preview">
      <div class="preview-card">
        <div class="preview-head">
          <span class="preview-label">Preview</span>
          <span class="tls-state" class:on={tls}>
            {#if tls}<Lock size={11} strokeWidth={2} aria-hidden="true" />{:else}<LockOpen size={11} strokeWidth={2} aria-hidden="true" />{/if}
            {tls ? 'TLS on' : 'TLS off'}
          </span>
        </div>
        <p class="preview-url mono">{previewUrl}</p>
        <p class="preview-domain mono">{www ? `${previewName}.test + www` : `${previewName}.test`}</p>

        <dl class="preview-meta">
          <div>
            <dt>Kind</dt>
            <dd>{kind === 'static' ? 'Static' : kind === 'php' ? 'PHP' : 'Node'}</dd>
          </div>
          <div>
            <dt>Runtime</dt>
            <dd class="mono">{previewRuntime}</dd>
          </div>
          <div>
            <dt>Folder</dt>
            <dd class="mono path" title={path}>{path || 'Not selected'}</dd>
          </div>
        </dl>

        <div class="next">
          <p class="next-title">What happens next</p>
          <ol>
            <li>
              <span class="num mono">01</span>
              <span>Reserve <span class="mono">{previewName}.test</span> in the hosts file.</span>
            </li>
            <li>
              <span class="num mono">02</span>
              <span>{tls ? 'Issue a certificate from the local CA.' : 'Skip TLS and serve over HTTP.'}</span>
            </li>
            <li>
              <span class="num mono">03</span>
              <span>Write the proxy route and start the site.</span>
            </li>
            <li>
              <span class="num mono">04</span>
              <span>Probe readiness and stream the first logs.</span>
            </li>
          </ol>
        </div>

        <p class="preview-note">
          <ShieldCheck size={13} strokeWidth={1.9} aria-hidden="true" />
          Local only. No remote access, no telemetry.
        </p>
      </div>
    </aside>
  </div>
</div>

<PathBrowser
  open={pathOpen}
  initialPath={initialBrowsePath}
  mode={sourceMode}
  onselect={(selected) => {
    path = selected;
    pathTouched = true;
    clearError('path');
    validateField('path');
  }}
  onclose={() => (pathOpen = false)}
/>

{#if createdLink}
  <Modal
    open={createdOpen}
    title="Database created"
    description="The site and its database are ready. Reveal the database password once now, or from the site's Databases card later."
    size="md"
    onclose={continueAfterCreate}
  >
    <div class="created-database">
      <DatabaseConnectionCard
        connection={{
          ...createdLink.connection,
          url: createdLink.connection.url
            ? sanitizeDatabaseUrl(createdLink.connection.url)
            : undefined,
        }}
      />
      {#if createdLink.password_managed}
        <SecretField
          label="password"
          value={revealValue}
          revealed={Boolean(revealValue)}
          loading={revealLoading}
          error={revealError ?? undefined}
          onreveal={revealCreatedPassword}
          onhide={() => (revealValue = '')}
          oncopy={copyCreatedPassword}
        />
      {:else if createdUserManaged}
        <p class="hint">
          The database password is unknown. Reset it from the site's Databases card.
        </p>
      {:else}
        <p class="hint">
          This link has no managed password. External password entry is not available in v1;
          set it in the engine and restart the site.
        </p>
      {/if}
    </div>
    {#snippet footer()}
      <Button variant="primary" onclick={continueAfterCreate}>Continue</Button>
    {/snippet}
  </Modal>
{/if}

<style>
  .page {
    padding-top: 18px;
  }

  .back {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 14px;
    color: var(--muted);
    font-size: 12px;
    font-weight: 500;
  }

  .back:hover {
    color: var(--fg);
  }

  .wizard {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 318px;
    align-items: start;
    gap: 18px;
  }

  .wizard-main {
    min-width: 0;
  }

  .stepper {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0 0 12px;
    padding: 0;
    list-style: none;
  }

  .stepper li {
    position: relative;
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    color: var(--muted-2);
    font-size: 11.5px;
    font-weight: 500;
    white-space: nowrap;
  }

  .stepper li:not(:last-child)::after {
    content: '';
    flex: 1;
    height: 1px;
    margin-left: 2px;
    background: var(--border);
  }

  .stepper li.current,
  .stepper li.done {
    color: var(--fg);
  }

  .stepper li.done::after {
    background: var(--accent-border);
  }

  .step-number {
    display: grid;
    place-items: center;
    flex: none;
    width: 22px;
    height: 22px;
    border: 1px solid var(--border-strong);
    border-radius: 50%;
    background: var(--surface);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .stepper li.current .step-number {
    border-color: var(--accent);
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .stepper li.done .step-number {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .step-name {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .panel {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
    overflow: hidden;
  }

  .panel-head {
    padding: 18px 20px 14px;
    border-bottom: 1px solid var(--border);
  }

  .eyebrow {
    margin-bottom: 4px;
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  h2 {
    font-size: 19px;
    letter-spacing: -0.02em;
  }

  h2:focus {
    outline: none;
  }

  .step-copy {
    margin-top: 3px;
    color: var(--muted);
    font-size: 12.5px;
  }

  .panel-body {
    padding: 18px 20px 22px;
  }

  .banner {
    margin-bottom: 14px;
  }

  .form-stack {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .suffix-wrap {
    position: relative;
  }

  .suffix-wrap :global(.input) {
    padding-right: 48px;
  }

  .suffix {
    position: absolute;
    right: 10px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--muted);
    font-size: 11.5px;
    pointer-events: none;
  }

  .path-row {
    display: flex;
    gap: 8px;
  }

  .path-row :global(.input) {
    flex: 1;
    min-width: 0;
  }

  .kind-group {
    margin: 0;
    padding: 0;
    border: 0;
  }

  .kind-group legend {
    margin-bottom: 7px;
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .kind-options {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .kind-card {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
    min-height: 112px;
    padding: 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    background: var(--input-bg);
    cursor: pointer;
    transition:
      border-color 150ms var(--ease),
      background-color 150ms var(--ease);
  }

  .kind-card:hover {
    border-color: var(--control-border);
  }

  .kind-card.checked {
    border-color: var(--accent);
    background: var(--accent-soft);
  }

  .kind-card > input {
    position: absolute;
    opacity: 0;
    width: 1px;
    height: 1px;
  }

  .kind-card:has(input:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .kind-icon {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--surface);
    color: var(--muted);
  }

  .checked .kind-icon {
    color: var(--success-fg);
    border-color: var(--accent-border);
  }

  .kind-copy {
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
  }

  .kind-title {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 600;
  }

  .kind-desc {
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .kind-check {
    position: absolute;
    top: 10px;
    right: 10px;
    color: var(--success-fg);
  }

  .options-box {
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--input-bg);
    padding: 2px 12px;
  }

  .toggle-detail {
    padding: 10px 0 12px;
    border-top: 1px solid var(--border);
  }

  .build-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .build-group legend {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .build-hint {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .toolchain-warning {
    margin: 0;
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .build-fields {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .build-env {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .build-env-label {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .options-list {
    padding: 2px 14px;
  }

  .options-list > :global(.toggle) {
    border-bottom: 1px solid var(--border);
    border-radius: 0;
    padding: 12px 2px;
  }

  .options-list > :global(.toggle:last-child) {
    border-bottom: 0;
  }

  .loading-field {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .notice {
    padding: 12px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-lg);
    background: var(--warning-soft);
  }

  .notice p {
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 500;
  }

  .notice .notice-copy {
    margin-top: 3px;
    color: var(--muted);
    font-size: 11.5px;
    font-weight: 400;
  }

  .env {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin: 0;
    padding: 0;
    border: 0;
  }

  .env legend {
    color: var(--fg);
    font-size: 12px;
    font-weight: 500;
  }

  .optional {
    margin-left: 5px;
    color: var(--muted-2);
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .env-hint {
    color: var(--muted);
    font-size: 11.5px;
  }

  .env-warning {
    margin: 6px 0 0;
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .review-warning {
    margin: 8px 0 0;
    padding: 8px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 36%, var(--border));
    border-radius: var(--radius-sm);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .env-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .env-add {
    display: flex;
    justify-content: flex-start;
  }

  .env-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 28px;
    gap: 6px;
    align-items: start;
  }

  .env-cell {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .env-error {
    color: var(--danger-fg);
    font-size: 10.5px;
    line-height: 1.4;
  }

  .review {
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    overflow: hidden;
  }

  .review-head {
    padding: 9px 12px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-2);
  }

  .review-head h3 {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .review dl {
    margin: 0;
  }

  .review-row {
    display: grid;
    grid-template-columns: 92px minmax(0, 1fr) 40px;
    gap: 10px;
    align-items: center;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
  }

  .review-row:first-child {
    border-top: 0;
  }

  .review dt {
    color: var(--muted);
    font-size: 11px;
  }

  .review dd {
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

  .protect-note {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .protect-note :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--muted-2);
  }

  .panel-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 20px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
  }

  .foot-right {
    display: flex;
    gap: 8px;
  }

  .preview {
    position: sticky;
    top: calc(var(--topbar-h) + 16px);
  }

  .preview-card {
    display: flex;
    flex-direction: column;
    padding: 15px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .preview-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .preview-label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .tls-state {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 20px;
    padding: 0 6px;
    border-radius: 5px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10px;
    font-weight: 500;
  }

  .tls-state.on {
    background: var(--success-soft);
    color: var(--success-fg);
  }

  .preview-url {
    margin-top: 12px;
    color: var(--fg);
    font-size: 14px;
    font-weight: 500;
    line-height: 1.4;
    overflow-wrap: anywhere;
  }

  .preview-domain {
    margin-top: 2px;
    color: var(--muted-2);
    font-size: 10.5px;
  }

  .preview-meta {
    display: flex;
    flex-direction: column;
    gap: 0;
    margin: 14px 0;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
  }

  .preview-meta > div {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 6px 0;
    border-top: 1px solid var(--border);
  }

  .preview-meta > div:first-child {
    border-top: 0;
  }

  .preview-meta dt {
    color: var(--muted);
    font-size: 10.5px;
  }

  .preview-meta dd {
    color: var(--fg);
    font-size: 10.5px;
    text-align: right;
    min-width: 0;
  }

  .preview-meta .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 190px;
  }

  .next-title {
    margin-bottom: 7px;
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .next ol {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .next li {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .next .num {
    flex: none;
    color: var(--muted-2);
    font-size: 9px;
    padding-top: 1px;
  }

  .preview-note {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 14px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
    color: var(--muted-2);
    font-size: 10px;
  }

  .error-summary {
    margin-bottom: 16px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 40%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .error-summary > p {
    color: var(--fg);
    font-size: 12px;
    font-weight: 600;
  }

  .error-summary ul {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin: 5px 0 0;
    padding: 0;
    list-style: none;
  }

  .error-summary button {
    padding: 1px 0;
    border: 0;
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.45;
    text-align: left;
    text-decoration: underline;
    text-decoration-color: color-mix(in srgb, var(--danger) 45%, transparent);
    text-underline-offset: 2px;
  }

  .error-summary button:hover {
    color: var(--fg);
  }

  .error-summary strong {
    color: var(--danger-fg);
    font-weight: 600;
  }

  .created-database {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  @media (max-width: 1080px) {
    .wizard {
      grid-template-columns: minmax(0, 1fr) 280px;
    }
  }

  @media (max-width: 940px) {
    .wizard {
      grid-template-columns: 1fr;
    }

    .preview {
      position: static;
    }

    .preview-card {
      max-width: none;
    }
  }

  @media (max-width: 620px) {
    .kind-options {
      grid-template-columns: 1fr;
    }

    .kind-card {
      min-height: 0;
      flex-direction: row;
      align-items: center;
    }

    .kind-copy {
      flex: 1;
    }

    .kind-check {
      position: static;
      margin-left: auto;
    }

    .path-row {
      flex-direction: column;
    }

    .panel-head {
      padding: 16px;
    }

    .panel-body {
      padding: 16px;
    }

    .panel-foot {
      padding: 12px 16px;
    }

    .review-row {
      grid-template-columns: 72px minmax(0, 1fr) 36px;
    }

    .env-row {
      grid-template-columns: minmax(0, 1fr) 28px;
      padding: 8px;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: var(--surface-2);
    }

    .env-row .env-cell:nth-child(2) {
      grid-column: 1;
    }

    .env-row > :global(button) {
      grid-row: 1;
      grid-column: 2;
      align-self: start;
    }

    .stepper li {
      gap: 5px;
      font-size: 10.5px;
    }

    .step-number {
      width: 20px;
      height: 20px;
    }
  }
</style>
