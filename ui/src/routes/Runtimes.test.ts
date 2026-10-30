import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Runtimes from './Runtimes.svelte';
import * as api from '../lib/api';
import { ApiError } from '../lib/errors';
import type {
  NodeRelease,
  NodeReleasePreflight,
  NodeReleaseSnapshot,
  RuntimeBuild,
  Site,
} from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  listRuntimes: vi.fn(),
  availableRuntimes: vi.fn(),
  listSites: vi.fn(),
  installRuntime: vi.fn(),
  removeRuntime: vi.fn(),
  migratePHP: vi.fn(),
  getPHPDefaults: vi.fn(),
  savePHPDefaults: vi.fn(),
  phpExtensions: vi.fn(),
  listNodeReleases: vi.fn(),
  getNodeRelease: vi.fn(),
  refreshNodeReleases: vi.fn(),
}));

const BUILD_A: RuntimeBuild = {
  id: 'php@8.4.4-r1',
  kind: 'php',
  version: '8.4.4',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.4.4-r1',
  checksum: 'a'.repeat(64),
  installed_at: '2026-10-20T10:00:00Z',
};

const BUILD_B: RuntimeBuild = {
  id: 'php@8.4.5-r1',
  kind: 'php',
  version: '8.4.5',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.4.5-r1',
  checksum: 'b'.repeat(64),
  installed_at: '2026-10-21T10:00:00Z',
};

const BUILD_C: RuntimeBuild = {
  id: 'php@8.5.0-r1',
  kind: 'php',
  version: '8.5.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/php/8.5.0-r1',
  checksum: 'c'.repeat(64),
  installed_at: '2026-10-22T10:00:00Z',
};

const NODE_24_9_0: NodeRelease = {
  version: '24.9.0',
  date: '2026-10-15',
  lts: 'Krypton',
  security: false,
  npm: '11.6.0',
  modules: '137',
  file: 'linux-x64',
};

const NODE_24_8_1: NodeRelease = {
  version: '24.8.1',
  date: '2026-09-24',
  lts: 'Krypton',
  security: false,
  npm: '11.5.2',
  modules: '137',
  file: 'linux-x64',
};

const NODE_24_8_0: NodeRelease = {
  version: '24.8.0',
  date: '2026-09-10',
  lts: 'Krypton',
  security: false,
  npm: '11.5.1',
  modules: '137',
  file: 'linux-x64',
};

const NODE_24_1_2: NodeRelease = {
  version: '24.1.2',
  date: '2026-04-08',
  lts: 'Krypton',
  security: false,
  npm: '11.0.0',
  modules: '137',
  file: 'linux-x64',
};

const NODE_23_5_0: NodeRelease = {
  version: '23.5.0',
  date: '2026-01-14',
  lts: '',
  security: false,
  npm: '10.9.2',
  modules: '131',
  file: 'linux-x64',
};

const NODE_22_20_0: NodeRelease = {
  version: '22.20.0',
  date: '2026-10-08',
  lts: 'Jod',
  security: true,
  npm: '10.9.4',
  modules: '127',
  file: 'linux-x64',
};

const NODE_22_19_0: NodeRelease = {
  version: '22.19.0',
  date: '2026-09-18',
  lts: 'Jod',
  security: true,
  npm: '10.9.0',
  modules: '127',
  file: 'linux-x64',
};

const NODE_RELEASES: NodeReleaseSnapshot = {
  majors: [
    { major: 24, latest: NODE_24_9_0, releases: [NODE_24_9_0] },
    { major: 23, latest: NODE_23_5_0, releases: [NODE_23_5_0] },
    { major: 22, latest: NODE_22_20_0, releases: [NODE_22_20_0] },
  ],
  all: [
    NODE_24_9_0,
    NODE_24_8_1,
    NODE_24_8_0,
    NODE_24_1_2,
    NODE_23_5_0,
    NODE_22_20_0,
    NODE_22_19_0,
  ],
  source: 'live',
  cached_at: '2026-10-30T09:00:00Z',
  stale: false,
};

const NODE_24_PREFLIGHT: NodeReleasePreflight = {
  entry: {
    id: 'node@24.9.0',
    kind: 'node',
    version: '24.9.0',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v24.9.0/node-v24.9.0-linux-x64.tar.xz',
    sha256: '9a4f7c02e1d8b63a5f0c92d47e18ba6c3f5d20e9a7b4c61d8f30e2a95c7b4d18',
    size: 51_904_512,
  },
  release: NODE_24_9_0,
};

