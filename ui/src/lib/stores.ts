import { writable } from 'svelte/store';
import { getSession, health, isMockMode } from './api';

export type ToastKind = 'success' | 'error' | 'info';

export interface Toast {
  id: string;
  kind: ToastKind;
  title: string;
  message?: string;
}

export const toasts = writable<Toast[]>([]);
const timers = new Map<string, ReturnType<typeof setTimeout>>();

let nextId = 0;

export function addToast(
  toast: Omit<Toast, 'id'>,
  duration = toast.kind === 'error' ? 7000 : 5000,
): string {
  const id = `toast-${++nextId}`;
  toasts.update((items) => [...items.slice(-2), { ...toast, id }]);
  const timer = setTimeout(() => dismissToast(id), duration);
  timers.set(id, timer);
  return id;
}

export function dismissToast(id: string): void {
  const timer = timers.get(id);
  if (timer) clearTimeout(timer);
  timers.delete(id);
  toasts.update((items) => items.filter((item) => item.id !== id));
}

export function clearToasts(): void {
  for (const timer of timers.values()) clearTimeout(timer);
  timers.clear();
  toasts.set([]);
}

export function toastError(title: string, message?: string): void {
  addToast({ kind: 'error', title, message });
}

export interface SessionState {
  checked: boolean;
  authenticated: boolean;
  mode: 'mock' | 'api';
}

export const session = writable<SessionState>({
  checked: false,
  authenticated: isMockMode,
  mode: isMockMode ? 'mock' : 'api',
});

export async function initSession(): Promise<void> {
  try {
    await getSession();
    session.set({ checked: true, authenticated: true, mode: isMockMode ? 'mock' : 'api' });
  } catch {
    session.set({ checked: true, authenticated: isMockMode, mode: isMockMode ? 'mock' : 'api' });
  }
}

export interface DaemonState {
  status: 'checking' | 'online' | 'offline';
  version: string;
  helper: 'ok' | 'warn' | 'down' | 'unknown';
  helperVersion: string;
  caFingerprint: string | null;
}

export const daemon = writable<DaemonState>({
  status: 'checking',
  version: '',
  helper: 'unknown',
  helperVersion: '',
  caFingerprint: null,
});

export async function refreshDaemon(): Promise<void> {
  try {
    const result = await health();
    daemon.set({
      status: result.status === 'ok' ? 'online' : 'offline',
      version: result.version,
      helper: result.helper ?? 'unknown',
      helperVersion: result.helper_version ?? '',
      caFingerprint: result.ca_fingerprint ?? null,
    });
  } catch {
    daemon.update((current) => ({ ...current, status: 'offline' }));
  }
}
