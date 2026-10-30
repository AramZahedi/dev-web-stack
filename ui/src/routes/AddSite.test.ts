import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import AddSite from './AddSite.svelte';
import * as api from '../lib/api';
import * as db from '../lib/databaseApi';
import { ApiError } from '../lib/errors';
import { clearToasts, toasts } from '../lib/stores';
import type { Database, DatabaseLink, DatabaseUser } from '../lib/databaseTypes';
import type {
  NodeRelease,
  NodeReleasePreflight,
  NodeReleaseSnapshot,
  RuntimeBuild,
  ServiceView,
  Settings,
} from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  listRuntimes: vi.fn(),
  getSettings: vi.fn(),
  getNodeDefault: vi.fn(),
  listNodeReleases: vi.fn(),
  getNodeRelease: vi.fn(),
  installRuntime: vi.fn(),
  createSite: vi.fn(),
  listServices: vi.fn(),
}));

vi.mock('../lib/databaseApi', () => ({
  listServiceDatabases: vi.fn(),
  listServiceUsers: vi.fn(),
  listSiteDatabases: vi.fn(),
  linkSiteDatabase: vi.fn(),
  unlinkSiteDatabase: vi.fn(),
  createSiteDatabase: vi.fn(),
  revealServiceUserPassword: vi.fn(),
  setServiceUserPassword: vi.fn(),
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

const NODE_24_8_1_BUILD: RuntimeBuild = {
  id: 'node@24.8.1',
  kind: 'node',
  version: '24.8.1',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/24.8.1',
  checksum: 'c'.repeat(64),
  installed_at: '2026-10-30T10:00:00Z',
};

function nodeRelease(version: string, npm = '11.6.0'): NodeRelease {
  return {
    version,
    date: '2026-10-15',
    lts: 'Krypton',
    security: false,
    npm,
    modules: '137',
    file: 'linux-x64',
  };
}

const NODE_24_8_1_PREFLIGHT: NodeReleasePreflight = {
  entry: {
    id: 'node@24.8.1',
    kind: 'node',
    version: '24.8.1',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v24.8.1/node-v24.8.1-linux-x64.tar.xz',
    sha256: 'c'.repeat(64),
    size: 52_000_000,
  },
  release: nodeRelease('24.8.1', '11.5.2'),
};

const NODE_RELEASES: NodeReleaseSnapshot = {
  source: 'cache',
  cached_at: '2026-10-30T09:00:00Z',
  stale: false,
  majors: [
    {
      major: 24,
      latest: nodeRelease('24.9.0'),
      releases: [nodeRelease('24.9.0'), nodeRelease('24.8.1', '11.5.2')],
    },
  ],
  all: [nodeRelease('24.9.0'), nodeRelease('24.8.1', '11.5.2')],
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

const MYSQL_SERVICE: ServiceView = {
  id: 'mysql',
  display_name: 'MySQL 8.4',
  group: 'mysql-family',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'container_mysql',
  image: 'mysql:8.4',
  ports: [{ host: 3306, container: 3306, protocol: 'tcp', loopback: true }],
};

const APP_DATABASE: Database = {
  name: 'app',
  service_id: 'mysql',
  engine: 'mysql',
  size_bytes: 1024,
  table_count: 1,
  linked_sites: [],
  managed: true,
};

const BLOG_DATABASE: Database = {
  ...APP_DATABASE,
  name: 'blog',
  size_bytes: 2048,
  table_count: 2,
};

const MISSING_DATABASE: Database = {
  ...BLOG_DATABASE,
  name: 'missing_db',
  missing: true,
};

const DEVSTACK_DATABASE: Database = {
  ...BLOG_DATABASE,
  name: 'devstack',
  managed: false,
};

const EXTERNAL_USER: DatabaseUser = {
  id: 'usr_ext',
  service_id: 'mysql',
  engine: 'mysql',
  username: 'ext',
  host: '%',
  enabled: true,
  managed: false,
  password_managed: false,
  grants: [],
  linked_sites: [],
};

const MANAGED_UNKNOWN_USER: DatabaseUser = {
  ...EXTERNAL_USER,
  id: 'usr_managed',
  username: 'managed_user',
  managed: true,
  password_managed: false,
};

const DISABLED_USER: DatabaseUser = {
  ...EXTERNAL_USER,
  id: 'usr_disabled',
  username: 'disabled',
  enabled: false,
};

const ROOT_USER: DatabaseUser = {
  ...EXTERNAL_USER,
  id: 'usr_root',
  username: 'root',
  enabled: true,
};

const SITE_DB_LINK: DatabaseLink = {
  id: 'lnk_1',
  site_id: 's_new',
  service_id: 'mysql',
  engine: 'mysql',
  database: 'app',
  user_id: 'usr_app',
  username: 'blog_user',
  host: '%',
  primary: true,
  password_managed: true,
  available: true,
  connection: {
    engine: 'mysql',
    host: '127.0.0.1',
    port: 3306,
    database: 'app',
    username: 'blog_user',
    password_managed: true,
    url: 'mysql://blog_user@127.0.0.1:3306/app',
    env: {
      DB_CONNECTION: 'mysql',
      DB_HOST: '127.0.0.1',
      DB_PORT: '3306',
      DB_DATABASE: 'app',
      DB_USERNAME: 'blog_user',
    },
  },
};

const BLOG_DB_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  id: 'lnk_blog',
  database: 'blog',
  connection: {
    ...SITE_DB_LINK.connection,
    database: 'blog',
    url: 'mysql://blog_user@127.0.0.1:3306/blog',
    env: {
      ...SITE_DB_LINK.connection.env,
      DB_DATABASE: 'blog',
    },
  },
};

const OTHER_DB_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  id: 'lnk_other',
  database: 'other',
  user_id: 'usr_other',
  username: 'other_user',
  connection: {
    ...SITE_DB_LINK.connection,
    database: 'other',
    username: 'other_user',
    url: 'mysql://other_user@127.0.0.1:3306/other',
    env: {
      ...SITE_DB_LINK.connection.env,
      DB_DATABASE: 'other',
      DB_USERNAME: 'other_user',
    },
  },
};

const MANAGED_UNKNOWN_DB_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  id: 'lnk_managed_unknown',
  user_id: 'usr_managed',
  username: 'managed_user',
  password_managed: false,
  connection: {
    ...SITE_DB_LINK.connection,
    username: 'managed_user',
    password_managed: false,
    url: 'mysql://managed_user@127.0.0.1:3306/app',
    env: {
      ...SITE_DB_LINK.connection.env,
      DB_USERNAME: 'managed_user',
    },
  },
};

const mockApi = vi.mocked(api);
const mockDb = vi.mocked(db);

beforeEach(() => {
  mockApi.listRuntimes.mockResolvedValue([NODE_BUILD, PHP_BUILD]);
  mockApi.getSettings.mockResolvedValue(SETTINGS);
  mockApi.getNodeDefault.mockResolvedValue({
    build: NODE_BUILD,
    shim_dir: '/home/dev/.devstack/tools/node-default/bin',
    shim_installed: true,
    source: 'newest',
  });
  mockApi.listNodeReleases.mockResolvedValue(NODE_RELEASES);
  mockApi.listServices.mockResolvedValue([MYSQL_SERVICE]);
  mockDb.listServiceDatabases.mockResolvedValue({ databases: [] });
  mockDb.listServiceUsers.mockResolvedValue({ users: [] });
  mockDb.listSiteDatabases.mockResolvedValue({ links: [] });
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

async function openOptionsStep(): Promise<void> {
  render(AddSite);
  await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
  await fillProject();
  await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
  await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
  expect(await screen.findByText('Domain and options')).toBeTruthy();
}

async function enableDatabase(database = 'blog', username = 'blog_user'): Promise<void> {
  await fireEvent.click(screen.getByLabelText('Create or link a database'));
  const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
  await waitFor(() => expect(service.value).toBe('mysql'));
  await fireEvent.input(screen.getByLabelText(/Database name/), { target: { value: database } });
  await fireEvent.input(screen.getByLabelText(/Database username/), {
    target: { value: username },
  });
}

async function openNodeReleaseList(): Promise<void> {
  render(AddSite);
  await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
  await fillProject();
  await fireEvent.click(screen.getByRole('radio', { name: /Node/ }));
  await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
  await fireEvent.click(
    await screen.findByRole('button', { name: 'Install another version' }),
  );
  await fireEvent.click(
    await screen.findByRole('button', { name: 'Show all Node 24 versions' }),
  );
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

  it('keeps windows source mode when a windows path is entered before choosing node', async () => {
    mockApi.getSettings.mockResolvedValue({
      ...SETTINGS,
      home: 'C:\\Users\\Aram\\.devstack',
      user_home: 'C:\\Users\\Aram',
    });

    render(AddSite);
    await waitFor(() => expect(mockApi.getSettings).toHaveBeenCalled());
    await fireEvent.input(screen.getByLabelText(/Site name/), { target: { value: 'demo' } });
    await fireEvent.input(screen.getByLabelText(/Project folder/), {
      target: { value: 'C:\\Users\\Aram\\projects\\demo' },
    });
    await fireEvent.click(screen.getByRole('radio', { name: /Node/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    expect(await screen.findByLabelText(/Node version/)).toBeTruthy();
    expect(screen.queryByText(/WSL source mode requires/)).toBeNull();
    expect(screen.queryByText(/Windows source mode requires/)).toBeNull();
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

  it('shows an unversioned inherit label when the configured default is not installed', async () => {
    mockApi.getNodeDefault.mockResolvedValue({
      configured: '22.11.0',
      build: undefined,
      shim_dir: '/home/dev/.devstack/tools/node-default/bin',
      shim_installed: false,
      source: 'setting',
    });

    render(AddSite);
    await waitFor(() => expect(mockApi.getNodeDefault).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const picker = (await screen.findByLabelText(/Node toolchain/)) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(screen.getByRole('option', { name: 'Inherit global default' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Inherit global default \(v/ })).toBeNull();
    expect(
      screen.getByText(
        /configured global Node default \(v22\.11\.0\) is not installed/,
      ),
    ).toBeTruthy();
  });

  it('omits the inherit version without a warning when the default request fails', async () => {
    mockApi.getNodeDefault.mockRejectedValue(
      new ApiError('NOT_FOUND', 'Node default is not available.', 404),
    );

    render(AddSite);
    await waitFor(() => expect(mockApi.getNodeDefault).toHaveBeenCalled());
    await fillProject();
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const picker = (await screen.findByLabelText(/Node toolchain/)) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(screen.getByRole('option', { name: 'Inherit global default' })).toBeTruthy();
    expect(screen.queryByText(/configured global Node default/)).toBeNull();
  });

  it('preflights and installs the exact selected build, then refreshes runtimes', async () => {
    mockApi.listRuntimes
      .mockResolvedValueOnce([NODE_BUILD])
      .mockResolvedValueOnce([NODE_BUILD, NODE_24_8_1_BUILD]);
    mockApi.getNodeRelease.mockResolvedValue(NODE_24_8_1_PREFLIGHT);
    mockApi.installRuntime.mockResolvedValue(NODE_24_8_1_BUILD);

    await openNodeReleaseList();
    await fireEvent.click(screen.getByRole('button', { name: 'Install 24.8.1' }));

    await waitFor(() => expect(mockApi.getNodeRelease).toHaveBeenCalledWith('24.8.1'));
    expect(mockApi.installRuntime).toHaveBeenCalledWith('node@24.8.1');
    await waitFor(() =>
      expect(mockApi.listRuntimes.mock.calls.length).toBeGreaterThanOrEqual(2),
    );

    const picker = screen.getByLabelText(/Node version/) as HTMLSelectElement;
    await waitFor(() => expect(picker.value).toBe('24.8.1'));
    expect(screen.getByRole('option', { name: /Node 24\.8\.1/ })).toBeTruthy();
  });

  it('shows the preflight error and does not install a missing build', async () => {
    mockApi.getNodeRelease.mockRejectedValue(
      new ApiError(
        'NODE_RELEASE_UNAVAILABLE',
        'Node release metadata is unavailable.',
        503,
      ),
    );

    await openNodeReleaseList();
    await fireEvent.click(screen.getByRole('button', { name: 'Install 24.8.1' }));

    expect(
      await screen.findByText('Node release metadata is unavailable.'),
    ).toBeTruthy();
    expect(mockApi.installRuntime).not.toHaveBeenCalled();
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

  it('keeps the database selection when navigating back to Options', async () => {
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');

    await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));

    const toggle = screen.getByLabelText('Create or link a database');
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    const databaseName = (await screen.findByLabelText(/Database name/)) as HTMLInputElement;
    const username = (await screen.findByLabelText(/Database username/)) as HTMLInputElement;
    expect(databaseName.value).toBe('blog');
    expect(username.value).toBe('blog_user');
  });

  it('defaults database identifiers to safe engine names', async () => {
    render(AddSite);
    await waitFor(() => expect(mockApi.listRuntimes).toHaveBeenCalled());
    await fireEvent.input(screen.getByLabelText(/Site name/), { target: { value: 'My Blog' } });
    await fireEvent.input(screen.getByLabelText(/Project folder/), {
      target: { value: '/home/dev/projects/my-blog' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await fireEvent.click(screen.getByLabelText('Create or link a database'));

    const databaseName = (await screen.findByLabelText(/Database name/)) as HTMLInputElement;
    const username = (await screen.findByLabelText(/Database username/)) as HTMLInputElement;
    expect(databaseName.value).toBe('my_blog');
    expect(username.value).toBe('my_blog');
  });

  it('rejects an unsafe new database name before submit', async () => {
    await openOptionsStep();
    await enableDatabase('blog-db', 'blog_user');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    expect(
      (await screen.findAllByText(/Use letters, numbers and underscores/)).length,
    ).toBeGreaterThan(0);
    expect(mockApi.createSite).not.toHaveBeenCalled();
  });

  it('rejects protected new database and user names before submit', async () => {
    await openOptionsStep();
    await enableDatabase('devstack', 'root');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    expect(
      (await screen.findAllByText('That database name is reserved.')).length,
    ).toBeGreaterThan(0);
    expect(
      (await screen.findAllByText('That username is reserved.')).length,
    ).toBeGreaterThan(0);
    expect(mockApi.createSite).not.toHaveBeenCalled();
  });

  it('focuses the database picker from the review row', async () => {
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    const row = screen.getByText(/blog on MySQL 8\.4/).closest('.review-row') as HTMLElement;
    await fireEvent.click(within(row).getByRole('button', { name: 'Edit' }));
    await waitFor(() => expect(document.activeElement?.id).toBe('database-service'));
  });

  it('filters missing and protected existing databases and users', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({
      databases: [APP_DATABASE, BLOG_DATABASE, MISSING_DATABASE, DEVSTACK_DATABASE],
    });
    mockDb.listServiceUsers.mockResolvedValue({
      users: [EXTERNAL_USER, DISABLED_USER, ROOT_USER],
    });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Use existing database'));
    const database = (await screen.findByLabelText(/Existing database/)) as HTMLSelectElement;
    await waitFor(() => expect(database.value).toBe('app'));
    const databaseOptions = Array.from(database.options).map((option) => option.value);
    expect(databaseOptions).toContain('app');
    expect(databaseOptions).toContain('blog');
    expect(databaseOptions).not.toContain('missing_db');
    expect(databaseOptions).not.toContain('devstack');

    await fireEvent.click(screen.getByLabelText('Use existing user'));
    const user = (await screen.findByLabelText(/Existing user/)) as HTMLSelectElement;
    await waitFor(() => expect(user.value).toBe('usr_ext'));
    const userOptions = Array.from(user.options).map((option) => option.value);
    expect(userOptions).toContain('usr_ext');
    expect(userOptions).not.toContain('usr_disabled');
    expect(userOptions).not.toContain('usr_root');
  });

  it('continues without a database when services fail to load', async () => {
    mockApi.listServices.mockRejectedValue(
      new ApiError('SERVICE_LIST_FAILED', 'Services unavailable.', 503),
    );
    await openOptionsStep();

    expect(await screen.findByText('Could not load database services')).toBeTruthy();
    expect(
      screen.getByText(/site creation can continue without a database/i),
    ).toBeTruthy();
    const toggle = screen.getByLabelText(
      'Create or link a database',
    ) as HTMLButtonElement;
    expect(toggle.disabled).toBe(true);

    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.not.objectContaining({ database: expect.anything() }),
    );
  });

  it('does not block site creation when no SQL service is active', async () => {
    mockApi.listServices.mockResolvedValue([]);
    await openOptionsStep();

    const toggle = screen.getByLabelText('Create or link a database') as HTMLButtonElement;
    await waitFor(() => expect(toggle.disabled).toBe(true));
    expect(screen.getByText(/no active SQL service/i)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Services' })).toBeTruthy();
    expect(screen.getByText(/no database will be linked/i)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.not.objectContaining({ database: expect.anything() }),
    );
  });

  it('adds an optional database panel in the Options step', async () => {
    await openOptionsStep();
    expect(await screen.findByText('Database')).toBeTruthy();
    const toggle = screen.getByLabelText('Create or link a database');
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(screen.getByText(/no database will be linked/i)).toBeTruthy();
  });

  it('builds a new database and new user selection', async () => {
    mockApi.listServices.mockResolvedValue([MYSQL_SERVICE]);
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [] });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Create new database'));
    await fireEvent.input(screen.getByLabelText(/Database name/), { target: { value: 'blog' } });
    await fireEvent.click(screen.getByLabelText('Create new user'));
    await fireEvent.input(screen.getByLabelText(/Database username/), {
      target: { value: 'blog_user' },
    });
    expect(screen.getByText('Read/write + schema')).toBeTruthy();
  });

  it('omits the database payload when the panel is disabled', async () => {
    await openOptionsStep();
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.not.objectContaining({ database: expect.anything() }),
    );
  });

  it('sends the exact new database and user payload', async () => {
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({
        database: {
          service_id: 'mysql',
          new_database: { name: 'blog' },
          new_user: {
            username: 'blog_user',
            grants: [{ database: 'blog', preset: 'read_write_schema' }],
          },
          grant: 'read_write_schema',
          primary: true,
        },
      }),
    );
  });

  it('shows connection values but never a password in review', async () => {
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    expect(
      screen.getByText('Password will be generated when the site is created.'),
    ).toBeTruthy();
    expect(screen.getByText(/blog on MySQL 8\.4/)).toBeTruthy();
    expect(
      screen.getByText(/blog_user · Read\/write \+ schema · 127\.0\.0\.1:3306/),
    ).toBeTruthy();
    expect(screen.queryByText('s3cret')).toBeNull();
  });

  it('reveals the new password only after creation', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [BLOG_DB_LINK] });
    mockDb.revealServiceUserPassword.mockResolvedValue({ value: 'generated' });
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    expect(await screen.findByText('Database created')).toBeTruthy();
    expect(screen.queryByText('generated')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Reveal password' }));
    expect(await screen.findByText('generated')).toBeTruthy();
    expect(mockDb.revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('opens the created link that matches the requested database', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [OTHER_DB_LINK, BLOG_DB_LINK] });
    mockDb.revealServiceUserPassword.mockResolvedValue({ value: 'generated' });
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('blog')).toBeTruthy();
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Reveal password' }),
    );
    expect(await within(dialog).findByText('generated')).toBeTruthy();
    expect(mockDb.revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('reports a missing created database instead of opening another link', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [OTHER_DB_LINK] });
    await openOptionsStep();
    await enableDatabase('blog', 'blog_user');
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    await waitFor(() =>
      expect(
        get(toasts).some((toast) => toast.title === 'Could not load the created database'),
      ).toBe(true),
    );
    expect(screen.queryByText('Database created')).toBeNull();
  });

  it('warns about an existing user without access and never adds a grant', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [APP_DATABASE] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [EXTERNAL_USER] });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Use existing database'));
    await fireEvent.change(screen.getByLabelText(/Existing database/), {
      target: { value: 'app' },
    });
    await fireEvent.click(screen.getByLabelText('Use existing user'));
    await fireEvent.change(screen.getByLabelText(/Existing user/), {
      target: { value: 'usr_ext' },
    });

    expect(screen.getByText(/does not have access to app/i)).toBeTruthy();
    const manage = screen.getByRole('link', { name: 'Manage database user' });
    expect(manage.getAttribute('href')).toBe('#/services/mysql?tab=users&user=usr_ext');
    expect(screen.queryByLabelText(/Database password/)).toBeNull();
    expect(
      screen.getByText(/external password entry is not available in v1/i),
    ).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({
        database: {
          service_id: 'mysql',
          database: 'app',
          user_id: 'usr_ext',
          primary: true,
        },
      }),
    );
  });

  it('links a metadata-only existing user without a password', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [APP_DATABASE] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [EXTERNAL_USER] });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Use existing database'));
    await fireEvent.change(screen.getByLabelText(/Existing database/), {
      target: { value: 'app' },
    });
    await fireEvent.click(screen.getByLabelText('Use existing user'));
    await fireEvent.change(screen.getByLabelText(/Existing user/), {
      target: { value: 'usr_ext' },
    });

    expect(screen.getByText('Password not managed.')).toBeTruthy();
    expect(screen.queryByLabelText(/Database password/)).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));
    await waitFor(() => expect(mockApi.createSite).toHaveBeenCalledTimes(1));
    expect(mockApi.createSite).toHaveBeenCalledWith(
      expect.objectContaining({
        database: {
          service_id: 'mysql',
          database: 'app',
          user_id: 'usr_ext',
          primary: true,
        },
      }),
    );
  });

  it('shows an unknown password for a managed existing user in review', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [APP_DATABASE] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [MANAGED_UNKNOWN_USER] });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Use existing database'));
    await fireEvent.change(screen.getByLabelText(/Existing database/), {
      target: { value: 'app' },
    });
    await fireEvent.click(screen.getByLabelText('Use existing user'));
    await fireEvent.change(screen.getByLabelText(/Existing user/), {
      target: { value: 'usr_managed' },
    });

    expect(
      screen.getByText(
        "The password is unknown. Reset it after creation from the site's Databases card.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Password unknown. Reset it from the site's Databases card after creation.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText(/external password entry is not available in v1/i)).toBeNull();
  });

  it('shows managed-unknown copy in the post-create modal', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [APP_DATABASE] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [MANAGED_UNKNOWN_USER] });
    mockDb.listSiteDatabases.mockResolvedValue({ links: [MANAGED_UNKNOWN_DB_LINK] });
    await openOptionsStep();

    await fireEvent.click(screen.getByLabelText('Create or link a database'));
    const service = (await screen.findByLabelText(/Database service/)) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(screen.getByLabelText('Use existing database'));
    await fireEvent.change(screen.getByLabelText(/Existing database/), {
      target: { value: 'app' },
    });
    await fireEvent.click(screen.getByLabelText('Use existing user'));
    await fireEvent.change(screen.getByLabelText(/Existing user/), {
      target: { value: 'usr_managed' },
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Create site' }));

    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        "The database password is unknown. Reset it from the site's Databases card.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).queryByText(/external password entry is not available in v1/i),
    ).toBeNull();
    expect(within(dialog).queryByRole('button', { name: 'Reveal password' })).toBeNull();
  });
});
