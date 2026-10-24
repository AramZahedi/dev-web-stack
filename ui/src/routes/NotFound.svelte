<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { Compass } from 'lucide-svelte';
  import Button from '../lib/components/Button.svelte';
</script>

<div class="page">
  <div class="not-found">
    <p class="code mono" aria-hidden="true">404</p>
    <span class="icon" aria-hidden="true"><Compass size={22} strokeWidth={1.6} /></span>
    <h2>This page does not exist</h2>
    <p>
      The dashboard has four workspaces: sites, runtimes, doctor and settings. The link may be
      stale, or the site was removed.
    </p>
    <Button variant="primary" onclick={() => push('/sites')}>Back to sites</Button>
  </div>
</div>

<style>
  .not-found {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    max-width: 440px;
    margin: 9vh auto 0;
    padding: 0 16px;
  }

  .code {
    color: var(--muted-2);
    font-size: 54px;
    font-weight: 600;
    line-height: 1;
    letter-spacing: -0.06em;
    opacity: 0.55;
  }

  .icon {
    display: grid;
    place-items: center;
    width: 44px;
    height: 44px;
    margin: 12px 0 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-lg);
    background: var(--surface);
    color: var(--muted);
  }

  h2 {
    font-size: 19px;
    letter-spacing: -0.02em;
  }

  p:not(.code) {
    margin: 6px 0 18px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.6;
  }
</style>
