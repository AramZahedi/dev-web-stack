import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Settings from './Settings.svelte';
import * as api from '../lib/api';
import { ApiError } from '../lib/errors';
import type {
  GlobalIni,
  PHPDefaultStatus,
  RuntimeBuild,
  Settings as SettingsType,
  ToolStatus,
  VhostStatus,
} from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  getSettings: vi.fn(),
  getTrust: vi.fn(),
  getVhostStatus: vi.fn(),
  setServingMode: vi.fn(),
  syncVhosts: vi.fn(),
  listSites: vi.fn(),
  repairTrust: vi.fn(),
  removeTrust: vi.fn(),
  getPHPDefault: vi.fn(),
  listRuntimes: vi.fn(),
  getGlobalIni: vi.fn(),
  saveGlobalIni: vi.fn(),
  setPHPDefault: vi.fn(),
  clearPHPDefault: vi.fn(),
  listTools: vi.fn(),
  installTool: vi.fn(),
  removeTool: vi.fn(),
}));

const SETTINGS: SettingsType = {
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

const VHOSTS: VhostStatus = {
  mode: 'builtin',
  server: '',
  server_version: '',
  bridge_path: '',
  include_present: false,
  current_release: '',
  config_ok: true,
  last_sync: '',
};

const PHP_84: RuntimeBuild = {
  id: 'php@8.4.4-r1',
  kind: 'php',
  version: '8.4.4',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.4.4-r1',
  checksum: 'a'.repeat(64),
  installed_at: '2026-10-20T10:00:00Z',
};

const PHP_83: RuntimeBuild = {
  id: 'php@8.3.14-r2',
  kind: 'php',
  version: '8.3.14',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.3.14-r2',
  checksum: 'b'.repeat(64),
  installed_at: '2026-10-18T10:00:00Z',
};

const DEFAULT_STATUS: PHPDefaultStatus = {
  configured: PHP_84.id,
  build: PHP_84,
  global_ini: '/home/dev/.devstack/tools/php-default/php.ini',
  shim_dir: '/home/dev/.devstack/tools/php-default/bin',
  shim_installed: true,
};

const INI: GlobalIni = {
  path: '/home/dev/.devstack/tools/php-default/php.ini',
  content: '[PHP]\nmemory_limit = 512M\n',
  build_id: PHP_84.id,
};

const COMPOSER: ToolStatus = {
  id: 'composer',
  installed: true,
  managed: true,
  wrapper_path: '/usr/local/bin/composer',
  target: '/home/dev/.devstack/tools/composer/composer.phar',
  version: '2.8.4',
};

const mockApi = vi.mocked(api);

beforeEach(() => {
  mockApi.getSettings.mockResolvedValue(SETTINGS);
  mockApi.getTrust.mockResolvedValue([]);
  mockApi.getVhostStatus.mockResolvedValue(VHOSTS);
  mockApi.getPHPDefault.mockResolvedValue(DEFAULT_STATUS);
  mockApi.listRuntimes.mockResolvedValue([PHP_84, PHP_83]);
  mockApi.getGlobalIni.mockResolvedValue(INI);
  mockApi.saveGlobalIni.mockResolvedValue(INI);
  mockApi.setPHPDefault.mockResolvedValue({
    ...DEFAULT_STATUS,
    configured: PHP_83.id,
    build: PHP_83,
  });
  mockApi.clearPHPDefault.mockResolvedValue({
    ...DEFAULT_STATUS,
    configured: undefined,
    build: PHP_84,
  });
  mockApi.listTools.mockResolvedValue([COMPOSER]);
  mockApi.installTool.mockResolvedValue(COMPOSER);
  mockApi.removeTool.mockResolvedValue({ ...COMPOSER, installed: false, managed: false });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('Settings tooling', () => {
  it('shows the global PHP default, shim status, editable ini and Composer tools', async () => {
    render(Settings);

    expect(await screen.findByRole('heading', { name: 'PHP CLI default' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Global CLI php.ini' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Native tools' })).toBeTruthy();

    const select = (await screen.findByLabelText('Default PHP build')) as HTMLSelectElement;
    expect(select.value).toBe(PHP_84.id);
    const phpCard = screen.getByRole('heading', { name: 'PHP CLI default' }).closest('section');
    expect(phpCard?.textContent).toContain('PHP 8.4.4');
    expect(phpCard?.textContent).toContain('Installed in /usr/local/bin');

    const ini = screen.getByLabelText('Global php.ini content') as HTMLTextAreaElement;
    expect(ini.value).toContain('memory_limit = 512M');

    expect(screen.getByRole('heading', { name: 'Composer' })).toBeTruthy();
    expect(screen.getByText('2.8.4')).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'ImageMagick CLI' })).toBeTruthy();
    expect(screen.getByText('Wave 2 backend pending')).toBeTruthy();
    expect(screen.getByText(/does not expose an ImageMagick tool endpoint/)).toBeTruthy();
    const toolCard = screen.getByRole('heading', { name: 'Native tools' }).closest('section');
    expect(within(toolCard as HTMLElement).queryByRole('button', { name: 'Install ImageMagick' })).toBeNull();
  });

  it('saves and clears the machine-wide PHP CLI default', async () => {
    render(Settings);
    const select = (await screen.findByLabelText('Default PHP build')) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: PHP_83.id } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save default' }));

    await waitFor(() => expect(mockApi.setPHPDefault).toHaveBeenCalledWith(PHP_83.id));
    await waitFor(() => {
      expect((screen.getByLabelText('Default PHP build') as HTMLSelectElement).value).toBe(
        PHP_83.id,
      );
    });
    const phpCard = screen.getByRole('heading', { name: 'PHP CLI default' }).closest('section');
    expect(phpCard?.textContent).toContain('PHP 8.3.14');

    await fireEvent.click(screen.getByRole('button', { name: 'Clear default' }));
    await waitFor(() => expect(mockApi.clearPHPDefault).toHaveBeenCalledTimes(1));
    expect(phpCard?.textContent).toContain('Newest installed');
  });

  it('saves the global CLI php.ini content', async () => {
    render(Settings);
    const ini = (await screen.findByLabelText('Global php.ini content')) as HTMLTextAreaElement;
    const next = '[PHP]\nmemory_limit = 1G\ndisplay_errors = On\n';
    await fireEvent.input(ini, { target: { value: next } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save php.ini' }));

    await waitFor(() => expect(mockApi.saveGlobalIni).toHaveBeenCalledWith(next));
    expect(await screen.findByText('Saved')).toBeTruthy();
  });

  it('updates and removes Composer through the managed tools API', async () => {
    render(Settings);
    await screen.findByRole('heading', { name: 'Composer' });

    await fireEvent.click(screen.getByRole('button', { name: 'Update' }));
    await waitFor(() => expect(mockApi.installTool).toHaveBeenCalledWith('composer', false));

    await fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog', { name: 'Remove composer?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Remove tool' }));

    await waitFor(() => expect(mockApi.removeTool).toHaveBeenCalledWith('composer'));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Remove composer?' })).toBeNull());
  });

  it('offers install for an unmanaged Composer and shows tool conflicts verbatim', async () => {
    mockApi.listTools.mockResolvedValue([
      { ...COMPOSER, installed: false, managed: false, conflict: 'existing /usr/local/bin/composer' },
    ]);
    mockApi.installTool.mockRejectedValue(
      new ApiError(
        'TOOL_CONFLICT',
        'helper: tool conflict: /usr/local/bin/composer: existing wrapper (rerun with force to back it up)',
        409,
      ),
    );

    render(Settings);
    const install = await screen.findByRole('button', { name: 'Install' });
    await fireEvent.click(install);

    const alert = await screen.findByText(/helper: tool conflict/);
    expect(alert).toBeTruthy();
    expect(screen.getByText(/rerun with force to back it up/)).toBeTruthy();
    expect(mockApi.installTool).toHaveBeenCalledWith('composer', false);
  });
});
