import type { Site } from './types';

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

export function formatBytes(bytes: number, decimals = 1): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const exponent = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    BYTE_UNITS.length - 1,
  );
  const value = bytes / 1024 ** exponent;
  const digits = exponent === 0 ? 0 : value >= 100 ? 0 : decimals;
  return `${value.toFixed(digits).replace(/\.0$/, '')} ${BYTE_UNITS[exponent]}`;
}

const dateFormat = new Intl.DateTimeFormat('en', {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

const dateTimeFormat = new Intl.DateTimeFormat('en', {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  hour: 'numeric',
  minute: '2-digit',
});

const clockFormat = new Intl.DateTimeFormat('en', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});

export function formatDate(value: string | Date | undefined): string {
  if (!value) return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return dateFormat.format(date);
}

export function formatDateTime(value: string | Date | undefined): string {
  if (!value) return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return dateTimeFormat.format(date);
}

export function formatClock(value: string | Date | undefined): string {
  if (!value) return '--:--:--';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--:--';
  return clockFormat.format(date);
}

const relativeFormat = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

export function formatRelative(value: string | Date | undefined): string {
  if (!value) return 'never';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return 'never';
  const seconds = Math.round((date.getTime() - Date.now()) / 1000);
  const abs = Math.abs(seconds);
  if (abs < 45) return 'just now';
  if (abs < 3600) return relativeFormat.format(Math.round(seconds / 60), 'minute');
  if (abs < 86_400) return relativeFormat.format(Math.round(seconds / 3600), 'hour');
  if (abs < 2_592_000) return relativeFormat.format(Math.round(seconds / 86_400), 'day');
  if (abs < 31_536_000) {
    return relativeFormat.format(Math.round(seconds / 2_592_000), 'month');
  }
  return relativeFormat.format(Math.round(seconds / 31_536_000), 'year');
}

export interface PublicPorts {
  http?: string;
  https?: string;
}

const configuredPublicPorts: PublicPorts = {
  http: import.meta.env.VITE_PUBLIC_HTTP_PORT as string | undefined,
  https: import.meta.env.VITE_PUBLIC_HTTPS_PORT as string | undefined,
};

function urlPort(port: string | undefined, defaultPort: string): string {
  const value = port?.trim();
  if (!value || value === defaultPort || !/^\d+$/.test(value)) return '';
  const n = Number(value);
  return n >= 1 && n <= 65535 ? `:${n}` : '';
}

export function siteUrl(
  site: Pick<Site, 'domain' | 'tls'>,
  ports: PublicPorts = configuredPublicPorts,
): string {
  const scheme = site.tls ? 'https' : 'http';
  const port = urlPort(scheme === 'https' ? ports.https : ports.http, scheme === 'https' ? '443' : '80');
  return `${scheme}://${site.domain}${port}`;
}

export function runtimeLabel(site: Pick<Site, 'kind' | 'node_version' | 'php_version'>): string {
  if (site.kind === 'node') return site.node_version ? `Node ${site.node_version}` : 'Node';
  if (site.kind === 'php') return site.php_version ? `PHP ${site.php_version}` : 'PHP';
  return 'Static';
}

export function daysUntil(value: string | undefined): number | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return Math.ceil((date.getTime() - Date.now()) / 86_400_000);
}

export function pluralize(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`;
}

export function truncateMiddle(value: string, head = 18, tail = 8): string {
  if (value.length <= head + tail + 1) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

export function checksumPrefix(value: string, length = 12): string {
  if (!value) return '—';
  const clean = value.replace(/^sha256:/i, '');
  return clean.length > length ? `${clean.slice(0, length)}…` : clean;
}

export function isSecretKey(key: string): boolean {
  return /(key|token|secret|password|passwd|credential|auth)/i.test(key);
}

export function isAbsolutePath(value: string): boolean {
  const input = value.trim();
  if (!input) return false;
  if (input.startsWith('/') && !input.startsWith('//')) {
    return !input.split('/').includes('..');
  }

  const windowsPath = input.replace(/\//g, '\\');
  if (/^C:\\/i.test(windowsPath)) {
    return !windowsPath.slice(3).split('\\').some((part) => part === '..' || part.includes(':'));
  }

  const wslPath = /^\\\\wsl(?:\.localhost|\$)\\([^\\]+)\\(.+)$/i.exec(windowsPath);
  return Boolean(
    wslPath &&
      wslPath[1] !== '.' &&
      wslPath[1] !== '..' &&
      !wslPath[2].split('\\').some((part) => part === '.' || part === '..' || part.includes(':')),
  );
}
