<script lang="ts">
  import { router } from 'svelte-spa-router';
  import {
    Boxes,
    LayoutGrid,
    Monitor,
    Moon,
    SquareTerminal,
    Stethoscope,
    Sun,
    Settings,
    X,
  } from 'lucide-svelte';
  import { theme, setTheme, cycleTheme, type ThemeMode } from '../theme';
  import { daemon } from '../stores';

  interface Props {
    drawerOpen: boolean;
    onclose: () => void;
  }

  let { drawerOpen, onclose }: Props = $props();

  const nav = [
    { href: '#/sites', section: '/sites', label: 'Sites', icon: LayoutGrid },
    { href: '#/runtimes', section: '/runtimes', label: 'Runtimes', icon: Boxes },
    { href: '#/doctor', section: '/doctor', label: 'Doctor', icon: Stethoscope },
    { href: '#/settings', section: '/settings', label: 'Settings', icon: Settings },
  ];

  const themes: { value: ThemeMode; label: string; icon: typeof Monitor }[] = [
    { value: 'system', label: 'System theme', icon: Monitor },
    { value: 'light', label: 'Light theme', icon: Sun },
    { value: 'dark', label: 'Dark theme', icon: Moon },
  ];

  const currentPath = $derived(router.location);

  const daemonText = $derived(
    $daemon.status === 'online'
      ? 'Daemon online'
      : $daemon.status === 'offline'
        ? 'Daemon offline'
        : 'Daemon connecting',
  );

  const helperText = $derived(
    $daemon.helper === 'ok'
      ? 'Helper ok'
      : $daemon.helper === 'warn'
        ? 'Helper warning'
        : $daemon.helper === 'down'
          ? 'Helper down'
          : 'Helper unknown',
  );

  const caPrefix = $derived(
    $daemon.caFingerprint
      ? $daemon.caFingerprint.split(':').slice(0, 3).join(':') + '…'
      : 'CA —',
  );

  function isActive(section: string): boolean {
    const path = currentPath;
    if (section === '/sites') return path === '/' || path === '' || path.startsWith('/sites');
    return path.startsWith(section);
  }

  function cycle(): void {
    cycleTheme();
  }
</script>

