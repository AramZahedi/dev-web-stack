<script lang="ts">
  import { tick } from 'svelte';
  import {
    ArrowDownToLine,
    Download,
    Pause,
    Play,
    Terminal,
    Trash2,
  } from 'lucide-svelte';
  import { openLogStream } from '../api';
  import { formatClock } from '../format';
  import { addToast } from '../stores';
  import type { LogLine, Site } from '../types';
  import IconButton from './IconButton.svelte';

  interface Props {
    site: Site;
  }

  let { site }: Props = $props();

  const MAX_LINES = 500;
  let lines = $state<LogLine[]>([]);
  let buffered = $state<LogLine[]>([]);
  let paused = $state(false);
  let autoScroll = $state(true);
  let stream = $state<'connecting' | 'live' | 'error'>('connecting');
  let viewport: HTMLDivElement | undefined = $state();

  function append(incoming: LogLine[]): void {
    if (incoming.length === 0) return;
    lines = [...lines, ...incoming].slice(-MAX_LINES);
  }

  $effect(() => {
    const ref = site.id;
    lines = [];
    buffered = [];
    paused = false;
    stream = 'connecting';

    const close = openLogStream(ref, {
      onLine: (line) => {
        if (paused) {
          buffered = [...buffered, line].slice(-MAX_LINES);
        } else {
          append([line]);
        }
      },
      onStatus: (status) => {
        stream = status;
      },
    });
    return close;
  });

  $effect(() => {
    const count = lines.length;
    if (!autoScroll || count === 0) return;
    tick().then(() => {
      if (viewport) viewport.scrollTop = viewport.scrollHeight;
    });
  });

  function togglePause(): void {
    if (!paused) {
      paused = true;
      return;
    }
    paused = false;
    if (buffered.length > 0) {
      append(buffered);
      buffered = [];
    }
  }

  function setAutoScroll(value: boolean): void {
    autoScroll = value;
    if (value && viewport) viewport.scrollTop = viewport.scrollHeight;
  }

  function clearView(): void {
    lines = [];
    buffered = [];
  }

  function download(): void {
    const visible = paused ? [...lines, ...buffered] : lines;
    if (visible.length === 0) {
      addToast({ kind: 'info', title: 'No log lines to download yet' });
      return;
    }
    const text = visible
      .map((line) => `${line.time} [${line.source}] ${line.text}`)
      .join('\n');
    const blob = new Blob([`${text}\n`], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${site.name}-logs.txt`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  function sourceClass(source: string): string {
    return ['app', 'proxy', 'supervisor', 'fpm'].includes(source) ? source : 'other';
  }

  const streamLabel = $derived(
    paused
      ? buffered.length > 0
        ? `Paused · ${buffered.length} buffered`
        : 'Paused'
      : stream === 'live'
        ? 'Live'
        : stream === 'error'
          ? 'Reconnecting'
          : 'Connecting',
  );
</script>

<section class="logs" aria-label="Site logs">
  <header class="logs-head">
    <div class="title-group">
      <span class="terminal-icon" aria-hidden="true">
        <Terminal size={15} strokeWidth={1.9} />
      </span>
      <h2>Logs</h2>
      <span class="stream {paused ? 'paused' : stream}">
        <span class="dot" aria-hidden="true"></span>
        {streamLabel}
      </span>
    </div>
    <div class="controls">
      <button
        type="button"
        class="tool"
        aria-label={paused ? 'Resume log stream' : 'Pause log stream'}
        aria-pressed={paused}
        onclick={togglePause}
      >
        {#if paused}
          <Play size={13} strokeWidth={2} aria-hidden="true" />
          <span class="tool-label">Resume</span>
        {:else}
          <Pause size={13} strokeWidth={2} aria-hidden="true" />
          <span class="tool-label">Pause</span>
        {/if}
      </button>
      <button
        type="button"
        class="tool"
        class:active={autoScroll}
        aria-label={autoScroll ? 'Disable auto-scroll' : 'Enable auto-scroll'}
        aria-pressed={autoScroll}
        onclick={() => setAutoScroll(!autoScroll)}
      >
        <ArrowDownToLine size={13} strokeWidth={2} aria-hidden="true" />
        <span class="tool-label">Auto-scroll</span>
      </button>
      <IconButton label="Clear log view" icon={Trash2} size="sm" onclick={clearView} />
      <IconButton label="Download logs" icon={Download} size="sm" onclick={download} />
    </div>
  </header>

  <div
    class="viewport"
    bind:this={viewport}
    role="log"
    aria-label="Live output from {site.name}"
    aria-live="off"
  >
    {#if lines.length === 0}
      <div class="waiting">
        <span class="waiting-icon" aria-hidden="true">
          <Terminal size={20} strokeWidth={1.6} />
        </span>
        <p class="waiting-title">Waiting for output…</p>
        <p class="waiting-copy">
          {#if site.status === 'stopped'}
            Start the site to begin streaming output.
          {:else}
            Log lines appear here as soon as the process writes to stdout or stderr.
          {/if}
        </p>
      </div>
    {:else}
      {#each lines as line, index (index)}
        <div class="log-line">
          <span class="time" title={line.time}>{formatClock(line.time)}</span>
          <span class="source {sourceClass(line.source)}">{line.source}</span>
          <span class="text">{line.text}</span>
        </div>
      {/each}
    {/if}
  </div>

  <footer class="logs-foot">
    <span>{lines.length} lines</span>
    {#if buffered.length > 0}<span class="buffered">{buffered.length} buffered while paused</span>{/if}
    <span class="spacer"></span>
    <span class="source-note">app · proxy · supervisor</span>
  </footer>
</section>

<style>
  .logs {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-width: 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    overflow: hidden;
    box-shadow: var(--shadow-sm);
  }

  .logs-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px;
    min-height: 44px;
    padding: 6px 10px 6px 14px;
    border-bottom: 1px solid var(--border);
    background: var(--log-header);
  }

  .title-group {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .terminal-icon {
    display: grid;
    place-items: center;
    color: var(--muted);
  }

  h2 {
    font-size: 13px;
    font-weight: 600;
  }

  .stream {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 20px;
    padding: 0 7px;
    border-radius: 999px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 500;
    white-space: nowrap;
  }

  .stream .dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  .stream.live {
    background: var(--success-soft);
    color: var(--success-fg);
  }

  .stream.live .dot {
    box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 14%, transparent);
  }

  .stream.error,
  .stream.paused {
    background: var(--warning-soft);
    color: var(--warning-fg);
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .tool {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 28px;
    padding: 0 7px;
    border: 1px solid transparent;
    border-radius: 6px;
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    font-weight: 500;
    white-space: nowrap;
  }

  .tool:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .tool.active {
    color: var(--fg);
    background: var(--surface-2);
    border-color: var(--border);
  }

  .viewport {
    flex: 1 1 408px;
    min-height: 408px;
    height: auto;
    overflow-y: auto;
    overscroll-behavior: contain;
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 11.5px;
    line-height: 1.65;
    padding: 8px 0;
  }

  .log-line {
    display: grid;
    grid-template-columns: 62px 66px minmax(0, 1fr);
    gap: 8px;
    padding: 1px 14px;
    min-height: 20px;
  }

  .log-line:hover {
    background: color-mix(in srgb, var(--log-fg) 4%, transparent);
  }

  .time {
    color: var(--log-muted);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .source {
    color: var(--log-muted);
    font-size: 9.5px;
    font-weight: 500;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    padding-top: 1px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .source.app {
    color: var(--success-fg);
  }

  .source.proxy {
    color: var(--info-fg);
  }

  .source.supervisor {
    color: var(--warning-fg);
  }

  .source.fpm {
    color: #c4b5fd;
  }

  :global(html[data-theme='light']) .source.fpm {
    color: #6d28d9;
  }

  .text {
    color: var(--log-fg);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    min-width: 0;
  }

  .waiting {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    height: 100%;
    gap: 5px;
    text-align: center;
    padding: 24px;
    color: var(--log-muted);
  }

  .waiting-icon {
    display: grid;
    place-items: center;
    width: 38px;
    height: 38px;
    margin-bottom: 4px;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: color-mix(in srgb, var(--log-fg) 4%, transparent);
  }

  .waiting-title {
    color: var(--log-fg);
    font-family: var(--font-sans);
    font-size: 12.5px;
    font-weight: 500;
  }

  .waiting-copy {
    max-width: 320px;
    font-family: var(--font-sans);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .logs-foot {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 30px;
    padding: 0 14px;
    border-top: 1px solid var(--border);
    background: var(--log-header);
    color: var(--muted-2);
    font-size: 10.5px;
  }

  .buffered {
    color: var(--warning-fg);
  }

  .spacer {
    flex: 1;
  }

  .source-note {
    font-family: var(--font-mono);
    font-size: 9.5px;
    letter-spacing: 0.02em;
  }

  @media (max-width: 900px) {
    .viewport {
      flex-basis: 360px;
      min-height: 360px;
    }
  }

  @media (max-width: 560px) {
    .logs-head {
      padding: 7px 8px 7px 12px;
    }

    .tool-label {
      display: none;
    }

    .tool {
      width: 28px;
      justify-content: center;
      padding: 0;
    }

    .viewport {
      flex-basis: 320px;
      min-height: 320px;
      font-size: 11px;
    }

    .log-line {
      grid-template-columns: 54px 46px minmax(0, 1fr);
      gap: 6px;
      padding: 1px 12px;
    }

    .source {
      font-size: 9px;
    }

    .source-note {
      display: none;
    }
  }
</style>
