import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { get } from 'svelte/store';
import SiteDetail from './SiteDetail.svelte';
import * as api from '../lib/api';
import * as db from '../lib/databaseApi';
import { ApiError } from '../lib/errors';
import { toasts } from '../lib/stores';
import type { Database, DatabaseLink, DatabaseUser } from '../lib/databaseTypes';
import type {
  NodeReleasePreflight,
  PHPInfo,
  RuntimeBuild,
  ServiceView,
  Site,
  UpdateSiteRequest,
} from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  getSite: vi.fn(),
  listRuntimes: vi.fn(),
  getNodeDefault: vi.fn(),
  getNodeRelease: vi.fn(),
  installRuntime: vi.fn(),
  getPHPInfo: vi.fn(),
  startSite: vi.fn(),
  stopSite: vi.fn(),
  restartSite: vi.fn(),
  applyPHP: vi.fn(),
  deleteSite: vi.fn(),
  getVhostStatus: vi.fn(),
  previewVhost: vi.fn(),
  updateSite: vi.fn(),
  changeSiteSource: vi.fn(),
  buildSite: vi.fn(),
  getBuildLogs: vi.fn(),
  openLogStream: vi.fn(() => () => undefined),
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

const NODE_22_11_BUILD: RuntimeBuild = {
  id: 'node@22.11.0',
  kind: 'node',
  version: '22.11.0',
  backend: 'local',
  path: '/home/dev/.devstack/runtimes/node/22.11.0',
  checksum: 'c'.repeat(64),
  installed_at: '2026-10-30T10:00:00Z',
};

const NODE_22_11_PREFLIGHT: NodeReleasePreflight = {
  entry: {
    id: 'node@22.11.0',
    kind: 'node',
    version: '22.11.0',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v22.11.0/node-v22.11.0-linux-x64.tar.xz',
    sha256: 'c'.repeat(64),
    size: 49_807_360,
  },
  release: {
    version: '22.11.0',
    date: '2025-10-28',
    lts: 'Jod',
    security: false,
    npm: '10.2.0',
    modules: '127',
    file: 'linux-x64',
  },
};

function staleNodeSite(overrides: Partial<Site> = {}): Site {
  return {
    ...STATIC_SITE,
    id: 's_shop',
    name: 'shop',
    domain: 'shop.test',
    kind: 'node',
    node_version: '22.11.0',
    node_toolchain: {
      version: '22.11.0',
      source: 'site',
      installed: false,
    },
    command: 'npm run dev',
    document_root: '',
    build: undefined,
    status: 'stopped',
    desired_state: 'stopped',
    http_port: 41002,
    ...overrides,
  };
}

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
  linked_sites: [{ id: 's_blog', name: 'blog' }],
  managed: true,
};

const APP_USER: DatabaseUser = {
  id: 'usr_app',
  service_id: 'mysql',
  engine: 'mysql',
  username: 'blog_user',
  host: '%',
  enabled: true,
  managed: true,
  password_managed: true,
  grants: [{ database: 'app', preset: 'read_write_schema' }],
  linked_sites: [{ id: 's_blog', name: 'blog' }],
};

const SITE_DB_LINK: DatabaseLink = {
  id: 'lnk_1',
  site_id: 's_blog',
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

const METADATA_ONLY_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  id: 'lnk_ext',
  user_id: 'usr_ext',
  username: 'ext',
  password_managed: false,
  connection: {
    ...SITE_DB_LINK.connection,
    username: 'ext',
    password_managed: false,
    url: 'mysql://ext@127.0.0.1:3306/app',
    env: {
      DB_CONNECTION: 'mysql',
      DB_HOST: '127.0.0.1',
      DB_PORT: '3306',
      DB_DATABASE: 'app',
      DB_USERNAME: 'ext',
    },
  },
};

const MANAGED_UNKNOWN_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  password_managed: false,
  connection: {
    ...SITE_DB_LINK.connection,
    password_managed: false,
  },
};

const MANAGED_UNKNOWN_USER: DatabaseUser = {
  ...APP_USER,
  password_managed: false,
};

