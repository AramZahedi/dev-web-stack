<script lang="ts">
  import { Check, FileCode, RefreshCw, Save, TriangleAlert } from 'lucide-svelte';
  import * as api from '../api';
  import { ApiError, describeError } from '../errors';
  import { addToast, toastError } from '../stores';
  import type {
    ServiceConfigConflict,
    ServiceConfigDiff,
    ServiceConfigFile,
    ServiceConfigResolution,
    ServiceConfigResult,
    ServiceView,
  } from '../types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import ErrorBanner from './ErrorBanner.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    open: boolean;
    service: ServiceView;
    onclose: () => void;
  }

  let { open, service, onclose }: Props = $props();

  let files = $state<ServiceConfigFile[]>([]);
  let loading = $state(true);
  let error = $state<{ code: string; message: string } | null>(null);
  let selectedPath = $state('');
  let content = $state('');
  let original = $state('');
  let contentLoading = $state(false);
  let contentError = $state<{ code: string; message: string } | null>(null);
  let diff = $state<ServiceConfigDiff | null>(null);
  let diffLoading = $state(false);
  let diffError = $state<{ code: string; message: string } | null>(null);
  let conflict = $state<ServiceConfigConflict | null>(null);
  let applying = $state(false);
  let actionError = $state<{ code: string; message: string } | null>(null);
  let result = $state<ServiceConfigResult | null>(null);
  let resetOpen = $state(false);
  let resetting = $state(false);
  let loadGeneration = 0;
  let loadedKey = '';

  const selectedFile = $derived(files.find((file) => file.path === selectedPath) ?? null);
  const dirty = $derived(content !== original);
  const inactive = $derived(!service.active);

  $effect(() => {
    const key = open ? `${service.id}:${service.state}:${service.active}` : '';
    if (key === loadedKey) return;
    loadedKey = key;
    if (!open) return;
    loadGeneration += 1;
    const generation = loadGeneration;
    files = [];
    selectedPath = '';
    content = '';
    original = '';
    diff = null;
    conflict = null;
    result = null;
    actionError = null;
    error = null;
    loading = true;
    void loadFiles(generation);
  });

  async function loadFiles(generation = loadGeneration): Promise<void> {
    loading = true;
    error = null;
    try {
      const listing = await api.listServiceConfig(service.id);
      if (generation !== loadGeneration) return;
      files = listing.files;
      if (files.length > 0 && !selectedPath) {
        await selectFile(files[0].path, generation);
      }
    } catch (caught) {
      if (generation === loadGeneration) error = describeError(caught);
    } finally {
      if (generation === loadGeneration) loading = false;
    }
  }

  async function selectFile(path: string, generation = loadGeneration): Promise<void> {
    if (path === selectedPath && original) return;
    selectedPath = path;
    contentLoading = true;
    contentError = null;
    diff = null;
    conflict = null;
    result = null;
    actionError = null;
    try {
      const loaded = await api.getServiceConfigContent(service.id, path);
      if (generation !== loadGeneration) return;
      content = loaded.content;
      original = loaded.content;
    } catch (caught) {
      if (generation === loadGeneration) contentError = describeError(caught);
    } finally {
      if (generation === loadGeneration) contentLoading = false;
    }
  }

  async function previewDiff(): Promise<void> {
    if (!selectedPath || diffLoading) return;
    diffLoading = true;
    diffError = null;
    try {
      diff = await api.diffServiceConfig(service.id, selectedPath, content);
      conflict = diff.conflict?.resolution_required ? diff.conflict : null;
    } catch (caught) {
      diffError = describeError(caught);
    } finally {
      diffLoading = false;
    }
  }

  async function apply(resolution?: ServiceConfigResolution): Promise<void> {
    if (!selectedPath || applying) return;
    applying = true;
    actionError = null;
    result = null;
    try {
      const applied = await api.applyServiceConfig(service.id, {
        path: selectedPath,
        content,
        resolution,
      });
      if (applied.conflict?.resolution_required) {
        conflict = applied.conflict;
        return;
      }
      result = applied;
      conflict = null;
      diff = null;
      addToast({
        kind: applied.changed === false ? 'info' : 'success',
        title:
          applied.action === 'reset'
            ? 'Configuration reset'
            : applied.changed === false
              ? 'No file changed'
              : 'Configuration applied',
        message:
          applied.message ??
          (applied.validation_pending
            ? 'Saved. Validation is pending until the service is active.'
            : applied.live
              ? 'The file was validated and the service reloaded.'
              : 'Saved. The running service was not reloaded.'),
      });
      await loadFiles();
      if (selectedPath) await selectFile(selectedPath);
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'CONFIG_CONFLICT') {
        conflict = conflictFromError(caught);
        return;
      }
      actionError = describeError(caught);
      toastError('Could not apply configuration', `${actionError.code}: ${actionError.message}`);
    } finally {
      applying = false;
    }
  }

  function conflictFromError(error: ApiError): ServiceConfigConflict {
    const details =
      error.details && typeof error.details === 'object'
        ? (error.details as Record<string, unknown>)
        : {};
    const nested =
      details.conflict && typeof details.conflict === 'object'
        ? (details.conflict as ServiceConfigConflict)
        : {};
    return {
      ...nested,
      active: nested.active ?? true,
      resolution_required: nested.resolution_required ?? true,
      reason:
        nested.reason ??
        (typeof details.reason === 'string' ? details.reason : undefined) ??
        error.message,
      allowed_resolutions: nested.allowed_resolutions ?? ['keep', 'use', 'merge'],
    };
  }

  async function keepMine(): Promise<void> {
    await apply('keep');
  }

  async function useProposed(): Promise<void> {
    await apply('use');
  }

  async function mergeManually(): Promise<void> {
    await apply('merge');
  }

  async function reset(): Promise<void> {
    if (!selectedPath || resetting) return;
    resetting = true;
    actionError = null;
    try {
      result = await api.resetServiceConfig(service.id, selectedPath);
      resetOpen = false;
      diff = null;
      conflict = null;
      addToast({
        kind: 'success',
        title: 'Configuration reset',
        message: result.message ?? 'Restored the last known backup for this file.',
      });
      await loadFiles();
      await selectFile(selectedPath);
    } catch (caught) {
      actionError = describeError(caught);
    } finally {
      resetting = false;
    }
  }

  function classVariant(cls: string): 'neutral' | 'accent' | 'info' {
    if (cls === 'generated') return 'accent';
    if (cls === 'base') return 'info';
    return 'neutral';
  }
