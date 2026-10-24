<script lang="ts">
  import { CircleAlert, X } from 'lucide-svelte';
  import IconButton from './IconButton.svelte';
  import Button from './Button.svelte';

  interface Props {
    message: string;
    code?: string;
    title?: string;
    onretry?: () => void;
    ondismiss?: () => void;
  }

  let { message, code, title = 'Something went wrong', onretry, ondismiss }: Props = $props();
</script>

<div class="banner" role="alert">
  <CircleAlert class="icon" size={17} strokeWidth={2} aria-hidden="true" />
  <div class="copy">
    <p class="title">
      {title}
      {#if code}<span class="code mono">{code}</span>{/if}
    </p>
    <p class="message">{message}</p>
  </div>
  <div class="actions">
    {#if onretry}
      <Button size="sm" variant="secondary" onclick={onretry}>Retry</Button>
    {/if}
    {#if ondismiss}
      <IconButton label="Dismiss error" icon={X} size="sm" onclick={ondismiss} />
    {/if}
  </div>
</div>

<style>
  .banner {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 12px;
    border: 1px solid color-mix(in srgb, var(--danger) 45%, var(--border));
    border-left: 3px solid var(--danger);
    border-radius: var(--radius-lg);
    background: var(--danger-soft);
  }

  .banner > :global(.icon) {
    flex: none;
    margin-top: 1px;
    color: var(--danger-fg);
  }

  .copy {
    min-width: 0;
    flex: 1;
  }

  .title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 7px;
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 600;
  }

  .code {
    padding: 1px 5px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    color: var(--danger-fg);
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.03em;
  }

  .message {
    margin-top: 2px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex: none;
  }
</style>
