<script lang="ts">
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Skeleton from './Skeleton.svelte';
  import ErrorBanner from './ErrorBanner.svelte';
  import { browseFs } from '../api';
  import { describeError } from '../errors';
  import type { FsListing } from '../types';
  import {
    ChevronRight,
    CornerLeftUp,
    Folder,
    FolderOpen,
    House,
    RefreshCw,
  } from 'lucide-svelte';

  interface Props {
    open: boolean;
    initialPath?: string;
    mode?: 'wsl' | 'windows' | 'local';
    onselect: (path: string) => void;
    onclose: () => void;
  }

  let { open, initialPath = '/', mode, onselect, onclose }: Props = $props();

  let listing = $state<FsListing | null>(null);
  let loading = $state(false);
  let error = $state<{ code: string; message: string } | null>(null);
  let requestedPath = $state('/');
  let previousOpen = false;
  let requestSeq = 0;

  const isWindows = $derived(mode === 'windows' || Boolean(listing?.path && /^[a-zA-Z]:[\\/]/.test(listing.path)));
  const rootPath = $derived(isWindows ? (listing?.path && /^[a-zA-Z]:/i.test(listing.path) ? listing.path.slice(0, 2) + '\\' : 'C:\\') : '/');

  function isAtRoot(p?: string): boolean {
    if (!p) return true;
    if (p === '/') return true;
    if (/^[a-zA-Z]:[\\/]?$/i.test(p)) return true;
    return false;
  }

  async function browse(path: string): Promise<void> {
    const seq = ++requestSeq;
    loading = true;
    requestedPath = path;
    error = null;
    try {
      const next = await browseFs(path, mode);
      if (seq !== requestSeq) return;
      listing = next;
    } catch (caught) {
      if (seq !== requestSeq) return;
      const described = describeError(caught);
      error = { code: described.code, message: described.message };
    } finally {
      if (seq === requestSeq) loading = false;
    }
  }

  $effect(() => {
    if (open && !previousOpen) {
      previousOpen = true;
      listing = null;
      error = null;
      void browse(initialPath || rootPath);
    }
    if (!open) previousOpen = false;
  });

  const crumbs = $derived.by(() => {
    const current = listing?.path;
    if (!current) return [];
    if (/^[a-zA-Z]:[\\/]/i.test(current)) {
      const drive = current.slice(0, 2);
      const rest = current.slice(2).replace(/^[\\/]+/, '');
      const parts = rest.split(/[\\/]+/).filter(Boolean);
      let accumulated = `${drive}\\`;
      return parts.map((part) => {
        accumulated += (accumulated.endsWith('\\') ? '' : '\\') + part;
        return { label: part, path: accumulated };
      });
    }
    const parts = current.split('/').filter(Boolean);
    let accumulated = '';
    return parts.map((part) => {
      accumulated += `/${part}`;
      return { label: part, path: accumulated };
    });
  });

  function parentPath(path: string): string {
    if (/^[a-zA-Z]:[\\/]?$/i.test(path)) return path.slice(0, 2) + '\\';
    if (/^[a-zA-Z]:[\\/]/i.test(path)) {
      const trimmed = path.replace(/[\\/]+$/, '');
      const lastIndex = Math.max(trimmed.lastIndexOf('\\'), trimmed.lastIndexOf('/'));
      if (lastIndex <= 2) return path.slice(0, 2) + '\\';
      return trimmed.slice(0, lastIndex);
    }
    if (path === '/') return '/';
    const trimmed = path.replace(/\/+$/, '');
    const index = trimmed.lastIndexOf('/');
    return index <= 0 ? '/' : trimmed.slice(0, index);
  }

  function choose(): void {
    if (!listing) return;
    onselect(listing.path);
    onclose();
  }
</script>

<Modal
  {open}
  title="Choose a project folder"
  description="Directories only. Your project files are never read or modified."
  size="lg"
  onclose={onclose}
