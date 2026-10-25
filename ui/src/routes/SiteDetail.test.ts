import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import SiteDetail from './SiteDetail.svelte';
import * as api from '../lib/api';
import type { PHPInfo, RuntimeBuild, Site, UpdateSiteRequest } from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  getSite: vi.fn(),
  listRuntimes: vi.fn(),
  getPHPInfo: vi.fn(),
  startSite: vi.fn(),
  stopSite: vi.fn(),
  restartSite: vi.fn(),
  applyPHP: vi.fn(),
  deleteSite: vi.fn(),
  getVhostStatus: vi.fn(),
  previewVhost: vi.fn(),
  updateSite: vi.fn(),
  buildSite: vi.fn(),
  getBuildLogs: vi.fn(),
  openLogStream: vi.fn(() => () => undefined),
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

const STATIC_SITE: Site = {
  id: 's_blog',
  name: 'blog',
  domain: 'blog.test',
  www: false,
  backend: 'local',
  host_path: '/home/dev/projects/blog',
  backend_path: '/home/dev/projects/blog',
  kind: 'static',
  php_version: '',
  php_build: '',
  php_ini_raw: '',
  php_config_hash: '',
  node_version: '',
  command: '',
  env: {},
  document_root: 'public',
  front_controller: false,
  front_controller_file: 'index.php',
  front_controller_mode: 'auto',
  apache_allow_override: 'all',
  spa_fallback: false,
  spa_fallback_file: 'index.html',
  build: {
    node_version: '24.9.0',
    command: 'npm run build',
    output: 'public',
    env: { NODE_ENV: 'production' },
  },
  tls: true,
  force_https: true,
  desired_state: 'running',
  status: 'running',
  http_port: 41001,
  fastcgi_port: 0,
  created_at: '2026-10-20T10:00:00Z',
  updated_at: '2026-10-20T10:00:00Z',
};

const PHP_SITE: Site = {
  ...STATIC_SITE,
  id: 's_app',
  name: 'app',
  domain: 'app.test',
  kind: 'php',
  php_version: '8.4',
  php_build: PHP_BUILD.id,
  document_root: 'public',
  front_controller: true,
  front_controller_file: 'index.php',
  front_controller_mode: 'auto',
  apache_allow_override: 'all',
  spa_fallback: false,
  build: undefined,
  fastcgi_port: 43001,
  http_port: 0,
};

const PHP_INFO: PHPInfo = {
  site: PHP_SITE,
  build: PHP_BUILD,
  defaults: { version: '8.4', ini: {}, extensions: {} },
  extensions: [],
  available_extensions: [],
  builtins: ['core'],
  ini: {},
  ini_raw: '',
  generated_ini: '',
  fpm_config: '',
  config_hash: 'abc123',
};

const mockApi = vi.mocked(api);

