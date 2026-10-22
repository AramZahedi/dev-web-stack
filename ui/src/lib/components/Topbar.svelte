<script lang="ts">
  import { router } from 'svelte-spa-router';
  import { Menu, Network, RefreshCw } from 'lucide-svelte';
  import IconButton from './IconButton.svelte';

  interface Props {
    onmenu: () => void;
  }

  let { onmenu }: Props = $props();

  const meta = $derived.by(() => {
    const path = router.location;
    if (path === '/sites/new') {
      return { title: 'Add site', subtitle: 'Sites / New local project' };
    }
    if (path.startsWith('/sites/')) {
      return { title: 'Site detail', subtitle: 'Sites / Logs and configuration' };
    }
    if (path === '/sites' || path === '/' || path === '') {
      return { title: 'Sites', subtitle: 'Local projects, status and TLS' };
    }
    if (path === '/runtimes') {
      return { title: 'Runtimes', subtitle: 'Verified Node builds on this machine' };
    }
    if (path === '/doctor') {
      return { title: 'Doctor', subtitle: 'Health checks for helper, hosts, CA and ports' };
    }
    if (path === '/settings') {
      return { title: 'Settings', subtitle: 'Machine configuration and local trust' };
    }
    return { title: 'Not found', subtitle: 'This route does not exist' };
  });

  $effect(() => {
    if (typeof document !== 'undefined') document.title = `${meta.title} · devstack`;
  });

  function refresh(): void {
    window.dispatchEvent(new CustomEvent('devstack:refresh'));
  }
</script>

<header class="topbar">
  <div class="left">
    <button type="button" class="menu-btn" aria-label="Open navigation" onclick={onmenu}>
      <Menu size={19} strokeWidth={1.9} aria-hidden="true" />
    </button>
    <div class="titles">
      <h1>{meta.title}</h1>
      <p>{meta.subtitle}</p>
    </div>
  </div>

  <div class="right">
    <span class="env-chip mono" title="The dashboard only accepts loopback connections">
      <Network size={12} strokeWidth={2} aria-hidden="true" />
      127.0.0.1
    </span>
    <IconButton label="Refresh page data" icon={RefreshCw} onclick={refresh} />
  </div>
</header>

<style>
  .topbar {
    position: sticky;
    top: 0;
    z-index: 30;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    height: var(--topbar-h);
    padding: 0 28px;
    background: var(--topbar-bg);
    border-bottom: 1px solid var(--border);
  }

  .left {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }

  .menu-btn {
    display: none;
    place-items: center;
    flex: none;
    width: 34px;
    height: 34px;
    padding: 0;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--surface-2);
    color: var(--fg);
  }

  .titles {
    min-width: 0;
  }

  h1 {
    color: var(--fg);
    font-size: 16px;
    font-weight: 600;
    line-height: 1.3;
    letter-spacing: -0.015em;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  p {
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.35;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .right {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: none;
  }

  .env-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 26px;
    padding: 0 8px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10.5px;
    letter-spacing: -0.01em;
  }

  @media (max-width: 767px) {
    .topbar {
      height: 60px;
      padding: 0 16px;
    }

    .menu-btn {
      display: grid;
    }

    h1 {
      font-size: 15px;
    }

    p {
      font-size: 11px;
    }

    .env-chip {
      display: none;
    }
  }

</style>
