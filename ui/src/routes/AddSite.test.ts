import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import AddSite from './AddSite.svelte';
import * as api from '../lib/api';
import type { RuntimeBuild, Settings } from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  listRuntimes: vi.fn(),
  getSettings: vi.fn(),
  createSite: vi.fn(),
}));

const NODE_BUILD: RuntimeBuild = {
  id: 'node-24.9.0',
  kind: 'node',
  version: '24.9.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/24.9.0',
  checksum: 'a'.repeat(64),
  installed_at: '2026-10-20T10:00:00Z',
};

const PHP_BUILD: RuntimeBuild = {
  id: 'php@8.4.4-r1',
  kind: 'php',
  version: '8.4.4',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.4.4-r1',
  checksum: 'b'.repeat(64),
  installed_at: '2026-10-20T10:00:00Z',
};

const SETTINGS: Settings = {
  home: '/home/dev/.devstack',
  user_home: '/home/dev',
  http_port: 8080,
  https_port: 8443,
  ui_port: 7070,
  serving_mode: 'builtin',
  serving_provider: 'native',
  wsl_distro: '',
  auto_start_sites: true,
  graceful_stop_seconds: 10,
  max_request_body_mb: 1024,
  log_max_mb: 10,
  log_keep: 5,
};

const mockApi = vi.mocked(api);

beforeEach(() => {
  mockApi.listRuntimes.mockResolvedValue([NODE_BUILD, PHP_BUILD]);
  mockApi.getSettings.mockResolvedValue(SETTINGS);
  mockApi.createSite.mockImplementation(async (request) => ({
    id: 's_new',
    name: request.name,
    domain: `${request.name}.test`,
    www: false,
    backend: 'local',
    host_path: request.path,
    backend_path: request.path,
    kind: request.kind,
    php_version: request.php_version ?? '',
    php_build: request.php_build ?? '',
    php_ini_raw: '',
    php_config_hash: '',
    node_version: request.node_version ?? '',
    command: request.command ?? '',
    env: request.env ?? {},
    document_root: request.document_root ?? '',
    front_controller: request.front_controller ?? false,
    front_controller_file: request.front_controller_file ?? 'index.php',
    front_controller_mode: request.front_controller_mode ?? 'auto',
    apache_allow_override: request.apache_allow_override ?? 'all',
    spa_fallback: request.spa_fallback ?? false,
    spa_fallback_file: request.spa_fallback_file ?? 'index.html',
    build: request.build,
    tls: request.tls ?? true,
    force_https: request.force_https ?? false,
    desired_state: 'running',
    status: 'starting',
    http_port: 0,
    fastcgi_port: 0,
    created_at: '2026-10-25T10:00:00Z',
    updated_at: '2026-10-25T10:00:00Z',
  }));
});

afterEach(() => {
  vi.clearAllMocks();
  window.location.hash = '';
});

async function fillProject(): Promise<void> {
  await fireEvent.input(screen.getByLabelText(/Site name/), { target: { value: 'demo' } });
  await fireEvent.input(screen.getByLabelText(/Project folder/), {
    target: { value: '/home/dev/projects/demo' },
  });
}

describe('AddSite Wave 2 fields', () => {
  it('submits front controller file/mode and Apache AllowOverride for PHP', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('radio', { name: /PHP/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.queryByRole('heading', { name: 'How should it run?' })).toBeTruthy();

    await fireEvent.change(await screen.findByLabelText(/PHP build/), {
      target: { value: PHP_BUILD.id },
    });
    await fireEvent.input(screen.getByLabelText(/Document root/), {
      target: { value: 'public' },
    });
    await fireEvent.input(screen.getByLabelText(/Front controller file/), {
      target: { value: 'public/index.php' },
    });
    await fireEvent.change(screen.getByLabelText('Front controller mode'), {
      target: { value: 'htaccess' },
    });
    await fireEvent.change(screen.getByLabelText('Apache AllowOverride'), {
      target: { value: 'fileinfo' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'php',
        document_root: 'public',
        front_controller: true,
        front_controller_file: 'public/index.php',
        front_controller_mode: 'htaccess',
        apache_allow_override: 'fileinfo',
      }),
    );
  });

  it('rejects an absolute front controller file before continuing', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('radio', { name: /PHP/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.queryByRole('heading', { name: 'How should it run?' })).toBeTruthy();

    await fireEvent.change(await screen.findByLabelText(/PHP build/), {
      target: { value: PHP_BUILD.id },
    });
    await fireEvent.input(screen.getByLabelText(/Front controller file/), {
      target: { value: '/index.php' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    expect((await screen.findAllByText('Use a relative path inside the project.')).length).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: 'Create site' })).toBeNull();
    expect(mockApi.createSite).not.toHaveBeenCalled();
  });

  it('submits SPA fallback file and a static build profile with environment', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.queryByRole('heading', { name: 'How should it run?' })).toBeTruthy();

    await fireEvent.input(await screen.findByLabelText(/Document root/), {
      target: { value: 'public' },
    });
    await fireEvent.click(screen.getByRole('switch', { name: 'SPA fallback' }));
    await fireEvent.input(screen.getByLabelText(/SPA fallback file/), {
      target: { value: 'app.html' },
    });

    await fireEvent.click(screen.getByRole('switch', { name: 'Build before serving' }));
    await fireEvent.change(await screen.findByLabelText(/Build Node version/), {
      target: { value: '24.9.0' },
    });
    await fireEvent.input(screen.getByLabelText(/Build command/), {
      target: { value: 'npm ci && npm run build' },
    });
    await fireEvent.input(screen.getByLabelText('Output directory'), {
      target: { value: 'public' },
    });
    await fireEvent.input(screen.getByLabelText('Build variable 1 name'), {
      target: { value: 'NODE_ENV' },
    });
    await fireEvent.input(screen.getByLabelText('Build variable 1 value'), {
      target: { value: 'production' },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByText('npm ci && npm run build')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'static',
        document_root: 'public',
        spa_fallback: true,
        spa_fallback_file: 'app.html',
        build: {
          node_version: '24.9.0',
          command: 'npm ci && npm run build',
          output: 'public',
          env: { NODE_ENV: 'production' },
        },
      }),
    );
  });
});
