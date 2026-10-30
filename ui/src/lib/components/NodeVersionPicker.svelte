<script lang="ts">
  import { ChevronDown, Download, PackagePlus } from 'lucide-svelte';
  import * as api from '../api';
  import { describeError } from '../errors';
  import type {
    NodeRelease,
    NodeReleaseMajor,
    NodeReleaseSnapshot,
    RuntimeBuild,
  } from '../types';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Field from './Field.svelte';

  interface Props {
    id: string;
    label?: string;
    hint?: string;
    error?: string;
    required?: boolean;
    /** Exact installed version, or '' to inherit the global default. */
    value: string;
    builds: RuntimeBuild[];
    /** When set, the picker offers an empty inherit choice with this label. */
    inheritLabel?: string;
    placeholder?: string;
    disabled?: boolean;
    loading?: boolean;
    onchange: (value: string) => void;
    /** Installs one exact release, then the parent refreshes `builds`. */
    oninstall?: (version: string) => void | Promise<void>;
  }

  let {
    id,
    label = 'Node version',
    hint,
    error,
    required = false,
    value,
    builds,
    inheritLabel,
    placeholder = 'Select an installed Node version',
    disabled = false,
    loading = false,
    onchange,
    oninstall,
  }: Props = $props();

  interface BuildGroup {
    major: number;
    builds: RuntimeBuild[];
  }

  function majorOf(version: string): number {
    const major = Number.parseInt(version.split('.')[0] ?? '0', 10);
    return Number.isFinite(major) ? major : 0;
  }

  const installed = $derived(
    builds
      .filter((build) => build.kind === 'node')
      .slice()
      .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true })),
  );

  const groups = $derived.by<BuildGroup[]>(() => {
    const grouped = new Map<number, RuntimeBuild[]>();
    for (const build of installed) {
      const major = majorOf(build.version);
      grouped.set(major, [...(grouped.get(major) ?? []), build]);
    }
    return Array.from(grouped.entries())
      .sort(([a], [b]) => b - a)
      .map(([major, entries]) => ({ major, builds: entries }));
  });

  const installedVersions = $derived(new Set(installed.map((build) => build.version)));
  const missingSelected = $derived(Boolean(value) && !installedVersions.has(value));

  let showDiscover = $state(false);
  let snapshot = $state<NodeReleaseSnapshot | null>(null);
  let discoverLoading = $state(false);
  let discoverError = $state('');
  let expanded = $state<Record<number, boolean>>({});
  let installBusy = $state('');
  let installError = $state('');

  async function loadReleases(): Promise<void> {
    discoverLoading = true;
    discoverError = '';
    try {
      snapshot = await api.listNodeReleases({ all: true });
    } catch (caught) {
      discoverError = describeError(caught).message;
    } finally {
      discoverLoading = false;
    }
  }

  async function toggleDiscover(): Promise<void> {
    showDiscover = !showDiscover;
    if (!showDiscover || snapshot || discoverLoading) return;
    await loadReleases();
  }

  function toggleMajor(major: number): void {
    expanded = { ...expanded, [major]: !expanded[major] };
  }

  function select(version: string): void {
    onchange(version);
    showDiscover = false;
  }

  async function install(version: string): Promise<void> {
    if (!oninstall || installBusy) return;
    installBusy = version;
    installError = '';
    try {
      await oninstall(version);
    } catch (caught) {
      installError = describeError(caught).message;
    } finally {
      installBusy = '';
    }
  }

  function olderByMinor(major: NodeReleaseMajor): { minor: string; releases: NodeRelease[] }[] {
    const grouped = new Map<string, NodeRelease[]>();
    for (const release of major.releases) {
      if (release.version === major.latest.version) continue;
      const parts = release.version.split('.');
      const minor = parts.length >= 2 ? `${parts[0]}.${parts[1]}` : release.version;
      grouped.set(minor, [...(grouped.get(minor) ?? []), release]);
    }
    return Array.from(grouped.entries()).map(([minor, releases]) => ({ minor, releases }));
  }

  const describedBy = $derived(
    error ? `${id}-error` : hint ? `${id}-hint` : undefined,
  );
</script>

