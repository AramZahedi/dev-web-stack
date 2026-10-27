import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import PerformancePanel from './PerformancePanel.svelte';
import * as api from '../api';
import type { PerformanceState } from '../types';

vi.mock('../api', () => ({
  isMockMode: false,
  getPerformance: vi.fn(),
  previewPerformance: vi.fn(),
  applyPerformance: vi.fn(),
  resetPerformance: vi.fn(),
  listSites: vi.fn(),
  listBenchmarks: vi.fn(),
  runBenchmark: vi.fn(),
}));

const mockApi = vi.mocked(api);

describe('PerformancePanel', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('displays host tuning disabled banner when host tuning is unavailable on Windows', async () => {
    const perfState: PerformanceState = {
      profile: 'balanced',
      values: {},
      resources: {
        cpu_count: 8,
        memory_total_bytes: 16 * 1024 * 1024 * 1024,
        memory_available_bytes: 8 * 1024 * 1024 * 1024,
        nofile_hard: 65536,
        nofile_soft: 65536,
      },
      host_tuning: {
        available: false,
        managed: false,
        applied: false,
        error: 'Host sysctl and limits tuning are disabled on Windows.',
      },
      warnings: ['Host sysctl and limits tuning are disabled on Windows.'],
    };

    mockApi.getPerformance.mockResolvedValue(perfState);
    mockApi.listSites.mockResolvedValue([]);
    mockApi.listBenchmarks.mockResolvedValue([]);

    render(PerformancePanel);

    const elements = await screen.findAllByText('Host sysctl and limits tuning are disabled on Windows.');
    expect(elements.length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Disabled')).toBeTruthy();
  });

  it('displays host tuning status when host tuning is available', async () => {
    const perfState: PerformanceState = {
      profile: 'balanced',
      values: {},
      resources: {
        cpu_count: 4,
        memory_total_bytes: 8 * 1024 * 1024 * 1024,
        memory_available_bytes: 4 * 1024 * 1024 * 1024,
        nofile_hard: 1048576,
        nofile_soft: 1048576,
      },
      host_tuning: {
        available: true,
        managed: true,
        applied: false,
      },
    };

    mockApi.getPerformance.mockResolvedValue(perfState);
    mockApi.listSites.mockResolvedValue([]);
    mockApi.listBenchmarks.mockResolvedValue([]);

    render(PerformancePanel);

    expect(await screen.findByText('Not applied')).toBeTruthy();
    expect(screen.queryByText('Host sysctl and limits tuning are disabled on Windows.')).toBeNull();
  });
});
