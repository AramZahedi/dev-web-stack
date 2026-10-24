# Dashboard — page override

> Overrides MASTER.md for the dev-web-stack dashboard. The generated MASTER pattern
> (FAQ/Documentation Landing) does NOT apply to this product; the pattern below wins.

## Product

A local developer-tool control panel. Dense, calm, precise. The user is a developer
who wants to see site state and act in one click. Content is operational, not marketing.

## Layout pattern

- Persistent left sidebar (240px) with wordmark, primary nav, daemon/helper status at the bottom.
- Compact top bar with page title + one-line subtitle on the left, environment chip and theme toggle on the right.
- Content column max-width 1280px, 24–32px page padding, 8px spacing rhythm.
- Tables for lists at >=768px; stacked cards below that. No horizontal scrolling at 375px.
- Sidebar collapses to a 72px icon rail at <1024px and an off-canvas drawer at <768px.

## Visual language

- Dark-first "developer console": deep slate background, slightly lighter cards, 1px borders,
  green reserved for running/success and the primary CTA. No large gradients, no glassmorphism.
- Light theme must be fully supported (system preference + manual toggle) with AA contrast.
- 10–12px radii, no heavy shadows; separation by border and surface tone.
- Monospace (JetBrains Mono) for domains, ports, commands, log lines, hashes. IBM Plex Sans for UI.
- Status color is never the only signal: pair every dot/pill with a text label and icon.
- Subtle motion only: 150–200ms transitions, 8px fade/rise on page enter; respect prefers-reduced-motion.

## Page specifics

### Sites
- Four compact stat tiles: Sites, Running, Crashed, Runtimes.
- Search/filter row + primary "Add site" button.
- Data table: status, name/domain, kind, runtime, URL, actions. Row click opens detail.
- Empty state with actionable copy; loading skeletons; error banner with retry.

### Add site
- Three-step flow: Project, Runtime, Options. Sticky live preview card showing the final URL,
  TLS state, runtime, and a "what happens next" checklist.
- Path browser is a modal listing directories only; breadcrumbs; no file contents.
- Inline validation on blur/submit; error summary linked to fields.

### Site detail
- Header: back link, name, status pill, URL with copy/open, start/stop/restart actions.
- Logs panel (2/3 width) streams via EventSource with pause, clear, download.
- Sidebar cards: overview (kind/runtime/ports/paths), certificate (expiry, SANs, state), environment.
- Danger zone with typed-name confirmation.

### Runtimes
- Installed and Available sections; version/kind badges; install confirmation shows version,
  size, checksum prefix. Remove is blocked while in use, with a clear explanation.

### Doctor
- Summary card with a large status ring and counts (ok/warn/fail/skipped).
- Check rows: icon + title, detail, remediation in a code block, status pill.
- Primary "Run checks", secondary "Repair".

### Settings
- Cards: machine (home/ports/auto-start/log retention), trust targets, daemon/socket, uninstall.
- Read-only values are visibly read-only; explain where to change them (config.yaml).

## Components and tokens

Use the MASTER palette for dark; define light equivalents:
`--bg #F8FAFC`, `--surface #FFFFFF`, `--surface-2 #F1F5F9`, `--fg #0F172A`,
`--muted-fg #475569`, `--border #CBD5E1`, `--accent #15803D` (white text),
`--accent-soft #DCFCE7`, `--danger #B91C1C`, `--warning #B45309`, `--info #1D4ED8`.

Buttons: 32px (sm), 36px (md), 40px (lg) heights; 8px radius; focus-visible ring 2px accent + 2px offset.
Inputs 36px; 8px radius; visible label; helper/error text below. Toggles are 36×20 with a label.
Badges are 11px uppercase, 6px radius. Status pills include a dot and text.

## Must not

- Emojis as icons; use lucide-svelte SVGs.
- Pure black or pure white surfaces in dark mode.
- Accent green for body text.
- Layout-shifting hover transforms.
- Unlabeled icon buttons, invisible focus, or color-only status.