const PASSWORD_ENV_LINK: DatabaseLink = {
  ...SITE_DB_LINK,
  connection: {
    ...SITE_DB_LINK.connection,
    env: {
      DB_CONNECTION: 'mysql',
      DB_HOST: '127.0.0.1',
      DB_PORT: '3306',
      DB_DATABASE: 'app',
      DB_USERNAME: 'blog_user',
      DB_APP_DATABASE_URL: 'mysql://blog_user:p@ss@127.0.0.1:3306/app',
      DB_APP_PWD: 's3cret',
      DB_APP_PASSWORD: 's3cret',
    },
  },
};

const BLOG_DATABASE: Database = {
  name: 'blog',
  service_id: 'mysql',
  engine: 'mysql',
  size_bytes: 2048,
  table_count: 2,
  linked_sites: [],
  managed: true,
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

const mockApi = vi.mocked(api);
const mockDb = vi.mocked(db);

beforeEach(() => {
  mockApi.getSite.mockResolvedValue({ ...STATIC_SITE });
  mockApi.listRuntimes.mockResolvedValue([NODE_BUILD, PHP_BUILD]);
  mockApi.getNodeDefault.mockResolvedValue({
    build: NODE_BUILD,
    shim_dir: '/home/dev/.devstack/tools/node-default/bin',
    shim_installed: true,
    source: 'newest',
  });
  mockApi.getPHPInfo.mockResolvedValue(PHP_INFO);
  mockApi.updateSite.mockImplementation(async (ref, body: UpdateSiteRequest) => ({
    ...STATIC_SITE,
    ...body,
    node_version: body.node_version ?? STATIC_SITE.node_version,
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
  mockApi.listServices.mockResolvedValue([MYSQL_SERVICE]);
  mockDb.listSiteDatabases.mockResolvedValue({ links: [SITE_DB_LINK] });
  mockDb.listServiceDatabases.mockResolvedValue({ databases: [APP_DATABASE] });
  mockDb.listServiceUsers.mockResolvedValue({ users: [APP_USER] });
  mockDb.revealServiceUserPassword.mockResolvedValue({ value: 'mock_db_password' });
  mockDb.setServiceUserPassword.mockResolvedValue({ value: 'mock_db_password' });
  mockDb.linkSiteDatabase.mockResolvedValue(SITE_DB_LINK);
  mockDb.createSiteDatabase.mockResolvedValue({
    link: SITE_DB_LINK,
    database: APP_DATABASE,
    user: APP_USER,
  });
});

afterEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

async function renderSite(name = 'blog'): Promise<void> {
  render(SiteDetail, { props: { params: { ref: name } } });
  await screen.findByRole('heading', { name });
}

describe('SiteDetail configuration and builds', () => {
  it('shows the resolved toolchain source on site detail', async () => {
    mockApi.getSite.mockResolvedValue({
      ...STATIC_SITE,
      node_version: '',
      node_toolchain: {
        version: '24.9.0',
        build_id: 'node@24.9.0',
        source: 'global',
        installed: true,
      },
    });
    await renderSite();

    expect(screen.getAllByText(/Inherited from the global default/).length).toBeGreaterThan(0);
    expect(screen.getByText(/node -v uses v24\.9\.0/)).toBeTruthy();
  });

  it('warns about the ABI rebuild when a stopped node site changes version', async () => {
    const nodeSite: Site = {
      ...STATIC_SITE,
      id: 's_shop',
      name: 'shop',
      domain: 'shop.test',
      kind: 'node',
      node_version: '22.20.0',
      node_toolchain: {
        version: '22.20.0',
        build_id: 'node-22.20.0',
        source: 'site',
        installed: true,
      },
      command: 'npm run dev',
      document_root: '',
      build: undefined,
      status: 'stopped',
      desired_state: 'stopped',
      http_port: 41002,
    };
    mockApi.getSite.mockResolvedValue(nodeSite);

    await renderSite('shop');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const picker = (await screen.findByLabelText(/Node version/)) as HTMLSelectElement;
    expect(picker.value).toBe('22.20.0');
    expect(screen.queryByRole('option', { name: /Inherit global default/ })).toBeNull();

    await fireEvent.change(picker, { target: { value: '24.9.0' } });

    expect(screen.getByText(/Native modules are compiled against the Node ABI/)).toBeTruthy();
    expect(screen.getByText(/switching from v22\.20\.0 to v24\.9\.0/)).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
    await waitFor(() =>
      expect(mockApi.updateSite).toHaveBeenCalledWith(
        's_shop',
        expect.objectContaining({ node_version: '24.9.0' }),
      ),
    );
  });

  it('shows an unversioned inherit option when the configured default is not installed', async () => {
    mockApi.getNodeDefault.mockResolvedValue({
      configured: '22.11.0',
      build: undefined,
      shim_dir: '/home/dev/.devstack/tools/node-default/bin',
      shim_installed: false,
      source: 'setting',
    });

    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

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

    await renderSite();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const picker = (await screen.findByLabelText(/Node toolchain/)) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(screen.getByRole('option', { name: 'Inherit global default' })).toBeTruthy();
    expect(screen.queryByText(/configured global Node default/)).toBeNull();
  });

  it('preflights and installs a stale pin, then selects the exact installed build', async () => {
    mockApi.getSite.mockResolvedValue(staleNodeSite());
    mockApi.listRuntimes
      .mockResolvedValueOnce([NODE_BUILD])
      .mockResolvedValueOnce([NODE_BUILD, NODE_22_11_BUILD]);
    mockApi.getNodeRelease.mockResolvedValue(NODE_22_11_PREFLIGHT);
    mockApi.installRuntime.mockResolvedValue(NODE_22_11_BUILD);

    await renderSite('shop');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const picker = (await screen.findByLabelText(/Node version/)) as HTMLSelectElement;
    expect(picker.value).toBe('22.11.0');
    await fireEvent.click(screen.getByRole('button', { name: 'Install Node 22.11.0' }));

    await waitFor(() => expect(mockApi.getNodeRelease).toHaveBeenCalledWith('22.11.0'));
    expect(mockApi.installRuntime).toHaveBeenCalledWith('node@22.11.0');
    await waitFor(() =>
      expect(mockApi.listRuntimes.mock.calls.length).toBeGreaterThanOrEqual(2),
    );
    await waitFor(() => expect(picker.value).toBe('22.11.0'));
    expect(screen.getByRole('option', { name: /Node 22\.11\.0/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Install Node 22.11.0' })).toBeNull();
  });

  it('keeps install-first enabled for a stale pin on a running node site', async () => {
    mockApi.getSite.mockResolvedValue(
      staleNodeSite({ status: 'running', desired_state: 'running' }),
    );
    mockApi.listRuntimes
      .mockResolvedValueOnce([NODE_BUILD])
      .mockResolvedValueOnce([NODE_BUILD, NODE_22_11_BUILD]);
    mockApi.getNodeRelease.mockResolvedValue(NODE_22_11_PREFLIGHT);
    mockApi.installRuntime.mockResolvedValue(NODE_22_11_BUILD);

    await renderSite('shop');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));

    const picker = (await screen.findByLabelText(/Node version/)) as HTMLSelectElement;
    expect(picker.disabled).toBe(true);
    const install = screen.getByRole('button', {
      name: 'Install Node 22.11.0',
    }) as HTMLButtonElement;
    expect(install.disabled).toBe(false);

    await fireEvent.click(install);
    await waitFor(() => expect(mockApi.installRuntime).toHaveBeenCalledWith('node@22.11.0'));
    await waitFor(() => expect(picker.value).toBe('22.11.0'));
    expect(screen.getByRole('option', { name: /Node 22\.11\.0/ })).toBeTruthy();
  });

  it('shows the preflight error for a stale pin and does not install', async () => {
    mockApi.getSite.mockResolvedValue(staleNodeSite());
    mockApi.getNodeRelease.mockRejectedValue(
      new ApiError(
        'NODE_RELEASE_UNAVAILABLE',
        'Node release metadata is unavailable.',
        503,
      ),
    );

    await renderSite('shop');
    await fireEvent.click(screen.getByRole('button', { name: 'Edit configuration' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Install Node 22.11.0' }));

    expect(
      await screen.findByText('Node release metadata is unavailable.'),
    ).toBeTruthy();
    expect(mockApi.installRuntime).not.toHaveBeenCalled();
  });

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
      node_version: body.node_version ?? PHP_SITE.node_version,
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

  it('renders source mode in overview card when set', async () => {
    mockApi.getSite.mockResolvedValue({ ...STATIC_SITE, source_mode: 'windows' });
    await renderSite();
    expect(await screen.findByText('Source mode')).toBeTruthy();
    expect(screen.getByText('Windows drive')).toBeTruthy();
  });

  it('copies a stopped site into the selected source mode', async () => {
    mockApi.getSite.mockResolvedValue({
      ...STATIC_SITE,
      source_mode: 'windows',
      status: 'stopped',
      desired_state: 'stopped',
    });
    mockApi.changeSiteSource.mockResolvedValue({
      ...STATIC_SITE,
      source_mode: 'wsl',
      status: 'stopped',
      desired_state: 'stopped',
    });
    await renderSite();

    const change = await screen.findByRole('button', { name: 'Change source' });
    expect((change as HTMLButtonElement).disabled).toBe(false);
    await fireEvent.click(change);

    const path = (await screen.findByLabelText(/Destination path/)) as HTMLInputElement;
    expect(path.value).toBe('/data/devstack/projects/blog');
    await fireEvent.click(screen.getByRole('button', { name: 'Copy and switch' }));

    await waitFor(() =>
      expect(mockApi.changeSiteSource).toHaveBeenCalledWith(
        's_blog',
        'wsl',
        '/data/devstack/projects/blog',
      ),
    );
  });

  it('requires a stopped site before changing source mode', async () => {
    mockApi.getSite.mockResolvedValue({ ...STATIC_SITE, source_mode: 'windows' });
    await renderSite();
    const change = await screen.findByRole('button', { name: 'Change source' });
    expect((change as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('Stop the site before changing source modes.')).toBeTruthy();
  });

  it('shows linked database connection details with a masked password', async () => {
    await renderSite('blog');
    expect(await screen.findByText('Databases')).toBeTruthy();
    expect(screen.getByText('app')).toBeTruthy();
    expect(screen.getByText('127.0.0.1:3306')).toBeTruthy();
    expect(screen.getByText('blog_user')).toBeTruthy();
    expect(screen.queryByText('s3cret')).toBeNull();
  });

  it('reveals a linked password through the audited API', async () => {
    mockDb.revealServiceUserPassword.mockResolvedValue({ value: 's3cret' });
    await renderSite('blog');
    await fireEvent.click(
      await screen.findByRole('button', { name: /reveal password for app/i }),
    );
    expect(await screen.findByText('s3cret')).toBeTruthy();
    expect(mockDb.revealServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('shows password not managed for a metadata-only link', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [METADATA_ONLY_LINK] });
    await renderSite('blog');
    expect(await screen.findByText('Password not managed')).toBeTruthy();
    expect(
      screen.getByText(/external password entry is not available in v1/i),
    ).toBeTruthy();
  });

  it('does not offer external password entry for a metadata-only link', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [METADATA_ONLY_LINK] });
    await renderSite('blog');
    expect(await screen.findByText('Password not managed')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /set password/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /reset password/i })).toBeNull();
    expect(
      screen.getByText(/external password entry is not available in v1/i),
    ).toBeTruthy();
  });

  it('offers a reset for a managed user whose password is unknown', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({ links: [MANAGED_UNKNOWN_LINK] });
    mockDb.listServiceUsers.mockResolvedValue({ users: [MANAGED_UNKNOWN_USER] });
    mockDb.setServiceUserPassword.mockResolvedValue({ value: 'rotated' });
    await renderSite('blog');

    expect(await screen.findByText(/password unknown/i)).toBeTruthy();
    expect(screen.queryByText(/external password entry is not available in v1/i)).toBeNull();
    expect(screen.getByText('Replaces the current password.')).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: /reset password/i }));
    expect(await screen.findByText('rotated')).toBeTruthy();
    expect(mockDb.setServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('resets a devstack-managed password and asks for a restart', async () => {
    mockDb.setServiceUserPassword.mockResolvedValue({ value: 'rotated' });
    await renderSite('blog');
    await fireEvent.click(await screen.findByRole('button', { name: /reset password/i }));
    expect(await screen.findByText('rotated')).toBeTruthy();
    expect(await screen.findByText(/restart site to apply/i)).toBeTruthy();
    expect(mockDb.setServiceUserPassword).toHaveBeenCalledWith('mysql', 'usr_app');
  });

  it('does not offer reset for an external user with a stored password', async () => {
    mockDb.listServiceUsers.mockResolvedValue({
      users: [{ ...APP_USER, managed: false }],
    });
    await renderSite('blog');
    expect(
      await screen.findByRole('button', { name: /reveal password for app/i }),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: /reset password/i })).toBeNull();
  });

  it('labels a user shared across sites from linked_sites', async () => {
    mockDb.listServiceUsers.mockResolvedValue({
      users: [
        {
          ...APP_USER,
          linked_sites: [
            { id: 's_blog', name: 'blog' },
            { id: 's_other', name: 'other' },
          ],
        },
      ],
    });
    await renderSite('blog');
    expect(await screen.findByText('shared user')).toBeTruthy();
    expect(mockDb.listServiceUsers).toHaveBeenCalledWith('mysql');
  });

  it('falls back to duplicate link inference when linked_sites is absent', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({
      links: [
        SITE_DB_LINK,
        { ...SITE_DB_LINK, id: 'lnk_2', database: 'blog' },
      ],
    });
    mockDb.listServiceUsers.mockResolvedValue({
      users: [{ ...APP_USER, linked_sites: undefined }],
    });
    await renderSite('blog');
    expect(await screen.findAllByText('shared user')).toHaveLength(2);
  });

  it('says linked databases remain when deleting the site', async () => {
    await renderSite('blog');
    await fireEvent.click(await screen.findByRole('button', { name: /remove site/i }));
    expect(screen.getByText(/database links/i)).toBeTruthy();
    expect(
      screen.getByText(/databases, users, grants, passwords, backups, and data remain/i),
    ).toBeTruthy();
  });

  it('shows database offline and the exclusive engine block', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({
      links: [
        {
          ...SITE_DB_LINK,
          available: false,
          error: 'exclusive engine conflict: MariaDB is active on port 3306',
        },
      ],
    });
    await renderSite('blog');
    expect(await screen.findByText(/database offline/i)).toBeTruthy();
    expect(
      screen.getByText(/MySQL-family database engine is active on port 3306/i),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: /activate/i })).toBeTruthy();
  });

  it('does not treat a generic port 3306 error as an exclusive engine conflict', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({
      links: [
        {
          ...SITE_DB_LINK,
          available: false,
          error: 'connection refused on port 3306',
        },
      ],
    });
    await renderSite('blog');
    expect(await screen.findByText(/database offline/i)).toBeTruthy();
    expect(screen.getByText('connection refused on port 3306')).toBeTruthy();
    expect(
      screen.queryByText(/MySQL-family database engine is active/i),
    ).toBeNull();
  });

  it('shows a stopped-service error without claiming an exclusive engine conflict', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({
      links: [
        {
          ...SITE_DB_LINK,
          available: false,
          error: 'service mysql is stopped',
        },
      ],
    });
    await renderSite('blog');
    expect(await screen.findByText(/database offline/i)).toBeTruthy();
    expect(screen.getByText('service mysql is stopped')).toBeTruthy();
    expect(
      screen.queryByText(/MySQL-family database engine is active/i),
    ).toBeNull();
  });

  it('recognizes a structured exclusive engine error code', async () => {
    mockDb.listSiteDatabases.mockResolvedValue({
      links: [
        {
          ...SITE_DB_LINK,
          available: false,
          error: JSON.stringify({
            code: 'DATABASE_EXCLUSIVE_ENGINE',
            message: 'MariaDB occupies the MySQL-family port.',
          }),
        },
      ],
    });
    await renderSite('blog');
    expect(
      await screen.findByText(/MySQL-family database engine is active/i),
    ).toBeTruthy();
    expect(screen.queryByText(/DATABASE_EXCLUSIVE_ENGINE/)).toBeNull();
  });

  it('links a different existing database as a non-primary link', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({
      databases: [APP_DATABASE, BLOG_DATABASE, MISSING_DATABASE, DEVSTACK_DATABASE],
    });
    await renderSite('blog');
    await fireEvent.click(await screen.findByRole('button', { name: 'Link database' }));
    const dialog = await screen.findByRole('dialog');
    const service = (await within(dialog).findByLabelText(
      /Database service/,
    )) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(within(dialog).getByLabelText('Use existing database'));
    const database = within(dialog).getByLabelText(/Existing database/) as HTMLSelectElement;
    await waitFor(() => expect(database.value).toBe('blog'));
    const options = Array.from(database.options).map((option) => option.value);
    expect(options).not.toContain('app');
    expect(options).not.toContain('missing_db');
    expect(options).not.toContain('devstack');
    expect(options).toContain('blog');
    await fireEvent.change(database, { target: { value: 'blog' } });
    await fireEvent.click(within(dialog).getByLabelText('Use existing user'));
    const user = within(dialog).getByLabelText(/Existing user/) as HTMLSelectElement;
    await waitFor(() => expect(user.value).toBe('usr_app'));
    await fireEvent.change(user, { target: { value: 'usr_app' } });
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Link database' }),
    );

    await waitFor(() =>
      expect(mockDb.linkSiteDatabase).toHaveBeenCalledWith('s_blog', {
        service_id: 'mysql',
        database: 'blog',
        user_id: 'usr_app',
        primary: false,
      }),
    );
  });

  it('rejects protected names for a new database and user in the link dialog', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [] });
    await renderSite('blog');
    await fireEvent.click(await screen.findByRole('button', { name: 'Link database' }));
    const dialog = await screen.findByRole('dialog');
    const service = (await within(dialog).findByLabelText(
      /Database service/,
    )) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));

    await fireEvent.input(within(dialog).getByLabelText(/Database name/), {
      target: { value: 'mysql' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/Database username/), {
      target: { value: 'postgres' },
    });
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Link database' }),
    );
    expect(
      await within(dialog).findByText('That database name is reserved.'),
    ).toBeTruthy();

    await fireEvent.input(within(dialog).getByLabelText(/Database name/), {
      target: { value: 'fresh_db' },
    });
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Link database' }),
    );
    expect(await within(dialog).findByText('That username is reserved.')).toBeTruthy();
    expect(mockDb.createSiteDatabase).not.toHaveBeenCalled();
  });

  it('keeps link-dialog validation visible for an invalid submission', async () => {
    mockDb.listServiceDatabases.mockResolvedValue({ databases: [] });
    await renderSite('blog');
    await fireEvent.click(await screen.findByRole('button', { name: 'Link database' }));
    const dialog = await screen.findByRole('dialog');
    const service = (await within(dialog).findByLabelText(
      /Database service/,
    )) as HTMLSelectElement;
    await waitFor(() => expect(service.value).toBe('mysql'));
    await fireEvent.click(within(dialog).getByLabelText('Use existing database'));
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'Link database' }),
    );

    expect(
      await within(dialog).findByText('Choose an existing database.'),
    ).toBeTruthy();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(within(dialog).getByText('Choose an existing database.')).toBeTruthy();
  });

  it('keeps previous links and offers retry when link loading fails', async () => {
    await renderSite('blog');
    expect(await screen.findByText('app')).toBeTruthy();

    mockDb.listSiteDatabases.mockRejectedValueOnce(
      new ApiError('DATABASE_LINKS_FAILED', 'Could not load database links.', 503),
    );
    window.dispatchEvent(new Event('devstack:refresh'));

    expect(await screen.findByText('Could not load databases')).toBeTruthy();
    expect(screen.getByText('app')).toBeTruthy();

    mockDb.listSiteDatabases.mockResolvedValue({ links: [SITE_DB_LINK] });
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByText('Could not load databases')).toBeNull());
  });

  it('clears a revealed password on devstack:refresh', async () => {
    mockDb.revealServiceUserPassword.mockResolvedValue({ value: 's3cret' });
    await renderSite('blog');
    await fireEvent.click(
      await screen.findByRole('button', { name: /reveal password for app/i }),
    );
    expect(await screen.findByText('s3cret')).toBeTruthy();

    window.dispatchEvent(new Event('devstack:refresh'));
    await waitFor(() => expect(screen.queryByText('s3cret')).toBeNull());
  });

  it('sanitizes copied .env snippets and never echoes them', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    mockDb.listSiteDatabases.mockResolvedValue({ links: [PASSWORD_ENV_LINK] });
    await renderSite('blog');

    expect(screen.queryByText(/p@ss/)).toBeNull();
    expect(screen.queryByText(/s3cret/)).toBeNull();

    await fireEvent.click(
      await screen.findByRole('button', { name: /copy \.env snippet for app/i }),
    );
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain('DB_APP_DATABASE_URL=mysql://blog_user@127.0.0.1:3306/app');
    expect(copied).not.toContain('p@ss');
    expect(copied).not.toContain('s3cret');
    expect(copied).not.toContain('DB_APP_PWD');
    expect(copied).not.toContain('DB_APP_PASSWORD');

    const toast = get(toasts).find((entry) => entry.title.includes('.env'));
    expect(toast?.message).not.toContain('DB_APP_DATABASE_URL');
    expect(toast?.message).not.toContain('p@ss');
    expect(toast?.message).not.toContain('s3cret');
  });
});
