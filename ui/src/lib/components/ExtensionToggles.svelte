<script lang="ts">
  import type { PHPExtension } from '../types';
  import Badge from './Badge.svelte';

  interface Props {
    extensions: PHPExtension[];
    builtins: string[];
    value: Record<string, boolean>;
    /** Effective version baseline for inherited extensions. */
    defaults?: Record<string, boolean>;
    onchange: (value: Record<string, boolean>) => void;
    disabled?: boolean;
  }

  let { extensions, builtins, value, defaults = {}, onchange, disabled = false }: Props = $props();

  type Mode = 'inherit' | 'on' | 'off';

  function modeOf(name: string): Mode {
    const override = value[name];
    if (override === undefined) return 'inherit';
    return override ? 'on' : 'off';
  }

  function inheritedState(extension: PHPExtension): boolean {
    return defaults[extension.name] ?? extension.default;
  }

  function stateLabel(extension: PHPExtension): string {
    const mode = modeOf(extension.name);
    if (mode === 'inherit') return `Inherit (${inheritedState(extension) ? 'on' : 'off'})`;
    return mode === 'on' ? 'Enabled for this site' : 'Disabled for this site';
  }

  function select(name: string, mode: Mode): void {
    if (disabled) return;
    const next = { ...value };
    if (mode === 'inherit') {
      delete next[name];
    } else {
      next[name] = mode === 'on';
    }
    onchange(next);
  }
</script>

<div class="extensions">
  {#if extensions.length === 0}
    <p class="empty">No shared extensions are available for this PHP line.</p>
  {:else}
    <div class="extension-list" role="list" aria-label="PHP extensions">
      {#each extensions as extension (extension.name)}
        {@const mode = modeOf(extension.name)}
        <div class="extension" role="group" aria-label={extension.name}>
          <div class="copy">
            <div class="name-row">
              <span class="name mono">{extension.name}</span>
              {#if extension.type === 'zend_extension'}<Badge>zend_extension</Badge>{/if}
              {#if extension.requires?.length}
                <span class="requires">requires {extension.requires.join(', ')}</span>
              {/if}
            </div>
            <p class="state">{stateLabel(extension)}</p>
          </div>
          <div class="modes">
            <button
              type="button"
              class="mode"
              class:active={mode === 'inherit'}
              aria-pressed={mode === 'inherit'}
              aria-label={`Inherit ${extension.name}`}
              {disabled}
              onclick={() => select(extension.name, 'inherit')}
            >
              Inherit
            </button>
            <button
              type="button"
              class="mode"
              class:active={mode === 'on'}
              aria-pressed={mode === 'on'}
              aria-label={`Enable ${extension.name}`}
              {disabled}
              onclick={() => select(extension.name, 'on')}
            >
              On
            </button>
            <button
              type="button"
              class="mode"
              class:active={mode === 'off'}
              aria-pressed={mode === 'off'}
              aria-label={`Disable ${extension.name}`}
              {disabled}
              onclick={() => select(extension.name, 'off')}
            >
              Off
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if builtins.length > 0}
    <div class="builtins">
      <p class="builtins-title">Built into PHP</p>
      <div class="builtin-list" role="list" aria-label="Built-in PHP extensions">
        {#each builtins as name (name)}
          <div class="builtin" role="listitem">
            <span class="mono">{name}</span>
            <span class="builtin-state">always on</span>
            <button
              type="button"
              class="builtin-switch"
              role="switch"
              aria-checked="true"
              aria-label={`${name} built-in extension`}
              disabled
            >
              <span class="builtin-track"><span class="builtin-thumb"></span></span>
            </button>
          </div>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .extensions {
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-width: 0;
  }

  .extension-list {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    overflow: hidden;
  }

  .extension {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 10px;
    border-top: 1px solid var(--border);
    background: var(--input-bg);
  }

  .extension:first-child {
    border-top: 0;
  }

  .copy {
    min-width: 0;
  }

  .name-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .name {
    color: var(--fg);
    font-size: 11.5px;
    font-weight: 500;
  }

  .requires,
  .state {
    color: var(--muted-2);
    font-size: 9.5px;
  }

  .state {
    margin-top: 2px;
  }

  .modes {
    display: inline-flex;
    flex: none;
    padding: 2px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-2);
  }

  .mode {
    min-width: 42px;
    height: 24px;
    padding: 0 7px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
    font-family: var(--font-sans);
    font-size: 10px;
    font-weight: 500;
  }

  .mode:hover:not(:disabled) {
    color: var(--fg);
  }

  .mode.active {
    background: var(--surface);
    color: var(--fg);
    box-shadow: var(--shadow-sm);
  }

  .mode.active[aria-pressed='true']:nth-child(2) {
    color: var(--success-fg);
  }

  .mode.active[aria-pressed='true']:nth-child(3) {
    color: var(--warning-fg);
  }

  .mode:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .empty {
    padding: 10px 12px;
    border: 1px dashed var(--border);
    border-radius: var(--radius-lg);
    color: var(--muted);
    font-size: 11.5px;
  }

  .builtins {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }

  .builtins-title {
    color: var(--muted-2);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .builtin-list {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .builtin {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 6px;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 9.5px;
  }

  .builtin-state {
    color: var(--muted-2);
    font-size: 8.5px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .builtin-switch {
    display: inline-flex;
    align-items: center;
    padding: 0;
    border: 0;
    background: transparent;
    opacity: 0.65;
    cursor: not-allowed;
  }

  .builtin-track {
    position: relative;
    width: 22px;
    height: 12px;
    border-radius: 999px;
    background: var(--accent);
    border: 1px solid var(--accent);
  }

  .builtin-thumb {
    position: absolute;
    top: 1px;
    left: 11px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent-fg);
  }

  @media (max-width: 560px) {
    .extension {
      flex-direction: column;
      align-items: flex-start;
      gap: 7px;
    }

    .modes {
      width: 100%;
    }

    .mode {
      flex: 1;
      min-height: 32px;
    }
  }
</style>
