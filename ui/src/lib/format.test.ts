import { describe, expect, it } from 'vitest';
import { formatBytes, formatDateOnly, formatRelative, checksumPrefix, daysUntil, siteUrl, isAbsolutePath, sourceModeLabel } from './format';

describe('format helpers', () => {
  it('accepts only Linux, C-drive, and WSL UNC absolute project paths', () => {
    expect(isAbsolutePath('/home/aram/project')).toBe(true);
    expect(isAbsolutePath('C:\\devstack\\projects\\site')).toBe(true);
    expect(isAbsolutePath('\\\\wsl.localhost\\devstack\\home\\aram\\site')).toBe(true);
    expect(isAbsolutePath('\\\\wsl$\\devstack\\home\\aram\\site')).toBe(true);
    expect(isAbsolutePath('D:\\projects\\site')).toBe(false);
    expect(isAbsolutePath('\\\\server\\share\\site')).toBe(false);
    expect(isAbsolutePath('\\\\?\\C:\\site')).toBe(false);
    expect(isAbsolutePath('C:\\site\\..\\other')).toBe(false);
    expect(isAbsolutePath('C:\\site\\file:stream')).toBe(false);
  });

  it('formats bytes with binary units', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(5 * 1024 * 1024)).toBe('5 MB');
    expect(formatBytes(52_428_800)).toBe('50 MB');
  });

  it('formats release date-only strings without a timezone shift', () => {
    expect(formatDateOnly('2026-10-15')).toBe('Oct 15, 2026');
    expect(formatDateOnly('2026-01-01')).toBe('Jan 1, 2026');
    expect(formatDateOnly('')).toBe('—');
  });

  it('formats relative time in human units', () => {
    expect(formatRelative(new Date(Date.now() - 5_000).toISOString())).toBe('just now');
    expect(formatRelative(new Date(Date.now() - 3 * 86_400_000).toISOString())).toBe('3 days ago');
  });

  it('shortens checksums without losing the prefix', () => {
    expect(checksumPrefix('9a4f7c02e1d8b63a', 8)).toBe('9a4f7c02…');
    expect(checksumPrefix('sha256:abcdef0123456789', 6)).toBe('abcdef…');
  });

  it('counts remaining certificate days', () => {
    const future = new Date(Date.now() + 10 * 86_400_000).toISOString();
    expect(daysUntil(future)).toBeGreaterThanOrEqual(9);
    expect(daysUntil(undefined)).toBeNull();
  });

  it('adds non-default public ports to site URLs', () => {
    expect(siteUrl({ domain: 'blog.test', tls: true }, { http: '8080', https: '8443' })).toBe(
      'https://blog.test:8443',
    );
    expect(siteUrl({ domain: 'blog.test', tls: false }, { http: '8080', https: '8443' })).toBe(
      'http://blog.test:8080',
    );
    expect(siteUrl({ domain: 'blog.test', tls: true }, { http: '80', https: '443' })).toBe(
      'https://blog.test',
    );
  });

  it('formats source mode labels for display', () => {
    expect(sourceModeLabel('windows')).toBe('Windows drive');
    expect(sourceModeLabel('wsl')).toBe('WSL distro');
    expect(sourceModeLabel('local')).toBe('Local');
    expect(sourceModeLabel(undefined)).toBe('');
  });
});
