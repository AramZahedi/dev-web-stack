import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import PerformancePanel from './PerformancePanel.svelte';
import * as api from '../api';
import type { BenchmarkRun, PerformanceState, Site } from '../types';

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

  it('displays an unavailable banner when host tuning is unavailable', async () => {
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
        error: 'The privileged host helper is not available.',
      },
      warnings: ['The privileged host helper is not available.'],
    };

    mockApi.getPerformance.mockResolvedValue(perfState);
    mockApi.listSites.mockResolvedValue([]);
    mockApi.listBenchmarks.mockResolvedValue([]);

    render(PerformancePanel);

    const elements = await screen.findAllByText('The privileged host helper is not available.');
    expect(elements.length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Unavailable')).toBeTruthy();
  });

  it('renders WSL and Windows host tuning targets', async () => {
    const perfState: PerformanceState = {
      profile: 'throughput',
      values: {},
      resources: { cpu_count: 8, memory_total_bytes: 16 * 1024 ** 3, memory_available_bytes: 8 * 1024 ** 3 },
      host_tuning: {
        available: true,
        managed: true,
        applied: true,
        targets: [
          { target: 'wsl', available: true, applied: true },
          { target: 'windows', available: true, applied: true, restart_required: true },
          { target: 'wsl-vm', available: true, applied: true, restart_required: true },
        ],
      },
    };
    mockApi.getPerformance.mockResolvedValue(perfState);
    mockApi.listSites.mockResolvedValue([]);
    mockApi.listBenchmarks.mockResolvedValue([]);

    render(PerformancePanel);

    expect(await screen.findByText('wsl')).toBeTruthy();
    expect(screen.getByText('windows')).toBeTruthy();
    expect(screen.getByText('wsl-vm')).toBeTruthy();
    expect(screen.getAllByText('Restart required').length).toBe(2);
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

  it('sends a custom method, path, headers, and JSON body', async () => {
    const site = {
      id: 'site1',
      name: 'laravel',
      domain: 'laravel.test',
      tls: true,
      force_https: true,
    } as unknown as Site;
    const run = {
      id: 'bench-1',
      site: 'laravel',
      target: 'https://laravel.test/api/orders',
      method: 'POST',
      path: '/api/orders',
      header_names: ['Content-Type', 'X-Bench'],
      body_bytes: 17,
      profile: 'balanced',
      status: 'completed',
      tool: 'devstack-go-http-load',
      tool_version: '1',
      duration_s: 10,
      connections: 32,
      started_at: new Date().toISOString(),
      finished_at: new Date().toISOString(),
      measured: {
        requests: 100,
        errors: 0,
        requests_per_second: 10,
        p50_ms: 1,
        p95_ms: 2,
        p99_ms: 3,
        duration_seconds: 10,
      },
      directory: '/tmp/bench-1',
      raw_file: '/tmp/bench-1/raw.log',
    } as unknown as BenchmarkRun;

    const perfState: PerformanceState = {
      profile: 'balanced',
      values: {},
      resources: { cpu_count: 4, memory_total_bytes: 8 * 1024 ** 3, memory_available_bytes: 4 * 1024 ** 3 },
      host_tuning: { available: true, managed: true, applied: false },
    };
    mockApi.getPerformance.mockResolvedValue(perfState);
    mockApi.listSites.mockResolvedValue([site]);
    mockApi.listBenchmarks.mockResolvedValue([]);
    mockApi.runBenchmark.mockResolvedValue(run);

    render(PerformancePanel);

    await fireEvent.change(await screen.findByLabelText('Method'), { target: { value: 'POST' } });
    await fireEvent.input(screen.getByLabelText('Path'), { target: { value: '/api/orders' } });
    await fireEvent.input(screen.getByLabelText('Header 1 name'), { target: { value: 'X-Bench' } });
    await fireEvent.input(screen.getByLabelText('Header 1 value'), { target: { value: 'yes' } });
    await fireEvent.input(screen.getByLabelText('Request body'), {
      target: { value: '{"hello":"world"}' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Run benchmark' }));

    await waitFor(() => expect(mockApi.runBenchmark).toHaveBeenCalledTimes(1));
    expect(mockApi.runBenchmark).toHaveBeenCalledWith({
      site: 'laravel',
      engine: 'fasthttp',
      method: 'POST',
      path: '/api/orders',
      headers: { 'X-Bench': 'yes', 'Content-Type': 'application/json' },
      body: '{"hello":"world"}',
      duration_s: 10,
      connections: 32,
    });
    expect(await screen.findByText(/https:\/\/laravel\.test\/api\/orders/)).toBeTruthy();
  });
});