>
  <div class="browser">
    <div class="toolbar">
      <button
        type="button"
        class="up"
        aria-label="Go up one level"
        title="Go up one level"
        disabled={!listing || isAtRoot(listing.path) || loading}
        data-autofocus
        onclick={() => listing && browse(parentPath(listing.path))}
      >
        <CornerLeftUp size={15} strokeWidth={2} aria-hidden="true" />
      </button>
      <div class="crumbs" aria-label="Current directory">
        <button type="button" class="crumb" onclick={() => browse(rootPath)} title="Root">
          <House size={13} strokeWidth={2} aria-hidden="true" />
          <span class="sr-only">Root</span>
        </button>
        {#each crumbs as crumb (crumb.path)}
          <ChevronRight class="sep" size={12} aria-hidden="true" />
          <button
            type="button"
            class="crumb common"
            title={crumb.path}
            onclick={() => browse(crumb.path)}
          >
            {crumb.label}
          </button>
        {/each}
      </div>
      <button
        type="button"
        class="refresh"
        aria-label="Reload directory"
        title="Reload directory"
        disabled={loading || !listing}
        onclick={() => listing && browse(listing.path)}
      >
        <RefreshCw size={14} strokeWidth={2} aria-hidden="true" />
      </button>
    </div>

    <div class="surface">
      {#if error}
        <div class="pad">
          <ErrorBanner
            title="Could not read that folder"
            code={error.code}
            message={error.message}
            onretry={() => browse(requestedPath || initialPath)}
          />
          <div class="error-actions">
            <Button size="sm" variant="secondary" onclick={() => browse('/')}>
              Go to root
            </Button>
          </div>
        </div>
      {:else if loading}
        <div class="dir-list" aria-label="Loading directories" aria-busy="true">
          {#each Array(5) as _, index (index)}
            <div class="dir-row skeleton-row">
              <Skeleton width="18px" height="18px" radius="4px" />
              <Skeleton width={`${44 + index * 9}%`} height="12px" />
            </div>
          {/each}
        </div>
      {:else if listing}
        {#if listing.entries.length === 0}
          <div class="empty">
            <FolderOpen size={22} strokeWidth={1.7} aria-hidden="true" />
            <p>This folder has no subdirectories.</p>
            <p class="sub">Choose it if this is the project root.</p>
          </div>
        {:else}
          <div class="dir-list" aria-label="Subdirectories">
            {#each listing.entries as entry (entry.path)}
              <button type="button" class="dir-row" onclick={() => browse(entry.path)}>
                <span class="row-icon" aria-hidden="true"><Folder size={16} strokeWidth={1.8} /></span>
                <span class="name">{entry.name}</span>
                <span class="chev" aria-hidden="true"><ChevronRight size={14} /></span>
              </button>
            {/each}
          </div>
        {/if}
      {/if}
    </div>

    <div class="selection">
      <span class="selection-label">Selected folder</span>
      <span class="selection-path mono">{listing?.path ?? '—'}</span>
    </div>
  </div>

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose}>Cancel</Button>
    <Button variant="primary" disabled={!listing || loading} onclick={choose}>
      Use this folder
    </Button>
  {/snippet}
</Modal>

<style>
  .browser {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .toolbar {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  .up,
  .refresh {
    display: grid;
    place-items: center;
    flex: none;
    width: 30px;
    height: 30px;
    padding: 0;
    border: 1px solid var(--border-strong);
    border-radius: 6px;
    background: var(--surface-2);
    color: var(--muted);
  }

  .up:hover:not(:disabled),
  .refresh:hover:not(:disabled) {
    color: var(--fg);
    border-color: var(--control-border);
  }

  .up:disabled,
  .refresh:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .crumbs {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
    min-width: 0;
    flex: 1;
    padding: 3px 6px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--input-bg);
    min-height: 30px;
  }

  .crumb {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 170px;
    padding: 2px 4px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .crumb:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  :global(.sep) {
    flex: none;
    color: var(--muted-2);
  }

  .surface {
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--input-bg);
    min-height: 280px;
    overflow: hidden;
  }

  .dir-list {
    display: flex;
    flex-direction: column;
    padding: 5px;
  }

  .dir-row {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    min-height: 38px;
    padding: 0 9px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--fg);
    text-align: left;
    font-size: 12.5px;
  }

  .row-icon {
    display: grid;
    place-items: center;
    flex: none;
    color: var(--muted);
  }

  .dir-row:hover {
    background: var(--surface-2);
  }

  .dir-row:hover .row-icon {
    color: var(--success-fg);
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .chev {
    display: grid;
    place-items: center;
    flex: none;
    color: var(--muted-2);
  }

  .skeleton-row {
    pointer-events: none;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: 280px;
    gap: 6px;
    padding: 24px;
    color: var(--muted);
    text-align: center;
  }

  .empty :global(svg) {
    margin-bottom: 4px;
    color: var(--muted-2);
  }

  .empty p {
    font-size: 12.5px;
  }

  .empty .sub {
    color: var(--muted-2);
    font-size: 11.5px;
  }

  .pad {
    padding: 12px;
  }

  .error-actions {
    display: flex;
    justify-content: flex-start;
    margin-top: 10px;
  }

  .selection {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-2);
  }

  .selection-label {
    flex: none;
    color: var(--muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .selection-path {
    min-width: 0;
    color: var(--fg);
    font-size: 11.5px;
    overflow-wrap: anywhere;
  }

  @media (max-width: 560px) {
    .crumb {
      max-width: 120px;
    }

    .selection {
      flex-direction: column;
      gap: 2px;
    }
  }
</style>
