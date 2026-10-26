<script lang="ts">
  import type { SourceMode, SiteKind } from '../types';

  interface Props {
    mode: SourceMode;
    kind: SiteKind;
    isWindows: boolean;
    onmodechange: (mode: SourceMode) => void;
  }

  let { mode, kind, isWindows, onmodechange }: Props = $props();
</script>

<fieldset class="source-group" aria-describedby="source-mode-help">
  <legend>Project source</legend>
  <div class="source-options">
    {#if isWindows}
      <label class="source-option" class:selected={mode === 'windows'}>
        <input
          type="radio"
          name="project-source-mode"
          value="windows"
          checked={mode === 'windows'}
          onchange={() => onmodechange('windows')}
        />
        <span class="option-copy">
          <strong>Windows drive</strong>
          <span>Project files stay on C: and are read by WSL.</span>
        </span>
      </label>
      <label class="source-option" class:selected={mode === 'wsl'}>
        <input
          type="radio"
          name="project-source-mode"
          value="wsl"
          checked={mode === 'wsl'}
          onchange={() => onmodechange('wsl')}
        />
        <span class="option-copy">
          <strong>WSL distro</strong>
          <span>Project files stay in the managed Linux filesystem.</span>
        </span>
      </label>
    {:else}
      <label class="source-option selected">
        <input
          type="radio"
          name="project-source-mode"
          value="local"
          checked={mode === 'local'}
          onchange={() => onmodechange('local')}
        />
        <span class="option-copy">
          <strong>Local Linux</strong>
          <span>Project files stay on this Linux host.</span>
        </span>
      </label>
    {/if}
  </div>
  <div id="source-mode-help" class="source-notes" aria-live="polite">
    {#if isWindows && kind === 'static' && mode === 'windows'}
      <p class="source-warning" role="status">
        Windows-mode static files are read through <code>/mnt/c</code>. NTFS can be slower for file watching and builds.
      </p>
    {/if}
    {#if isWindows}
      <p class="source-copy-note">
        Changing source modes requires a stopped site and an agent-verified copy. The original source is kept by default.
      </p>
    {/if}
  </div>
</fieldset>

<style>
  .source-group {
    min-width: 0;
    margin: 0;
    padding: 0;
    border: 0;
  }

  legend {
    margin-bottom: 9px;
    color: var(--text-primary);
    font-size: 12px;
    font-weight: 600;
  }

  .source-options {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 8px;
  }

  .source-option {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    min-height: 50px;
    padding: 11px 12px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md, 8px);
    background: var(--surface-raised);
    cursor: pointer;
  }

  .source-option.selected {
    border-color: var(--accent);
    background: var(--accent-subtle, var(--surface-raised));
  }

  input {
    width: 15px;
    height: 15px;
    margin: 2px 0 0;
    accent-color: var(--accent);
    flex: 0 0 auto;
  }

  .option-copy {
    display: grid;
    gap: 3px;
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.45;
  }

  strong {
    color: var(--text-primary);
    font-size: 12px;
    font-weight: 600;
  }

  .source-notes {
    display: grid;
    gap: 4px;
    margin-top: 8px;
  }

  p {
    margin: 0;
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.5;
  }

  .source-warning {
    color: var(--warning-text, var(--text-primary));
  }

  code {
    font-family: var(--font-mono);
  }
</style>