</script>

<Modal
  {open}
  title={`${service.display_name} configuration`}
  description={`Editable files under services/${service.id}/config only. Data, logs, secrets, certificates, and backups are never exposed.`}
  size="lg"
  onclose={onclose}
>
  {#if inactive}
    <div class="inactive-note" role="status">
      <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
      <p>
        This service is inactive. Saves are allowed, but validation is pending until the
        service is activated; the file is not claimed to be live.
      </p>
    </div>
  {/if}

  {#if error && files.length === 0}
    <ErrorBanner
      title="Could not load service configuration"
      code={error.code}
      message={error.message}
      onretry={() => loadFiles()}
    />
  {:else if loading && files.length === 0}
    <div class="config-loading" aria-busy="true" aria-label="Loading service configuration">
      <Skeleton width="32%" height="14px" />
      <Skeleton width="100%" height="290px" radius="8px" />
    </div>
  {:else}
    <div class="config-layout">
      <aside class="file-list" aria-label="Configuration files">
        <div class="file-list-head">
          <span>{files.length} files</span>
          <Button size="sm" variant="ghost" loading={loading} onclick={() => loadFiles()}>
            {#if !loading}<RefreshCw size={12} strokeWidth={2} aria-hidden="true" />{/if}
            Reload
          </Button>
        </div>
        {#if files.length === 0}
          <p class="no-files">No editable config files were found.</p>
        {:else}
          {#each files as file (file.path)}
            <button
              type="button"
              class="file-row"
              class:active={selectedPath === file.path}
              aria-current={selectedPath === file.path ? 'true' : undefined}
              onclick={() => selectFile(file.path)}
            >
              <FileCode size={13} strokeWidth={1.8} aria-hidden="true" />
              <span class="file-copy">
                <span class="file-path mono">{file.path}</span>
                <span class="file-badges">
                  <Badge variant={classVariant(file.class)}>{file.class}</Badge>
                  {#if file.modified}<Badge variant="warning">Modified</Badge>{/if}
                  {#if file.detached}<Badge variant="danger">Detached</Badge>{/if}
                  {#if file.conflict}<Badge variant="warning">Conflict</Badge>{/if}
                </span>
              </span>
            </button>
          {/each}
        {/if}
      </aside>

      <section class="editor-pane" aria-label="Selected configuration file">
        {#if contentLoading}
          <div class="config-loading" aria-busy="true">
            <Skeleton width="46%" height="12px" />
            <Skeleton width="100%" height="300px" radius="8px" />
          </div>
        {:else if contentError}
          <ErrorBanner
            title="Could not load the file"
            code={contentError.code}
            message={contentError.message}
            onretry={() => selectFile(selectedPath)}
          />
        {:else if selectedPath}
          <div class="editor-head">
            <div class="editor-title">
              <span class="mono">{selectedPath}</span>
              <div>
                {#if selectedFile}
                  <Badge variant={classVariant(selectedFile.class)}>{selectedFile.class}</Badge>
                  {#if dirty}<Badge variant="warning">Unsaved changes</Badge>{/if}
                {/if}
              </div>
            </div>
            <div class="editor-actions">
              <Button
                size="sm"
                variant="ghost"
                loading={diffLoading}
                disabled={diffLoading || !dirty}
                onclick={previewDiff}
              >
                Preview diff
              </Button>
              <Button
                size="sm"
                variant="primary"
                loading={applying}
                disabled={applying || (!dirty && !conflict)}
                onclick={() => apply()}
              >
                {#if !applying}<Save size={12} strokeWidth={2} aria-hidden="true" />{/if}
                Apply
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={applying}
                onclick={() => (resetOpen = true)}
              >
                Reset
              </Button>
            </div>
          </div>

          {#if diffError}
            <div class="inline-error">
              <ErrorBanner
                title="Could not build diff"
                code={diffError.code}
                message={diffError.message}
                onretry={previewDiff}
                ondismiss={() => (diffError = null)}
              />
            </div>
          {/if}

          {#if conflict}
            <div class="conflict" role="alert">
              <TriangleAlert size={14} strokeWidth={2} aria-hidden="true" />
              <div>
                <p class="conflict-title">Conflict detected</p>
                <p>{conflict.reason ?? 'This file has local edits that conflict with generated output.'}</p>
                <div class="conflict-actions">
                  <Button size="sm" variant="ghost" onclick={keepMine}>Keep mine</Button>
                  <Button size="sm" variant="secondary" onclick={useProposed}>Use proposed</Button>
                  <Button size="sm" variant="primary" onclick={mergeManually}>Use merged editor content</Button>
                </div>
              </div>
            </div>
          {/if}

          {#if actionError}
            <div class="inline-error">
              <ErrorBanner
                title="Configuration action failed"
                code={actionError.code}
                message={actionError.message}
                ondismiss={() => (actionError = null)}
              />
            </div>
          {/if}

          {#if result}
            <div class="result" role="status">
              <Check size={13} strokeWidth={2.2} aria-hidden="true" />
              <div>
                <p>
                  {result.message ??
                    (result.action === 'reset' ? 'Configuration reset.' : 'Configuration applied.')}
                </p>
                {#if result.validation_pending}<p>Validation pending until activation.</p>{/if}
                {#if result.restart_required}<p>Restart required before this file is live.</p>{/if}
                {#if result.backup_path}<p class="mono">Backup: {result.backup_path}</p>{/if}
              </div>
            </div>
          {/if}

          <label class="editor-label" for="service-config-editor">File content</label>
          <textarea
            id="service-config-editor"
            class="input mono config-editor"
            rows="18"
            spellcheck="false"
            bind:value={content}
            oninput={() => {
              result = null;
              conflict = null;
            }}
          ></textarea>

          {#if diff}
            <div class="diff-panel">
              <p class="diff-title">Unified diff</p>
              <pre>{diff.diff}</pre>
            </div>
          {/if}
        {:else}
          <p class="no-files">Select a file to edit.</p>
        {/if}
      </section>
    </div>
  {/if}

  {#snippet footer()}
    <Button variant="ghost" onclick={onclose}>Close</Button>
  {/snippet}
</Modal>

<ConfirmDialog
  open={resetOpen}
  title={`Reset ${selectedPath || 'configuration'}?`}
  description="Restores the last known backup for this file. Any current unsaved edits are discarded."
  confirmLabel="Reset file"
  requireText="RESET"
  danger
  busy={resetting}
  onconfirm={reset}
  oncancel={() => (resetOpen = false)}
/>

<style>
  .inactive-note {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 12px;
    padding: 9px 10px;
    border: 1px solid color-mix(in oklab, var(--warning-fg) 35%, var(--border));
    border-radius: var(--radius);
    background: color-mix(in oklab, var(--warning-bg) 45%, var(--surface));
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .inactive-note :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--warning-fg);
  }

  .config-loading {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .config-layout {
    display: grid;
    grid-template-columns: minmax(190px, 0.7fr) minmax(0, 2.3fr);
    gap: 12px;
    min-height: 360px;
  }

  .file-list {
    min-width: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface-2);
    overflow: hidden auto;
    max-height: 560px;
  }

  .file-list-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
    padding: 7px 9px;
    border-bottom: 1px solid var(--border);
    color: var(--muted);
    font-size: 9.5px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .file-row {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    width: 100%;
    padding: 8px 9px;
    border: 0;
    border-bottom: 1px solid var(--border);
    background: transparent;
    color: var(--fg);
    text-align: left;
  }

  .file-row:hover {
    background: var(--surface);
  }

  .file-row.active {
    background: var(--accent-soft);
    box-shadow: inset 2px 0 0 var(--accent);
  }

  .file-row :global(svg) {
    flex: none;
    margin-top: 2px;
    color: var(--muted-2);
  }

  .file-copy {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .file-path {
    font-size: 10px;
    overflow-wrap: anywhere;
  }

  .file-badges {
    display: flex;
    flex-wrap: wrap;
    gap: 3px;
  }

  .editor-pane {
    min-width: 0;
  }

  .editor-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 8px;
  }

  .editor-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .editor-title > span {
    font-size: 10.5px;
    overflow-wrap: anywhere;
  }

  .editor-title > div {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .editor-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
  }

  .editor-label {
    display: block;
    margin: 4px 0 5px;
    color: var(--muted);
    font-size: 10px;
    font-weight: 500;
  }

  .config-editor {
    width: 100%;
    min-height: 290px;
    resize: vertical;
    line-height: 1.55;
  }

  .inline-error {
    margin-bottom: 9px;
  }

  .conflict {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin-bottom: 9px;
    padding: 9px 10px;
    border: 1px solid color-mix(in oklab, var(--warning-fg) 35%, var(--border));
    border-radius: var(--radius);
    background: color-mix(in oklab, var(--warning-bg) 45%, var(--surface));
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .conflict :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--warning-fg);
  }

  .conflict-title {
    color: var(--warning-fg);
    font-size: 11px;
    font-weight: 600;
  }

  .conflict-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
  }

  .result {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    margin-bottom: 9px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .result :global(svg) {
    flex: none;
    margin-top: 1px;
    color: var(--success-fg);
  }

  .result .mono {
    margin-top: 2px;
    font-size: 9.5px;
    overflow-wrap: anywhere;
  }

  .diff-panel {
    margin-top: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .diff-title {
    padding: 6px 9px;
    border-bottom: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }

  .diff-panel pre {
    max-height: 220px;
    margin: 0;
    padding: 9px;
    overflow: auto;
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 10.5px;
    line-height: 1.5;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .no-files {
    padding: 18px;
    color: var(--muted);
    font-size: 11px;
  }

  @media (max-width: 780px) {
    .config-layout {
      grid-template-columns: minmax(0, 1fr);
    }

    .file-list {
      max-height: 190px;
    }
  }
</style>
