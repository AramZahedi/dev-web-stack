<script lang="ts">
  import { tick } from 'svelte';
  import { ChevronDown, Plus, Trash2 } from 'lucide-svelte';
  import * as api from '../api';
  import { describePHPError, type PHPErrorDescription } from '../errors';
  import type { PHPExtension, PHPDefaults, PHPMigrationResult, Site } from '../types';
  import Button from './Button.svelte';
  import ErrorBanner from './ErrorBanner.svelte';
  import ExtensionToggles from './ExtensionToggles.svelte';
  import Field from './Field.svelte';
  import Modal from './Modal.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    open: boolean;
    version: string;
    versions: string[];
    sites: Site[];
    onclose: () => void;
    onsaved?: () => void;
  }

  let { open, version, versions, sites, onclose, onsaved }: Props = $props();

  interface IniRow {
    id: number;
    key: string;
    value: string;
  }

  type Phase = 'edit' | 'confirm' | 'result';

  let activeVersion = $state('');
  let loadedVersion = $state<string | null>(null);
  let phase = $state<Phase>('edit');
  let loading = $state(false);
  let saving = $state(false);
  let loadError = $state<PHPErrorDescription | null>(null);
  let error = $state<PHPErrorDescription | null>(null);
  let extensions = $state<PHPExtension[]>([]);
  let rows = $state<IniRow[]>([]);
  let overrides = $state<Record<string, boolean>>({});
  let results = $state<PHPMigrationResult[] | null>(null);
  let rowSeq = 0;
  let requestSeq = 0;
  let retryToken = $state(0);
  let wasOpen = false;

  const canSave = $derived(
    !loading &&
      !saving &&
      !loadError &&
      loadedVersion !== null &&
      loadedVersion === activeVersion,
  );

  const versionOptions = $derived(
    Array.from(new Set([...versions, version])).sort((a, b) =>
      b.localeCompare(a, undefined, { numeric: true }),
    ),
  );

  const affected = $derived(
    sites.filter((site) => site.kind === 'php' && site.php_version === activeVersion),
  );

  const directiveCount = $derived(rows.filter((row) => row.key.trim()).length);
  const enabledCount = $derived(
    extensions.filter((extension) => {
      const override = overrides[extension.name];
      return override === undefined ? extension.default : override;
    }).length,
  );

  $effect(() => {
    if (open && !wasOpen) {
      wasOpen = true;
      activeVersion = version;
      phase = 'edit';
      results = null;
      loadError = null;
      error = null;
    } else if (!open) {
      wasOpen = false;
    }
  });

  $effect(() => {
    if (!open || !activeVersion) return;
    void retryToken;
    const target = activeVersion;
    const requestID = ++requestSeq;
    loading = true;
    loadedVersion = null;
    loadError = null;
    error = null;
    extensions = [];
    rows = [];
    overrides = {};
    results = null;
    phase = 'edit';
    Promise.all([api.getPHPDefaults(target), api.phpExtensions(target)])
      .then(([defaults, catalog]) => {
        if (requestID !== requestSeq) return;
        applyLoaded(defaults, catalog);
        loadedVersion = target;
      })
      .catch((caught) => {
        if (requestID !== requestSeq) return;
        loadError = describePHPError(caught);
        loadedVersion = null;
      })
      .finally(() => {
        if (requestID === requestSeq) loading = false;
      });
  });

  function retryLoad(): void {
    loadError = null;
    retryToken += 1;
  }

  function applyLoaded(defaults: PHPDefaults, catalog: PHPExtension[]): void {
    extensions = catalog;
    rows = Object.entries(defaults.ini).map(([key, value]) => ({
      id: ++rowSeq,
      key,
      value,
    }));
    const next: Record<string, boolean> = {};
    for (const extension of catalog) {
      const current = defaults.extensions[extension.name];
      if (current !== undefined && current !== extension.default) next[extension.name] = current;
    }
    overrides = next;
  }

  function addRow(): void {
    rows = [...rows, { id: ++rowSeq, key: '', value: '' }];
    void tick().then(() => {
      document.getElementById(`default-ini-key-${rows[rows.length - 1]?.id}`)?.focus();
    });
  }

  function removeRow(id: number): void {
    rows = rows.filter((row) => row.id !== id);
  }

  function buildIni(): Record<string, string> | null {
    const ini: Record<string, string> = {};
    for (const row of rows) {
      const key = row.key.trim();
      if (!key) {
        if (row.value.trim()) {
          error = { code: 'INVALID_REQUEST', message: 'Add a directive name for this value.' };
          return null;
        }
        continue;
      }
      if (key in ini) {
        error = {
          code: 'INVALID_REQUEST',
          message: `The directive “${key}” is defined more than once.`,
        };
        return null;
      }
      ini[key] = row.value;
    }
    return ini;
  }

  function buildExtensions(): Record<string, boolean> {
    const values: Record<string, boolean> = {};
    for (const extension of extensions) {
      const override = overrides[extension.name];
      values[extension.name] = override === undefined ? extension.default : override;
    }
    return values;
  }

  function review(): void {
    error = null;
    if (!canSave || loadedVersion !== activeVersion) return;
    if (!buildIni()) return;
    phase = 'confirm';
  }

  async function save(): Promise<void> {
    if (saving || !canSave || loadedVersion !== activeVersion) return;
    const ini = buildIni();
    if (!ini) {
      phase = 'edit';
      return;
    }
    saving = true;
    error = null;
    try {
      const result = await api.savePHPDefaults(activeVersion, {
        ini,
        extensions: buildExtensions(),
      });
      results = result.sites;
      phase = 'result';
      onsaved?.();
    } catch (caught) {
      error = describePHPError(caught);
      phase = 'edit';
    } finally {
      saving = false;
    }
  }

  function close(): void {
    if (saving) return;
    onclose();
  }

  function switchVersion(event: Event): void {
    const select = event.currentTarget as HTMLSelectElement;
    activeVersion = select.value;
  }