<aside class="sidebar" class:open={drawerOpen} aria-label="Sidebar">
  <div class="brand">
    <a class="brand-link" href="#/sites" aria-label="devstack home" onclick={onclose}>
      <span class="brand-mark" aria-hidden="true">
        <SquareTerminal size={16} strokeWidth={1.9} />
      </span>
      <span class="wordmark">devstack<span class="caret" aria-hidden="true"></span></span>
    </a>
    <button type="button" class="drawer-close" aria-label="Close navigation" onclick={onclose}>
      <X size={17} strokeWidth={2} aria-hidden="true" />
    </button>
  </div>

  <nav class="nav" aria-label="Primary">
    <p class="nav-label" aria-hidden="true">Workspace</p>
    {#each nav as item (item.href)}
      {@const active = isActive(item.section)}
      <a
        class="nav-item"
        class:active
        href={item.href}
        aria-current={active ? 'page' : undefined}
        title={item.label}
        onclick={onclose}
      >
        <item.icon size={18} strokeWidth={1.8} aria-hidden="true" />
        <span class="nav-text">{item.label}</span>
      </a>
    {/each}
  </nav>

  <div class="sidebar-spacer"></div>

  <div class="system-card">
    <div class="system-head">
      <span class="system-title">Local system</span>
      <span class="version mono">v{$daemon.version || '0.1.0'}</span>
    </div>
    <div class="status-row daemon-row" title={daemonText}>
      <span class="dot { $daemon.status }" aria-hidden="true"></span>
      <span class="status-text">{daemonText}</span>
    </div>
    <div class="status-row helper-row" title={helperText}>
      <span class="dot helper-{$daemon.helper}" aria-hidden="true"></span>
      <span class="status-text">{helperText}</span>
    </div>
    <p class="ca mono" title={$daemon.caFingerprint ?? 'CA fingerprint unavailable'}>
      {$daemon.caFingerprint ? 'CA' : ''} {caPrefix}
    </p>

    <div class="theme">
      <span class="theme-label">Theme</span>
      <div class="theme-options" role="group" aria-label="Theme">
        {#each themes as option (option.value)}
          <button
            type="button"
            class="theme-btn"
            class:selected={$theme === option.value}
            aria-pressed={$theme === option.value}
            aria-label={option.label}
            title={option.label}
            onclick={() => setTheme(option.value)}
          >
            <option.icon size={13} strokeWidth={1.9} aria-hidden="true" />
          </button>
        {/each}
      </div>
      <button
        type="button"
        class="theme-cycle"
        aria-label="Change theme"
        title="Change theme"
        onclick={cycle}
      >
        {#if $theme === 'system'}
          <Monitor size={14} strokeWidth={1.9} aria-hidden="true" />
        {:else if $theme === 'light'}
          <Sun size={14} strokeWidth={1.9} aria-hidden="true" />
        {:else}
          <Moon size={14} strokeWidth={1.9} aria-hidden="true" />
        {/if}
      </button>
    </div>
  </div>

</aside>

<style>
  .sidebar {
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    width: var(--sidebar-w);
    padding: 14px 12px;
    background: var(--sidebar-bg);
    border-right: 1px solid var(--border);
  }

  .brand {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 2px 6px 18px;
  }

  .brand-link {
    display: flex;
    align-items: center;
    gap: 9px;
    min-width: 0;
    border-radius: 6px;
  }

  .brand-mark {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    border-radius: var(--radius);
    background: var(--accent-soft);
    border: 1px solid var(--accent-border);
    color: var(--success-fg);
  }

  .wordmark {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-family: var(--font-mono);
    font-size: 15.5px;
    font-weight: 600;
    letter-spacing: -0.03em;
    color: var(--fg);
    white-space: nowrap;
  }

  .caret {
    display: inline-block;
    width: 6px;
    height: 14px;
    margin-left: 1px;
    background: var(--accent);
    border-radius: 1px;
    opacity: 0.85;
  }

  .drawer-close {
    display: none;
    place-items: center;
    width: 28px;
    height: 28px;
    flex: none;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--muted);
  }

  .drawer-close:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .nav {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .nav-label {
    padding: 4px 10px 6px;
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.11em;
    text-transform: uppercase;
  }

  .nav-item {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 36px;
    padding: 0 10px;
    border-radius: var(--radius);
    color: var(--muted);
    font-size: 13px;
    font-weight: 500;
    transition:
      background-color 150ms var(--ease),
      color 150ms var(--ease);
  }

  .nav-item::before {
    content: '';
    position: absolute;
    left: -12px;
    top: 8px;
    bottom: 8px;
    width: 2px;
    border-radius: 2px;
    background: transparent;
  }

  .nav-item:hover {
    background: var(--surface-2);
    color: var(--fg);
  }

  .nav-item.active {
    background: var(--accent-soft);
    color: var(--fg);
  }

  .nav-item.active::before {
    background: var(--accent);
  }

  .nav-item.active > :global(svg) {
    color: var(--success-fg);
  }

  .sidebar-spacer {
    flex: 1;
    min-height: 20px;
  }

  .system-card {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    background: var(--surface);
  }

  .system-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 8px;
    padding-bottom: 2px;
  }

  .system-title {
    color: var(--muted);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .version {
    color: var(--muted-2);
    font-size: 9.5px;
  }

  .status-row {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
  }

  .status-text {
    color: var(--fg);
    font-size: 11.5px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .dot {
    width: 6px;
    height: 6px;
    flex: none;
    border-radius: 50%;
    background: var(--muted-2);
  }

  .dot.online {
    background: var(--success);
    box-shadow: 0 0 0 3px var(--success-soft);
  }

  .dot.offline {
    background: var(--danger);
  }

  .dot.checking {
    background: var(--warning);
  }

  .dot.helper-ok {
    background: var(--success);
  }

  .dot.helper-warn {
    background: var(--warning);
  }

  .dot.helper-down {
    background: var(--danger);
  }

  .dot.helper-unknown {
    background: var(--muted-2);
  }

  .ca {
    margin: 1px 0 2px;
    color: var(--muted-2);
    font-size: 9.5px;
    letter-spacing: 0.02em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .theme {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 6px;
    padding-top: 7px;
    border-top: 1px solid var(--border);
  }

  .theme-label {
    color: var(--muted-2);
    font-size: 9.5px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .theme-options {
    display: flex;
    gap: 2px;
    padding: 2px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface-2);
  }

  .theme-btn {
    display: grid;
    place-items: center;
    width: 22px;
    height: 20px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
  }

  .theme-btn:hover {
    color: var(--fg);
  }

  .theme-btn.selected {
    background: var(--surface-3);
    color: var(--fg);
  }

  .theme-cycle {
    display: none;
    place-items: center;
    width: 26px;
    height: 24px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 5px;
    background: var(--surface-2);
    color: var(--muted);
  }

  @media (max-width: 1023px) and (min-width: 768px) {
    .sidebar {
      width: var(--rail-w);
      padding: 14px 10px;
      align-items: center;
    }

    .brand {
      padding: 2px 0 18px;
    }

    .brand-link {
      justify-content: center;
    }

    .wordmark,
    .caret,
    .nav-label {
      display: none;
    }

    .nav-text {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }

    .nav {
      width: 100%;
      align-items: center;
    }

    .nav-item {
      justify-content: center;
      width: 40px;
      height: 40px;
      padding: 0;
    }

    .nav-item::before {
      left: -10px;
      top: 10px;
      bottom: 10px;
    }

    .system-card {
      align-items: center;
      gap: 8px;
      width: 100%;
      padding: 8px 6px;
    }

    .system-head,
    .ca,
    .theme-label,
    .theme-options {
      display: none;
    }

    .status-row {
      display: none;
    }

    .daemon-row,
    .helper-row {
      display: flex;
      justify-content: center;
    }

    .status-text {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0, 0, 0, 0);
      white-space: nowrap;
      border: 0;
    }

    .theme {
      justify-content: center;
      width: 100%;
      padding-top: 0;
      border-top: 0;
    }

    .theme-cycle {
      display: grid;
      width: 28px;
      height: 26px;
    }
  }

  @media (max-width: 767px) {
    .sidebar {
      width: min(288px, 86vw);
      padding: 16px;
      transform: translateX(-100%);
      transition: transform 180ms var(--ease);
      box-shadow: var(--shadow-lg);
      visibility: hidden;
    }

    .sidebar.open {
      transform: translateX(0);
      visibility: visible;
    }

    .drawer-close {
      display: grid;
    }

    .nav-item {
      height: 40px;
      font-size: 13.5px;
    }

    .brand {
      padding-bottom: 20px;
    }
  }
</style>
