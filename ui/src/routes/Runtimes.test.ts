import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Runtimes from './Runtimes.svelte';
import * as api from '../lib/api';
import { ApiError } from '../lib/errors';
import type { RuntimeBuild, Site } from '../lib/types';

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
  spa_fallback: false,
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
    const affectedSites = within(dialog).getByLabelText('Sites using the build');
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
