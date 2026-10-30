import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Settings from './Settings.svelte';
import * as api from '../lib/api';
import { ApiError } from '../lib/errors';
import type {
  GlobalIni,
  NodeDefaultStatus,
  PHPDefaultStatus,
  RuntimeBuild,
  Settings as SettingsType,
  ToolStatus,
  VhostStatus,
} from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
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
  getNodeDefault: vi.fn(),
  setNodeDefault: vi.fn(),
  clearNodeDefault: vi.fn(),
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
  restart_required: false,
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

const NODE_24: RuntimeBuild = {
  id: 'node@24.9.0',
  kind: 'node',
  version: '24.9.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/24.9.0-linux-x64',
  checksum: 'd'.repeat(64),
  installed_at: '2026-10-29T10:00:00Z',
};

const NODE_22: RuntimeBuild = {
  id: 'node@22.11.0',
  kind: 'node',
  version: '22.11.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/22.11.0-linux-x64',
  checksum: 'e'.repeat(64),
  installed_at: '2026-10-10T10:00:00Z',
};

const NODE_DEFAULT_STATUS: NodeDefaultStatus = {
  configured: NODE_24.version,
  build: NODE_24,
  shim_dir: '/home/dev/.devstack/tools/node-default/bin',
  shim_installed: true,
  source: 'setting',
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
  mockApi.updateSettings.mockResolvedValue({ ...SETTINGS, restart_required: false });
  mockApi.getTrust.mockResolvedValue([]);
  mockApi.getVhostStatus.mockResolvedValue(VHOSTS);
  mockApi.getPHPDefault.mockResolvedValue(DEFAULT_STATUS);
  mockApi.listRuntimes.mockResolvedValue([PHP_84, PHP_83, NODE_24, NODE_22]);
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
  mockApi.getNodeDefault.mockResolvedValue(NODE_DEFAULT_STATUS);
  mockApi.setNodeDefault.mockResolvedValue({
    ...NODE_DEFAULT_STATUS,
    configured: NODE_22.version,
    build: NODE_22,
  });
  mockApi.clearNodeDefault.mockResolvedValue({
    ...NODE_DEFAULT_STATUS,
    configured: undefined,
    build: NODE_24,
    source: 'newest',
  });
  mockApi.listTools.mockResolvedValue([COMPOSER]);
  mockApi.installTool.mockResolvedValue(COMPOSER);
  mockApi.removeTool.mockResolvedValue({ ...COMPOSER, installed: false, managed: false });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('Settings tooling', () => {
  it('presents Windows and WSL runtime guidance for a Windows installation', async () => {
    mockApi.getSettings.mockResolvedValue({
      ...SETTINGS,
      home: 'C:\\devstack',
      user_home: 'C:\\Users\\Aram',
      wsl_distro: 'devstack',
      public_ports_fixed: true,
    });
    render(Settings);

    const card = await screen.findByRole('heading', { name: 'Windows + WSL' });
    expect(card).toBeTruthy();
    const detail = card.closest('section')?.textContent ?? '';
    expect(detail).toMatch(/Site backends and PHP\/Node runtimes run in WSL/);
    expect(detail).toMatch(/Windows-drive static projects are read through \/mnt\/c/);
    expect(detail).toContain('devstack');

    const http = (await screen.findByLabelText('HTTP proxy port')) as HTMLInputElement;
    const https = screen.getByLabelText('HTTPS proxy port') as HTMLInputElement;
    const ui = screen.getByLabelText('Dashboard/API port') as HTMLInputElement;
    expect(http.disabled).toBe(true);
    expect(https.disabled).toBe(true);
    expect(ui.disabled).toBe(false);
  });

  it('keeps local settings visible when the helper trust status is unavailable', async () => {
    mockApi.getTrust.mockRejectedValue(
      new ApiError('HELPER_UNAVAILABLE', 'devstack-helper is not running', 502),
    );
    render(Settings);

    expect(await screen.findByRole('heading', { name: 'Trust' })).toBeTruthy();
    expect(await screen.findByText('Could not read trust status')).toBeTruthy();
    expect(screen.getByLabelText('HTTP proxy port')).toBeTruthy();
  });

  it('shows the global PHP default, shim status, editable ini and Composer tools', async () => {
    render(Settings);

    expect(await screen.findByRole('heading', { name: 'PHP CLI default' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Global CLI php.ini' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Native tools' })).toBeTruthy();

    const select = (await screen.findByLabelText('Default PHP build')) as HTMLSelectElement;
    expect(select.value).toBe(PHP_84.id);
    const phpCard = screen.getByRole('heading', { name: 'PHP CLI default' }).closest('section');
    expect(phpCard?.textContent).toContain('PHP 8.4.4');
    expect(phpCard?.textContent).toContain(
      'Installed in /home/dev/.devstack/tools/php-default/bin',
    );
    expect(phpCard?.textContent).not.toContain('/usr/local/bin');

    const ini = screen.getByLabelText('Global php.ini content') as HTMLTextAreaElement;
    expect(ini.value).toContain('memory_limit = 512M');

    expect(screen.getByRole('heading', { name: 'Composer' })).toBeTruthy();
    expect(screen.getByText('2.8.4')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'ImageMagick CLI' })).toBeNull();
    expect(screen.queryByText(/ImageMagick/i)).toBeNull();
    const toolCard = screen.getByRole('heading', { name: 'Native tools' }).closest('section');
    expect(toolCard?.className).toContain('native-tools-card');
  });

  it('saves listener ports and shows durable restart guidance', async () => {
    const updated = {
      ...SETTINGS,
      http_port: 18080,
      https_port: 18443,
      ui_port: 17070,
      restart_required: true,
    };
    mockApi.updateSettings.mockResolvedValue({
      ...updated,
      warnings: ['Run `devstack daemon restart` to move the listeners.'],
    });

    render(Settings);
    const http = (await screen.findByLabelText('HTTP proxy port')) as HTMLInputElement;
    const https = screen.getByLabelText('HTTPS proxy port') as HTMLInputElement;
    const ui = screen.getByLabelText('Dashboard/API port') as HTMLInputElement;
    expect(http.value).toBe('8080');
    expect(https.value).toBe('8443');
    expect(ui.value).toBe('7070');

    await fireEvent.input(http, { target: { value: '18080' } });
    await fireEvent.input(https, { target: { value: '18443' } });
    await fireEvent.input(ui, { target: { value: '17070' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save ports' }));

    await waitFor(() =>
      expect(mockApi.updateSettings).toHaveBeenCalledWith({
        http_port: 18080,
        https_port: 18443,
        ui_port: 17070,
      }),
    );
    expect((await screen.findAllByText('Restart required')).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/devstack daemon restart/).length).toBeGreaterThan(0);
    expect(http.value).toBe('18080');
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

  it('saves and clears the node cli default', async () => {
    render(Settings);
    const heading = await screen.findByRole('heading', { name: 'Node CLI default' });
    const card = heading.closest('section') as HTMLElement;
    const select = (await screen.findByLabelText('Default Node build')) as HTMLSelectElement;
    expect(select.value).toBe(NODE_24.version);

    await fireEvent.change(select, { target: { value: NODE_22.version } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save Node default' }));

    await waitFor(() => expect(mockApi.setNodeDefault).toHaveBeenCalledWith(NODE_22.version));
    await waitFor(() => {
      expect((screen.getByLabelText('Default Node build') as HTMLSelectElement).value).toBe(
        NODE_22.version,
      );
    });
    expect(card.textContent).toContain('Node 22.11.0');

    await fireEvent.click(screen.getByRole('button', { name: 'Clear Node default' }));
    await waitFor(() => expect(mockApi.clearNodeDefault).toHaveBeenCalledTimes(1));
    expect(card.textContent).toContain('Newest installed (Node 24.9.0)');
    expect(within(card).getByRole('link', { name: 'Install more Node versions' })).toBeTruthy();
  });

  it('shows a configured node default that is not installed', async () => {
    mockApi.getNodeDefault.mockResolvedValue({
      configured: NODE_22.version,
      build: undefined,
      shim_dir: '/home/dev/.devstack/tools/node-default/bin',
      shim_installed: false,
      source: 'setting',
    });
    render(Settings);

    const card = (await screen.findByRole('heading', { name: 'Node CLI default' })).closest(
      'section',
    ) as HTMLElement;

    expect(
      await within(card).findByText(/Configured Node 22\.11\.0 is not installed/i),
    ).toBeTruthy();
    const select = within(card).getByLabelText('Default Node build') as HTMLSelectElement;
    expect(select.value).toBe('');
    expect(
      (within(card).getByRole('button', { name: 'Save Node default' }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(card.textContent).toContain('Configured · Node 22.11.0 (not installed)');
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

  it('surfaces tool removal errors inside the confirmation dialog', async () => {
    mockApi.removeTool.mockRejectedValue(
      new ApiError('HELPER_UNAVAILABLE', 'service helper is unavailable', 503),
    );

    render(Settings);
    await screen.findByRole('heading', { name: 'Composer' });
    await fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog', { name: 'Remove composer?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Remove tool' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toContain('HELPER_UNAVAILABLE');
    expect(alert.textContent).toContain('service helper is unavailable');
    expect(screen.getByRole('dialog', { name: 'Remove composer?' })).toBeTruthy();
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
