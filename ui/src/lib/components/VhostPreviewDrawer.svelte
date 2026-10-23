<script lang="ts">
  import { tick } from 'svelte';
  import { Check, Copy, FileCode, X } from 'lucide-svelte';
  import * as api from '../api';
  import { copyText } from '../clipboard';
  import { describeError } from '../errors';
  import type { VhostPreview } from '../types';
  import Button from './Button.svelte';
  import ErrorBanner from './ErrorBanner.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    open: boolean;
    site: string;
    initialServer?: 'nginx' | 'apache';
    returnFocus?: HTMLElement | null;
    onclose: () => void;
  }

  let {
    open,
    site,
    initialServer = 'nginx',
    returnFocus = null,
    onclose,
  }: Props = $props();

  let server = $state<'nginx' | 'apache'>('nginx');
  let preview = $state<VhostPreview | null>(null);
  let loading = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);
  let copied = $state(false);
  let retryToken = $state(0);
  let panelEl: HTMLDivElement | undefined = $state();
  let closeEl: HTMLButtonElement | undefined = $state();
  let previousFocus: HTMLElement | null = null;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let previewRequest = 0;
  let wasOpen = false;

  const titleId = `vhost-preview-title-${Math.random().toString(36).slice(2, 8)}`;
  const title = $derived(site ? `${site} vhost preview` : 'Vhost preview');

  $effect(() => {
    if (open && !wasOpen) {
      wasOpen = true;
      server = initialServer;
      copied = false;
    } else if (!open) {
      wasOpen = false;
    }
  });

  $effect(() => {
    if (!open) return;
    previousFocus =
      returnFocus ??
      (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    tick().then(() => closeEl?.focus());
    return () => {
      document.body.style.overflow = originalOverflow;
      previousFocus?.focus?.();
    };
  });

  $effect(() => {
    if (!open) return;
    void retryToken;
    const requestID = ++previewRequest;
    const targetSite = site;
    const targetServer = server;
    error = null;
    preview = null;
    if (!targetSite) {
      loading = false;
      return;
    }
    loading = true;
    api
      .previewVhost(targetSite, targetServer)
      .then((result) => {
        if (requestID !== previewRequest) return;
        preview = result;
      })
      .catch((caught) => {
        if (requestID !== previewRequest) return;
        error = describeError(caught);
      })
      .finally(() => {
        if (requestID === previewRequest) loading = false;
      });
  });

  function isUsable(element: HTMLElement | null | undefined): element is HTMLElement {
    if (!element) return false;
    if (element.hasAttribute('disabled')) return false;
    if (element.getAttribute('aria-disabled') === 'true') return false;
    return element.offsetParent !== null;
  }

  function focusable(): HTMLElement[] {
    if (!panelEl) return [];
    return Array.from(
      panelEl.querySelectorAll<HTMLElement>(
        'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter(isUsable);
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (!open) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      onclose();
      return;
    }
    if (event.key !== 'Tab') return;
    const elements = focusable();
    if (elements.length === 0) {
      event.preventDefault();
      panelEl?.focus();
      return;
    }
    const first = elements[0];
    const last = elements[elements.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function selectServer(next: 'nginx' | 'apache'): void {
    if (server === next) return;
    server = next;
    copied = false;
  }

  async function copyPreview(): Promise<void> {
    if (!preview?.content) return;
    const ok = await copyText(preview.content);
    if (!ok) return;
    copied = true;
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copied = false), 1800);
  }

  $effect(() => {
    return () => {
      if (copyTimer) clearTimeout(copyTimer);
    };
  });
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <div class="overlay">
    <div class="backdrop" aria-hidden="true" onclick={onclose}></div>
    <div
      class="drawer"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      tabindex="-1"
      bind:this={panelEl}
    >
      <header class="head">
        <div class="copy">
          <p class="eyebrow">Rendered config</p>
          <h2 id={titleId}>{title}</h2>
          <p class="subtitle">
            Generated from the site record and managed certificates. Nothing is written to disk.
          </p>
        </div>
        <button
          type="button"
          class="close"
          aria-label="Close vhost preview"
          bind:this={closeEl}
          onclick={onclose}
        >
          <X size={17} strokeWidth={2} aria-hidden="true" />
        </button>
      </header>

      <div class="tabs" role="tablist" aria-label="Vhost server">
        <button
          type="button"
          role="tab"
          id="vhost-tab-nginx"
          aria-selected={server === 'nginx'}
          aria-controls="vhost-preview-panel"
          class:active={server === 'nginx'}
          onclick={() => selectServer('nginx')}
        >
          nginx
        </button>
        <button
          type="button"
          role="tab"
          id="vhost-tab-apache"
          aria-selected={server === 'apache'}
          aria-controls="vhost-preview-panel"
          class:active={server === 'apache'}
          onclick={() => selectServer('apache')}
        >
          Apache
        </button>
      </div>

      <div
        class="body"
        id="vhost-preview-panel"
        role="tabpanel"
        aria-labelledby={`vhost-tab-${server}`}
      >
        {#if loading}
          <div class="loading" aria-busy="true" aria-label="Loading vhost preview">
            <Skeleton width="150px" height="11px" />
            {#each Array(8) as _, index (index)}
              <Skeleton width={`${88 - (index % 4) * 11}%`} height="11px" />
            {/each}
          </div>
        {:else if error}
          <ErrorBanner
            title="Could not load the vhost preview"
            code={error.code}
            message={error.message}
            onretry={() => (retryToken += 1)}
          />
        {:else if !site}
          <div class="empty">
            <FileCode size={22} strokeWidth={1.7} aria-hidden="true" />
            <p>No site is available to preview yet.</p>
          </div>
        {:else if !preview?.content}
          <div class="empty">
            <FileCode size={22} strokeWidth={1.7} aria-hidden="true" />
            <p>No vhost content was returned for this site.</p>
          </div>
        {:else}
          <div class="code-head">
            <span class="code-label mono">{preview.server} · {preview.site}</span>
            <Button size="sm" variant="ghost" onclick={copyPreview}>
              {#if copied}
                <Check size={12} strokeWidth={2} aria-hidden="true" />
                Copied
              {:else}
                <Copy size={12} strokeWidth={2} aria-hidden="true" />
                Copy
              {/if}
            </Button>
          </div>
          <pre aria-label={`${server} vhost configuration`}>{preview.content}</pre>
        {/if}
      </div>

      <footer class="foot">
        <Button variant="ghost" onclick={onclose}>Close</Button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 95;
    display: flex;
    justify-content: flex-end;
  }

  .backdrop {
    position: absolute;
    inset: 0;
    background: rgba(2, 6, 23, 0.68);
    animation: fade-in 150ms ease both;
  }

  :global(html[data-theme='light']) .backdrop {
    background: rgba(15, 23, 42, 0.38);
  }

  .drawer {
    position: relative;
    display: flex;
    flex-direction: column;
    width: min(720px, 100vw);
    height: 100dvh;
    background: var(--surface);
    border-left: 1px solid var(--border-strong);
    box-shadow: var(--shadow-lg);
    animation: drawer-in 200ms var(--ease) both;
  }

  .drawer:focus {
    outline: none;
  }

  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    padding: 18px 18px 14px;
    border-bottom: 1px solid var(--border);
  }

  .copy {
    min-width: 0;
  }

  .eyebrow {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  h2 {
    margin-top: 2px;
    font-size: 15.5px;
    font-weight: 600;
    letter-spacing: -0.01em;
    overflow-wrap: anywhere;
  }

  .subtitle {
    margin-top: 4px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .close {
    display: grid;
    place-items: center;
    flex: none;
    width: 32px;
    height: 32px;
    margin: -4px -4px 0 0;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--muted);
  }

  .close:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .tabs {
    display: flex;
    gap: 2px;
    padding: 10px 18px 0;
    border-bottom: 1px solid var(--border);
  }

  .tabs button {
    min-height: 36px;
    padding: 0 12px;
    border: 0;
    border-bottom: 2px solid transparent;
    border-radius: 6px 6px 0 0;
    background: transparent;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11.5px;
    font-weight: 500;
  }

  .tabs button:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .tabs button.active {
    border-bottom-color: var(--accent);
    color: var(--fg);
    background: var(--accent-soft);
  }

  .body {
    flex: 1;
    min-height: 0;
    padding: 14px 18px 18px;
    overflow: auto;
    overscroll-behavior: contain;
  }

  .loading {
    display: flex;
    flex-direction: column;
    gap: 9px;
    padding-top: 4px;
  }

  .code-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 8px;
  }

  .code-label {
    min-width: 0;
    color: var(--muted);
    font-size: 10px;
    overflow-wrap: anywhere;
  }

  pre {
    margin: 0;
    padding: 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 11px;
    line-height: 1.65;
    white-space: pre;
    overflow: auto;
    max-width: 100%;
    tab-size: 4;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 44px 16px;
    color: var(--muted);
    text-align: center;
    font-size: 12px;
  }

  .empty :global(svg) {
    color: var(--muted-2);
  }

  .foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
  }

  @keyframes fade-in {
    from {
      opacity: 0;
    }
  }

  @keyframes drawer-in {
    from {
      transform: translateX(18px);
      opacity: 0;
    }
  }

  @media (max-width: 560px) {
    .head {
      padding: 16px 16px 12px;
    }

    .tabs {
      padding: 8px 16px 0;
    }

    .tabs button {
      min-height: 44px;
    }

    .body {
      padding: 12px 16px 16px;
    }

    .foot {
      padding: 10px 16px;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .backdrop,
    .drawer {
      animation: none;
    }
  }
</style>