<Field {id} {label} {hint} {error} {required}>
  <div class="picker" class:disabled>
    <div class="select-wrap">
      <select
        {id}
        class="input mono"
        value={value}
        disabled={disabled || loading}
        aria-invalid={Boolean(error)}
        aria-describedby={describedBy}
        onchange={(event) => onchange(event.currentTarget.value)}
      >
        {#if inheritLabel}
          <option value="">{inheritLabel}</option>
        {:else if !value}
          <option value="">{placeholder}</option>
        {/if}
        {#if missingSelected}
          <option value={value}>Node {value} (not installed)</option>
        {/if}
        {#each groups as group (group.major)}
          <optgroup label={`Node ${group.major}`}>
            {#each group.builds as build (build.id)}
              <option value={build.version}>Node {build.version} · {build.backend}</option>
            {/each}
          </optgroup>
        {/each}
      </select>
      <ChevronDown size={14} strokeWidth={2} aria-hidden="true" />
    </div>

    {#if missingSelected}
      <div class="install-first" role="status">
        <p>
          Node {value} is not installed. Install it first, then choose the exact build.
        </p>
        {#if oninstall}
          <Button
            size="sm"
            variant="secondary"
            loading={installBusy === value}
            disabled={Boolean(installBusy) || disabled}
            onclick={() => install(value)}
          >
            <PackagePlus size={13} strokeWidth={2} aria-hidden="true" />
            Install Node {value}
          </Button>
        {/if}
      </div>
    {/if}

    {#if !disabled}
      <div class="picker-actions">
        <Button
          size="sm"
          variant="ghost"
          ariaExpanded={showDiscover}
          ariaControls={`${id}-discover`}
          onclick={toggleDiscover}
        >
          <Download size={13} strokeWidth={2} aria-hidden="true" />
          {showDiscover ? 'Hide available versions' : 'Install another version'}
        </Button>
      </div>
    {/if}

    {#if installError}
      <p class="picker-error" role="alert">{installError}</p>
    {/if}

    {#if showDiscover}
      <div class="discover" id={`${id}-discover`}>
        {#if discoverLoading}
          <p class="discover-state" role="status">Loading Node releases…</p>
        {:else if discoverError}
          <div class="discover-state" role="alert">
            <p>{discoverError}</p>
            <Button size="sm" variant="ghost" onclick={loadReleases}>Retry</Button>
          </div>
        {:else if snapshot}
          <div class="discover-head">
            <p class="discover-title">Available Node versions</p>
            <Badge variant="neutral">{snapshot.source}</Badge>
          </div>
          {#if snapshot.warning}
            <p class="discover-warning">{snapshot.warning}</p>
          {/if}
          {#if snapshot.majors.length === 0}
            <p class="discover-state">No Node releases are available offline.</p>
          {:else}
            {#each snapshot.majors as entry (entry.major)}
              <section class="major" aria-label={`Node ${entry.major} releases`}>
                <div class="major-head">
                  <p class="major-name">Node {entry.major}</p>
                  {#if entry.releases.length > 1}
                    <Button
                      size="sm"
                      variant="ghost"
                      ariaExpanded={expanded[entry.major] === true}
                      onclick={() => toggleMajor(entry.major)}
                    >
                      {expanded[entry.major]
                        ? `Hide Node ${entry.major} versions`
                        : `Show all Node ${entry.major} versions`}
                    </Button>
                  {/if}
                </div>

                <div class="release">
                  <div class="release-copy">
                    <span class="version">Node {entry.latest.version}</span>
                    <span class="meta">
                      {entry.latest.lts ? `LTS ${entry.latest.lts}` : 'Current'}
                      {entry.latest.security ? ' · security' : ''}
                      {' · '}npm {entry.latest.npm}
                    </span>
                  </div>
                  {#if installedVersions.has(entry.latest.version)}
                    <Badge variant="accent">Installed</Badge>
                  {:else if oninstall}
                    <Button
                      size="sm"
                      variant="secondary"
                      loading={installBusy === entry.latest.version}
                      disabled={Boolean(installBusy)}
                      onclick={() => install(entry.latest.version)}
                    >
                      Install {entry.latest.version}
                    </Button>
                  {/if}
                </div>

                {#if expanded[entry.major]}
                  {#each olderByMinor(entry) as minor (minor.minor)}
                    <p class="minor-label">Node {minor.minor}</p>
                    {#each minor.releases as release (release.version)}
                      <div class="release">
                        <div class="release-copy">
                          <span class="version">Node {release.version}</span>
                          <span class="meta">
                            {release.lts ? `LTS ${release.lts}` : 'Current'}
                            {release.security ? ' · security' : ''}
                            {' · '}npm {release.npm}
                          </span>
                        </div>
                        {#if installedVersions.has(release.version)}
                          {#if value === release.version}
                            <Badge variant="accent">Selected</Badge>
                          {:else}
                            <Button
                              size="sm"
                              variant="ghost"
                              onclick={() => select(release.version)}
                            >
                              Use {release.version}
                            </Button>
                          {/if}
                        {:else if oninstall}
                          <Button
                            size="sm"
                            variant="secondary"
                            loading={installBusy === release.version}
                            disabled={Boolean(installBusy)}
                            onclick={() => install(release.version)}
                          >
                            Install {release.version}
                          </Button>
                        {/if}
                      </div>
                    {/each}
                  {/each}
                {/if}
              </section>
            {/each}
          {/if}
        {/if}
      </div>
    {/if}
  </div>
</Field>

<style>
  .picker {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .select-wrap {
    position: relative;
  }

  .select-wrap :global(> svg) {
    position: absolute;
    right: 11px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--muted);
    pointer-events: none;
  }

  .picker-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
  }

  .install-first {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 9px 11px;
    background: var(--warning-soft, var(--surface-2));
    border: 1px solid var(--warning-border, var(--border));
    border-radius: var(--radius);
  }

  .install-first p {
    margin: 0;
    color: var(--fg);
    font-size: 12px;
    line-height: 1.45;
  }

  .picker-error {
    margin: 0;
    color: var(--danger-fg);
    font-size: 12px;
  }

  .discover {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 11px;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .discover-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .discover-title {
    margin: 0;
    color: var(--fg);
    font-size: 12px;
    font-weight: 600;
  }

  .discover-warning {
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .discover-state {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }

  .major {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding-top: 9px;
    border-top: 1px solid var(--border);
  }

  .major-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .major-name {
    margin: 0;
    color: var(--fg);
    font-size: 12px;
    font-weight: 600;
  }

  .release {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    min-height: 32px;
    padding: 3px 0;
  }

  .release-copy {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }

  .version {
    color: var(--fg);
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .meta {
    color: var(--muted);
    font-size: 11px;
    line-height: 1.4;
  }

  .minor-label {
    margin: 4px 0 0;
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 500;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
</style>
