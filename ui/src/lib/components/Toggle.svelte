<script lang="ts">
  interface Props {
    checked: boolean;
    label: string;
    description?: string;
    disabled?: boolean;
    onchange?: (checked: boolean) => void;
  }

  let { checked = $bindable(false), label, description, disabled = false, onchange }: Props = $props();

  const descriptionId = `toggle-desc-${Math.random().toString(36).slice(2, 8)}`;
</script>

<button
  type="button"
  class="toggle"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  aria-describedby={description ? descriptionId : undefined}
  {disabled}
  onclick={() => {
    if (disabled) return;
    checked = !checked;
    onchange?.(checked);
  }}
>
  <span class="copy" aria-hidden="true">
    <span class="label">{label}</span>
    {#if description}<span class="description" id={descriptionId}>{description}</span>{/if}
  </span>
  <span class="track" class:on={checked} aria-hidden="true">
    <span class="thumb"></span>
  </span>
</button>

<style>
  .toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    width: 100%;
    padding: 10px 2px;
    border: 0;
    background: transparent;
    text-align: left;
    border-radius: var(--radius);
  }

  .copy {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .label {
    font-size: 13px;
    font-weight: 500;
    color: var(--fg);
  }

  .description {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .track {
    position: relative;
    width: 36px;
    height: 20px;
    flex: none;
    border-radius: 999px;
    background: var(--surface-3);
    border: 1px solid var(--control-border);
    transition:
      background-color 160ms var(--ease),
      border-color 160ms var(--ease);
  }

  .track.on {
    background: var(--accent);
    border-color: var(--accent);
  }

  .thumb {
    position: absolute;
    top: 1px;
    left: 1px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #ffffff;
    box-shadow: 0 1px 2px rgba(2, 6, 23, 0.35);
    transition: transform 160ms var(--ease);
  }

  .track.on .thumb {
    transform: translateX(16px);
    background: var(--accent-fg);
  }

  .toggle:hover:not(:disabled) .track {
    border-color: var(--fg);
  }

  .toggle:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
