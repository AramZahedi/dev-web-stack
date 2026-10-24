<script lang="ts">
  import { onMount } from 'svelte';
  import {
    CircleCheck,
    CircleSlash,
    CircleX,
    Clock,
    LoaderCircle,
    RefreshCw,
    ShieldCheck,
    TriangleAlert,
    Wrench,
  } from 'lucide-svelte';
  import * as api from '../lib/api';
  import { describeError } from '../lib/errors';
  import { formatDateTime, formatRelative } from '../lib/format';
  import { addToast, toastError } from '../lib/stores';
  import type { CheckStatus, DoctorCheck, DoctorReport } from '../lib/types';
  import Button from '../lib/components/Button.svelte';
  import ErrorBanner from '../lib/components/ErrorBanner.svelte';
  import Skeleton from '../lib/components/Skeleton.svelte';

  const STATUS_ORDER: Record<CheckStatus, number> = {
    fail: 0,
    warn: 1,
    ok: 2,
    skipped: 3,
  };

  const STATUS_LABEL: Record<CheckStatus, string> = {
    ok: 'OK',
    warn: 'Warning',
    fail: 'Failed',
    skipped: 'Skipped',
  };

  const statusIcons = {
    ok: CircleCheck,
    warn: TriangleAlert,
    fail: CircleX,
    skipped: CircleSlash,
  };

  let report = $state<DoctorReport | null>(null);
  let lastRun = $state<string | undefined>();
  let loading = $state(true);
  let running = $state(false);
  let repairing = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);

  const counts = $derived.by(() => {
    const base: Record<CheckStatus, number> = { ok: 0, warn: 0, fail: 0, skipped: 0 };
    for (const check of report?.checks ?? []) base[check.status] += 1;
    return base;
  });

  const orderedChecks = $derived(
    (report?.checks ?? [])
      .slice()
      .sort((a, b) => STATUS_ORDER[a.status] - STATUS_ORDER[b.status]),
  );

  const overall = $derived.by(() => {
    if (!report) return { title: 'No report', message: '', tone: 'neutral' as const };
    if (counts.fail > 0) {
      return {
        title: 'Action needed',
        message: `${counts.fail} ${counts.fail === 1 ? 'check failed' : 'checks failed'}. Fix ${counts.fail === 1 ? 'it' : 'them'} before serving sites.`,
        tone: 'fail' as const,
      };
    }
    if (counts.warn > 0) {
      return {
        title: 'Mostly healthy',
        message: `${counts.warn} ${counts.warn === 1 ? 'warning needs' : 'warnings need'} review. Sites keep serving normally.`,
        tone: 'warn' as const,
      };
    }
    if (report.checks.length === 0) {
      return { title: 'No checks reported', message: 'Run the checks to inspect this machine.', tone: 'neutral' as const };
    }
    return {
      title: 'All systems healthy',
      message: 'Every check passed. Domains, certificates and runtimes are ready.',
      tone: 'ok' as const,
    };
  });

  const total = $derived(report?.checks.length ?? 0);
  const RADIUS = 56;
  const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

  const segments = $derived.by(() => {
    if (!report || total === 0) return [];
    const statuses: CheckStatus[] = ['ok', 'warn', 'fail', 'skipped'];
    let offset = 0;
    return statuses
      .map((status) => {
        const count = counts[status];
        if (count === 0) return null;
        const length = (count / total) * CIRCUMFERENCE;
        const gap = total > 1 ? 3 : 0;
        const dash = Math.max(length - gap, 0.5);
        const segment = {
          status,
          count,
          dasharray: `${dash} ${CIRCUMFERENCE - dash}`,
          dashoffset: -offset,
        };
        offset += length;
        return segment;
      })
      .filter((segment) => segment !== null);
  });

  const ringLabel = $derived(
    total === 0
      ? 'No checks recorded'
      : `${counts.ok} of ${total} checks passed${counts.warn ? `, ${counts.warn} warning${counts.warn === 1 ? '' : 's'}` : ''}${counts.fail ? `, ${counts.fail} failed` : ''}${counts.skipped ? `, ${counts.skipped} skipped` : ''}`,
  );

  async function load(initial = false): Promise<void> {
    if (initial) loading = true;
    error = null;
    try {
      report = await api.getDoctor();
      if (report.last_run) lastRun = report.last_run;
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
    return () => window.removeEventListener('devstack:refresh', refresh);
  });

  async function run(): Promise<void> {
    if (running || repairing) return;
    running = true;
    try {
      report = await api.runDoctor();
      lastRun = report.last_run ?? new Date().toISOString();
      if (report.ok) {
        addToast({
          kind: 'success',
          title: 'Checks complete',
          message: 'No failures detected. Warnings are listed below.',
        });
      } else {
        addToast({
          kind: 'error',
          title: 'Checks need attention',
          message: `${report.checks.filter((check) => check.status === 'fail').length} failed. Review the remediation steps.`,
        });
      }
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not run checks', `${described.code}: ${described.message}`);
    } finally {
      running = false;
    }
  }

  async function repair(): Promise<void> {
    if (running || repairing) return;
    repairing = true;
    try {
      report = await api.repairDoctor();
      lastRun = report.last_run ?? new Date().toISOString();
      addToast({
        kind: 'success',
        title: 'Repair finished',
        message: report.ok
          ? 'All available targets are healthy again.'
          : 'Some checks still need manual attention. Review the report below.',
      });
    } catch (caught) {
      const described = describeError(caught);
      toastError('Could not repair', `${described.code}: ${described.message}`);
    } finally {
      repairing = false;
    }
  }