</script>

<Modal
  {open}
  title={`PHP ${activeVersion} defaults`}
  description="Shared php.ini directives and extension state for every site on this line. A site can override them individually."
  size="lg"
  onclose={close}
>
  <div class="defaults">
    {#if phase === 'result'}
      <div class="result">
        <p class="result-title">Defaults saved for PHP {activeVersion}.</p>
        {#if results && results.length > 0}
          <p class="result-copy">Running sites on this line were re-applied independently.</p>
          <ul class="site-results" aria-label="Defaults apply results">
            {#each results as result (result.site)}
              <li class:failed={!result.ok}>
                <span class="mono">{result.site}</span>
                <span>{result.ok ? 'Applied' : result.error ?? 'Failed'}</span>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="result-copy">
            No running sites on PHP {activeVersion} needed re-applying. New sites inherit these
            defaults.
          </p>
        {/if}
      </div>
    {:else if phase === 'confirm'}
      <div class="confirm">
        <p class="confirm-title">Save and apply these defaults to PHP {activeVersion}?</p>
        <p class="confirm-copy">
          Running sites on this line are validated and swapped independently. A failing site keeps
          its previous configuration and is reported below; the others still apply.
        </p>
        <div class="confirm-block">
          <p class="label">Affected sites</p>
          {#if affected.length === 0}
            <p class="empty">No sites currently use PHP {activeVersion}.</p>
          {:else}
            <ul class="site-chips" aria-label="Sites using PHP {activeVersion}">
              {#each affected as site (site.id)}
                <li class="mono">{site.name}</li>
              {/each}
            </ul>
          {/if}
        </div>
        <dl class="confirm-summary">
          <div><dt>Directives</dt><dd>{directiveCount}</dd></div>
          <div><dt>Extensions</dt><dd>{enabledCount} on</dd></div>
        </dl>
      </div>
    {:else if loading}
      <div class="loading" aria-busy="true" aria-label="Loading PHP defaults">
        <Skeleton width="160px" height="11px" />
        {#each Array(5) as _, index (index)}
          <Skeleton width={`${92 - (index % 3) * 12}%`} height="30px" radius="7px" />
        {/each}
      </div>
    {:else if loadError}
      <ErrorBanner
        title="Could not load PHP defaults"
        code={loadError.code}
        message={loadError.message}
        onretry={retryLoad}
      />
    {:else}
      {#if error}
        <div class="php-error" role="alert">
          <div class="error-title">
            <span class="code mono">{error.code}</span>
            <span>{error.message}</span>
          </div>
          {#if error.details?.message}
            <p class="detail">{error.details.message}</p>
          {/if}
          {#if error.details?.line !== undefined && error.details.line > 0}
            <p class="detail">Line {error.details.line}</p>
          {/if}
          {#if error.details?.field}
            <p class="detail">Field <span class="mono">{error.details.field}</span></p>
          {/if}
          {#if error.details?.output}
            <pre class="output" aria-label="PHP defaults validation output">{error.details.output}</pre>
          {/if}
        </div>
      {/if}
      <div class="form">
        <Field id="defaults-version" label="PHP line">
          <div class="select-wrap">
            <select
              id="defaults-version"
              class="input mono"
              value={activeVersion}
              onchange={switchVersion}
            >
              {#each versionOptions as option (option)}
                <option value={option}>PHP {option}</option>
              {/each}
            </select>
            <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
          </div>
        </Field>

        <div class="block">
          <div class="block-head">
            <div>
              <h3>Directives</h3>
              <p>Baseline values merged into every site's php.ini on this line.</p>
            </div>
            <Button size="sm" variant="ghost" onclick={addRow}>
              <Plus size={13} strokeWidth={2.2} aria-hidden="true" />
              Add directive
            </Button>
          </div>
          {#if rows.length === 0}
            <p class="empty">No baseline directives. The embedded PHP defaults still apply.</p>
          {:else}
            <div class="rows" role="list" aria-label="Default PHP directives">
              {#each rows as row, index (row.id)}
                <div class="row" role="listitem">
                  <label class="sr-only" for={`default-ini-key-${row.id}`}>Directive {index + 1} name</label>
                  <input
                    id={`default-ini-key-${row.id}`}
                    class="input mono key"
                    type="text"
                    bind:value={row.key}
                    aria-label={`Default directive ${index + 1} name`}
                    autocomplete="off"
                    spellcheck="false"
                  />
                  <span class="equals mono" aria-hidden="true">=</span>
                  <label class="sr-only" for={`default-ini-value-${row.id}`}>Directive {index + 1} value</label>
                  <input
                    id={`default-ini-value-${row.id}`}
                    class="input mono value"
                    type="text"
                    bind:value={row.value}
                    aria-label={`Default directive ${index + 1} value`}
                    autocomplete="off"
                    spellcheck="false"
                  />
                  <button
                    type="button"
                    class="remove"
                    aria-label={`Remove default directive ${index + 1}`}
                    onclick={() => removeRow(row.id)}
                  >
                    <Trash2 size={14} strokeWidth={1.9} aria-hidden="true" />
                  </button>
                </div>
              {/each}
            </div>
          {/if}
        </div>

        <div class="block">
          <h3>Extensions</h3>
          <p class="block-copy">
            Inherit uses the embedded catalog default. Site overrides still win on individual
            sites.
          </p>
          <ExtensionToggles
            {extensions}
            builtins={[]}
            value={overrides}
            onchange={(next) => (overrides = next)}
          />
        </div>

        <div class="block">
          <p class="label">Affected sites</p>
          {#if affected.length === 0}
            <p class="empty">No sites currently use PHP {activeVersion}.</p>
          {:else}
            <ul class="site-chips" aria-label="Sites using PHP {activeVersion}">
              {#each affected as site (site.id)}
                <li class="mono">{site.name}</li>
              {/each}
            </ul>
          {/if}
        </div>
      </div>
    {/if}
  </div>

  {#snippet footer()}
    {#if phase === 'result'}
      <Button variant="primary" onclick={close}>Done</Button>
    {:else if phase === 'confirm'}
      <Button variant="ghost" onclick={() => (phase = 'edit')} disabled={saving}>Back</Button>
      <Button variant="primary" onclick={save} loading={saving} disabled={saving || !canSave}>
        Save and apply
      </Button>
    {:else}
      <Button variant="ghost" onclick={close} disabled={saving || loading}>Cancel</Button>
      <Button variant="primary" onclick={review} disabled={!canSave}>
        Save defaults
      </Button>
    {/if}
  {/snippet}
</Modal>

<style>
  .defaults {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .form {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  .block {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .block-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }

  h3 {
    font-size: 12.5px;
    font-weight: 600;
  }

  .block p,
  .block-copy {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .php-error {
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 45%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .error-title {
    display: flex;
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 6px;
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 500;
    line-height: 1.45;
  }

  .code {
    padding: 0 5px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--danger) 14%, transparent);
    color: var(--danger-fg);
    font-size: 10px;
  }

  .detail {
    margin-top: 4px;
    color: var(--muted);
    font-size: 10.5px;
    line-height: 1.5;
  }

  .output {
    margin: 6px 0 0;
    padding: 9px;
    max-height: 160px;
    overflow: auto;
    border: 1px solid color-mix(in srgb, var(--danger) 25%, var(--border));
    border-radius: var(--radius);
    background: var(--log-bg);
    color: var(--log-fg);
    font-family: var(--font-mono);
    font-size: 9.5px;
    line-height: 1.55;
    white-space: pre-wrap;
  }

  .select-wrap {
    position: relative;
    max-width: 220px;
  }

  .select-wrap :global(select) {
    appearance: none;
    width: 100%;
    padding-right: 30px;
  }

  .select-wrap :global(svg) {
    position: absolute;
    top: 50%;
    right: 10px;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .rows {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .row {
    display: grid;
    grid-template-columns: minmax(0, 0.9fr) 10px minmax(0, 1.1fr) 30px;
    align-items: center;
    gap: 6px;
  }

  .key,
  .value {
    min-width: 0;
  }

  .equals {
    color: var(--muted-2);
    font-size: 11px;
    text-align: center;
  }

  .remove {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    padding: 0;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: transparent;
    color: var(--muted-2);
  }

  .remove:hover {
    border-color: var(--border);
    background: var(--surface-2);
    color: var(--danger-fg);
  }

  .empty {
    padding: 9px 11px;
    border: 1px dashed var(--border);
    border-radius: var(--radius-lg);
    color: var(--muted);
    font-size: 11px;
  }

  .label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .site-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .site-chips li {
    padding: 2px 6px;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface-2);
    color: var(--fg);
    font-size: 10.5px;
  }

  .loading {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .confirm {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .confirm-title {
    color: var(--fg);
    font-size: 13px;
    font-weight: 600;
  }

  .confirm-copy {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .confirm-block {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .confirm-summary {
    display: flex;
    gap: 8px;
    margin: 0;
  }

  .confirm-summary > div {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 7px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface-2);
  }

  .confirm-summary dt {
    color: var(--muted-2);
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .confirm-summary dd {
    margin: 0;
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 11px;
  }

  .result {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .result-title {
    color: var(--fg);
    font-size: 13px;
    font-weight: 600;
  }

  .result-copy {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .site-results {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .site-results li {
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

  .site-results li:first-child {
    border-top: 0;
  }

  .site-results li.failed {
    color: var(--danger-fg);
  }

  @media (max-width: 560px) {
    .block-head {
      flex-direction: column;
    }

    .row {
      grid-template-columns: minmax(0, 1fr) 28px;
      padding: 8px;
      border: 1px solid var(--border);
      border-radius: var(--radius);
      background: var(--surface-2);
    }

    .equals {
      display: none;
    }

    .row .value {
      grid-column: 1;
    }

    .remove {
      grid-column: 2;
      grid-row: 1 / span 2;
      align-self: center;
    }

    .select-wrap {
      max-width: none;
    }
  }
</style>
