import { describe, expect, it } from 'vitest';
import { formatBytes, formatRelative, checksumPrefix, daysUntil } from './format';

describe('format helpers', () => {
  it('formats bytes with binary units', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(5 * 1024 * 1024)).toBe('5 MB');
    expect(formatBytes(52_428_800)).toBe('50 MB');
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
});