beforeEach(() => {
  mockApi.getSite.mockResolvedValue({ ...STATIC_SITE });
  mockApi.listRuntimes.mockResolvedValue([NODE_BUILD, PHP_BUILD]);
  mockApi.getPHPInfo.mockResolvedValue(PHP_INFO);
  mockApi.updateSite.mockImplementation(async (ref, body: UpdateSiteRequest) => ({
    ...STATIC_SITE,
    ...body,
    build: body.build ?? STATIC_SITE.build,
  }));
  mockApi.getBuildLogs.mockResolvedValue({
    site: 'blog',
    path: '/home/dev/.devstack/logs/builds/blog/build.log',
    content: 'vite v6.1.0 building for production...\n✓ built in 4.28s',
  });
  mockApi.buildSite.mockResolvedValue({
    site: 'blog',
    success: true,
    command: 'npm run build',
    duration_ms: 4280,
    log_path: '/home/dev/.devstack/logs/builds/blog/build.log',
    output_path: '/home/dev/projects/blog/public',
    restored: false,
    snapshot_skipped: false,
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

async function renderSite(name = 'blog'): Promise<void> {
  render(SiteDetail, { props: { params: { ref: name } } });
  await screen.findByRole('heading', { name });
}

describe('SiteDetail configuration and builds', () => {
  it('edits the document root, SPA fallback file and static build profile', async () => {
    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const root = (await screen.findByLabelText(/Document root/)) as HTMLInputElement;
    expect(root.value).toBe('public');
    await fireEvent.input(root, { target: { value: '.' } });

    await fireEvent.click(screen.getByRole('switch', { name: 'SPA fallback' }));
    await fireEvent.input(screen.getByLabelText(/SPA fallback file/), {
      target: { value: 'app.html' },
    });

    expect(screen.queryByRole('switch', { name: 'Build profile' })).toBeNull();
    expect(screen.queryByRole('switch', { name: 'Add build profile' })).toBeNull();
    expect(screen.getByText(/removing a build profile is not supported/)).toBeTruthy();

    const command = screen.getByLabelText(/Build command/) as HTMLInputElement;
    expect(command.value).toBe('npm run build');
    await fireEvent.input(command, { target: { value: 'npm ci && npm run build' } });
    await fireEvent.input(screen.getByLabelText(/Output directory/), {
      target: { value: 'dist' },
    });
    await fireEvent.input(screen.getByLabelText('Build variable 1 name'), {
      target: { value: 'NODE_ENV' },
    });
    await fireEvent.input(screen.getByLabelText('Build variable 1 value'), {
      target: { value: 'production' },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));

    await waitFor(() => expect(mockApi.updateSite).toHaveBeenCalledTimes(1));
    expect(mockApi.updateSite).toHaveBeenCalledWith('s_blog', {
      document_root: '.',
      spa_fallback: true,
      spa_fallback_file: 'app.html',
      build: {
        node_version: '24.9.0',
        command: 'npm ci && npm run build',
        output: 'dist',
        env: { NODE_ENV: 'production' },
      },
    });
  });

  it('runs a build, shows duration/output, and refreshes the log tail', async () => {
    await renderSite();
    expect(await screen.findByLabelText('Build log tail')).toBeTruthy();
    expect(await screen.findByText(/built in 4.28s/)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Run build' }));

    expect(await screen.findByText('Build succeeded')).toBeTruthy();
    expect(screen.getByText('4.3 s')).toBeTruthy();
    expect(screen.getByText('/home/dev/projects/blog/public')).toBeTruthy();
    await waitFor(() => expect(mockApi.buildSite).toHaveBeenCalledWith('s_blog'));
    await waitFor(() => expect(mockApi.getBuildLogs.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it('shows a failed build result and the daemon error verbatim', async () => {
    mockApi.buildSite.mockResolvedValue({
      site: 'blog',
      success: false,
      command: 'npm run build',
      duration_ms: 1900,
      restored: true,
      snapshot_skipped: false,
      error: 'vite build failed: Could not resolve "./src/missing"',
      restore_note: 'Previous output restored from snapshot.',
    });

    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Run build' }));

    expect(await screen.findByText('Build failed')).toBeTruthy();
    expect(
      screen.getByText('vite build failed: Could not resolve "./src/missing"'),
    ).toBeTruthy();
    expect(screen.getByText('Previous output restored from snapshot.')).toBeTruthy();
    expect(screen.getByText('Previous output restored')).toBeTruthy();
  });

  it('edits PHP front controller file, mode and Apache AllowOverride', async () => {
    mockApi.getSite.mockResolvedValue({ ...PHP_SITE });
    mockApi.updateSite.mockImplementation(async (ref, body: UpdateSiteRequest) => ({
      ...PHP_SITE,
      ...body,
    }));

    await renderSite('app');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const file = (await screen.findByLabelText(/Front controller file/)) as HTMLInputElement;
    expect(file.value).toBe('index.php');
    expect(screen.getByText(/not public\/index.php when the document root is public/)).toBeTruthy();
    await fireEvent.input(file, { target: { value: 'app.php' } });
    await fireEvent.change(screen.getByLabelText('Front controller mode'), {
      target: { value: 'htaccess' },
    });
    await fireEvent.change(screen.getByLabelText('Apache AllowOverride'), {
      target: { value: 'fileinfo' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));

    await waitFor(() => expect(mockApi.updateSite).toHaveBeenCalledTimes(1));
    expect(mockApi.updateSite).toHaveBeenCalledWith('s_app', {
      document_root: 'public',
      front_controller: true,
      front_controller_file: 'app.php',
      front_controller_mode: 'htaccess',
      apache_allow_override: 'fileinfo',
    });
  });

  it('requires a build output before saving a static build profile', async () => {
    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));
    const output = (await screen.findByLabelText(/Output directory/)) as HTMLInputElement;
    await fireEvent.input(output, { target: { value: '' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));

    expect(
      (await screen.findAllByText(/Enter the output directory produced by the build/)).length,
    ).toBeGreaterThan(0);
    expect(mockApi.updateSite).not.toHaveBeenCalled();
  });

  it('only offers an add-profile toggle when no build profile exists', async () => {
    mockApi.getSite.mockResolvedValue({ ...STATIC_SITE, build: undefined });
    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const add = (await screen.findByRole('switch', { name: 'Add build profile' })) as HTMLElement;
    expect(add.getAttribute('aria-checked')).toBe('false');
    expect(screen.queryByLabelText(/Build command/)).toBeNull();

    await fireEvent.click(add);
    expect(await screen.findByLabelText(/Build command/)).toBeTruthy();
    expect(screen.getByLabelText(/Output directory/)).toBeTruthy();
  });

  it('renders the degraded status in text', async () => {
    mockApi.getSite.mockResolvedValue({ ...STATIC_SITE, status: 'degraded' });
    await renderSite();
    expect(screen.getByLabelText('Status: Degraded')).toBeTruthy();
  });
});
