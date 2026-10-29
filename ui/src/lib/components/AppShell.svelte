<script lang="ts">
  import { router } from 'svelte-spa-router';
  import type { Snippet } from 'svelte';
  import Sidebar from './Sidebar.svelte';
  import Topbar from './Topbar.svelte';
  import ToastHost from './ToastHost.svelte';
  import { session } from '../stores';

  interface Props {
    children: Snippet;
  }

  let { children }: Props = $props();
  let drawerOpen = $state(false);

  $effect(() => {
    void router.location;
    drawerOpen = false;
  });

  $effect(() => {
    if (!drawerOpen) return;
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    const onKeydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') drawerOpen = false;
    };
    window.addEventListener('keydown', onKeydown);
    return () => {
      document.body.style.overflow = originalOverflow;
      window.removeEventListener('keydown', onKeydown);
    };
  });

  function skipToMain(event: MouseEvent): void {
    event.preventDefault();
    const main = document.getElementById('main');
    main?.focus();
    main?.scrollIntoView({ block: 'start' });
  }
</script>

<a class="skip-link" href="#main" onclick={skipToMain}>Skip to content</a>

<Sidebar {drawerOpen} onclose={() => (drawerOpen = false)} />

{#if drawerOpen}
  <button
    type="button"
    class="backdrop"
    aria-label="Close navigation"
    onclick={() => (drawerOpen = false)}
  ></button>
{/if}

<div class="shell-main">
  <Topbar onmenu={() => (drawerOpen = true)} />
  {#if $session.checked && !$session.authenticated}
    <div class="auth-banner" role="alert">
      Dashboard locked. Run <code>devstack ui</code> to open an authenticated session.
    </div>
  {/if}
  <main id="main" tabindex="-1">
    {@render children()}
  </main>
</div>

<ToastHost />

<style>
  .skip-link {
    position: fixed;
    top: 8px;
    left: 8px;
    z-index: 200;
    padding: 8px 12px;
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--fg);
    font-size: 12.5px;
    font-weight: 500;
    transform: translateY(-160%);
    transition: transform 150ms var(--ease);
  }

  .skip-link:focus {
    transform: translateY(0);
  }

  .auth-banner {
    margin: 12px 24px 0;
    padding: 10px 14px;
    border: 1px solid var(--warning, #d97706);
    border-radius: var(--radius);
    background: color-mix(in srgb, var(--warning, #d97706) 10%, var(--surface));
    color: var(--fg);
    font-size: 13px;
  }

  .auth-banner code {
    font-family: var(--font-mono, monospace);
  }

  .shell-main {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
    margin-left: var(--sidebar-w);
  }

  main {
    flex: 1;
  }

  main:focus {
    outline: none;
  }

  .backdrop {
    display: none;
    position: fixed;
    inset: 0;
    z-index: 40;
    padding: 0;
    border: 0;
    background: rgba(2, 6, 23, 0.62);
  }

  @media (max-width: 1023px) {
    .shell-main {
      margin-left: var(--rail-w);
    }
  }

  @media (max-width: 767px) {
    .shell-main {
      margin-left: 0;
    }

    .backdrop {
      display: block;
    }
  }
</style>