const NODE_22_PREFLIGHT: NodeReleasePreflight = {
  entry: {
    id: 'node@22.19.0',
    kind: 'node',
    version: '22.19.0',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v22.19.0/node-v22.19.0-linux-x64.tar.xz',
    sha256: '1f2c3d4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d',
    size: 48_234_496,
  },
  release: NODE_22_19_0,
};

const NODE_24_BUILD: RuntimeBuild = {
  id: 'node@24.9.0',
  kind: 'node',
  version: '24.9.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/24.9.0-linux-x64',
  checksum: 'd'.repeat(64),
  installed_at: '2026-10-29T10:00:00Z',
};

const PHP_EXTENSIONS = [
  {
    name: 'opcache',
    type: 'zend_extension' as const,
    so: 'opcache.so',
    default: true,
    standard: true,
    versions: ['8.4', '8.5'],
  },
  {
    name: 'xdebug',
    type: 'zend_extension' as const,
    so: 'xdebug.so',
    default: false,
    standard: false,
    versions: ['8.4', '8.5'],
  },
];

const DEFAULTS_84 = {
  version: '8.4',
  ini: { memory_limit: '256M' },
  extensions: { opcache: true, xdebug: false },
};

const PHP_SITE: Site = {
  id: 's_blog',
  name: 'blog',
  domain: 'blog.test',
  www: false,
  backend: 'local',
  host_path: '/home/dev/projects/blog',
  backend_path: '/home/dev/projects/blog',
  kind: 'php',
  php_version: '8.4',
  php_build: BUILD_A.id,
  php_ini_raw: '',
  php_config_hash: '',
  node_version: '',
  command: '',
  env: {},
  document_root: 'public',
  front_controller: true,
  front_controller_file: 'index.php',
  front_controller_mode: 'auto',
  apache_allow_override: 'all',
  spa_fallback: false,
  spa_fallback_file: 'index.html',
  tls: true,
  force_https: false,
  desired_state: 'running',
  status: 'running',
  http_port: 0,
  fastcgi_port: 43001,
  created_at: '2026-10-20T10:00:00Z',
  updated_at: '2026-10-20T10:00:00Z',
};

const mockApi = vi.mocked(api);

beforeEach(() => {
  mockApi.listRuntimes.mockResolvedValue([BUILD_A, BUILD_B]);
  mockApi.availableRuntimes.mockResolvedValue([]);
  mockApi.listSites.mockResolvedValue([]);
  mockApi.migratePHP.mockResolvedValue([
    { site: 'blog', ok: true },
    { site: 'shop', ok: true },
  ]);
  mockApi.removeRuntime.mockReset();
  mockApi.listNodeReleases.mockResolvedValue(NODE_RELEASES);
  mockApi.getNodeRelease.mockResolvedValue(NODE_24_PREFLIGHT);
  mockApi.refreshNodeReleases.mockResolvedValue(NODE_RELEASES);
  mockApi.installRuntime.mockResolvedValue(NODE_24_BUILD);
});

afterEach(() => {
  vi.clearAllMocks();
});

async function openMigrationDialog(): Promise<HTMLElement> {
  const row = (await screen.findByRole('heading', { name: 'PHP 8.4.4' })).closest('article');
  expect(row).not.toBeNull();
  await fireEvent.click(within(row as HTMLElement).getByRole('button', { name: /Remove/ }));
  await fireEvent.click(await screen.findByRole('button', { name: 'Remove build' }));
  return screen.findByRole('dialog', { name: /Migrate sites off PHP 8.4.4/ });
}

async function openDefaultsDrawer(): Promise<HTMLElement> {
  const row = (await screen.findByRole('heading', { name: 'PHP 8.4.4' })).closest('article');
  expect(row).not.toBeNull();
  const lineBlock = row?.closest('.line-block') as HTMLElement;
  await fireEvent.click(within(lineBlock).getByRole('button', { name: /Defaults/ }));
  return screen.findByRole('dialog', { name: /PHP .* defaults/ });
}

