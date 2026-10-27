<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Activity,
    Cpu,
    Gauge,
    MemoryStick,
    Play,
    RefreshCw,
    RotateCcw,
    ShieldAlert,
    TriangleAlert,
  } from 'lucide-svelte';
  import * as api from '../api';
  import { ApiError, describeError } from '../errors';
  import { addToast, toastError } from '../stores';
  import type {
    BenchmarkRun,
    PerformanceApplyResult,
    PerformancePreview,
    PerformanceProfile,
    PerformanceResetResult,
    PerformanceState,
    PerformanceValues,
    Site,
  } from '../types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Card from './Card.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import EmptyState from './EmptyState.svelte';
  import ErrorBanner from './ErrorBanner.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';

  const profiles: { value: PerformanceProfile; label: string; hint: string }[] = [
    { value: 'balanced', label: 'Balanced', hint: 'Conservative local development' },
    { value: 'throughput', label: 'Throughput', hint: 'Use safe spare capacity' },
    { value: 'custom', label: 'Custom', hint: 'Edit validated values' },
  ];

  let performance = $state<PerformanceState | null>(null);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let preview = $state<PerformancePreview | null>(null);
  let previewLoading = $state(false);
  let previewError = $state<{ code: string; message: string } | null>(null);
  let profileBusy = $state(false);
  let customOpen = $state(false);
  let customText = $state('');
  let customError = $state<{ code: string; message: string } | null>(null);
  let applyOpen = $state(false);
  let resetOpen = $state(false);
  let actionBusy = $state(false);
  let actionError = $state<{ code: string; message: string } | null>(null);
  let hostTuning = $state(false);
  let result = $state<PerformanceApplyResult | PerformanceResetResult | null>(null);

  let sites = $state<Site[]>([]);
  let benchmarks = $state<BenchmarkRun[]>([]);
  let benchmarkLoading = $state(true);
  let benchmarkError = $state<{ code: string; message: string } | null>(null);
  let benchSite = $state('');
  let benchDuration = $state(10);
  let benchConnections = $state(32);
  let benchBusy = $state(false);
  let selectedRun = $state<BenchmarkRun | null>(null);

  const activeProfile = $derived(performance?.profile ?? 'balanced');
  const resources = $derived(performance?.resources ?? {});
  const values = $derived(performance?.values ?? {});
  const nginx = $derived(section(values, 'nginx'));
  const apache = $derived(section(values, 'apache'));
  const php = $derived(section(values, 'php'));
  const host = $derived(section(values, 'host'));
  const hostTuningState = $derived(performance?.host_tuning ?? { applied: false });
  const hostTuningAvailable = $derived(performance?.host_tuning?.available !== false);
  const appliedAt = $derived(performance?.applied?.applied_at ?? '');
  const benchSiteName = $derived(
    sites.find((site) => site.name === benchSite || site.id === benchSite)?.name ?? benchSite,
  );
  const previewWarnings = $derived([
    ...new Set([
      ...(preview?.warnings ?? []),
      ...(preview?.host_tuning?.warnings ?? []),
    ]),
  ]);

  function section(source: PerformanceValues, key: string): Record<string, unknown> {
    const value = source[key];
    return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  }

  function numberValue(source: Record<string, unknown>, key: string): string {
    const value = source[key];
    return typeof value === 'number' ? value.toLocaleString() : '—';
  }

  function gib(bytes: unknown): string {
    if (typeof bytes !== 'number' || !Number.isFinite(bytes)) return '—';
    return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
  }

  function timestamp(value: string | undefined): string {
    if (!value || value.startsWith('0001-01-01')) return 'Never';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
  }

  function ms(value: number | undefined): string {
    if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
    return `${value.toFixed(2)} ms`;
  }

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      performance = await api.getPerformance();
    } catch (caught) {
      error = describeError(caught);
    } finally {
      loading = false;
    }
  }

  async function loadBenchmarks(initial = false): Promise<void> {
    if (initial) benchmarkLoading = true;
    benchmarkError = null;
    try {
      benchmarks = await api.listBenchmarks();
    } catch (caught) {
      benchmarkError = describeError(caught);
    } finally {
      benchmarkLoading = false;
    }
  }

  async function loadSites(): Promise<void> {
    try {
      sites = await api.listSites();
      if (!benchSite && sites.length > 0) benchSite = sites[0].name;
    } catch {
      // Benchmark form still explains that a registered site is required.
    }
  }

  onMount(() => {
    void load(true);
    void loadBenchmarks(true);
    void loadSites();
    const refresh = () => {
      void load();
      void loadBenchmarks();
    };
    window.addEventListener('devstack:refresh', refresh);
    return () => window.removeEventListener('devstack:refresh', refresh);
  });

  async function chooseProfile(profile: PerformanceProfile): Promise<void> {
    if (profileBusy || profile === activeProfile) return;
    if (profile === 'custom') {
      customOpen = true;
      customError = null;
      preview = null;
      previewError = null;
      customText = JSON.stringify(performance?.custom ?? performance?.values ?? {}, null, 2);
      return;
    }
    profileBusy = true;
    preview = null;
    try {
      performance = await api.setPerformanceProfile(profile);
      customOpen = false;
      addToast({
        kind: 'success',
        title: `${profile === 'balanced' ? 'Balanced' : 'Throughput'} profile selected`,
        message: 'Preview the generated values before applying them.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not select profile', `${described.code}: ${described.message}`);
    } finally {
      profileBusy = false;
    }
  }

  async function saveCustom(): Promise<void> {
    if (profileBusy) return;
    let parsed: PerformanceValues;
    try {
      parsed = JSON.parse(customText) as PerformanceValues;
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Custom values must be a JSON object.');
      }
    } catch (caught) {
      customError = {
        code: 'INVALID_REQUEST',
        message: caught instanceof Error ? caught.message : 'Custom values must be valid JSON.',
      };
      return;
    }
    profileBusy = true;
    customError = null;
    preview = null;
    try {
      performance = await api.setPerformanceProfile('custom', parsed);
      customOpen = false;
      addToast({
        kind: 'success',
        title: 'Custom profile saved',
        message: 'Preview the generated include and host tuning diff before applying.',
      });
    } catch (caught) {
      customError = describeError(caught);
    } finally {
      profileBusy = false;
    }
  }

  async function loadPreview(): Promise<void> {
    if (previewLoading) return;
    previewLoading = true;
    previewError = null;
    try {
      preview = await api.previewPerformance();
    } catch (caught) {
      previewError = describeError(caught);
    } finally {
      previewLoading = false;
    }
  }

  async function apply(): Promise<void> {
    if (actionBusy) return;
    actionBusy = true;
    actionError = null;
    try {
      result = await api.applyPerformance(hostTuning);
      applyOpen = false;
      preview = null;
      await load();
      addToast({
        kind: 'success',
        title: result.host_tuning ? 'Performance applied with host tuning' : 'Performance applied',
        message: result.backup_dir
          ? `Backups are under ${result.backup_dir}. Review warnings for any skipped work.`
          : 'Managed includes and PHP ini changes were applied. Review warnings for any skipped work.',
      });
    } catch (caught) {
      const partial = partialPerformanceResult(caught);
      if (partial) result = partial as PerformanceApplyResult;
      actionError = describeError(caught);
      toastError('Could not apply performance', `${actionError.code}: ${actionError.message}`);
    } finally {
      actionBusy = false;
    }
  }

  function partialPerformanceResult(value: unknown): PerformanceApplyResult | PerformanceResetResult | null {
    if (!(value instanceof ApiError) || !value.details || typeof value.details !== 'object') {
      return null;
    }
    const result = (value.details as Record<string, unknown>).result;
    return result && typeof result === 'object'
      ? (result as PerformanceApplyResult | PerformanceResetResult)
      : null;
  }

  async function reset(): Promise<void> {
    if (actionBusy) return;
    actionBusy = true;
    actionError = null;
    try {
      result = await api.resetPerformance();
      resetOpen = false;
      preview = null;
      await load();
      addToast({
        kind: 'success',
        title: 'Performance reset',
        message: 'Host files and managed includes were restored.',
      });
    } catch (caught) {
      const partial = partialPerformanceResult(caught);
      if (partial) result = partial as PerformanceResetResult;
      actionError = describeError(caught);
      toastError('Could not reset performance', `${actionError.code}: ${actionError.message}`);
    } finally {
      actionBusy = false;
    }
  }

  async function runBenchmark(): Promise<void> {
    if (benchBusy || !benchSite) return;
    benchBusy = true;
    benchmarkError = null;
    try {
      const run = await api.runBenchmark({
        site: benchSite,
        duration_s: benchDuration,
        connections: benchConnections,
      });
      benchmarks = [run, ...benchmarks];
      const measured = run.measured;
      addToast({
        kind: run.status === 'completed' ? 'success' : 'error',
        title: run.status === 'completed' ? 'Benchmark complete' : `Benchmark ${run.status}`,
        message: measured
          ? `${measured.requests_per_second.toFixed(0)} req/s measured with ${run.tool} ${run.tool_version}.`
          : run.error ?? 'The run produced no measurement.',
      });
    } catch (caught) {
      benchmarkError = describeError(caught);
      toastError('Benchmark failed', `${benchmarkError.code}: ${benchmarkError.message}`);
    } finally {
      benchBusy = false;
    }
  }
