<script lang="ts">
  import { onDestroy } from 'svelte';
  import { RotateCw, SquareTerminal, X } from 'lucide-svelte';
  import type { FitAddon as FitAddonType } from '@xterm/addon-fit';
  import type { Terminal as XTerm } from '@xterm/xterm';
  import '@xterm/xterm/css/xterm.css';
  import * as api from '../api';
  import { describeError } from '../errors';
  import type { Site, TerminalConnection, TerminalStatus } from '../types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Card from './Card.svelte';
  import ErrorBanner from './ErrorBanner.svelte';

  interface Props {
    site: Site;
  }

  let { site }: Props = $props();

  type PanelStatus = 'idle' | TerminalStatus;
  let terminalEl = $state<HTMLDivElement | null>(null);
  let status = $state<PanelStatus>('idle');
  let error = $state<{ code: string; message: string } | null>(null);

  let terminal: XTerm | null = null;
  let fitAddon: FitAddonType | null = null;
  let connection: TerminalConnection | null = null;
  let resizeObserver: ResizeObserver | null = null;

  const statusLabel = $derived(
    status === 'connected'
      ? 'Connected'
      : status === 'connecting'
        ? 'Connecting'
        : status === 'error'
          ? 'Error'
          : status === 'closed'
            ? 'Closed'
            : 'Not started',
  );

  function statusVariant(): 'accent' | 'info' | 'neutral' | 'danger' {
    if (status === 'connected') return 'accent';
    if (status === 'connecting') return 'info';
    if (status === 'error') return 'danger';
    return 'neutral';
  }

  function fit(): void {
    if (!fitAddon || !terminalEl) return;
    try {
      fitAddon.fit();
      if (connection && terminal) connection.resize(terminal.cols, terminal.rows);
    } catch {
      // A hidden or zero-sized container cannot be measured; the next resize
      // or start attempt recovers.
    }
  }

  function disposeTerminal(): void {
    connection?.close();
    connection = null;
    resizeObserver?.disconnect();
    resizeObserver = null;
    terminal?.dispose();
    terminal = null;
    fitAddon = null;
  }

  async function start(): Promise<void> {
    const surface =
      terminalEl ?? document.getElementById(`site-terminal-${site.id}`) as HTMLDivElement | null;
    if (!surface) return;
    terminalEl = surface;
    disposeTerminal();
    error = null;
    status = 'connecting';

    let TerminalCtor: typeof import('@xterm/xterm').Terminal;
    let FitAddonCtor: typeof import('@xterm/addon-fit').FitAddon;
    try {
      [{ Terminal: TerminalCtor }, { FitAddon: FitAddonCtor }] = await Promise.all([
        import('@xterm/xterm'),
        import('@xterm/addon-fit'),
      ]);
    } catch (caught) {
      error = {
        code: 'TERMINAL_UNAVAILABLE',
        message: caught instanceof Error ? caught.message : 'Could not load the terminal renderer.',
      };
      status = 'error';
      return;
    }
    if (status !== 'connecting') return;

    const term = new TerminalCtor({
      cursorBlink: true,
      fontFamily: '"JetBrains Mono", ui-monospace, SFMono-Regular, monospace',
      fontSize: 12.5,
      lineHeight: 1.25,
      scrollback: 5000,
      convertEol: false,
      theme: {
        background: '#0c1017',
        foreground: '#d8dee9',
        cursor: '#7dd3fc',
        selectionBackground: '#334155',
        black: '#1f2937',
        red: '#f87171',
        green: '#86efac',
        yellow: '#fde68a',
        blue: '#93c5fd',
        magenta: '#d8b4fe',
        cyan: '#67e8f9',
        white: '#e5e7eb',
      },
    });
    const fitAddonInstance = new FitAddonCtor();
    term.loadAddon(fitAddonInstance);
    term.open(terminalEl);
    terminal = term;
    fitAddon = fitAddonInstance;
    fit();
    term.focus();
    term.onData((data) => connection?.send(data));

    resizeObserver = new ResizeObserver(() => fit());
    resizeObserver.observe(terminalEl);

    void api
      .openTerminal(site.id, {
        onOutput: (text) => term.write(text),
        onExit: (code) => {
          term.write(`\r\n\x1b[2m[process exited with code ${code}]\x1b[0m\r\n`);
          status = 'closed';
        },
        onStatus: (next) => {
          status = next;
        },
      })
      .then((next) => {
        connection = next;
        fit();
      })
      .catch((caught) => {
        const described = describeError(caught);
        error = described;
        status = 'error';
        term.write(`\r\n\x1b[31m${described.code}: ${described.message}\x1b[0m\r\n`);
      });
  }

  function close(): void {
    connection?.close();
    connection = null;
    status = 'closed';
  }

  onDestroy(disposeTerminal);
