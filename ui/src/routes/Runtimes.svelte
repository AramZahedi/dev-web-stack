<script lang="ts">
  import { onMount } from 'svelte';
  import {
    ArrowUpCircle,
    Boxes,
    ChevronDown,
    Code,
    Download,
    FileCode,
    HardDrive,
    RefreshCw,
    Settings2,
    ShieldCheck,
    Trash2,
    TriangleAlert,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { describeError } from '../lib/errors';
  import { checksumPrefix, formatBytes, formatDate, formatRelative } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type {
    CatalogEntry,
    NodeRelease,
    NodeReleaseMajor,
    NodeReleasePreflight,
    NodeReleaseSnapshot,
    PHPMigrationResult,
    PHPRuntimeBuild,
    RuntimeBuild,
    Site,
  } from '../lib/types';
  import { ApiError } from '../lib/errors';
  import Badge from '../lib/components/Badge.svelte';
  import Button from '../lib/components/Button.svelte';
  import ConfirmDialog from '../lib/components/ConfirmDialog.svelte';
  import EmptyState from '../lib/components/EmptyState.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Field from '../lib/components/Field.svelte';
  import Modal from '../lib/components/Modal.svelte';
  import PHPDefaultsDrawer from '../lib/components/PHPDefaultsDrawer.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  interface MigrationJob {
    runtime: RuntimeBuild;
    sites: string[];
  }

  let runtimes = $state<RuntimeBuild[]>([]);
  let catalog = $state<CatalogEntry[]>([]);
  let sites = $state<Site[]>([]);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let installTarget = $state<CatalogEntry | null>(null);
  let installingId = $state<string | null>(null);
  let removeTarget = $state<RuntimeBuild | null>(null);
  let removing = $state(false);
  let defaultsLine = $state<string | null>(null);

  let nodeSnapshot = $state<NodeReleaseSnapshot | null>(null);
  let nodeLoading = $state(true);
  let nodeError = $state<{ code: string; message: string } | null>(null);
  let nodeRefreshing = $state(false);
  let nodeRefreshError = $state<{ code: string; message: string } | null>(null);
  let expandedMajors = $state<Record<number, boolean>>({});
  let releaseTarget = $state<NodeRelease | null>(null);
  let releasePreflight = $state<NodeReleasePreflight | null>(null);
  let releasePreflightLoading = $state(false);
  let releasePreflightError = $state<{ code: string; message: string } | null>(null);
  let releaseInstalling = $state(false);

  let migration = $state<MigrationJob | null>(null);
  let migrationTarget = $state('');
  let migrating = $state(false);
  let migrationResults = $state<PHPMigrationResult[] | null>(null);
  let migrationError = $state<{ code: string; message: string } | null>(null);

  function phpLine(version: string): string {
    const parts = version.split('.');
    return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
  }

  function phpRevision(id: string): number {
    const match = /-r([1-9][0-9]*)$/.exec(id);
    return match ? Number.parseInt(match[1], 10) : 0;
  }

  function compareVersions(a: string, b: string): number {
    return a.localeCompare(b, undefined, { numeric: true });
  }

  function compareBuilds(a: RuntimeBuild, b: RuntimeBuild): number {
    return compareVersions(a.version, b.version) || phpRevision(a.id) - phpRevision(b.id);
  }

  const nodeRuntimes = $derived(
    runtimes
      .filter((runtime) => runtime.kind === 'node')
      .slice()
      .sort((a, b) => compareVersions(b.version, a.version)),
  );

  const phpRuntimes = $derived<PHPRuntimeBuild[]>(
    runtimes
      .filter((runtime) => runtime.kind === 'php')
      .map((runtime) => ({
        ...runtime,
        line: phpLine(runtime.version),
        revision: phpRevision(runtime.id),
      }))
      .sort((a, b) => compareBuilds(b, a)),
  );

  const phpLines = $derived(
    Array.from(new Set(phpRuntimes.map((runtime) => runtime.line))).sort((a, b) =>
      compareVersions(b, a),
    ),
  );

  const nodeMajors = $derived<NodeReleaseMajor[]>(nodeSnapshot?.majors ?? []);
  const installedNodeVersions = $derived(new Set(nodeRuntimes.map((runtime) => runtime.version)));
  const nodeReleaseCount = $derived(nodeSnapshot?.all.length ?? 0);

  const availablePHP = $derived(
    catalog
      .filter((entry) => entry.kind === 'php')
      .slice()
      .sort(
        (a, b) =>
          compareVersions(a.version, b.version) || (a.revision ?? 1) - (b.revision ?? 1),
      ),
  );

  const availableCount = $derived(nodeReleaseCount + availablePHP.length);
  const migrationCandidates = $derived(
    migration ? phpRuntimes.filter((runtime) => runtime.id !== migration?.runtime.id) : [],
  );

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      const [builds, entries, siteList] = await Promise.all([
        api.listRuntimes(),
        api.availableRuntimes(),
        api.listSites(),
      ]);
      runtimes = builds;
      catalog = entries;
      sites = siteList;
    } catch (caught) {
      error = describeError(caught);
    } finally {
      loading = false;
    }
  }

  async function loadReleases(initial = false): Promise<void> {
    if (initial) nodeLoading = true;
    nodeError = null;
    try {
      nodeSnapshot = await api.listNodeReleases();
      expandedMajors = {};
    } catch (caught) {
      nodeError = describeError(caught);
    } finally {
      nodeLoading = false;
    }
  }

  async function refreshReleases(): Promise<void> {
    if (nodeRefreshing) return;
    nodeRefreshing = true;
    nodeRefreshError = null;
    try {
      nodeSnapshot = await api.refreshNodeReleases();
      nodeError = null;
      expandedMajors = {};
      addToast({
        kind: nodeSnapshot.stale ? 'info' : 'success',
        title: nodeSnapshot.stale ? 'Node index refreshed from cache' : 'Node index refreshed',
        message: `Source ${sourceLabel(nodeSnapshot.source)}. Cache age ${releaseAge(
          nodeSnapshot.cached_at,
        )}${nodeSnapshot.warning ? `. ${nodeSnapshot.warning}` : '.'}`,
      });
    } catch (caught) {
      nodeRefreshError = describeError(caught);
    } finally {
      nodeRefreshing = false;
    }
  }

  onMount(() => {
    void load(true);
    void loadReleases(true);
    const refresh = () => {
      void load();
      void loadReleases();
    };
    window.addEventListener('devstack:refresh', refresh);
    return () => window.removeEventListener('devstack:refresh', refresh);
  });

  function usedBy(runtime: RuntimeBuild): string[] {
    if (runtime.kind === 'php') {
      const line = phpLine(runtime.version);
      return sites
        .filter(
          (site) =>
            site.kind === 'php' &&
            (site.php_build === runtime.id ||
              (!site.php_build && site.php_version === line)),
        )
        .map((site) => site.name)
        .sort();
    }
    return sites
      .filter((site) => site.kind === 'node' && site.node_version === runtime.version)
      .map((site) => site.name);
  }

  function phpSitesOn(line: string): Site[] {
    return sites.filter((site) => site.kind === 'php' && site.php_version === line);
  }

  function buildsOn(line: string): PHPRuntimeBuild[] {
    return phpRuntimes.filter((runtime) => runtime.line === line);
  }

  function updateFor(line: string): CatalogEntry | null {
    const installed = buildsOn(line);
    if (installed.length === 0) return null;
    const newestInstalled = installed.slice().sort(compareBuilds).at(-1);
    if (!newestInstalled) return null;
    const candidates = availablePHP.filter((entry) => phpLine(entry.version) === line);
    const newestCatalog = candidates.at(-1);
    if (!newestCatalog) return null;
    const candidate: RuntimeBuild = {
      id: newestCatalog.id,
      kind: 'php',
      version: newestCatalog.version,
      backend: 'local',
      path: '',
      checksum: newestCatalog.sha256,
      installed_at: '',
    };
    return compareBuilds(candidate, newestInstalled) > 0 ? newestCatalog : null;
  }

  function switchHref(line: string): string {
    const target = phpSitesOn(line)[0];
    return target ? `#/sites/${encodeURIComponent(target.id)}` : '#/sites';
  }

  function isInstalled(entry: CatalogEntry): boolean {
    if (entry.kind === 'php') return runtimes.some((runtime) => runtime.id === entry.id);
    return runtimes.some(
      (runtime) => runtime.kind === entry.kind && runtime.version === entry.version,
    );
  }

  function buildLabel(runtime: Pick<RuntimeBuild, 'kind' | 'version'>): string {
    return `${runtime.kind === 'php' ? 'PHP' : 'Node'} ${runtime.version}`;
  }

  function catalogLabel(entry: CatalogEntry): string {
    if (entry.kind === 'php') {
      return `PHP ${entry.version} (r${entry.revision ?? 1})`;
    }
    return `Node ${entry.version}`;
  }

  function releaseMajor(version: string): number {
    return Number.parseInt(version.split('.')[0] ?? '0', 10);
  }

  function releaseMinor(version: string): string {
    const parts = version.split('.');
    return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
  }

  function releaseAge(value: string): string {
    if (!value || value.startsWith('0001-01-01')) return 'unknown';
    return formatRelative(value);
  }

  function sourceLabel(source: string): string {
    if (source === 'live') return 'Live';
    if (source === 'cache') return 'Cached';
    if (source === 'embedded') return 'Offline catalog';
    return source ? source[0].toUpperCase() + source.slice(1) : 'Unknown';
  }

  function sourceVariant(source: string, stale: boolean): 'accent' | 'warning' | 'info' | 'neutral' {
    if (source === 'live') return stale ? 'warning' : 'accent';
    if (source === 'embedded') return 'warning';
    if (source === 'cache') return stale ? 'warning' : 'info';
    return 'neutral';
  }

  function olderReleaseGroups(entry: NodeReleaseMajor): { minor: string; releases: NodeRelease[] }[] {
    const grouped = new Map<string, NodeRelease[]>();
    for (const release of nodeSnapshot?.all ?? []) {
      if (releaseMajor(release.version) !== entry.major) continue;
      if (release.version === entry.latest.version) continue;
      const minor = releaseMinor(release.version);
      grouped.set(minor, [...(grouped.get(minor) ?? []), release]);
    }
    return Array.from(grouped.entries()).map(([minor, releases]) => ({ minor, releases }));
  }

  function toggleMajor(major: number): void {
    expandedMajors = { ...expandedMajors, [major]: !expandedMajors[major] };
  }

  function newestPatchFor(version: string): string | null {
    const major = releaseMajor(version);
    return nodeSnapshot?.majors.find((entry) => entry.major === major)?.latest.version ?? null;
  }

  function olderPatchWarning(release: NodeRelease): string | null {
    const newest = newestPatchFor(release.version);
    if (!newest || newest === release.version) return null;
    return `Node ${release.version} is an older patch release. The newest patch in Node ${releaseMajor(
      release.version,
    )} is ${newest}.`;
  }

  async function openReleaseInstall(release: NodeRelease): Promise<void> {
    releaseTarget = release;
    releasePreflight = null;
    releasePreflightError = null;
    releasePreflightLoading = true;
    try {
      const preflight = await api.getNodeRelease(release.version);
      if (releaseTarget?.version === release.version) releasePreflight = preflight;
    } catch (caught) {
      if (releaseTarget?.version === release.version) {
        releasePreflightError = describeError(caught);
      }
    } finally {
      if (releaseTarget?.version === release.version) releasePreflightLoading = false;
    }
  }

  function closeReleaseInstall(): void {
    if (releaseInstalling) return;
    releaseTarget = null;
    releasePreflight = null;
    releasePreflightError = null;
    releasePreflightLoading = false;
  }

  async function installRelease(): Promise<void> {
    const preflight = releasePreflight;
    if (!preflight || releaseInstalling) return;
    releaseInstalling = true;
    try {
      await api.installRuntime(preflight.entry.id);
      addToast({
        kind: 'success',
        title: `Node ${preflight.entry.version} installed`,
        message: 'Archive verified against the release checksum. Available for new sites.',
      });
      releaseTarget = null;
      releasePreflight = null;
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not install Node release', `${described.code}: ${described.message}`);
    } finally {
      releaseInstalling = false;
    }
  }

  async function install(): Promise<void> {
    const target = installTarget;
    if (!target) return;
    installTarget = null;
    installingId = target.id;
    try {
      await api.installRuntime(target.id);
      addToast({
        kind: 'success',
        title: `${catalogLabel(target)} installed`,
        message: 'Archive verified against the catalog SHA-256. Available for new sites.',
      });
      await load();
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not install runtime', `${described.code}: ${described.message}`);
    } finally {
      installingId = null;
    }
  }

  function openMigration(target: RuntimeBuild, names: string[]): void {
    const line = phpLine(target.version);
    // The backend migrates the whole line, not only the build named by the
    // 409. List the union so no site moves without being shown.
    const listed = new Set<string>(phpSitesOn(line).map((site) => site.name));
    for (const name of names) listed.add(name);
    migration = {
      runtime: target,
      sites: Array.from(listed).sort(),
    };
    migrationError = null;
    migrationResults = null;
    const candidates = phpRuntimes.filter((runtime) => runtime.id !== target.id);
    const sameLine = candidates.filter((runtime) => runtime.line === line);
    migrationTarget = (sameLine[0] ?? candidates[0])?.id ?? '';
  }

  async function remove(): Promise<void> {
    if (!removeTarget) return;
    const target = removeTarget;
    removing = true;
    try {
      await api.removeRuntime(target.id);
      addToast({
        kind: 'success',
        title: `${buildLabel(target)} removed`,
        message: 'The extracted runtime was deleted from the devstack home.',
      });
      removeTarget = null;
      await load();
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'PHP_VERSION_IN_USE' && target.kind === 'php') {
        const details = caught.details as { sites?: unknown } | undefined;
        const names = Array.isArray(details?.sites)
          ? details.sites.filter((name): name is string => typeof name === 'string')
          : [];
        removeTarget = null;
        openMigration(target, names);
        return;
      }
      const described = describeError(caught);
      toastError('Could not remove runtime', `${described.code}: ${described.message}`);
    } finally {
      removing = false;
    }
  }

  async function migrateAndRemove(): Promise<void> {
    const job = migration;
    const target = migrationCandidates.find((runtime) => runtime.id === migrationTarget);
    if (!job || !target || migrating) return;
    migrating = true;
    migrationError = null;
    migrationResults = null;
    try {
      const results = await api.migratePHP({
        from_version: phpLine(job.runtime.version),
        to_version: phpLine(target.version),
        to_build: target.id,
      });
      migrationResults = results;
      if (!results.every((result) => result.ok)) return;
      try {
        await api.removeRuntime(job.runtime.id);
        addToast({
          kind: 'success',
          title: `${buildLabel(job.runtime)} removed`,
          message: `${results.length} site${results.length === 1 ? '' : 's'} moved to ${buildLabel(target)}.`,
        });
        migration = null;
        await load();
      } catch (caught) {
        migrationError = describeError(caught);
        toastError('Could not remove runtime', migrationError.message);
      }
    } catch (caught) {
      migrationError = describeError(caught);
    } finally {
      migrating = false;
    }
  }

  function closeMigration(): void {
    if (migrating) return;
    migration = null;
    migrationResults = null;
    migrationError = null;
  }

  function scrollToAvailable(): void {
    document.getElementById('available')?.scrollIntoView({ block: 'start' });
  }