</script>

<div class="page">
  {#if error && !report}
    <div class="error-state">
      <ErrorBanner
        title="Could not load the health report"
        code={error.code}
        message={error.message}
        onretry={() => load(true)}
      />
    </div>
  {:else if loading}
    <section class="summary skeleton-summary" aria-busy="true" aria-label="Loading doctor report">
      <Skeleton width="148px" height="148px" radius="50%" />
      <div class="skeleton-main">
        <Skeleton width="120px" height="10px" />
        <Skeleton width="260px" height="24px" />
        <Skeleton width="min(420px, 100%)" height="12px" />
        <div class="skeleton-legend">
          {#each Array(4) as _, index (index)}
            <Skeleton width="80px" height="18px" />
          {/each}
        </div>
      </div>
    </section>
    <section class="checks-card" aria-busy="true" aria-label="Loading checks">
      {#each Array(5) as _, index (index)}
        <div class="check-skeleton">
          <Skeleton width="22px" height="22px" radius="50%" />
          <div class="check-skeleton-copy">
            <Skeleton width={`${42 + index * 5}%`} height="12px" />
            <Skeleton width={`${72 + index * 4}%`} height="10px" />
          </div>
          <Skeleton width="58px" height="18px" radius="999px" />
        </div>
      {/each}
    </section>
  {:else if report}
    <section class="summary tone-{overall.tone}" aria-labelledby="doctor-summary">
      <div class="ring-wrap">
        <div
          class="ring"
          role="img"
          aria-label={ringLabel}
        >
          <svg viewBox="0 0 140 140" width="148" height="148" aria-hidden="true">
            <circle
              cx="70"
              cy="70"
              r={RADIUS}
              fill="none"
              stroke="var(--surface-3)"
              stroke-width="11"
            />
            {#each segments as segment (segment.status)}
              <circle
                class="segment {segment.status}"
                cx="70"
                cy="70"
                r={RADIUS}
                fill="none"
                stroke-width="11"
                stroke-dasharray={segment.dasharray}
                stroke-dashoffset={segment.dashoffset}
                transform="rotate(-90 70 70)"
              />
            {/each}
          </svg>
          <div class="ring-center">
            <span class="ring-value mono">{counts.ok}<span class="ring-total">/{total}</span></span>
            <span class="ring-label">checks pass</span>
          </div>
        </div>
      </div>

      <div class="summary-main">
        <div class="summary-top">
          <div class="summary-copy">
            <p class="eyebrow">Health report</p>
            <div class="summary-title">
              <h2 id="doctor-summary">{overall.title}</h2>
              {#if overall.tone === 'ok'}
                <ShieldCheck size={18} strokeWidth={1.9} class="summary-icon ok" aria-hidden="true" />
              {:else if overall.tone === 'warn'}
                <TriangleAlert size={18} strokeWidth={1.9} class="summary-icon warn" aria-hidden="true" />
              {:else if overall.tone === 'fail'}
                <CircleX size={18} strokeWidth={1.9} class="summary-icon fail" aria-hidden="true" />
              {/if}
            </div>
            <p class="summary-message">{overall.message}</p>
          </div>
          <div class="summary-actions">
            <Button variant="primary" onclick={run} loading={running} disabled={running || repairing}>
              {#if !running}<RefreshCw size={14} strokeWidth={2} aria-hidden="true" />{/if}
              Run checks
            </Button>
            <Button variant="secondary" onclick={repair} loading={repairing} disabled={running || repairing}>
              {#if !repairing}<Wrench size={14} strokeWidth={2} aria-hidden="true" />{/if}
              Repair
            </Button>
          </div>
        </div>

        <div class="summary-bottom">
          <dl class="legend">
            <div><dt><span class="dot ok" aria-hidden="true"></span>OK</dt><dd class="mono">{counts.ok}</dd></div>
            <div><dt><span class="dot warn" aria-hidden="true"></span>Warnings</dt><dd class="mono">{counts.warn}</dd></div>
            <div><dt><span class="dot fail" aria-hidden="true"></span>Failed</dt><dd class="mono">{counts.fail}</dd></div>
            <div><dt><span class="dot skipped" aria-hidden="true"></span>Skipped</dt><dd class="mono">{counts.skipped}</dd></div>
          </dl>
          <p class="last-run" title={lastRun ? formatDateTime(lastRun) : ''}>
            <Clock size={12} strokeWidth={1.9} aria-hidden="true" />
            {lastRun ? `Last run ${formatRelative(lastRun)}` : 'Last run not reported'}
          </p>
        </div>

        {#if running}
          <p class="run-state" role="status">
            <LoaderCircle class="spin" size={13} strokeWidth={2} aria-hidden="true" />
            Running {total || 8} checks — the report below stays visible until results arrive.
          </p>
        {:else if repairing}
          <p class="run-state" role="status">
            <LoaderCircle class="spin" size={13} strokeWidth={2} aria-hidden="true" />
            Repairing trust, hosts and runtime integrity…
          </p>
        {/if}
      </div>
    </section>

    <section class="checks-card" aria-labelledby="checks-heading">
      <header class="checks-head">
        <div>
          <h2 id="checks-heading">Checks</h2>
          <p>Ordered by severity: failures and warnings first.</p>
        </div>
        <span class="checks-count mono">{total}</span>
      </header>

      {#if orderedChecks.length === 0}
        <div class="no-checks">
          <CircleSlash size={18} strokeWidth={1.8} aria-hidden="true" />
          <p>No checks were returned. Run the checks again after starting the daemon.</p>
        </div>
      {:else}
        {#each orderedChecks as check (check.id)}
          {@const Icon = statusIcons[check.status]}
          <article class="check-row {check.status}">
            <span class="check-icon" aria-hidden="true">
              <Icon size={17} strokeWidth={1.9} />
            </span>
            <div class="check-main">
              <div class="check-title">
                <h3>{check.title}</h3>
                <span class="pill {check.status}">
                  <span class="dot" aria-hidden="true"></span>
                  {STATUS_LABEL[check.status]}
                </span>
              </div>
              <p class="check-detail">{check.detail}</p>
              {#if check.remediation}
                <div class="remediation">
                  <span class="remediation-label">Fix</span>
                  <code>{check.remediation}</code>
                </div>
              {/if}
            </div>
          </article>
        {/each}
      {/if}
    </section>

    {#if error}
      <div class="inline-error">
        <ErrorBanner
          title="Could not refresh the report"
          code={error.code}
          message={error.message}
          onretry={() => load()}
          ondismiss={() => (error = null)}
        />
      </div>
    {/if}
  {/if}
</div>

<style>
  .summary {
    display: grid;
    grid-template-columns: 168px minmax(0, 1fr);
    gap: 16px;
    padding: 18px;
    margin-bottom: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
  }

  .summary.tone-fail {
    border-color: color-mix(in srgb, var(--danger) 34%, var(--border));
  }

  .summary.tone-warn {
    border-color: color-mix(in srgb, var(--warning) 30%, var(--border));
  }

  .ring-wrap {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .ring {
    position: relative;
    width: 148px;
    height: 148px;
    flex: none;
  }

  .ring svg {
    display: block;
  }

  .segment {
    transition:
      stroke-dasharray 500ms var(--ease),
      stroke-dashoffset 500ms var(--ease);
    stroke-linecap: butt;
  }

  .segment.ok {
    stroke: var(--success);
  }

  .segment.warn {
    stroke: var(--warning);
  }

  .segment.fail {
    stroke: var(--danger);
  }

  .segment.skipped {
    stroke: var(--muted-2);
  }

  .ring-center {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1px;
    pointer-events: none;
  }

  .ring-value {
    color: var(--fg);
    font-size: 27px;
    font-weight: 600;
    line-height: 1;
    letter-spacing: -0.04em;
  }

  .ring-total {
    color: var(--muted-2);
    font-size: 14px;
    font-weight: 400;
  }

  .ring-label {
    color: var(--muted);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .summary-main {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: 12px;
    min-width: 0;
  }

  .summary-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }

  .summary-copy {
    min-width: 0;
  }

  .eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .summary-title {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-top: 2px;
  }

  .summary h2 {
    font-size: 21px;
    font-weight: 600;
    letter-spacing: -0.025em;
  }

  .summary-icon.ok {
    color: var(--success-fg);
  }

  .summary-icon.warn {
    color: var(--warning-fg);
  }

  .summary-icon.fail {
    color: var(--danger-fg);
  }

  .summary-message {
    margin-top: 3px;
    max-width: 620px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .summary-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }

  .summary-bottom {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    flex-wrap: wrap;
    padding-top: 10px;
    border-top: 1px solid var(--border);
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 16px;
    margin: 0;
  }

  .legend > div {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .legend dt {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--muted);
    font-size: 10px;
  }

  .legend dd {
    margin: 0;
    color: var(--fg);
    font-size: 10.5px;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--muted-2);
  }

  .dot.ok {
    background: var(--success);
  }

  .dot.warn {
    background: var(--warning);
  }

  .dot.fail {
    background: var(--danger);
  }

  .dot.skipped {
    background: var(--muted-2);
  }

  .last-run {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--muted-2);
    font-size: 10px;
  }

  .run-state {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 1px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .spin {
    animation: spin 800ms linear infinite;
    flex: none;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .checks-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    overflow: hidden;
    box-shadow: var(--shadow-sm);
  }

  .checks-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 13px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-2);
  }

  .checks-head h2 {
    font-size: 13px;
    font-weight: 600;
  }

  .checks-head p {
    margin-top: 1px;
    color: var(--muted);
    font-size: 10.5px;
  }

  .checks-count {
    color: var(--muted-2);
    font-size: 10.5px;
  }

  .check-row {
    display: flex;
    align-items: flex-start;
    gap: 11px;
    padding: 12px 16px;
    border-top: 1px solid var(--border);
  }

  .check-row:first-child {
    border-top: 0;
  }

  .check-row.fail {
    background: color-mix(in srgb, var(--danger-soft) 46%, transparent);
  }

  .check-icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 26px;
    height: 26px;
    margin-top: 1px;
    border-radius: 6px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .check-row.ok .check-icon {
    color: var(--success-fg);
    background: var(--success-soft);
  }

  .check-row.warn .check-icon {
    color: var(--warning-fg);
    background: var(--warning-soft);
  }

  .check-row.fail .check-icon {
    color: var(--danger-fg);
    background: var(--danger-soft);
  }

  .check-main {
    flex: 1;
    min-width: 0;
  }

  .check-title {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }

  .check-title h3 {
    font-size: 12.5px;
    font-weight: 600;
  }

  .check-detail {
    margin-top: 2px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
    max-width: 900px;
  }

  .pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 18px;
    padding: 0 6px;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.01em;
    white-space: nowrap;
  }

  .pill .dot {
    width: 4px;
    height: 4px;
    background: currentColor;
  }

  .pill.ok {
    color: var(--success-fg);
    background: var(--success-soft);
  }

  .pill.warn {
    color: var(--warning-fg);
    background: var(--warning-soft);
  }

  .pill.fail {
    color: var(--danger-fg);
    background: var(--danger-soft);
  }

  .remediation {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-top: 7px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
  }

  .remediation-label {
    flex: none;
    padding-top: 1px;
    color: var(--muted-2);
    font-size: 8.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .remediation code {
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 10px;
    line-height: 1.55;
    overflow-wrap: anywhere;
  }

  .no-checks {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 24px 16px;
    color: var(--muted);
    font-size: 12px;
  }

  .skeleton-summary {
    grid-template-columns: 168px minmax(0, 1fr);
  }

  .skeleton-main {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding-top: 4px;
    min-width: 0;
  }

  .skeleton-legend {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 8px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }

  .check-skeleton {
    display: flex;
    align-items: flex-start;
    gap: 11px;
    padding: 13px 16px;
    border-top: 1px solid var(--border);
  }

  .check-skeleton:first-of-type {
    border-top: 0;
  }

  .check-skeleton-copy {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
    min-width: 0;
  }

  .error-state {
    max-width: 720px;
    margin: 32px auto;
  }

  .inline-error {
    margin-top: 14px;
  }

  @media (max-width: 900px) {
    .summary {
      grid-template-columns: 140px minmax(0, 1fr);
    }

    .ring,
    .ring svg {
      width: 132px;
      height: 132px;
    }

    .summary-top {
      flex-direction: column;
      gap: 10px;
    }
  }

  @media (max-width: 680px) {
    .summary,
    .skeleton-summary {
      grid-template-columns: 1fr;
    }

    .ring-wrap {
      padding: 4px 0;
    }

    .summary-actions {
      width: 100%;
    }

    .summary-actions :global(.btn) {
      flex: 1;
    }

    .summary-bottom {
      flex-direction: column;
      align-items: flex-start;
    }

    .check-row {
      padding: 12px;
    }
  }
</style>