describe('Runtimes PHP removal', () => {
  it('opens migration for an in-use PHP build without a failed removal', async () => {
    mockApi.listSites.mockResolvedValue([PHP_SITE]);

    render(Runtimes);
    const row = (await screen.findByRole('heading', { name: 'PHP 8.4.4' })).closest('article');
    expect(row).not.toBeNull();
    await fireEvent.click(within(row as HTMLElement).getByRole('button', { name: 'Migrate' }));

    const dialog = await screen.findByRole('dialog', { name: /Migrate sites off PHP 8.4.4/ });
    expect(within(dialog).getByText('blog')).toBeTruthy();
    const select = within(dialog).getByLabelText('Target build') as HTMLSelectElement;
    expect(select.value).toBe(BUILD_B.id);
    expect(mockApi.removeRuntime).not.toHaveBeenCalled();
  });

  it('lists the affected sites, migrates to the selected build, then retries removal', async () => {
    mockApi.removeRuntime
      .mockRejectedValueOnce(
        new ApiError('PHP_VERSION_IN_USE', 'PHP 8.4.4 is used by blog, shop.', 409, {
          build: BUILD_A.id,
          sites: ['blog', 'shop'],
        }),
      )
      .mockResolvedValueOnce(undefined);

    render(Runtimes);
    const dialog = await openMigrationDialog();

    expect(within(dialog).getByText('blog')).toBeTruthy();
    expect(within(dialog).getByText('shop')).toBeTruthy();

    const select = within(dialog).getByLabelText('Target build') as HTMLSelectElement;
    expect(select.value).toBe(BUILD_B.id);

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Migrate and remove' }));

    await waitFor(() => {
      expect(mockApi.migratePHP).toHaveBeenCalledWith({
        from_version: '8.4',
        to_version: '8.4',
        to_build: BUILD_B.id,
      });
    });
    await waitFor(() => expect(mockApi.removeRuntime).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Migrate sites off/ })).toBeNull());
  });

  it('lists every site on the target line, not only the 409 build', async () => {
    const shop: Site = {
      ...PHP_SITE,
      id: 's_shop',
      name: 'shop',
      domain: 'shop.test',
      php_build: 'php@8.4.4-r2',
      fastcgi_port: 43002,
    };
    const api: Site = {
      ...PHP_SITE,
      id: 's_api',
      name: 'api',
      domain: 'api.test',
      php_version: '8.5',
      php_build: BUILD_C.id,
      fastcgi_port: 43003,
    };
    mockApi.listSites.mockResolvedValue([shop, api]);
    mockApi.listRuntimes.mockResolvedValue([BUILD_A, BUILD_B, BUILD_C]);
    mockApi.removeRuntime.mockRejectedValueOnce(
      new ApiError('PHP_VERSION_IN_USE', 'PHP 8.4.4 is used by blog.', 409, {
        build: BUILD_A.id,
        sites: ['blog'],
      }),
    );

    render(Runtimes);
    const dialog = await openMigrationDialog();

    const listed = within(dialog).getByLabelText('Sites to move (all sites on PHP 8.4)');
    expect(within(listed).getByText('blog')).toBeTruthy();
    expect(within(listed).getByText('shop')).toBeTruthy();
    expect(within(listed).queryByText('api')).toBeNull();
    expect(within(dialog).getByText(/every site listed/i)).toBeTruthy();

    const select = within(dialog).getByLabelText('Target build') as HTMLSelectElement;
    expect(select.value).toBe(BUILD_B.id);
  });

  it('keeps the build installed and reports per-site errors when migration fails', async () => {
    mockApi.removeRuntime.mockRejectedValueOnce(
      new ApiError('PHP_VERSION_IN_USE', 'PHP 8.4.4 is used by blog, shop.', 409, {
        sites: ['blog', 'shop'],
      }),
    );
    mockApi.migratePHP.mockResolvedValue([
      { site: 'blog', ok: true },
      { site: 'shop', ok: false, error: 'php.ini failed validation on line 4' },
    ]);

    render(Runtimes);
    const dialog = await openMigrationDialog();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Migrate and remove' }));

    expect(await within(dialog).findByText(/php.ini failed validation on line 4/)).toBeTruthy();
    const affectedSites = within(dialog).getByLabelText('Sites to move (all sites on PHP 8.4)');
    expect(within(affectedSites).getByText('shop')).toBeTruthy();
    expect(mockApi.removeRuntime).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('dialog', { name: /Migrate sites off PHP 8.4.4/ })).toBeTruthy();
  });

  it('opens the per-line defaults drawer and saves after confirmation', async () => {
    mockApi.listSites.mockResolvedValue([PHP_SITE]);
    mockApi.getPHPDefaults.mockResolvedValue({
      version: '8.4',
      ini: { memory_limit: '256M' },
      extensions: { opcache: true, xdebug: false },
    });
    mockApi.phpExtensions.mockResolvedValue([
      {
        name: 'opcache',
        type: 'zend_extension',
        so: 'opcache.so',
        default: true,
        standard: true,
        versions: ['8.4'],
      },
      {
        name: 'xdebug',
        type: 'zend_extension',
        so: 'xdebug.so',
        default: false,
        standard: false,
        versions: ['8.4'],
      },
    ]);
    mockApi.savePHPDefaults.mockResolvedValue({
      defaults: {
        version: '8.4',
        ini: { memory_limit: '512M' },
        extensions: { opcache: true, xdebug: true },
      },
      sites: [{ site: 'blog', ok: true }],
    });

    render(Runtimes);
    const row = (await screen.findByRole('heading', { name: 'PHP 8.4.4' })).closest('article');
    const lineBlock = row?.closest('.line-block') as HTMLElement;
    await fireEvent.click(within(lineBlock).getByRole('button', { name: /Defaults/ }));

    const dialog = await screen.findByRole('dialog', { name: 'PHP 8.4 defaults' });
    const memory = (await within(dialog).findByLabelText(
      'Default directive 1 value',
    )) as HTMLInputElement;
    expect(memory.value).toBe('256M');

    await fireEvent.input(memory, { target: { value: '512M' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Enable xdebug' }));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save defaults' }));

    expect(await within(dialog).findByText(/Save and apply these defaults/)).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save and apply' }));

    await waitFor(() => {
      expect(mockApi.savePHPDefaults).toHaveBeenCalledWith('8.4', {
        ini: { memory_limit: '512M' },
        extensions: { opcache: true, xdebug: true },
      });
    });
    expect(await within(dialog).findByText('Defaults saved for PHP 8.4.')).toBeTruthy();
    expect(within(dialog).getByText('Applied')).toBeTruthy();
  });

  it('disables saving when the initial defaults load fails', async () => {
    mockApi.getPHPDefaults.mockRejectedValue(new ApiError('INTERNAL', 'defaults unavailable', 500));
    mockApi.phpExtensions.mockResolvedValue(PHP_EXTENSIONS);

    render(Runtimes);
    const dialog = await openDefaultsDrawer();

    expect(await within(dialog).findByText('defaults unavailable')).toBeTruthy();
    expect(within(dialog).queryByLabelText('Default directive 1 value')).toBeNull();
    expect(within(dialog).getByRole('button', { name: 'Retry' })).toBeTruthy();
    const save = within(dialog).getByRole('button', { name: 'Save defaults' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    await fireEvent.click(save);
    expect(mockApi.savePHPDefaults).not.toHaveBeenCalled();
  });

  it('does not save the previous line defaults after a failed version switch', async () => {
    mockApi.listRuntimes.mockResolvedValue([BUILD_A, BUILD_B, BUILD_C]);
    mockApi.getPHPDefaults.mockImplementation((version: string) =>
      version === '8.4'
        ? Promise.resolve(DEFAULTS_84)
        : Promise.reject(new ApiError('INTERNAL', '8.5 defaults unavailable', 500)),
    );
    mockApi.phpExtensions.mockResolvedValue(PHP_EXTENSIONS);

    render(Runtimes);
    const dialog = await openDefaultsDrawer();
    const memory = (await within(dialog).findByLabelText(
      'Default directive 1 value',
    )) as HTMLInputElement;
    expect(memory.value).toBe('256M');

    const select = within(dialog).getByLabelText('PHP line') as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: '8.5' } });

    expect(await within(dialog).findByText('8.5 defaults unavailable')).toBeTruthy();
    expect(within(dialog).queryByLabelText('Default directive 1 value')).toBeNull();
    const save = within(dialog).getByRole('button', { name: 'Save defaults' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    await fireEvent.click(save);
    expect(mockApi.savePHPDefaults).not.toHaveBeenCalled();
  });

  it('retries the selected line and enables saving after a load failure', async () => {
    mockApi.getPHPDefaults
      .mockRejectedValueOnce(new ApiError('INTERNAL', 'defaults unavailable', 500))
      .mockResolvedValue(DEFAULTS_84);
    mockApi.phpExtensions.mockResolvedValue(PHP_EXTENSIONS);

    render(Runtimes);
    const dialog = await openDefaultsDrawer();
    await within(dialog).findByText('defaults unavailable');

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));

    const memory = (await within(dialog).findByLabelText(
      'Default directive 1 value',
    )) as HTMLInputElement;
    expect(memory.value).toBe('256M');
    const save = within(dialog).getByRole('button', { name: 'Save defaults' }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    expect(mockApi.getPHPDefaults).toHaveBeenCalledTimes(2);
  });

  it('shows PHP_CONFIG_INVALID details when saving defaults is rejected', async () => {
    mockApi.getPHPDefaults.mockResolvedValue(DEFAULTS_84);
    mockApi.phpExtensions.mockResolvedValue(PHP_EXTENSIONS);
    mockApi.savePHPDefaults.mockRejectedValue(
      new ApiError('PHP_CONFIG_INVALID', 'bad defaults', 422, {
        line: 4,
        field: 'ini',
        output: 'syntax error',
      }),
    );

    render(Runtimes);
    const dialog = await openDefaultsDrawer();
    await within(dialog).findByLabelText('Default directive 1 value');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save defaults' }));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save and apply' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toContain('PHP_CONFIG_INVALID');
    expect(alert.textContent).toContain('bad defaults');
    expect(alert.textContent).toContain('Line 4');
    expect(alert.textContent).toContain('Field');
    expect(alert.textContent).toContain('ini');
    expect(alert.textContent).toContain('syntax error');
  });

  it('does not render Line 0 for line-less save errors', async () => {
    mockApi.getPHPDefaults.mockResolvedValue(DEFAULTS_84);
    mockApi.phpExtensions.mockResolvedValue(PHP_EXTENSIONS);
    mockApi.savePHPDefaults.mockRejectedValue(
      new ApiError('PHP_CONFIG_INVALID', 'bad defaults', 422, {
        line: 0,
        output: 'syntax error',
      }),
    );

    render(Runtimes);
    const dialog = await openDefaultsDrawer();
    await within(dialog).findByLabelText('Default directive 1 value');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save defaults' }));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save and apply' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toContain('PHP_CONFIG_INVALID');
    expect(alert.textContent).not.toContain('Line 0');
  });

  it('disables remove for a PHP build pinned by a site', async () => {
    mockApi.listSites.mockResolvedValue([PHP_SITE]);
    render(Runtimes);

    const row = (await screen.findByRole('heading', { name: 'PHP 8.4.4' })).closest('article');
    const button = within(row as HTMLElement).getByRole('button', { name: /Remove/ }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(button.getAttribute('title')).toContain('blog');
    expect(within(row as HTMLElement).getByText(/Used by blog/)).toBeTruthy();
  });
});