</script>

<div class="performance-stack">
  <Card
    title="Performance"
    subtitle="Resource-aware web server, PHP-FPM, and optional host tuning"
    headerWrap
    className="performance-card"
  >
    {#snippet actions()}
      <Badge variant={activeProfile === 'throughput' ? 'accent' : 'neutral'}>
        {activeProfile}
      </Badge>
      <Button
        size="sm"
        variant="ghost"
        loading={loading}
        disabled={loading || profileBusy}
        onclick={() => load()}
      >
        {#if !loading}<RefreshCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
        Reload
      </Button>
      <Button
        size="sm"
        variant="secondary"
        loading={previewLoading}
        disabled={previewLoading || !performance || customOpen}
        onclick={loadPreview}
      >
        {#if !previewLoading}<Gauge size={12} strokeWidth={2} aria-hidden="true" />{/if}
        Preview
      </Button>
    {/snippet}

    {#if loading && !performance}
      <div class="performance-loading" aria-busy="true" aria-label="Loading performance profile">
        <Skeleton width="46%" height="14px" />
        <Skeleton width="100%" height="70px" />
        <Skeleton width="80%" height="44px" />
      </div>
    {:else if error && !performance}
      <ErrorBanner
        title="Could not load performance"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
      />
    {:else if performance}
      {#if error}
        <div class="inline-error">
          <ErrorBanner
            title="Performance status is out of date"
            code={error.code}
            message={error.message}
            onretry={() => load()}
            ondismiss={() => (error = null)}
          />
        </div>
      {/if}

      <div class="resource-grid">
        <div class="resource">
          <Cpu size={14} strokeWidth={1.9} aria-hidden="true" />
          <div>
            <span>CPU</span>
            <strong>{numberValue(resources, 'cpu_count')} cores</strong>
            <small>
              {typeof resources.cpu_quota_cpus === 'number' && resources.cpu_quota_cpus > 0
                ? `${resources.cpu_quota_cpus} cgroup quota`
                : 'No cgroup quota'}
            </small>
          </div>
        </div>
        <div class="resource">
          <MemoryStick size={14} strokeWidth={1.9} aria-hidden="true" />
          <div>
            <span>Memory</span>
            <strong>{gib(resources.memory_available_bytes)} available</strong>
            <small>{gib(resources.memory_total_bytes)} total</small>
          </div>
        </div>
        <div class="resource">
          <Activity size={14} strokeWidth={1.9} aria-hidden="true" />
          <div>
            <span>Open files</span>
            <strong>{numberValue(resources, 'nofile_hard')} hard</strong>
            <small>{numberValue(resources, 'nofile_soft')} soft</small>
          </div>
        </div>
        <div class="resource">
          <ShieldAlert size={14} strokeWidth={1.9} aria-hidden="true" />
          <div>
            <span>Host tuning</span>
            {#if !hostTuningAvailable}
              <strong>Disabled</strong>
              <small>{performance?.host_tuning?.error || 'Unavailable on Windows'}</small>
            {:else}
              <strong>{hostTuningState.applied ? 'Applied' : 'Not applied'}</strong>
              <small>{appliedAt ? `Last apply ${timestamp(appliedAt)}` : 'Explicit opt-in'}</small>
            {/if}
          </div>
        </div>
      </div>

      {#if !hostTuningAvailable}
        <div class="host-tuning-banner" role="status">
          <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
          <span>{performance?.host_tuning?.error || 'Host sysctl and limits tuning are disabled on Windows.'}</span>
        </div>
      {/if}

      <div class="profile-row" role="group" aria-label="Performance profile">
        {#each profiles as profile (profile.value)}
          <button
            type="button"
            class="profile-option"
            class:active={activeProfile === profile.value}
            aria-pressed={activeProfile === profile.value}
            disabled={profileBusy}
            onclick={() => chooseProfile(profile.value)}
          >
            <span>{profile.label}</span>
            <small>{profile.hint}</small>
          </button>
        {/each}
      </div>

      {#if customOpen}
        <div class="custom-editor">
          <div class="custom-head">
            <h3>Custom profile values</h3>
            <p>Validated against the same safety caps on every preview and apply.</p>
          </div>
          {#if customError}
            <ErrorBanner
              title="Invalid custom values"
              code={customError.code}
              message={customError.message}
              ondismiss={() => (customError = null)}
            />
          {/if}
          <label class="custom-label" for="performance-custom">Custom JSON</label>
          <textarea
            id="performance-custom"
            class="input mono custom-json"
            rows="12"
            spellcheck="false"
            bind:value={customText}
          ></textarea>
          <div class="custom-actions">
            <Button size="sm" variant="primary" loading={profileBusy} onclick={saveCustom}>
              Save custom profile
            </Button>
            <Button size="sm" variant="ghost" onclick={() => (customOpen = false)}>Cancel</Button>
          </div>
        </div>
      {/if}

      <dl class="value-grid">
        <div>
          <dt>nginx workers</dt>
          <dd>{numberValue(nginx, 'workers')} · {numberValue(nginx, 'worker_connections')} conn</dd>
        </div>
        <div>
          <dt>Apache workers</dt>
          <dd>{numberValue(apache, 'max_request_workers')} · {numberValue(apache, 'server_limit')} slots</dd>
        </div>
        <div>
          <dt>PHP OPcache</dt>
          <dd>{numberValue(section(php, 'opcache'), 'memory_mb')} MB · realpath and validate-timestamps applied</dd>
        </div>
        <div>
          <dt>Host limits</dt>
          <dd>{numberValue(host, 'file_max')} file-max · {numberValue(host, 'somaxconn')} somaxconn</dd>
        </div>
      </dl>
      <p class="value-note">
        Per-site PHP-FPM <span class="mono">pm.*</span> pool sizing, OPcache, and realpath
        ini settings are applied to each running PHP master through the site
        validate/swap flow when you apply this profile.
      </p>

      {#if previewError}
        <div class="inline-error">
          <ErrorBanner
            title="Could not preview performance"
            code={previewError.code}
            message={previewError.message}
            onretry={loadPreview}
            ondismiss={() => (previewError = null)}
          />
        </div>
      {/if}

      {#if preview}
        <div class="preview">
          <div class="preview-head">
            <div>
              <h3>Preview</h3>
              <p>Nothing has been written. Apply explicitly confirms the change.</p>
            </div>
            {#if preview.profile}<Badge>{preview.profile}</Badge>{/if}
          </div>
          {#if previewWarnings.length > 0}
            <ul class="warnings" role="status">
              {#each previewWarnings as warning, index (`${index}:${warning}`)}
                <li><TriangleAlert size={12} strokeWidth={2} aria-hidden="true" />{warning}</li>
              {/each}
            </ul>
          {/if}
          {#if preview.host_tuning?.files?.some((file) => file.diff)}
            <details class="preview-details" open>
              <summary>Host tuning diff</summary>
              {#each preview.host_tuning.files as file (file.path)}
                {#if file.diff}
                  <div class="preview-file">
                    <p class="mono">{file.path}</p>
                    <pre>{file.diff}</pre>
                  </div>
                {/if}
              {/each}
            </details>
          {/if}
          {#if preview.managed_files && preview.managed_files.length > 0}
            <details class="preview-details">
              <summary>Managed include files ({preview.managed_files.length})</summary>
              {#each preview.managed_files as file (file.path)}
                <div class="preview-file">
                  <p class="mono">{file.path}{#if file.active} · active{/if}</p>
                  <pre>{file.content ?? ''}</pre>
                </div>
              {/each}
            </details>
          {/if}
        </div>
      {/if}

      {#if actionError}
        <div class="inline-error">
          <ErrorBanner
            title="Performance action failed"
            code={actionError.code}
            message={actionError.message}
            ondismiss={() => (actionError = null)}
          />
        </div>
      {/if}

      {#if result}
        <div class="result" role="status">
          <div class="result-head">
            <h3>
              {'reset' in result
                ? result.reset
                  ? 'Reset complete'
                  : 'Reset failed'
                : result.applied
                  ? 'Apply complete'
                  : 'Apply failed'}
            </h3>
            {#if 'host_tuning' in result && result.host_tuning}
              <Badge variant="accent">Host tuned</Badge>
            {/if}
          </div>
          {#if result.steps && result.steps.length > 0}
            <ul>
              {#each result.steps as step (step.name)}
                <li class:failed={step.status === 'failed' || step.status === 'rolled_back'}>
                  <span>{step.name}</span>
                  <span>{step.detail ?? step.status}</span>
                </li>
              {/each}
            </ul>
          {/if}
          {#if result.warnings && result.warnings.length > 0}
            <ul class="result-warnings">
              {#each result.warnings as warning, index (`${index}:${warning}`)}
                <li>{warning}</li>
              {/each}
            </ul>
          {/if}
          {#if result.backup_dir}
            <p class="result-path mono">Backups: {result.backup_dir}</p>
          {/if}
        </div>
      {/if}

      <div class="action-row">
        <Button
          size="sm"
          variant="primary"
          disabled={!preview || actionBusy}
          onclick={() => {
            hostTuning = false;
            applyOpen = true;
          }}
        >
          <Play size={12} strokeWidth={2} aria-hidden="true" />
          Apply profile
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={actionBusy}
          onclick={() => (resetOpen = true)}
        >
          <RotateCcw size={12} strokeWidth={2} aria-hidden="true" />
          Reset
        </Button>
        <span class="action-hint">
          {preview ? 'Preview loaded — review before applying.' : 'Preview to unlock apply.'}
        </span>
      </div>
    {/if}
  </Card>

  <Card
    title="Benchmarks"
    subtitle="Measured HTTP throughput against a registered site"
    headerWrap
    className="benchmark-card"
  >
    {#snippet actions()}
      <Badge variant={benchmarks.length > 0 ? 'info' : 'neutral'}>
        {benchmarks.length} run{benchmarks.length === 1 ? '' : 's'}
      </Badge>
    {/snippet}

    <div class="benchmark-form">
      <Field id="benchmark-site" label="Site">
        <select id="benchmark-site" class="input" bind:value={benchSite} disabled={benchBusy}>
          {#if sites.length === 0}<option value="">No registered sites</option>{/if}
          {#each sites as site (site.id)}
            <option value={site.name}>{site.name} · {site.domain}</option>
          {/each}
        </select>
      </Field>
      <Field id="benchmark-duration" label="Duration (s)">
        <input
          id="benchmark-duration"
          class="input mono"
          type="number"
          min="1"
          max="3600"
          aria-describedby="benchmark-limits"
          bind:value={benchDuration}
          disabled={benchBusy}
        />
      </Field>
      <Field id="benchmark-connections" label="Connections">
        <input
          id="benchmark-connections"
          class="input mono"
          type="number"
          min="1"
          max="100000"
          aria-describedby="benchmark-limits"
          bind:value={benchConnections}
          disabled={benchBusy}
        />
      </Field>
      <div class="benchmark-action">
        <Button
          size="sm"
          variant="secondary"
          loading={benchBusy}
          disabled={benchBusy || !benchSite}
          onclick={runBenchmark}
        >
          {#if !benchBusy}<Play size={12} strokeWidth={2} aria-hidden="true" />{/if}
          Run benchmark
        </Button>
      </div>
    </div>
    <p class="benchmark-limits" id="benchmark-limits">
      Allowed ranges: duration 1–3600 seconds · connections 1–100000.
    </p>
    <p class="benchmark-note">
      Runs are measured by the daemon's built-in Go HTTP generator against
      {benchSiteName || 'the selected site'} and stored under <span class="mono">logs/performance/</span>.
      No RPS is estimated or synthesized.
    </p>

    {#if benchmarkError}
      <div class="inline-error">
        <ErrorBanner
          title="Could not run benchmark"
          code={benchmarkError.code}
          message={benchmarkError.message}
          ondismiss={() => (benchmarkError = null)}
        />
      </div>
    {/if}

    {#if benchmarkLoading && benchmarks.length === 0}
      <div class="benchmark-loading" aria-busy="true" aria-label="Loading benchmarks">
        <Skeleton width="100%" height="44px" />
        <Skeleton width="92%" height="44px" />
      </div>
    {:else if benchmarks.length === 0}
      <EmptyState
        compact
        icon={Activity}
        title="No benchmark runs yet"
        description="Run a benchmark to record measured requests per second, latency, and errors. Results only appear after a real run."
      />
    {:else}
      <div class="benchmark-table-wrap">
        <table class="benchmark-table">
          <thead>
            <tr>
              <th>Started</th>
              <th>Site</th>
              <th>Profile</th>
              <th>Status</th>
              <th>RPS</th>
              <th>p95</th>
              <th>Errors</th>
              <th>Tool</th>
              <th><span class="sr-only">Details</span></th>
            </tr>
          </thead>
          <tbody>
            {#each benchmarks as run (run.id)}
              <tr>
                <td>{timestamp(run.started_at)}</td>
                <td>{run.site}</td>
                <td><Badge>{run.profile ?? '—'}</Badge></td>
                <td>
                  <Badge variant={run.status === 'completed' ? 'accent' : 'danger'}>
                    {run.status}
                  </Badge>
                </td>
                <td class="mono">{run.measured ? Math.round(run.measured.requests_per_second).toLocaleString() : '—'}</td>
                <td class="mono">{run.measured ? ms(run.measured.p95_ms) : '—'}</td>
                <td class:bad={(run.measured?.errors ?? 0) > 0}>
                  {(run.measured?.errors ?? 0).toLocaleString()}
                </td>
                <td class="tool-cell">{run.tool} {run.tool_version}</td>
                <td><Button size="sm" variant="ghost" onclick={() => (selectedRun = run)}>View</Button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</div>

<ConfirmDialog
  open={applyOpen}
  title="Apply performance profile?"
  description="The daemon writes managed includes, validates them, reloads only nginx/Apache and PHP-FPM, and keeps a backup. Host tuning is a separate global change."
  confirmLabel="Apply profile"
  requireText="APPLY"
  busy={actionBusy}
  onconfirm={apply}
  oncancel={() => (applyOpen = false)}
>
  {#snippet details()}
    <div class="apply-details">
      <p>
        Profile: <strong>{activeProfile}</strong>. A backup is written before any managed file
        changes. Validation or reload failure rolls the changed files back.
      </p>
      <label class="host-toggle">
        <input type="checkbox" bind:checked={hostTuning} disabled={!hostTuningAvailable} />
        <span>
          {hostTuningAvailable
            ? 'Also apply host-wide sysctl and limits tuning through the privileged helper.'
            : (performance?.host_tuning?.error || 'Host tuning is unavailable on Windows or because the privileged helper is not available.')}
          <small>
            {hostTuningAvailable
              ? 'This affects the whole machine and can be reset later.'
              : 'Managed server and PHP includes can still be applied.'}
          </small>
        </span>
      </label>
      {#if hostTuning}
        <p class="host-warning" role="status">
          <TriangleAlert size={13} strokeWidth={2} aria-hidden="true" />
          Host tuning changes kernel and login limits globally. The helper backs up, verifies,
          and can restore the previous files.
        </p>
      {/if}
    </div>
  {/snippet}
</ConfirmDialog>

<ConfirmDialog
  open={resetOpen}
  title="Reset performance?"
  description="Restores the previous host tuning files and replaces the managed performance includes with safe empty snippets."
  confirmLabel="Reset performance"
  requireText="RESET"
  danger
  busy={actionBusy}
  onconfirm={reset}
  oncancel={() => (resetOpen = false)}
/>

<Modal
  open={selectedRun !== null}
  title={selectedRun ? `Benchmark ${selectedRun.id}` : 'Benchmark'}
  description={selectedRun ? `${selectedRun.site} · ${selectedRun.tool}` : undefined}
  size="md"
  onclose={() => (selectedRun = null)}
>
  {#if selectedRun}
    <dl class="run-detail">
      <div><dt>Target</dt><dd class="mono break">{selectedRun.target ?? '—'}</dd></div>
      <div><dt>Started</dt><dd>{timestamp(selectedRun.started_at)}</dd></div>
      <div><dt>Duration</dt><dd class="mono">{selectedRun.duration_s}s · {selectedRun.connections} connections</dd></div>
      <div><dt>Status</dt><dd><Badge variant={selectedRun.status === 'completed' ? 'accent' : 'danger'}>{selectedRun.status}</Badge></dd></div>
      <div><dt>Requests/sec</dt><dd class="mono">{selectedRun.measured ? Math.round(selectedRun.measured.requests_per_second).toLocaleString() : '—'}</dd></div>
      <div><dt>Latency p50 / p95 / p99</dt><dd class="mono">{selectedRun.measured ? `${ms(selectedRun.measured.p50_ms)} / ${ms(selectedRun.measured.p95_ms)} / ${ms(selectedRun.measured.p99_ms)}` : '—'}</dd></div>
      <div><dt>Errors</dt><dd class:bad={(selectedRun.measured?.errors ?? 0) > 0}>{selectedRun.measured?.errors ?? 0}</dd></div>
      {#if selectedRun.error}<div><dt>Error</dt><dd class="bad break">{selectedRun.error}</dd></div>{/if}
      <div><dt>Result directory</dt><dd class="mono break">{selectedRun.directory}</dd></div>
      <div><dt>Raw output</dt><dd class="mono break">{selectedRun.raw_file}</dd></div>
      {#if selectedRun.limitations && selectedRun.limitations.length > 0}
        <div><dt>Limitations</dt><dd>{selectedRun.limitations.join(' · ')}</dd></div>
      {/if}
    </dl>
    <p class="run-disclaimer">
      These values are the measured output of the named tool. devstack never fills a missing
      run with estimated throughput.
    </p>
  {/if}
</Modal>

<style>
  .performance-stack {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .performance-loading,
  .benchmark-loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .inline-error {
    margin-bottom: 10px;
  }

  .resource-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 9px;
  }

  .resource {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    min-width: 0;
  }

  .resource :global(svg) {
    flex: none;
    margin-top: 2px;
    color: var(--accent);
  }

  .resource div {
    min-width: 0;
  }

  .resource span {
    display: block;
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .resource strong {
    display: block;
    font-size: 12px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }

  .resource small {
    display: block;
    color: var(--muted);
    font-size: 9.5px;
    line-height: 1.35;
  }

  .host-tuning-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    border-radius: var(--radius);
    background: var(--warning-bg, rgba(234, 179, 8, 0.08));
    border: 1px solid var(--warning-border, rgba(234, 179, 8, 0.25));
    color: var(--warning-fg, #b45309);
    font-size: 11.5px;
    line-height: 1.4;
    margin-top: 10px;
  }

  .host-tuning-banner :global(svg) {
    flex-shrink: 0;
  }

  .profile-row {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
    margin-top: 12px;
  }

  .profile-option {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    text-align: left;
    color: var(--fg);
  }

  .profile-option:hover:not(:disabled) {
    border-color: var(--border-strong);
    background: var(--surface-2);
  }

  .profile-option.active {
    border-color: var(--accent);
    box-shadow: inset 0 0 0 1px var(--accent);
    background: var(--accent-soft);
  }

  .profile-option span {
    font-size: 11.5px;
    font-weight: 600;
  }

  .profile-option small {
    color: var(--muted);
    font-size: 9.5px;
  }

  .value-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1px;
    margin: 12px 0 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--border);
    overflow: hidden;
  }

  .value-grid > div {
    padding: 8px 10px;
    background: var(--surface);
  }

  .value-grid dt {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .value-grid dd {
    margin-top: 1px;
    font-family: var(--font-mono);
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .value-note {
    margin-top: 7px;
    color: var(--warning-fg);
    font-size: 10px;
    line-height: 1.5;
  }

  .custom-editor {
    margin-top: 12px;
    padding: 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .custom-head h3 {
    font-size: 12px;
    font-weight: 600;
  }

  .custom-head p {
    margin-top: 2px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .custom-label {
    display: block;
    margin: 9px 0 5px;
    font-size: 11px;
    font-weight: 500;
  }

  .custom-json {
    width: 100%;
    min-height: 190px;
    resize: vertical;
    line-height: 1.55;
  }

  .custom-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 8px;
  }

  .preview {
    margin-top: 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
    overflow: hidden;
  }

  .preview-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
  }

  .preview-head h3 {
    font-size: 12px;
    font-weight: 600;
  }

  .preview-head p {
    margin-top: 2px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .warnings {
    display: flex;
    flex-direction: column;
    gap: 5px;
    margin: 0;
    padding: 9px 12px;
    list-style: none;
    border-bottom: 1px solid var(--border);
    color: var(--warning-fg);
    font-size: 10.5px;
    line-height: 1.45;
  }

  .warnings li {
    display: flex;
    align-items: flex-start;
    gap: 6px;
  }

  .warnings :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .preview-details {
    border-top: 1px solid var(--border);
  }

  .preview-details summary {
    padding: 8px 12px;
    cursor: pointer;
    font-size: 11px;
    font-weight: 500;
    color: var(--fg);
  }

  .preview-details pre {
    max-height: 240px;
    margin: 0;
    padding: 9px 12px 12px;
    overflow: auto;
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 10.5px;
    line-height: 1.55;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .preview-file {
    border-top: 1px solid var(--border);
  }

  .preview-file p {
    padding: 7px 12px 0;
    color: var(--muted);
    font-size: 9.5px;
    overflow-wrap: anywhere;
  }

  .result {
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }

  .result-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .result-head h3 {
    font-size: 12px;
    font-weight: 600;
  }

  .result ul {
    display: flex;
    flex-direction: column;
    gap: 3px;
    margin: 7px 0 0;
    padding: 0;
    list-style: none;
  }

  .result li {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    font-size: 10.5px;
    color: var(--muted);
  }

  .result li.failed {
    color: var(--danger-fg);
  }

  .result-warnings {
    margin: 7px 0 0 16px;
    color: var(--warning-fg);
    font-size: 10px;
    line-height: 1.45;
  }

  .result-path {
    margin-top: 7px;
    color: var(--muted-2);
    font-size: 9.5px;
    overflow-wrap: anywhere;
  }

  .action-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 12px;
    padding-top: 11px;
    border-top: 1px solid var(--border);
  }

  .action-hint {
    color: var(--muted-2);
    font-size: 10px;
  }

  .apply-details p {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .host-toggle {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 12px;
    padding: 9px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .host-toggle input {
    margin-top: 2px;
  }

  .host-toggle small {
    display: block;
    color: var(--muted);
    font-size: 10px;
  }

  .host-warning {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    margin-top: 9px;
    color: var(--warning-fg) !important;
    font-size: 10.5px !important;
  }

  .host-warning :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  .benchmark-form {
    display: grid;
    grid-template-columns: minmax(0, 1.4fr) 130px 150px auto;
    align-items: end;
    gap: 10px;
  }

  .benchmark-limits {
    margin-top: 6px;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.5;
  }

  .benchmark-note {
    margin-top: 4px;
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.5;
  }

  .benchmark-table-wrap {
    margin-top: 12px;
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .benchmark-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 10.5px;
    white-space: nowrap;
  }

  .benchmark-table th {
    padding: 7px 9px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-align: left;
    text-transform: uppercase;
  }

  .benchmark-table td {
    padding: 7px 9px;
    border-top: 1px solid var(--border);
  }

  .benchmark-table tbody tr:first-child td {
    border-top: 0;
  }

  .benchmark-table .tool-cell {
    max-width: 180px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .benchmark-table .bad {
    color: var(--danger-fg);
    font-weight: 600;
  }

  .run-detail {
    display: flex;
    flex-direction: column;
    margin: 0;
  }

  .run-detail > div {
    display: flex;
    justify-content: space-between;
    gap: 14px;
    padding: 7px 0;
    border-top: 1px solid var(--border);
  }

  .run-detail > div:first-child {
    border-top: 0;
  }

  .run-detail dt {
    color: var(--muted);
    font-size: 10.5px;
  }

  .run-detail dd {
    text-align: right;
    font-size: 10.5px;
  }

  .run-detail dd.bad {
    color: var(--danger-fg);
    font-weight: 600;
  }

  .run-disclaimer {
    margin-top: 12px;
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.5;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  @media (max-width: 900px) {
    .resource-grid,
    .value-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .benchmark-form {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    }

    .benchmark-form > :first-child {
      grid-column: 1 / -1;
    }
  }

  @media (max-width: 620px) {
    .resource-grid,
    .value-grid,
    .profile-row,
    .benchmark-form {
      grid-template-columns: minmax(0, 1fr);
    }

    .action-row {
      flex-wrap: wrap;
    }
  }
</style>
