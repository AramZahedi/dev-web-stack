import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import AddSite from './AddSite.svelte';
import * as api from '../lib/api';
import { clearToasts, toasts } from '../lib/stores';
import type { RuntimeBuild, Settings } from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  listRuntimes: vi.fn(),
  getSettings: vi.fn(),
  getNodeDefault: vi.fn(),
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
  mockApi.getNodeDefault.mockResolvedValue({
    build: NODE_BUILD,
    shim_dir: '/home/dev/.devstack/tools/node-default/bin',
    shim_installed: true,
    source: 'newest',
  });
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
  clearToasts();
  window.location.hash = '';
});

async function fillProject(): Promise<void> {
  await fireEvent.input(screen.getByLabelText(/Site name/), { target: { value: 'demo' } });
  await fireEvent.input(screen.getByLabelText(/Project folder/), {
    target: { value: '/home/dev/projects/demo' },
  });
}

describe('AddSite Wave 2 fields', () => {
  it('defaults static sites to inherit and shows the global version', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.getNodeDefault).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const picker = (await screen.findByLabelText(/Node toolchain/)) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(
      await screen.findByRole('option', { name: 'Inherit global default (v24.9.0)' }),
    ).toBeTruthy();
  });

  it('requires an explicit version for node sites', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('radio', { name: /Node/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const picker = (await screen.findByLabelText(/Node version/)) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(screen.queryByRole('option', { name: /Inherit global default/ })).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(
      (await screen.findAllByText('Choose an installed Node version.')).length,
    ).toBeGreaterThan(0);
    expect(mockApi.createSite).not.toHaveBeenCalled();
  });

  it('defaults Windows static sources to NTFS and PHP/Node sources to WSL', async () => {
    mockApi.getSettings.mockResolvedValue({
      ...SETTINGS,
      home: 'C:\\devstack',
      user_home: 'C:\\Users\\Aram',
      wsl_distro: 'devstack',
    });
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());

    const windowsMode = await screen.findByRole('radio', { name: /Windows drive/ });
    expect((windowsMode as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(screen.getByRole('radio', { name: /PHP/ }));
    expect((screen.getByRole('radio', { name: /WSL distro/ }) as HTMLInputElement).checked).toBe(true);
    await fireEvent.click(screen.getByRole('radio', { name: /Node/ }));
    expect((screen.getByRole('radio', { name: /WSL distro/ }) as HTMLInputElement).checked).toBe(true);
  });

  it('submits the selected Windows source mode when creating a static site', async () => {
    mockApi.getSettings.mockResolvedValue({
      ...SETTINGS,
      home: 'C:\\devstack',
      user_home: 'C:\\Users\\Aram',
      wsl_distro: 'devstack',
    });
    render(AddSite);
    await screen.findByRole('radio', { name: /Windows drive/ });
    await fireEvent.input(screen.getByLabelText(/Site name/), { target: { value: 'winstatic' } });
    await fireEvent.input(screen.getByLabelText(/Project folder/), {
      target: { value: 'C:\\devstack\\projects\\winstatic' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({ source_mode: 'windows', path: 'C:\\devstack\\projects\\winstatic' }),
    );
  });

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
    expect(screen.getByText(/not public\/index.php when the document root is public/)).toBeTruthy();
    await fireEvent.input(screen.getByLabelText(/Front controller file/), {
      target: { value: 'index.php' },
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
        front_controller_file: 'index.php',
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

  it('requires a build output instead of treating it as optional', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    await fireEvent.input(await screen.findByLabelText(/Document root/), {
      target: { value: 'public' },
    });
    await fireEvent.click(screen.getByRole('switch', { name: 'Build before serving' }));
    const output = (await screen.findByLabelText(/Output directory/)) as HTMLInputElement;
    expect(screen.getByText(/Required\. Relative to the project folder/)).toBeTruthy();
    await fireEvent.input(output, { target: { value: '' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    expect(
      (await screen.findAllByText(/Enter the output directory produced by the build/)).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByRole('button', { name: 'Create site' })).toBeNull();
    expect(mockApi.createSite).not.toHaveBeenCalled();
  });

  it('tells the user to build and start when the build output is missing', async () => {
    const implementation = mockApi.createSite.getMockImplementation();
    mockApi.createSite.mockImplementationOnce(async (request) => {
      const created = await implementation?.(request);
      return { ...created!, status: 'stopped', desired_state: 'stopped' };
    });

    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.input(await screen.findByLabelText(/Document root/), {
      target: { value: 'public' },
    });
    await fireEvent.click(screen.getByRole('switch', { name: 'Build before serving' }));
    await fireEvent.input(await screen.findByLabelText(/Output directory/), {
      target: { value: 'dist' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    const toast = get(toasts).find((entry) => entry.title === 'demo added');
    expect(toast?.message).toContain('created stopped because dist does not exist yet');
    expect(toast?.message).toContain('Run Build on the detail page, then Start the site.');
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
    await fireEvent.input(screen.getByLabelText(/Output directory/), {
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

  it('auto-slugifies the site name and prefills the project directory based on source mode', async () => {
    mockApi.getSettings.mockResolvedValue({
      ...SETTINGS,
      home: 'C:\\Users\\Aram\\.devstack',
      user_home: 'C:\\Users\\Aram',
      wsl_distro: 'devstack',
    });
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());

    const nameInput = screen.getByLabelText(/Site name/);
    const pathInput = screen.getByLabelText(/Project folder/) as HTMLInputElement;

    // Type a name with uppercase and spaces
    await fireEvent.input(nameInput, { target: { value: 'My Awesome Blog' } });
    await fireEvent.blur(nameInput);

    // Verify name was auto-slugified
    expect((nameInput as HTMLInputElement).value).toBe('my-awesome-blog');

    // By default static on Windows starts in Windows mode:
    expect(pathInput.value).toBe('C:\\Users\\Aram\\.devstack\\projects\\my-awesome-blog');

    // Switch to PHP (which switches to WSL mode on Windows)
    await fireEvent.click(screen.getByRole('radio', { name: /PHP/ }));
    expect(pathInput.value).toBe('/data/devstack/projects/my-awesome-blog');
  });
});
