<script lang="ts">
  import { tick } from 'svelte';
  import { X } from 'lucide-svelte';
  import type { Snippet } from 'svelte';

  interface Props {
    open: boolean;
    title: string;
    description?: string;
    size?: 'sm' | 'md' | 'lg';
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
  }

  let { open, title, description, size = 'md', onclose, children, footer }: Props = $props();

  let dialogEl: HTMLDivElement | undefined = $state();
  let previousFocus: HTMLElement | null = null;
  const titleId = `dialog-title-${Math.random().toString(36).slice(2, 8)}`;
  const descriptionId = `dialog-desc-${Math.random().toString(36).slice(2, 8)}`;

  function isUsable(element: HTMLElement | null | undefined): element is HTMLElement {
    if (!element) return false;
    if (element.hasAttribute('disabled')) return false;
    if (element.getAttribute('aria-disabled') === 'true') return false;
    return element.offsetParent !== null;
  }

  function focusable(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(
      dialogEl.querySelectorAll<HTMLElement>(
        'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter(isUsable);
  }

  $effect(() => {
    if (!open) return;
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    tick().then(() => {
      const autofocusCandidates = Array.from(
        dialogEl?.querySelectorAll<HTMLElement>('[data-autofocus]') ?? [],
      );
      const auto = autofocusCandidates.find(isUsable);
      const target = auto ?? focusable()[0] ?? dialogEl;
      target?.focus();
    });

    return () => {
      document.body.style.overflow = originalOverflow;
      previousFocus?.focus?.();
    };
  });

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
      dialogEl?.focus();
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
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <div class="overlay">
    <div class="backdrop" aria-hidden="true" onclick={onclose}></div>
    <div
      class="dialog {size}"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      aria-describedby={description ? descriptionId : undefined}
      tabindex="-1"
      bind:this={dialogEl}
    >
      <header class="head">
        <div class="copy">
          <h2 id={titleId}>{title}</h2>
          {#if description}<p id={descriptionId}>{description}</p>{/if}
        </div>
        <button type="button" class="close" aria-label="Close dialog" onclick={onclose}>
          <X size={17} strokeWidth={2} aria-hidden="true" />
        </button>
      </header>
      <div class="body">
        {@render children()}
      </div>
      {#if footer}
        <footer class="foot">{@render footer()}</footer>
      {/if}
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 90;
    display: grid;
    place-items: center;
    padding: 24px;
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

  .dialog {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    max-height: calc(100dvh - 48px);
    background: var(--surface);
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-lg);
    animation: dialog-in 180ms var(--ease) both;
  }

  .dialog.sm {
    max-width: 400px;
  }

  .dialog.md {
    max-width: 520px;
  }

  .dialog.lg {
    max-width: 720px;
  }

  .dialog:focus {
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

  h2 {
    font-size: 15.5px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .copy p {
    margin-top: 4px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.5;
  }

  .close {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    margin: -3px -3px 0 0;
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

  .body {
    padding: 18px;
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 13px 18px;
    border-top: 1px solid var(--border);
    background: var(--surface-2);
    border-radius: 0 0 var(--radius-xl) var(--radius-xl);
  }

  @keyframes fade-in {
    from {
      opacity: 0;
    }
  }

  @keyframes dialog-in {
    from {
      opacity: 0;
      transform: translateY(8px) scale(0.99);
    }
  }

  @media (max-width: 560px) {
    .overlay {
      padding: 12px;
    }

    .dialog {
      max-height: calc(100dvh - 24px);
    }

    .head {
      padding: 16px 16px 12px;
    }

    .body {
      padding: 16px;
    }

    .foot {
      padding: 12px 16px;
    }
  }
</style>
