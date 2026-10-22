import { get, writable } from 'svelte/store';

export type ThemeMode = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'devstack.theme';
const THEME_ORDER: ThemeMode[] = ['system', 'light', 'dark'];

function readStored(): ThemeMode {
  if (typeof localStorage === 'undefined') return 'system';
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    if (value === 'light' || value === 'dark' || value === 'system') return value;
  } catch {
    // Private mode or blocked storage: fall back to the system preference.
  }
  return 'system';
}

function systemTheme(): ResolvedTheme {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'dark';
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

export function resolveTheme(mode: ThemeMode): ResolvedTheme {
  return mode === 'system' ? systemTheme() : mode;
}

export const theme = writable<ThemeMode>(readStored());
export const resolvedTheme = writable<ResolvedTheme>(resolveTheme(readStored()));

function apply(mode: ThemeMode): void {
  if (typeof document === 'undefined') return;
  const resolved = resolveTheme(mode);
  const root = document.documentElement;
  root.dataset.theme = resolved;
  root.style.colorScheme = resolved;
  resolvedTheme.set(resolved);
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (meta) meta.content = resolved === 'dark' ? '#0F172A' : '#F8FAFC';
}

export function setTheme(mode: ThemeMode): void {
  theme.set(mode);
  try {
    localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // Storage is optional; the in-memory selection still applies.
  }
  apply(mode);
}

export function cycleTheme(): void {
  const current = get(theme);
  const next = THEME_ORDER[(THEME_ORDER.indexOf(current) + 1) % THEME_ORDER.length];
  setTheme(next);
}

export function initTheme(): void {
  if (typeof window === 'undefined') return;
  apply(get(theme));

  if (typeof window.matchMedia !== 'function') return;
  const query = window.matchMedia('(prefers-color-scheme: light)');
  const onChange = () => {
    if (get(theme) === 'system') apply('system');
  };
  if (typeof query.addEventListener === 'function') {
    query.addEventListener('change', onChange);
  }
}