</script>

<div class="page">
  <section class="intro" aria-labelledby="runtime-intro">
    <span class="intro-icon" aria-hidden="true"><ShieldCheck size={19} strokeWidth={1.8} /></span>
    <div class="intro-copy">
      <h2 id="runtime-intro">Verified runtimes</h2>
      <p>
        Node and PHP builds are pinned to the embedded catalog. Every archive is checked against
        its published SHA-256 before extraction, and installs stay inside the devstack home.
        PHP lines keep the shared extensions and defaults you choose here.
      </p>
    </div>
    <div class="intro-chips">
      <Badge variant="accent">Checksum verified</Badge>
      <Badge>Local only</Badge>
    </div>
  </section>

  {#if error}
    <div class="banner">
      <ErrorBanner
        title="Could not load runtimes"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
        ondismiss={runtimes.length > 0 ? () => (error = null) : undefined}
      />
    </div>
  {/if}

  <section class="section" aria-labelledby="installed-heading">
    <div class="section-head">
      <div>
        <p class="eyebrow">On this machine</p>
        <h2 id="installed-heading">Installed</h2>
      </div>
      <span class="section-count mono">{runtimes.length}</span>
    </div>

    {#if loading}
      <div class="runtime-list" aria-busy="true" aria-label="Loading installed runtimes">
        {#each Array(2) as _, index (index)}
          <div class="runtime-row skeleton">
            <Skeleton width="40px" height="40px" radius="8px" />
            <div class="skeleton-copy">
              <Skeleton width="40%" height="15px" />
              <Skeleton width={`${64 + index * 10}%`} height="10px" />
              <Skeleton width="88%" height="9px" />
            </div>
            <span class="skeleton-action"><Skeleton width="96px" height="30px" radius="8px" /></span>
          </div>
        {/each}
      </div>
    {:else if runtimes.length === 0 && !error}
      <div class="empty-box">
        <EmptyState
          icon={Boxes}
          title="No runtimes installed"
          description="Install a Node or PHP build from the catalog below before adding a site."
        >
          <Button variant="secondary" onclick={scrollToAvailable}>Browse available builds</Button>
        </EmptyState>
      </div>
    {:else}
      <div class="runtime-groups">
        {#if nodeRuntimes.length > 0}
          <div class="runtime-group" aria-label="Node builds">
            <div class="group-head">
              <span class="group-label">Node</span>
              <span class="group-count mono">{nodeRuntimes.length}</span>
            </div>
            <div class="runtime-list">
              {#each nodeRuntimes as runtime (runtime.id)}
                {@const users = usedBy(runtime)}
                <article class="runtime-row" class:in-use={users.length > 0}>
                  <span class="runtime-icon" aria-hidden="true"><Code size={18} strokeWidth={1.8} /></span>
                  <div class="runtime-main">
                    <div class="runtime-title">
                      <h3 class="mono">Node {runtime.version}</h3>
                      <Badge>{runtime.kind}</Badge>
                      {#if users.length > 0}
                        <span class="usage-pill"><span class="dot" aria-hidden="true"></span> In use</span>
                      {/if}
                    </div>
                    <p class="runtime-path mono" title={runtime.path}>{runtime.path}</p>
                    <div class="runtime-meta">
                      <span class="checksum" title={runtime.checksum}>
                        sha256 <span class="mono">{checksumPrefix(runtime.checksum, 12)}</span>
                      </span>
                      <span>Installed {formatDate(runtime.installed_at)}</span>
                      <span>{runtime.size ? formatBytes(runtime.size) : 'Size unavailable'}</span>
                      <span>{runtime.backend}</span>
                    </div>
                    {#if users.length > 0}
                      <p class="usage-note">
                        Used by {users.join(', ')}. Remove is blocked while a site references this build.
                      </p>
                    {/if}
                  </div>
                  <div class="runtime-actions">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={users.length > 0 || Boolean(installingId)}
                      title={users.length > 0 ? `In use by ${users.join(', ')}` : `Remove Node ${runtime.version}`}
                      onclick={() => (removeTarget = runtime)}
                    >
                      <Trash2 size={13} strokeWidth={2} aria-hidden="true" />
                      Remove
                    </Button>
                  </div>
                </article>
              {/each}
            </div>
          </div>
        {/if}

        {#if phpRuntimes.length > 0}
          <div class="runtime-group" aria-label="PHP builds">
            <div class="group-head">
              <span class="group-label">PHP</span>
              <span class="group-count mono">{phpRuntimes.length}</span>
            </div>
            {#each phpLines as line (line)}
              {@const builds = buildsOn(line)}
              {@const update = updateFor(line)}
              <div class="line-block">
                <div class="line-head">
                  <div class="line-title">
                    <span class="line-name mono">PHP {line}</span>
                    <span class="line-meta">{builds.length} build{builds.length === 1 ? '' : 's'}</span>
                    {#if update}<Badge variant="info">Update available</Badge>{/if}
                  </div>
                  <div class="line-actions">
                    {#if update}
                      <a class="update-link" href={switchHref(line)}>
                        <ArrowUpCircle size={12} strokeWidth={2} aria-hidden="true" />
                        Switch a site
                      </a>
                    {/if}
                    <Button
                      size="sm"
                      variant="ghost"
                      title={`Edit PHP ${line} defaults`}
                      onclick={() => (defaultsLine = line)}
                    >
                      <Settings2 size={12} strokeWidth={2} aria-hidden="true" />
                      Defaults
                    </Button>
                  </div>
                </div>
                {#if update}
                  <p class="line-note">
                    {catalogLabel(update)} is newer than every installed build on this line.{' '}
                    <a href={switchHref(line)}>Switch a site</a> to pick it up, or install it below.
                  </p>
                {/if}
                <div class="runtime-list">
                  {#each builds as runtime (runtime.id)}
                    {@const users = usedBy(runtime)}
                    <article class="runtime-row" class:in-use={users.length > 0}>
                      <span class="runtime-icon php" aria-hidden="true"><FileCode size={18} strokeWidth={1.8} /></span>
                      <div class="runtime-main">
                        <div class="runtime-title">
                          <h3 class="mono">PHP {runtime.version}</h3>
                          <Badge variant="info">r{runtime.revision}</Badge>
                          <Badge>{runtime.line}</Badge>
                          {#if users.length > 0}
                            <span class="usage-pill"><span class="dot" aria-hidden="true"></span> In use</span>
                          {/if}
                        </div>
                        <p class="runtime-path mono" title={runtime.id}>{runtime.id}</p>
                        <div class="runtime-meta">
                          <span class="checksum" title={runtime.checksum}>
                            sha256 <span class="mono">{checksumPrefix(runtime.checksum, 12)}</span>
                          </span>
                          <span>Installed {formatDate(runtime.installed_at)}</span>
                          <span>{runtime.size ? formatBytes(runtime.size) : 'Size unavailable'}</span>
                          <span>{runtime.backend}</span>
                        </div>
                        {#if users.length > 0}
                          <p class="usage-note">
                            Used by {users.join(', ')}. Remove is blocked while a site pins this build.
                          </p>
                        {/if}
                      </div>
                      <div class="runtime-actions">
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={users.length > 0 || Boolean(installingId)}
                          title={users.length > 0
                            ? `In use by ${users.join(', ')} — switch those sites to another build first`
                            : `Remove PHP ${runtime.version} (r${runtime.revision})`}
                          onclick={() => (removeTarget = runtime)}
                        >
                          <Trash2 size={13} strokeWidth={2} aria-hidden="true" />
                          Remove
                        </Button>
                      </div>
                    </article>
                  {/each}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </section>

  <section class="section" id="available" aria-labelledby="available-heading">
    <div class="section-head">
      <div>
        <p class="eyebrow">Catalog</p>
        <h2 id="available-heading">Available</h2>
      </div>
      <span class="section-count mono">{availableCount}</span>
    </div>

    {#if loading || nodeLoading}
      <div class="catalog-list" aria-busy="true" aria-label="Loading runtime catalog">
        {#each Array(3) as _, index (index)}
          <div class="catalog-row skeleton">
            <Skeleton width="36px" height="36px" radius="7px" />
            <div class="skeleton-copy">
              <Skeleton width="42%" height="14px" />
              <Skeleton width={`${64 + index * 8}%`} height="9px" />
            </div>
            <span class="skeleton-action"><Skeleton width="80px" height="30px" radius="8px" /></span>
          </div>
        {/each}
      </div>
    {:else if availableCount === 0 && !error && !nodeError}
      <div class="empty-box">
        <EmptyState
          icon={Download}
          title="Catalog unavailable"
          description="The daemon returned no runtime catalog entries. Run Doctor to check connectivity and try again."
          compact
        />
      </div>
    {:else}
      <div class="catalog-groups">
        {#if nodeError && !nodeSnapshot}
          <div class="catalog-group" aria-label="Available Node releases">
            <div class="group-head">
              <span class="group-label">Node</span>
            </div>
            <ErrorBanner
              title="Could not load Node releases"
              code={nodeError.code}
              message={nodeError.message}
              onretry={() => loadReleases(true)}
            />
          </div>
        {:else if nodeSnapshot && nodeMajors.length > 0}
          <div class="catalog-group" aria-label="Available Node releases">
            <div class="group-head node-group-head">
              <span class="group-label">Node</span>
              <span class="group-count mono">{nodeReleaseCount}</span>
              <span class="node-source">
                <Badge variant={sourceVariant(nodeSnapshot.source, nodeSnapshot.stale)}>
                  {sourceLabel(nodeSnapshot.source)}
                </Badge>
                {#if nodeSnapshot.stale}<Badge variant="warning">Stale</Badge>{/if}
                <span class="release-age">Cache age {releaseAge(nodeSnapshot.cached_at)}</span>
              </span>
              <Button
                size="sm"
                variant="ghost"
                loading={nodeRefreshing}
                onclick={refreshReleases}
              >
                {#if !nodeRefreshing}<RefreshCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
                Refresh available versions
              </Button>
            </div>

            {#if nodeSnapshot.warning}
              <p class="release-warning" role="status">{nodeSnapshot.warning}</p>
            {/if}
            {#if nodeRefreshError}
              <div class="release-error">
                <ErrorBanner
                  title="Could not refresh Node releases"
                  code={nodeRefreshError.code}
                  message={nodeRefreshError.message}
                  onretry={refreshReleases}
                  ondismiss={() => (nodeRefreshError = null)}
                />
              </div>
            {/if}
            {#if nodeError}
              <div class="release-error">
                <ErrorBanner
                  title="Node releases are out of date"
                  code={nodeError.code}
                  message={nodeError.message}
                  onretry={() => loadReleases(true)}
                  ondismiss={() => (nodeError = null)}
                />
              </div>
            {/if}

            <div class="catalog-list">
              {#each nodeMajors as entry (entry.major)}
                {@const older = olderReleaseGroups(entry)}
                {@const latestInstalled = installedNodeVersions.has(entry.latest.version)}
                <article class="node-major" aria-labelledby={`node-major-${entry.major}`}>
                  <div class="node-major-head">
                    <div class="node-major-title">
                      <h3 id={`node-major-${entry.major}`}>Node {entry.major}</h3>
                      {#if entry.latest.lts}
                        <Badge variant="accent">LTS {entry.latest.lts}</Badge>
                      {/if}
                      {#if entry.latest.security}<Badge variant="warning">Security</Badge>{/if}
                      {#if latestInstalled}<Badge variant="accent">Installed</Badge>{/if}
                    </div>
                    <div class="node-major-actions">
                      {#if older.length > 0}
                        <Button
                          size="sm"
                          variant="ghost"
                          ariaExpanded={expandedMajors[entry.major] === true}
                          ariaControls={`node-major-${entry.major}-older`}
                          onclick={() => toggleMajor(entry.major)}
                        >
                          {expandedMajors[entry.major]
                            ? `Hide Node ${entry.major} versions`
                            : `Show all Node ${entry.major} versions`}
                        </Button>
                      {/if}
                      {#if latestInstalled}
                        <span class="installed-note">Already installed</span>
                      {:else}
                        <Button
                          size="sm"
                          variant="secondary"
                          ariaLabel={`Install Node ${entry.latest.version}`}
                          onclick={() => openReleaseInstall(entry.latest)}
                        >
                          <Download size={13} strokeWidth={2} aria-hidden="true" />
                          Install
                        </Button>
                      {/if}
                    </div>
                  </div>

                  <div class="node-release">
                    <span class="node-version mono">Node {entry.latest.version}</span>
                    <span class="release-meta">
                      {entry.latest.lts ? `LTS ${entry.latest.lts}` : 'Current'}
                      {' · '}npm {entry.latest.npm}
                      {' · '}released {formatDate(entry.latest.date)}
                      {' · '}{entry.latest.file}
                    </span>
                  </div>

                  {#if expandedMajors[entry.major] && older.length > 0}
                    <div class="node-older" id={`node-major-${entry.major}-older`}>
                      {#each older as group (group.minor)}
                        <p class="minor-label">Node {group.minor}</p>
                        {#each group.releases as release (release.version)}
                          {@const installed = installedNodeVersions.has(release.version)}
                          <div class="node-release">
                            <div class="release-copy">
                              <span class="node-version mono">Node {release.version}</span>
                              <span class="release-meta">
                                {release.lts ? `LTS ${release.lts}` : 'Current'}
                                {' · '}npm {release.npm}
                                {' · '}released {formatDate(release.date)}
                              </span>
                            </div>
                            {#if installed}
                              <Badge variant="accent">Installed</Badge>
                            {:else}
                              <Button
                                size="sm"
                                variant="secondary"
                                ariaLabel={`Install Node ${release.version}`}
                                onclick={() => openReleaseInstall(release)}
                              >
                                <Download size={12} strokeWidth={2} aria-hidden="true" />
                                Install
                              </Button>
                            {/if}
                          </div>
                        {/each}
                      {/each}
                    </div>
                  {/if}
                </article>
              {/each}
            </div>
          </div>
        {/if}

        {#if availablePHP.length > 0}
          <div class="catalog-group" aria-label="Available PHP builds">
            <div class="group-head">
              <span class="group-label">PHP</span>
              <span class="group-count mono">{availablePHP.length}</span>
            </div>
            <div class="catalog-list">
              {#each availablePHP as entry (entry.id)}
                {@const installed = isInstalled(entry)}
                <article class="catalog-row" class:installed class:busy={installingId === entry.id}>
                  <span class="catalog-icon php" aria-hidden="true">
                    {#if installed}<Boxes size={17} strokeWidth={1.8} />{:else}<Download size={17} strokeWidth={1.8} />{/if}
                  </span>
                  <div class="catalog-main">
                    <div class="catalog-title">
                      <h3 class="mono">{catalogLabel(entry)}</h3>
                      <Badge variant="info">PHP {phpLine(entry.version)}</Badge>
                      <Badge>{entry.build}</Badge>
                      {#if installed}<Badge variant="accent">Installed</Badge>{/if}
                    </div>
                    <div class="catalog-meta">
                      <span><HardDrive size={11} strokeWidth={1.8} aria-hidden="true" /> {formatBytes(entry.size)}</span>
                      <span class="checksum" title={entry.sha256}>
                        sha256 <span class="mono">{checksumPrefix(entry.sha256, 16)}</span>
                      </span>
                    </div>
                    {#if installingId === entry.id}
                      <div
                        class="install-progress"
                        role="progressbar"
                        aria-label="Installing {catalogLabel(entry)}"
                        aria-valuetext="Downloading and verifying"
                      >
                        <span></span>
                      </div>
                      <p class="install-copy">Downloading, verifying checksum and extracting…</p>
                    {/if}
                  </div>
                  <div class="catalog-actions">
                    {#if installed}
                      <span class="installed-note">Already installed</span>
                    {:else}
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={Boolean(installingId)}
                        onclick={() => (installTarget = entry)}
                      >
                        <Download size={13} strokeWidth={2} aria-hidden="true" />
                        Install
                      </Button>
                    {/if}
                  </div>
                </article>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    {/if}
  </section>

  <p class="foot-note">
    <ShieldCheck size={13} strokeWidth={1.9} aria-hidden="true" />
    Builds are downloaded over HTTPS from the official distribution server and never executed
    during installation.
  </p>
</div>

<ConfirmDialog
  open={Boolean(installTarget)}
  title={installTarget ? `Install ${catalogLabel(installTarget)}?` : 'Install runtime?'}
  description="devstack downloads the archive once, verifies its SHA-256, then extracts it under the devstack home. Existing sites are not restarted."
  confirmLabel={installTarget ? `Install ${catalogLabel(installTarget)}` : 'Install'}
  busy={Boolean(installingId)}
  onconfirm={install}
  oncancel={() => {
    if (!installingId) installTarget = null;
  }}
>
  {#snippet details()}
    {#if installTarget}
      <dl class="install-details">
        <div>
          <dt>Version</dt>
          <dd class="mono">{catalogLabel(installTarget)}</dd>
        </div>
        {#if installTarget.kind === 'php'}
          <div>
            <dt>Line</dt>
            <dd class="mono">PHP {phpLine(installTarget.version)}</dd>
          </div>
        {/if}
        <div>
          <dt>Build</dt>
          <dd class="mono">{installTarget.build}</dd>
        </div>
        <div>
          <dt>Download</dt>
          <dd class="mono">{formatBytes(installTarget.size)}</dd>
        </div>
        <div class="checksum-row">
          <dt>SHA-256</dt>
          <dd class="mono full-hash">{installTarget.sha256}</dd>
        </div>
      </dl>
    {/if}
  {/snippet}
</ConfirmDialog>

<ConfirmDialog
  open={Boolean(removeTarget)}
  title={removeTarget ? `Remove ${buildLabel(removeTarget)}?` : 'Remove runtime?'}
  description={removeTarget
    ? `The extracted build at ${removeTarget.path} will be deleted. This cannot be undone.`
    : ''}
  confirmLabel="Remove build"
  danger
  busy={removing}
  onconfirm={remove}
  oncancel={() => {
    if (!removing) removeTarget = null;
  }}
/>

<Modal
  open={Boolean(migration)}
  title={migration ? `Migrate sites off PHP ${migration.runtime.version}` : 'Migrate PHP sites'}
  description={migration
    ? `This build is still pinned by sites. Migration moves every site on PHP ${phpLine(migration.runtime.version)} to the selected build, not only the sites listed below. Removal only runs after every site migrates successfully.`
    : 'The build is still pinned by sites. Choose another installed PHP build.'}
  size="md"
  onclose={closeMigration}
>
  {#if migration}
    <div class="migration">
      <div class="migration-block">
        <p class="migration-label">Sites to move (all sites on PHP {phpLine(migration.runtime.version)})</p>
        <ul class="migration-sites" aria-label={`Sites to move (all sites on PHP ${phpLine(migration.runtime.version)})`}>
          {#each migration.sites as name (name)}
            <li class="mono">{name}</li>
          {/each}
        </ul>
        <p class="migration-note">
          Migration targets the PHP {phpLine(migration.runtime.version)} line, so every site listed
          here moves even when it pins a different revision.
        </p>
      </div>

      <Field id="migration-target" label="Target build">
        {#if migrationCandidates.length === 0}
          <p class="migration-empty">
            No other PHP build is installed. Install one from the catalog before removing this build.
          </p>
        {:else}
          <div class="select-wrap">
            <select
              id="migration-target"
              class="input mono"
              bind:value={migrationTarget}
              disabled={migrating}
            >
              {#each migrationCandidates as runtime (runtime.id)}
                <option value={runtime.id}>
                  PHP {runtime.version} (r{runtime.revision}) · line {runtime.line}
                </option>
              {/each}
            </select>
            <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
          </div>
        {/if}
      </Field>

      {#if migrationError}
        <ErrorBanner
          title="Migration failed"
          code={migrationError.code}
          message={migrationError.message}
        />
      {/if}

      {#if migrationResults}
        <div class="migration-block" aria-live="polite">
          <p class="migration-label">Migration results</p>
          <ul class="migration-results">
            {#each migrationResults as result (result.site)}
              <li class:failed={!result.ok}>
                <span class="mono">{result.site}</span>
                <span class="result-text">{result.ok ? 'Migrated' : result.error ?? 'Failed'}</span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={closeMigration} disabled={migrating}>Cancel</Button>
    <Button
      variant="primary"
      onclick={migrateAndRemove}
      loading={migrating}
      disabled={!migrationTarget || migrating}
    >
      Migrate and remove
    </Button>
  {/snippet}
</Modal>

<Modal
  open={Boolean(releaseTarget)}
  title={releaseTarget ? `Install Node ${releaseTarget.version}` : 'Install Node'}
  description="devstack downloads the official Linux x64 archive once, verifies its SHA-256, then extracts it under the devstack home. Existing sites are not restarted."
  size="md"
  onclose={closeReleaseInstall}
>
  {#if releaseTarget}
    {#if releasePreflightLoading}
      <div class="preflight-loading" aria-busy="true">
        <Skeleton width="46%" height="12px" />
        <Skeleton width="100%" height="112px" radius="8px" />
        <p class="preflight-copy" role="status">
          Loading checksum and size for Node {releaseTarget.version}…
        </p>
      </div>
    {:else if releasePreflightError}
      <ErrorBanner
        title="Could not load release details"
        code={releasePreflightError.code}
        message={releasePreflightError.message}
        onretry={() => {
          if (releaseTarget) void openReleaseInstall(releaseTarget);
        }}
      />
    {:else if releasePreflight}
      <dl class="install-details">
        <div>
          <dt>Version</dt>
          <dd class="mono">Node {releasePreflight.release.version}</dd>
        </div>
        <div>
          <dt>npm</dt>
          <dd class="mono">npm {releasePreflight.release.npm}</dd>
        </div>
        <div>
          <dt>Download</dt>
          <dd class="mono">{formatBytes(releasePreflight.entry.size)}</dd>
        </div>
        <div>
          <dt>Architecture</dt>
          <dd class="mono">{releasePreflight.entry.build}</dd>
        </div>
        <div class="checksum-row">
          <dt>SHA-256</dt>
          <dd class="mono" title={releasePreflight.entry.sha256}>
            {checksumPrefix(releasePreflight.entry.sha256, 16)}
          </dd>
        </div>
        <div>
          <dt>Released</dt>
          <dd class="mono">{formatDate(releasePreflight.release.date)}</dd>
        </div>
      </dl>

      {#if releasePreflight.release.security}
        <p class="release-warning" role="status">
          <TriangleAlert size={13} strokeWidth={2} aria-hidden="true" />
          Node {releasePreflight.release.version} includes security fixes. Review the release notes
          before pinning an older line.
        </p>
      {/if}
      {#if olderPatchWarning(releasePreflight.release)}
        <p class="release-warning" role="status">
          <TriangleAlert size={13} strokeWidth={2} aria-hidden="true" />
          {olderPatchWarning(releasePreflight.release)}
        </p>
      {/if}
    {/if}
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={closeReleaseInstall} disabled={releaseInstalling}>
      Cancel
    </Button>
    <Button
      variant="primary"
      onclick={installRelease}
      loading={releaseInstalling}
      disabled={
        !releasePreflight ||
        releasePreflightLoading ||
        Boolean(releasePreflightError) ||
        releaseInstalling
      }
    >
      Install Node {releaseTarget?.version ?? ''}
    </Button>
  {/snippet}
</Modal>

<PHPDefaultsDrawer
  open={Boolean(defaultsLine)}
  version={defaultsLine ?? '8.4'}
  versions={phpLines}
  {sites}
  onclose={() => (defaultsLine = null)}
/>

<style>
  .intro {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px 16px;
    margin-bottom: 22px;
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
    box-shadow: var(--shadow-sm);
  }

  .intro-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 34px;
    height: 34px;
    border: 1px solid var(--accent-border);
    border-radius: 8px;
    background: var(--accent-soft);
    color: var(--success-fg);
  }

  .intro-copy {
    flex: 1;
    min-width: 0;
  }

  .intro h2 {
    font-size: 13.5px;
    font-weight: 600;
  }

  .intro p {
    margin-top: 3px;
    max-width: 780px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .intro-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    flex: none;
  }

  .banner {
    margin-bottom: 14px;
  }

  .section {
    margin-bottom: 28px;
    scroll-margin-top: calc(var(--topbar-h) + 12px);
  }

  .section-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 9px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
  }

  .eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .section h2 {
    margin-top: 1px;
    font-size: 17px;
    font-weight: 600;
    letter-spacing: -0.015em;
  }

  .section-count {
    color: var(--muted-2);
    font-size: 11px;
  }

  .runtime-groups,
  .catalog-groups {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .runtime-group,
  .catalog-group {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .group-head {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 1px 2px;
  }

  .group-label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .group-count {
    padding: 0 5px;
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted-2);
    font-size: 9px;
  }

  .line-block {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .line-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
    padding: 0 2px;
  }

  .line-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
    min-width: 0;
  }

  .line-name {
    color: var(--fg);
    font-size: 12px;
    font-weight: 600;
  }

  .line-meta {
    color: var(--muted-2);
    font-size: 10px;
  }

  .line-actions {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .update-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 6px;
    border-radius: 5px;
    color: var(--info-fg);
    font-size: 10px;
    font-weight: 500;
  }

  .update-link:hover {
    background: var(--info-soft);
  }

  .line-note {
    padding: 0 2px;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.5;
  }

  .line-note a {
    color: var(--info-fg);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .node-group-head {
    flex-wrap: wrap;
    row-gap: 6px;
  }

  .node-source {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
    margin-left: 2px;
  }

  .release-age {
    color: var(--muted-2);
    font-size: 9.5px;
    white-space: nowrap;
  }

  .release-warning {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin: 0 2px;
    padding: 7px 9px;
    border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .release-warning :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .release-error {
    margin: 0 2px;
  }

  .node-major {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 11px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-sm);
  }

  .node-major-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
  }

  .node-major-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .node-major-title h3 {
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.015em;
  }

  .node-major-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
  }

  .node-release {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    min-height: 30px;
    padding: 3px 0;
    border-top: 1px solid var(--border);
  }

  .node-version {
    color: var(--fg);
    font-size: 12px;
    font-weight: 600;
  }

  .release-copy {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }

  .release-meta {
    color: var(--muted-2);
    font-size: 9.5px;
    line-height: 1.45;
  }

  .node-older {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 5px 0 1px;
  }

  .minor-label {
    margin: 5px 0 1px;
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .preflight-loading {
    display: flex;
    flex-direction: column;
    gap: 9px;
  }

  .preflight-copy {
    color: var(--muted);
    font-size: 11px;
  }

  .runtime-list,
  .catalog-list {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .runtime-row {
    display: grid;
    grid-template-columns: 40px minmax(0, 1fr) auto;
    align-items: start;
    gap: 12px;
    padding: 13px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-sm);
  }

  .runtime-row.in-use {
    border-color: color-mix(in srgb, var(--accent) 24%, var(--border));
  }

  .runtime-icon,
  .catalog-icon {
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .runtime-row.in-use .runtime-icon {
    color: var(--success-fg);
    border-color: var(--accent-border);
    background: var(--accent-soft);
  }

  .runtime-icon.php,
  .catalog-icon.php {
    color: var(--info-fg);
    border-color: color-mix(in srgb, var(--info) 28%, var(--border));
    background: var(--info-soft);
  }

  .runtime-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  h3 {
    font-size: 14.5px;
    font-weight: 600;
    letter-spacing: -0.02em;
  }

  .usage-pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--success-fg);
    font-size: 10px;
    font-weight: 500;
  }

  .usage-pill .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  .runtime-path {
    margin-top: 2px;
    color: var(--muted);
    font-size: 10px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .runtime-meta,
  .catalog-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    margin-top: 5px;
    color: var(--muted-2);
    font-size: 9.5px;
  }

  .checksum {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    min-width: 0;
  }

  .runtime-actions {
    display: flex;
    align-items: center;
    padding-top: 2px;
  }

  .usage-note {
    margin-top: 6px;
    color: var(--warning-fg);
    font-size: 10px;
    line-height: 1.45;
  }

  .catalog-row {
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr) auto;
    align-items: center;
    gap: 12px;
    padding: 11px 14px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
  }

  .catalog-row.installed {
    opacity: 0.76;
  }

  .catalog-icon {
    width: 36px;
    height: 36px;
    border-radius: 7px;
  }

  .catalog-row.busy {
    border-color: var(--accent-border);
  }

  .catalog-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .catalog-title h3 {
    font-size: 13.5px;
  }

  .catalog-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: 96px;
  }

  .installed-note {
    color: var(--muted-2);
    font-size: 10px;
    white-space: nowrap;
  }

  .install-progress {
    position: relative;
    height: 3px;
    margin-top: 8px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--surface-3);
  }

  .install-progress span {
    position: absolute;
    inset: 0 auto 0 0;
    width: 40%;
    border-radius: inherit;
    background: var(--accent);
    animation: indeterminate 1.2s var(--ease) infinite;
  }

  .install-copy {
    margin-top: 5px;
    color: var(--muted);
    font-size: 10px;
  }

  @keyframes indeterminate {
    0% {
      left: -40%;
    }
    100% {
      left: 100%;
    }
  }

  .empty-box {
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    background: var(--surface);
  }

  .foot-note {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    max-width: 760px;
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.5;
  }

  .foot-note :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .skeleton {
    align-items: center;
  }

  .skeleton-copy {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .skeleton-action {
    display: flex;
    align-items: center;
  }

  .install-details {
    display: flex;
    flex-direction: column;
    margin: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .install-details > div {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 10px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
  }

  .install-details > div:first-child {
    border-top: 0;
  }

  .install-details dt {
    color: var(--muted);
    font-size: 10.5px;
  }

  .install-details dd {
    margin: 0;
    color: var(--fg);
    font-size: 10.5px;
    text-align: right;
    overflow-wrap: anywhere;
  }

  .checksum-row dd {
    font-size: 9px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }

  .select-wrap {
    position: relative;
  }

  .select-wrap :global(select) {
    appearance: none;
    width: 100%;
    padding-right: 32px;
  }

  .select-wrap :global(svg) {
    position: absolute;
    top: 50%;
    right: 10px;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .migration {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .migration-block {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .migration-label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .migration-sites {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .migration-sites li {
    padding: 2px 6px;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface-2);
    color: var(--fg);
    font-size: 10.5px;
  }

  .migration-note {
    margin-top: 2px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .migration-empty {
    padding: 9px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 35%, var(--border));
    border-radius: var(--radius);
    background: var(--warning-soft);
    color: var(--warning-fg);
    font-size: 11px;
    line-height: 1.45;
  }

  .migration-results {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .migration-results li {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 6px 9px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--success-fg);
    font-size: 10.5px;
  }

  .migration-results li:first-child {
    border-top: 0;
  }

  .migration-results li.failed {
    color: var(--danger-fg);
  }

  .result-text {
    text-align: right;
    overflow-wrap: anywhere;
  }

  @media (max-width: 900px) {
    .intro {
      flex-wrap: wrap;
    }

    .intro-chips {
      width: 100%;
      padding-left: 46px;
    }
  }

  @media (max-width: 640px) {
    .skeleton-action {
      display: none;
    }

    .runtime-row {
      grid-template-columns: 36px minmax(0, 1fr);
    }

    .runtime-icon {
      width: 36px;
      height: 36px;
    }

    .runtime-actions {
      grid-column: 1 / -1;
      justify-content: stretch;
      padding-top: 0;
    }

    .runtime-actions :global(.btn) {
      width: 100%;
    }

    .catalog-row {
      grid-template-columns: 32px minmax(0, 1fr);
    }

    .catalog-icon {
      width: 32px;
      height: 32px;
    }

    .catalog-actions {
      grid-column: 1 / -1;
      justify-content: stretch;
      min-width: 0;
    }

    .catalog-actions :global(.btn) {
      width: 100%;
    }

    .installed-note {
      padding: 4px 0;
    }

    .runtime-path {
      white-space: normal;
      overflow-wrap: anywhere;
    }

    .intro-chips {
      padding-left: 0;
    }

    .line-head {
      align-items: flex-start;
      flex-direction: column;
    }

    .node-major-head {
      align-items: flex-start;
      flex-direction: column;
    }
  }
</style>