</script>

<Card
  title="Terminal"
  subtitle="Site-scoped shell with the site's PHP/Node environment"
  headerWrap
  className="terminal-card"
>
  {#snippet actions()}
    <Badge variant={statusVariant()}>{statusLabel}</Badge>
    {#if status === 'idle' || status === 'error' || status === 'closed'}
      <Button size="sm" variant="primary" onclick={() => void start()}>
        <SquareTerminal size={12} strokeWidth={2} aria-hidden="true" />
        {status === 'idle' ? 'Start terminal' : 'Reconnect'}
      </Button>
    {:else}
      <Button size="sm" variant="secondary" onclick={() => void start()} loading={status === 'connecting'}>
        {#if status !== 'connecting'}<RotateCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
        Reconnect
      </Button>
      <Button size="sm" variant="ghost" onclick={close} disabled={status !== 'connected'}>
        <X size={12} strokeWidth={2} aria-hidden="true" />
        Close
      </Button>
    {/if}
  {/snippet}

  {#if error}
    <div class="terminal-error">
      <ErrorBanner
        title="Terminal unavailable"
        code={error.code}
        message={error.message}
        onretry={() => void start()}
        ondismiss={() => (error = null)}
      />
    </div>
  {/if}

  <div class="terminal-shell" class:connected={status === 'connected'}>
    {#if status === 'idle'}
      <div class="terminal-placeholder">
        <SquareTerminal size={22} strokeWidth={1.6} aria-hidden="true" />
        <div>
          <p class="terminal-title">Open a shell in this site</p>
          <p>
            Commands run as your user in <span class="mono">{site.host_path}</span>. The
            site's PHP and Node shims are first in <span class="mono">PATH</span>; root,
            sudo, and the Docker socket are never available.
          </p>
        </div>
      </div>
    {/if}
    <div
      bind:this={terminalEl}
      id={`site-terminal-${site.id}`}
      class="terminal-surface"
      class:hidden={status === 'idle'}
      role="application"
      aria-label={`${site.name} terminal`}
    ></div>
  </div>
  <p class="terminal-foot">
    Sessions are closed on disconnect and idle timeout. Output history is not persisted.
  </p>
</Card>

<style>
  :global(.terminal-card) {
    margin-top: 14px;
  }

  .terminal-error {
    margin-bottom: 10px;
  }

  .terminal-shell {
    position: relative;
    min-height: 150px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: #0c1017;
    overflow: hidden;
  }

  .terminal-shell.connected {
    min-height: 330px;
  }

  .terminal-surface {
    height: 330px;
    padding: 8px 6px 6px;
  }

  .terminal-surface.hidden {
    position: absolute;
    inset: 0;
    height: 100%;
    visibility: hidden;
    pointer-events: none;
  }

  .terminal-placeholder {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    min-height: 150px;
    padding: 18px;
    color: #94a3b8;
  }

  .terminal-placeholder :global(svg) {
    flex: none;
    margin-top: 2px;
    color: #67e8f9;
  }

  .terminal-title {
    margin-bottom: 3px;
    color: #e2e8f0;
    font-size: 12px;
    font-weight: 600;
  }

  .terminal-placeholder p:not(.terminal-title) {
    max-width: 60ch;
    color: #94a3b8;
    font-size: 11px;
    line-height: 1.55;
  }

  .terminal-placeholder .mono {
    color: #cbd5e1;
  }

  .terminal-foot {
    margin-top: 8px;
    color: var(--muted-2);
    font-size: 10px;
    line-height: 1.45;
  }

  @media (max-width: 620px) {
    .terminal-surface,
    .terminal-shell.connected {
      height: 280px;
      min-height: 280px;
    }
  }
</style>