describe('Runtimes Node discovery', () => {
  it('shows only the newest patch per major by default', async () => {
    mockApi.listRuntimes.mockResolvedValue([BUILD_A, BUILD_B, NODE_24_BUILD]);
    render(Runtimes);

    const node24 = (await screen.findByRole('heading', { name: 'Node 24' })).closest(
      'article',
    ) as HTMLElement;
    const node23 = screen.getByRole('heading', { name: 'Node 23' }).closest(
      'article',
    ) as HTMLElement;
    const node22 = screen.getByRole('heading', { name: 'Node 22' }).closest(
      'article',
    ) as HTMLElement;

    expect(within(node24).getAllByText(/24\.9\.0/).length).toBeGreaterThan(0);
    expect(within(node24).queryByText(/24\.8\.1/)).toBeNull();
    expect(within(node24).queryByText(/24\.1\.2/)).toBeNull();
    expect(within(node22).queryByText(/22\.19\.0/)).toBeNull();
    expect(within(node24).getByText('LTS Krypton')).toBeTruthy();
    expect(within(node22).getByText('Security')).toBeTruthy();
    expect(within(node24).getByText('Installed')).toBeTruthy();
    expect(within(node24).getByText(/released Oct 15, 2026/)).toBeTruthy();
    expect(within(node23).getByText(/Non-LTS/)).toBeTruthy();
    expect(screen.getByLabelText('3 Node major release lines').textContent).toBe('3');
    expect(mockApi.listNodeReleases).toHaveBeenCalledWith();
  });

  it('expands a major to show every stable release', async () => {
    render(Runtimes);

    const node24 = (await screen.findByRole('heading', { name: 'Node 24' })).closest(
      'article',
    ) as HTMLElement;
    expect(within(node24).queryByText(/24\.8\.1/)).toBeNull();
    const toggle = within(node24).getByRole('button', {
      name: 'Show all Node 24 versions',
    });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(toggle.getAttribute('aria-controls')).toBeNull();

    await fireEvent.click(toggle);

    expect(await within(node24).findByText(/24\.8\.1/)).toBeTruthy();
    expect(within(node24).getByText(/24\.8\.0/)).toBeTruthy();
    expect(within(node24).getByText(/24\.1\.2/)).toBeTruthy();
    expect(within(node24).getByText('Node 24.8')).toBeTruthy();
    expect(within(node24).getByText('Node 24.1')).toBeTruthy();
    const hide = within(node24).getByRole('button', { name: 'Hide Node 24 versions' });
    expect(hide.getAttribute('aria-expanded')).toBe('true');
    expect(hide.getAttribute('aria-controls')).toBe('node-major-24-older');
    expect(screen.queryByText(/22\.19\.0/)).toBeNull();
  });

  it('lazily loads checksum and size in the install dialog', async () => {
    render(Runtimes);

    const node24 = (await screen.findByRole('heading', { name: 'Node 24' })).closest(
      'article',
    ) as HTMLElement;
    expect(mockApi.getNodeRelease).not.toHaveBeenCalled();

    await fireEvent.click(
      within(node24).getByRole('button', { name: 'Install Node 24.9.0' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: /Install Node 24\.9\.0/,
    });

    await waitFor(() => expect(mockApi.getNodeRelease).toHaveBeenCalledWith('24.9.0'));
    expect(await within(dialog).findByText('49.5 MB')).toBeTruthy();
    expect(within(dialog).getByText('npm 11.6.0')).toBeTruthy();
    expect(within(dialog).getByText(/9a4f7c02e1d8b63a…/)).toBeTruthy();

    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Install Node 24.9.0' }),
    );
    await waitFor(() =>
      expect(mockApi.installRuntime).toHaveBeenCalledWith(NODE_24_PREFLIGHT.entry.id),
    );
  });

  it('warns about older patch releases before installing', async () => {
    mockApi.getNodeRelease.mockResolvedValue(NODE_22_PREFLIGHT);
    render(Runtimes);

    const node22 = (await screen.findByRole('heading', { name: 'Node 22' })).closest(
      'article',
    ) as HTMLElement;
    await fireEvent.click(
      within(node22).getByRole('button', { name: 'Show all Node 22 versions' }),
    );
    await fireEvent.click(
      await within(node22).findByRole('button', { name: 'Install Node 22.19.0' }),
    );

    const dialog = await screen.findByRole('dialog', {
      name: /Install Node 22\.19\.0/,
    });
    expect(await within(dialog).findByText(/older patch release/i)).toBeTruthy();
    expect(within(dialog).getByText(/newest patch in Node 22 is 22\.20\.0/i)).toBeTruthy();
    expect(within(dialog).getByText(/security fixes/i)).toBeTruthy();
  });

  it('shows stale/offline refresh state', async () => {
    const cachedAt = new Date(Date.now() - 5 * 60 * 60 * 1000).toISOString();
    mockApi.listNodeReleases.mockResolvedValue({
      ...NODE_RELEASES,
      source: 'cache',
      stale: true,
      cached_at: cachedAt,
      warning: 'nodejs.org is unreachable. Using the cached index.',
    });
    mockApi.refreshNodeReleases.mockResolvedValue({
      ...NODE_RELEASES,
      source: 'embedded',
      stale: true,
      cached_at: cachedAt,
      warning: 'Offline: serving the embedded Node catalog.',
    });
    render(Runtimes);

    expect(await screen.findByText('Cached')).toBeTruthy();
    expect(screen.getByText('Stale')).toBeTruthy();
    expect(screen.getByText(/Cache age 5 hours ago/)).toBeTruthy();
    expect(screen.getByText(/nodejs\.org is unreachable/)).toBeTruthy();

    await fireEvent.click(
      screen.getByRole('button', { name: 'Refresh available versions' }),
    );

    await waitFor(() => expect(mockApi.refreshNodeReleases).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('Offline catalog')).toBeTruthy();
    expect(screen.getByText(/embedded Node catalog/)).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Node 24' })).toBeTruthy();
  });

  it('keeps discovered Node choices when refresh fails', async () => {
    mockApi.refreshNodeReleases.mockRejectedValue(
      new ApiError('NETWORK_ERROR', 'nodejs.org is unreachable', 0),
    );
    render(Runtimes);

    await screen.findByRole('heading', { name: 'Node 24' });
    await fireEvent.click(
      screen.getByRole('button', { name: 'Refresh available versions' }),
    );

    expect(await screen.findByText('Could not refresh Node releases')).toBeTruthy();
    expect(screen.getByText(/NETWORK_ERROR/)).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Node 24' })).toBeTruthy();
  });
});
