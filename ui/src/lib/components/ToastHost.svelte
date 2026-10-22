<script lang="ts">
  import { CircleAlert, CircleCheck, Info, X } from 'lucide-svelte';
  import { dismissToast, toasts } from '../stores';
  import type { IconComponent } from '../icons';

  const icons: Record<string, IconComponent> = {
    success: CircleCheck,
    error: CircleAlert,
    info: Info,
  };
</script>

<div class="toast-host" aria-live="polite" aria-relevant="additions">
  {#each $toasts as toast (toast.id)}
    {@const Icon = icons[toast.kind]}
    <div class="toast {toast.kind}" role={toast.kind === 'error' ? 'alert' : 'status'}>
      <span class="icon" aria-hidden="true">
        <Icon size={16} strokeWidth={2} />
      </span>
      <div class="copy">
        <p class="title">{toast.title}</p>
        {#if toast.message}<p class="message">{toast.message}</p>{/if}
      </div>
      <button
        type="button"
        class="close"
        aria-label="Dismiss notification"
        onclick={() => dismissToast(toast.id)}
      >
        <X size={14} strokeWidth={2} aria-hidden="true" />
      </button>
    </div>
  {/each}
</div>

<style>
  .toast-host {
    position: fixed;
    right: 20px;
    bottom: 20px;
    z-index: 120;
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: min(360px, calc(100vw - 32px));
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 11px 10px 11px 12px;
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-left: 3px solid var(--info);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-md);
    pointer-events: auto;
    animation: toast-in 180ms var(--ease) both;
  }

  .toast.success {
    border-left-color: var(--success);
  }

  .toast.error {
    border-left-color: var(--danger);
  }

  .icon {
    display: grid;
    place-items: center;
    flex: none;
    width: 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--info-soft);
    color: var(--info-fg);
  }

  .success .icon {
    background: var(--success-soft);
    color: var(--success-fg);
  }

  .error .icon {
    background: var(--danger-soft);
    color: var(--danger-fg);
  }

  .copy {
    flex: 1;
    min-width: 0;
  }

  .title {
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 600;
  }

  .message {
    margin-top: 2px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
    overflow-wrap: anywhere;
  }

  .close {
    display: grid;
    place-items: center;
    flex: none;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--muted);
  }

  .close:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }

  @media (max-width: 560px) {
    .toast-host {
      right: 16px;
      bottom: 16px;
      left: 16px;
      width: auto;
    }
  }
</style>
