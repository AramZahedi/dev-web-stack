<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title?: string;
    subtitle?: string;
    actions?: Snippet;
    className?: string;
    headerWrap?: boolean;
    children: Snippet;
  }

  let { title, subtitle, actions, className = '', headerWrap = false, children }: Props = $props();
</script>

<section class="card {className}">
  {#if title || actions}
    <header class="card-head" class:wrap={headerWrap}>
      <div class="copy">
        {#if title}<h2>{title}</h2>{/if}
        {#if subtitle}<p>{subtitle}</p>{/if}
      </div>
      {#if actions}
        <div class="actions">{@render actions()}</div>
      {/if}
    </header>
  {/if}
  <div class="card-body">
    {@render children()}
  </div>
</section>

<style>
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-sm);
    min-width: 0;
  }

  .card-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 13px 16px;
    border-bottom: 1px solid var(--border);
  }

  .copy {
    min-width: 0;
  }

  h2 {
    font-size: 13px;
    font-weight: 600;
    letter-spacing: -0.005em;
  }

  p {
    margin-top: 2px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.4;
  }

  .actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }

  .card-body {
    padding: 16px;
  }

  @media (max-width: 560px) {
    .card-head.wrap {
      flex-wrap: wrap;
    }

    .card-head.wrap .actions {
      width: 100%;
      justify-content: flex-end;
      padding-top: 2px;
    }
  }
</style>
