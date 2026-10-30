import { ApiError } from './errors';
import { mockDatabaseApi } from './databaseMock';
import type {
  BuildLogs,
  BuildResult,
  CatalogEntry,
  CreateSiteRequest,
  DaemonEvent,
  DoctorCheck,
  DoctorReport,
  FsListing,
  GlobalIni,
  Health,
  LogLine,
  NodeDefaultStatus,
  NodeRelease,
  NodeReleasePreflight,
  NodeReleaseSnapshot,
  NodeToolchain,
  PHPChange,
  PHPDefaults,
  PHPDefaultsResult,
  PHPExecResult,
  PHPExtension,
  BenchmarkRequest,
  BenchmarkRun,
  PerformanceApplyResult,
  PerformancePreview,
  PerformanceProfile,
  PerformanceResetResult,
  PerformanceState,
  PerformanceValues,
  PHPInfo,
  PHPMigrationRequest,
  PHPMigrationResult,
  PHPDefaultStatus,
  PHPResolvedExtension,
  RuntimeBuild,
  ServiceConfigApplyRequest,
  ServiceConfigContent,
  ServiceConfigDiff,
  ServiceConfigFile,
  ServiceConfigList,
  ServiceConfigResult,
  ServiceCredentials,
  ServiceUninstallResult,
  ServiceView,
  Session,
  Settings,
  SettingsUpdate,
  SettingsUpdateResult,
  ServingMode,
  TerminalConnection,
  TerminalHandlers,
  TerminalSession,
  Site,
  SiteStatus,
  SourceMode,
  ToolStatus,
  TrustTarget,
  UpdateSiteRequest,
  VhostPreview,
  VhostStatus,
} from './types';

const now = Date.now();
const iso = (offsetMs: number) => new Date(now + offsetMs).toISOString();
const days = (value: number) => value * 86_400_000;

function wait(min = 150, max = 350): Promise<void> {
  const duration = min + Math.random() * (max - min);
  return new Promise((resolve) => setTimeout(resolve, duration));
}

function clone<T>(value: T): T {
  return structuredClone(value);
}

const sessions: Session = { csrf_token: 'mock-csrf-token-7f3c9a' };

const PHP_LINES = ['8.2', '8.3', '8.4', '8.5'];
/** The mock refuses this build so the dashboard rollback path is exercised. */
export const FAILING_PHP_BUILD = 'php@8.5.0-r1';
const PHP_MANAGED_BEGIN = '; BEGIN devstack extensions (managed)';
const PHP_MANAGED_END = '; END devstack extensions (managed)';

function phpLineOf(version: string): string {
  const parts = version.split('.');
  return parts.length >= 2 ? `${parts[0]}.${parts[1]}` : version;
}

function phpRevisionOf(id: string): number {
  const match = /-r([1-9][0-9]*)$/.exec(id);
  return match ? Number.parseInt(match[1], 10) : 0;
}

function mockHash(value: string): string {
  const source = value || 'devstack';
  let hash = '';
  for (let index = 0; index < 64; index += 1) {
    const code = source.charCodeAt(index % source.length) + index * 31;
    hash += (code % 16).toString(16);
  }
  return hash;
}

const phpCatalogExtensions: PHPExtension[] = [
  { name: 'apcu', type: 'extension', so: 'apcu.so', default: false, standard: false, versions: PHP_LINES },
  { name: 'bcmath', type: 'extension', so: 'bcmath.so', default: true, standard: true, versions: PHP_LINES },
  { name: 'curl', type: 'extension', so: 'curl.so', default: true, standard: true, versions: PHP_LINES },
  { name: 'gd', type: 'extension', so: 'gd.so', default: true, standard: true, versions: PHP_LINES },
  { name: 'igbinary', type: 'extension', so: 'igbinary.so', default: false, standard: false, versions: PHP_LINES },
  { name: 'imagick', type: 'extension', so: 'imagick.so', default: false, standard: false, versions: PHP_LINES },
  { name: 'intl', type: 'extension', so: 'intl.so', default: true, standard: true, versions: PHP_LINES },
  { name: 'mbstring', type: 'extension', so: 'mbstring.so', default: true, standard: true, versions: PHP_LINES },
  {
    name: 'opcache',
    type: 'zend_extension',
    so: 'opcache.so',
    default: true,
    standard: true,
    versions: PHP_LINES,
  },
  { name: 'pdo_mysql', type: 'extension', so: 'pdo_mysql.so', default: true, standard: true, versions: PHP_LINES },
  { name: 'pdo_pgsql', type: 'extension', so: 'pdo_pgsql.so', default: true, standard: true, versions: PHP_LINES },
  {
    name: 'redis',
    type: 'extension',
    so: 'redis.so',
    default: false,
    standard: false,
    versions: PHP_LINES,
    requires: ['igbinary'],
  },
  { name: 'sodium', type: 'extension', so: 'sodium.so', default: true, standard: true, versions: PHP_LINES },
  {
    name: 'xdebug',
    type: 'zend_extension',
    so: 'xdebug.so',
    default: false,
    standard: false,
    versions: PHP_LINES,
  },
  { name: 'zip', type: 'extension', so: 'zip.so', default: true, standard: true, versions: PHP_LINES },
];

const phpBuiltins = ['core', 'ctype', 'date', 'filter', 'hash', 'json', 'pcre', 'random', 'spl', 'standard'];
const phpBuiltinsByVersion: Record<string, string[]> = { '8.5': ['opcache'] };

function phpBuiltinsFor(version: string): string[] {
  return [...phpBuiltins, ...(phpBuiltinsByVersion[version] ?? [])];
}

const phpDefaultIni: Record<string, string> = {
  display_errors: 'On',
  error_reporting: 'E_ALL',
  memory_limit: '256M',
  upload_max_filesize: '64M',
  post_max_size: '64M',
  max_execution_time: '120',
  'date.timezone': 'UTC',
  'opcache.enable': '1',
  'opcache.validate_timestamps': '1',
  'opcache.revalidate_freq': '0',
};

function seedPHPDefaults(): Record<string, PHPDefaults> {
  const extensions = Object.fromEntries(
    phpCatalogExtensions.map((extension) => [extension.name, extension.default]),
  );
  const defaults = Object.fromEntries(
    PHP_LINES.map((version) => [
      version,
      { version, ini: { ...phpDefaultIni }, extensions: { ...extensions } },
    ]),
  );
  delete defaults['8.5']?.extensions.opcache;
  return defaults;
}

let phpDefaults = seedPHPDefaults();

const DEFAULT_PHP_BUILD = 'php@8.4.4-r1';
const DEFAULT_GLOBAL_INI = `; devstack global CLI php.ini
[PHP]
memory_limit = 512M
display_errors = On
date.timezone = UTC
`;
let phpDefaultConfigured = DEFAULT_PHP_BUILD;
let globalIniContent = DEFAULT_GLOBAL_INI;
const seedToolStatuses: ToolStatus[] = [
  {
    id: 'php-default',
    installed: true,
    managed: true,
    wrapper_path: '/usr/local/bin/php',
    target: '/home/dev/.devstack/tools/php-default/bin/php',
    version: '8.4.4',
  },
  {
    id: 'composer',
    installed: true,
    managed: true,
    wrapper_path: '/usr/local/bin/composer',
    target: '/home/dev/.devstack/tools/composer/composer.phar',
    version: '2.8.4',
  },
];
let toolStatuses: ToolStatus[] = clone(seedToolStatuses);

/** Restore service and tooling fixtures between unit tests. */
export function resetMockServiceStateForTests(): void {
  mockServices = clone(seedMockServices);
  toolStatuses = clone(seedToolStatuses);
  settings = clone(seedSettings);
  nodeDefaultConfigured = '';
  mockPerformance = {
    profile: 'balanced',
    values: mockPerformanceValues('balanced'),
    resources: mockPerformanceResources,
    active_server: 'nginx',
    host_tuning: {
      available: true,
      managed: false,
      applied: false,
      sysctl_path: '/etc/sysctl.d/99-devstack-performance.conf',
      limits_path: '/etc/security/limits.d/99-devstack-performance.conf',
      backup_dir: '/home/dev/.devstack/backups/performance',
    },
  };
  mockBenchmarks = [];
  mockConfigs = {};
  phpDefaultConfigured = DEFAULT_PHP_BUILD;
  globalIniContent = DEFAULT_GLOBAL_INI;
}

const seedMockServices: ServiceView[] = [
  {
    id: 'nginx',
    display_name: 'Nginx Web Server',
    group: 'web',
    state: 'active',
    active: true,
    installed: true,
    health: 'healthy',
    container_id: 'd4e5f6a7b8c9',
    image: 'nginx:1.30.6',
    ports: [
      { host: 80, container: 80, protocol: 'http' },
      { host: 443, container: 443, protocol: 'https' },
    ],
    config_path: '/data/devstack/services/nginx/config',
    data_path: '/data/devstack/services/nginx/data',
  },
  {
    id: 'apache',
    display_name: 'Apache HTTP Server',
    group: 'web',
    state: 'installed',
    active: false,
    installed: true,
    health: '',
    container_id: 'b7c8d9e0f1a2',
    image: 'httpd:2.4.69',
    ports: [
      { host: 80, container: 80, protocol: 'http' },
      { host: 443, container: 443, protocol: 'https' },
    ],
    config_path: '/data/devstack/services/apache/config',
    data_path: '/data/devstack/services/apache/data',
  },
  {
    id: 'mysql',
    display_name: 'MySQL 8.4',
    group: 'mysql-family',
    state: 'active',
    active: true,
    installed: true,
    health: 'healthy',
    container_id: 'a1b2c3d4e5f6',
    image: 'mysql:8.4.13',
    ports: [
      { host: 3306, container: 3306, protocol: 'tcp', loopback: true },
    ],
    config_path: '/data/devstack/services/mysql/config',
    data_path: '/data/devstack/services/mysql/data',
  },
  {
    id: 'mariadb',
    display_name: 'MariaDB 11.8',
    group: 'mysql-family',
    state: 'not_installed',
    active: false,
    installed: false,
    health: '',
    container_id: '',
    image: 'mariadb:11.8.9',
    ports: [
      { host: 3306, container: 3306, protocol: 'tcp', loopback: true },
    ],
    config_path: '/data/devstack/services/mariadb/config',
    data_path: '/data/devstack/services/mariadb/data',
  },
  {
    id: 'postgresql',
    display_name: 'PostgreSQL 18',
    group: 'standalone',
    state: 'installed',
    active: false,
    installed: true,
    health: '',
    container_id: 'c3d4e5f6a7b8',
    image: 'postgres:18.6',
    ports: [
      { host: 5432, container: 5432, protocol: 'tcp', loopback: true },
    ],
    config_path: '/data/devstack/services/postgresql/config',
    data_path: '/data/devstack/services/postgresql/data',
  },
  {
    id: 'redis',
    display_name: 'Redis 8.8',
    group: 'standalone',
    state: 'not_installed',
    active: false,
    installed: false,
    health: '',
    container_id: '',
    image: 'redis:8.8.3',
    ports: [
      { host: 6379, container: 6379, protocol: 'tcp', loopback: true },
    ],
    config_path: '/data/devstack/services/redis/config',
    data_path: '/data/devstack/services/redis/data',
  },
];

let mockServices: ServiceView[] = clone(seedMockServices);

function resolvePHPExtensions(
  version: string,
  defaults: Record<string, boolean>,
  overrides: Record<string, boolean> | undefined,
): PHPResolvedExtension[] {
  const catalog = new Map(phpCatalogExtensions.map((extension) => [extension.name, extension]));
  const builtins = new Set(phpBuiltinsFor(version));
  const selected = new Map<string, PHPResolvedExtension>();
  for (const extension of phpCatalogExtensions) {
    if (builtins.has(extension.name)) continue;
    const enabled =
      overrides?.[extension.name] ?? defaults[extension.name] ?? extension.default;
    if (!enabled) continue;
    selected.set(extension.name, {
      name: extension.name,
      type: extension.type,
      so: extension.so,
      required: false,
    });
  }
  for (const extension of catalog.values()) {
    if (!selected.has(extension.name)) continue;
    for (const requirement of extension.requires ?? []) {
      const dependency = catalog.get(requirement);
      if (!dependency) continue;
      selected.set(requirement, {
        name: dependency.name,
        type: dependency.type,
        so: dependency.so,
        required: true,
        required_by: [extension.name],
      });
    }
  }
  return Array.from(selected.values()).sort((a, b) => a.name.localeCompare(b.name));
}

function renderPHPIni(site: Site, defaults: PHPDefaults): string {
  const resolved = resolvePHPExtensions(
    site.php_version,
    defaults.extensions,
    site.php_extensions,
  );
  const overrides = Object.entries(site.php_ini ?? {});
  const lines = [
    '; Generated by devstack. Do not edit.',
    '',
    '; BEGIN devstack version defaults',
    ...Object.entries(defaults.ini).map(([key, value]) => `${key} = ${value}`),
    '; END devstack version defaults',
    '',
    '; BEGIN devstack site overrides',
    ...overrides.map(([key, value]) => `${key} = ${value}`),
    '; END devstack site overrides',
    '',
    '[PHP]',
    PHP_MANAGED_BEGIN,
    ...resolved.map((extension) => `${extension.type}=${extension.so}`),
    PHP_MANAGED_END,
    '',
  ];
  return lines.join('\n');
}

type EventListener = (event: DaemonEvent) => void;
const eventListeners = new Set<EventListener>();

function emitEvent(type: string, siteID: string | undefined, message: string): void {
  const event: DaemonEvent = {
    type,
    site_id: siteID,
    message,
    created_at: new Date().toISOString(),
  };
  for (const listener of eventListeners) listener(event);
}

const sites: Site[] = [
  {
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
    env: {
      PUBLIC_SITE_URL: 'https://blog.test',
      SESSION_SECRET: 'blog_dev_7a1c3e5d9f2b',
    },
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
    created_at: iso(-days(34)),
    updated_at: iso(-1000 * 60 * 7),
    certificate: {
      not_after: iso(days(83)),
      domains: ['blog.test'],
      serial: '6f:2c:91:aa:0e:44',
    },
  },
  {
    id: 's_shop',
    name: 'shop',
    domain: 'shop.test',
    www: true,
    backend: 'local',
    host_path: '/home/dev/projects/shop',
    backend_path: '/home/dev/projects/shop',
    kind: 'node',
    php_version: '',
    php_build: '',
    php_ini_raw: '',
    php_config_hash: '',
    node_version: '24.9.0',
    command: 'npm run dev',
    env: {
      NODE_ENV: 'development',
      PORT: '41002',
      DATABASE_URL: 'postgres://localhost:5432/shop',
      SESSION_SECRET: 'shop_dev_2f9c4a6e8b1d',
    },
    document_root: '',
    front_controller: false,
    front_controller_file: 'index.php',
    front_controller_mode: 'auto',
    apache_allow_override: 'all',
    spa_fallback: false,
    spa_fallback_file: 'index.html',
    tls: true,
    force_https: true,
    desired_state: 'running',
    status: 'running',
    http_port: 41002,
    fastcgi_port: 0,
    created_at: iso(-days(19)),
    updated_at: iso(-1000 * 60 * 2),
    certificate: {
      not_after: iso(days(18)),
      domains: ['shop.test', 'www.shop.test'],
      serial: '71:9e:04:c2:88:af',
    },
  },
  {
    id: 's_api',
    name: 'api',
    domain: 'api.test',
    www: false,
    backend: 'local',
    host_path: '/home/dev/projects/api',
    backend_path: '/home/dev/projects/api',
    kind: 'node',
    php_version: '',
    php_build: '',
    php_ini_raw: '',
    php_config_hash: '',
    node_version: '22.20.0',
    command: 'npm start',
    env: {
      NODE_ENV: 'production',
      API_TOKEN: 'tok_live_81ea5d9f0c27',
      LOG_LEVEL: 'debug',
    },
    document_root: '',
    front_controller: false,
    front_controller_file: 'index.php',
    front_controller_mode: 'auto',
    apache_allow_override: 'all',
    spa_fallback: false,
    spa_fallback_file: 'index.html',
    tls: false,
    force_https: false,
    desired_state: 'running',
    status: 'crashed',
    http_port: 0,
    fastcgi_port: 0,
    created_at: iso(-days(11)),
    updated_at: iso(-1000 * 60 * 34),
    certificate: undefined,
  },
];

const runtimes: RuntimeBuild[] = [
  {
    id: 'node-24.9.0',
    kind: 'node',
    version: '24.9.0',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/node/24.9.0',
    checksum: '9a4f7c02e1d8b63a5f0c92d47e18ba6c3f5d20e9a7b4c61d8f30e2a95c7b4d18',
    installed_at: iso(-days(26)),
    size: 51_904_512,
  },
  {
    id: 'node-22.20.0',
    kind: 'node',
    version: '22.20.0',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/node/22.20.0',
    checksum: '41c8e5b09d7a2f63c1e0b8d94a75f2c6e3d10b8a9f4c72e5d6a0b3c81f9e2d47',
    installed_at: iso(-days(58)),
    size: 49_807_360,
  },
  {
    id: 'php@8.4.4-r1',
    kind: 'php',
    version: '8.4.4',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/php/8.4.4-r1',
    checksum: '5c1f8d7e2a9b4c6d0e3f7a1b8c5d2e9f4a6b0c3d7e1f8a2b5c9d4e6f0a3b7c1d',
    installed_at: iso(-days(22)),
    size: 78_643_200,
  },
  {
    id: 'php@8.3.14-r2',
    kind: 'php',
    version: '8.3.14',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/php/8.3.14-r2',
    checksum: '8b3e6a1c4f7d9b2e5a0c8f3d6b1e4a7c9f2d5b8e0a3c6f1d4b7e9a2c5f8d0b3e',
    installed_at: iso(-days(47)),
    size: 77_594_240,
  },
  {
    id: 'php@8.5.0-r1',
    kind: 'php',
    version: '8.5.0',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/php/8.5.0-r1',
    checksum: '1d7b4e9a2c5f8b0d3e6a1c4f7b9d2e5a8c0f3b6d1e4a7c9f2b5d8e0a3c6f1b4d',
    installed_at: iso(-days(9)),
    size: 79_691_776,
  },
];

const catalog: CatalogEntry[] = [
  {
    id: 'node@24.11.0-linux-x64',
    kind: 'node',
    version: '24.11.0',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v24.11.0/node-v24.11.0-linux-x64.tar.xz',
    sha256: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    size: 52_428_800,
  },
  {
    id: 'node@22.21.1-linux-x64',
    kind: 'node',
    version: '22.21.1',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v22.21.1/node-v22.21.1-linux-x64.tar.xz',
    sha256: 'a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a',
    size: 49_807_360,
  },
  {
    id: 'node@24.9.0-linux-x64',
    kind: 'node',
    version: '24.9.0',
    build: 'linux-x64',
    url: 'https://nodejs.org/dist/v24.9.0/node-v24.9.0-linux-x64.tar.xz',
    sha256: '9a4f7c02e1d8b63a5f0c92d47e18ba6c3f5d20e9a7b4c61d8f30e2a95c7b4d18',
    size: 51_904_512,
  },
  {
    id: 'php@8.4.5-r1',
    kind: 'php',
    version: '8.4.5',
    build: 'linux-x64',
    revision: 1,
    url: 'https://devstack.invalid/php/php-8.4.5-r1-linux-x64.tar.gz',
    sha256: 'a2c5f8b0d3e6a1c4f7b9d2e5a8c0f3b6d1e4a7c9f2b5d8e0a3c6f1b4d7e9a2c5',
    size: 78_905_344,
  },
  {
    id: 'php@8.5.1-r1',
    kind: 'php',
    version: '8.5.1',
    build: 'linux-x64',
    revision: 1,
    url: 'https://devstack.invalid/php/php-8.5.1-r1-linux-x64.tar.gz',
    sha256: 'e4a7c9f2b5d8e0a3c6f1b4d7e9a2c5f8b0d3e6a1c4f7b9d2e5a8c0f3b6d1e4a7',
    size: 79_953_408,
  },
  {
    id: 'php@8.5.1-r2',
    kind: 'php',
    version: '8.5.1',
    build: 'linux-x64',
    revision: 2,
    url: 'https://devstack.invalid/php/php-8.5.1-r2-linux-x64.tar.gz',
    sha256: 'c9f2b5d8e0a3c6f1b4d7e9a2c5f8b0d3e6a1c4f7b9d2e5a8c0f3b6d1e4a7c9f2',
    size: 79_958_016,
  },
];

function nodeRelease(
  version: string,
  date: string,
  npm: string,
  lts = '',
  security = false,
): NodeRelease {
  return {
    version,
    date,
    lts,
    security,
    npm,
    modules: version.startsWith('24') ? '137' : version.startsWith('22') ? '127' : '115',
    file: 'linux-x64',
  };
}

const nodeReleases: NodeRelease[] = [
  nodeRelease('24.9.0', '2026-10-15', '11.6.0', 'Krypton'),
  nodeRelease('24.8.1', '2026-09-24', '11.5.2', 'Krypton'),
  nodeRelease('24.8.0', '2026-09-10', '11.5.1', 'Krypton'),
  nodeRelease('24.1.2', '2026-04-08', '11.0.0', 'Krypton'),
  nodeRelease('22.20.0', '2026-10-08', '10.9.4', 'Jod', true),
  nodeRelease('22.19.0', '2026-09-18', '10.9.0', 'Jod'),
  nodeRelease('22.11.0', '2025-10-28', '10.2.0', 'Jod'),
  nodeRelease('20.19.5', '2026-07-22', '10.8.2', 'Iron'),
  nodeRelease('20.18.1', '2025-11-20', '10.8.1', 'Iron'),
].sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true }));

let nodeDefaultConfigured = '';

let doctorChecks: DoctorCheck[] = [
  {
    id: 'helper',
    title: 'Helper service',
    status: 'ok',
    detail: 'devstack-helper 0.1.0 is responding on /run/devstack/helper.sock.',
    remediation: '',
  },
  {
    id: 'hosts',
    title: 'Hosts entries',
    status: 'ok',
    detail: '3 managed domains match the hosts block (blog.test, shop.test, api.test).',
    remediation: '',
  },
  {
    id: 'ca',
    title: 'Local certificate authority',
    status: 'ok',
    detail: 'CA key is present with mode 0600. Leaf certificates are issued on demand.',
    remediation: '',
  },
  {
    id: 'trust',
    title: 'Browser trust',
    status: 'warn',
    detail: 'Firefox profile was not found, so the CA is not trusted in Firefox.',
    remediation: 'Install Firefox, then run `devstack trust repair` after its first launch.',
  },
  {
    id: 'ports',
    title: 'Proxy ports',
    status: 'ok',
    detail: 'Ports 80 and 443 are forwarded by the helper; 8080, 8443 and 7070 are free.',
    remediation: '',
  },
  {
    id: 'runtimes',
    title: 'Runtime integrity',
    status: 'ok',
    detail: '2 Node builds verified against their recorded SHA-256 checksums.',
    remediation: '',
  },
  {
    id: 'disk',
    title: 'Disk space',
    status: 'ok',
    detail: '18.4 GB free in /home/dev/.devstack — well above the 1 GB minimum.',
    remediation: '',
  },
  {
    id: 'wsl',
    title: 'WSL2 backend',
    status: 'skipped',
    detail: 'No WSL2 distribution is configured on this Linux machine.',
    remediation: 'WSL2 support arrives with the Windows backend in a later release.',
  },
];

let lastDoctorRun = iso(-1000 * 60 * 9);

let trustTargets: TrustTarget[] = [
  {
    name: 'System trust store',
    ok: true,
    detail: 'devstack-local-ca installed in /usr/local/share/ca-certificates.',
  },
  {
    name: 'Chrome / Chromium',
    ok: true,
    detail: 'NSS database ~/.pki/nssdb contains “devstack local CA”.',
  },
  {
    name: 'Firefox',
    ok: false,
    detail: 'No profile found in ~/.mozilla/firefox/profiles.ini.',
  },
];

const seedSettings: Settings = {
  home: '/home/dev/.devstack',
  user_home: '/home/dev',
  http_port: 8080,
  https_port: 8443,
  ui_port: 7070,
  serving_mode: 'builtin',
  serving_provider: 'native',
  wsl_distro: 'devstack',
  auto_start_sites: true,
  graceful_stop_seconds: 10,
  max_request_body_mb: 1024,
  log_max_mb: 10,
  log_keep: 5,
  version: '0.1.0',
  socket_path: '/home/dev/.devstack/devstack.sock',
  pid_path: '/home/dev/.devstack/devstack.pid',
  restart_required: false,
};
let settings: Settings = clone(seedSettings);

let vhostStatus: VhostStatus = {
  mode: 'builtin',
  server: '',
  server_version: '',
  bridge_path: '',
  include_present: false,
  current_release: '',
  config_ok: true,
  last_sync: '',
};

function mockPerformanceValues(profile: PerformanceProfile): PerformanceValues {
  const throughput = profile === 'throughput';
  return {
    profile,
    nginx: {
      workers: 4,
      worker_connections: throughput ? 4096 : 1024,
      worker_rlimit_nofile: throughput ? 8192 : 4096,
      open_file_cache_max: 4096,
      keepalive_timeout_seconds: 65,
      keepalive_requests: 1000,
      gzip: true,
      gzip_comp_level: 5,
    },
    apache: {
      threads_per_child: 25,
      server_limit: 16,
      max_request_workers: 400,
      start_servers: 4,
      min_spare_threads: 25,
      max_spare_threads: 100,
      async_request_worker_factor: 2,
      max_connections_per_child: 10000,
      keep_alive: true,
      max_keepalive_requests: 1000,
      keepalive_timeout_seconds: 5,
      listen_backlog: 511,
    },
    php: {
      mode: 'dynamic',
      global_children: 32,
      sites: [],
      listen_backlog: 511,
      opcache: {
        enabled: true,
        memory_mb: 128,
        interned_strings_mb: 32,
        max_accelerated_files: 20000,
        validate_timestamps: true,
        revalidate_freq_seconds: 2,
      },
      realpath_cache_size_kb: 4096,
      realpath_cache_ttl_seconds: 120,
    },
    host: {
      file_max: 100000,
      somaxconn: 4096,
      netdev_max_backlog: 4096,
      ip_local_port_range: { min: 10240, max: 60999 },
      nofile_soft: 20000,
      nofile_hard: 40000,
      user: 'dev',
      group: 'dev',
    },
  };
}

const mockPerformanceResources = {
  hostname: 'dev-workstation',
  arch: 'x86_64',
  cpu_count: 8,
  cpu_quota_cpus: 0,
  memory_total_bytes: 17_179_869_184,
  memory_available_bytes: 9_126_805_504,
  page_size_bytes: 4096,
  nofile_soft: 1024,
  nofile_hard: 524288,
  user: 'dev',
  group: 'dev',
  sysctl: {
    file_max: 9223372036854775807,
    somaxconn: 4096,
    netdev_max_backlog: 16384,
    ip_local_port_range: { min: 32768, max: 60999 },
  },
};

let mockPerformance: PerformanceState = {
  profile: 'balanced',
  values: mockPerformanceValues('balanced'),
  resources: mockPerformanceResources,
  active_server: 'nginx',
  host_tuning: {
    available: true,
    managed: false,
    applied: false,
    sysctl_path: '/etc/sysctl.d/99-devstack-performance.conf',
    limits_path: '/etc/security/limits.d/99-devstack-performance.conf',
    backup_dir: '/home/dev/.devstack/backups/performance',
  },
};
let mockBenchmarks: BenchmarkRun[] = [];

interface MockConfigFile {
  content: string;
  class: string;
  generated: boolean;
  modified: boolean;
  detached: boolean;
  seeded: string;
}

let mockConfigs: Record<string, Record<string, MockConfigFile>> = {};

function seedConfigFiles(service: ReturnType<typeof findService>): Record<string, MockConfigFile> {
  const files: Record<string, MockConfigFile> = {};
  const add = (path: string, cls: string, content: string) => {
    files[path] = {
      content,
      class: cls,
      generated: cls === 'generated',
      modified: false,
      detached: false,
      seeded: content,
    };
  };
  if (service.id === 'nginx') {
    add('nginx.conf', 'base', 'worker_processes auto;\ninclude conf.d/*.conf;\n');
    add('conf.d/devstack.conf', 'generated', '# Generated by devstack. Do not edit.\nserver {\n    listen 80;\n    server_name devstack.test;\n}\n');
    add('conf.d/custom.conf', 'custom', '# User file. devstack never rewrites custom files.\n');
  } else if (service.id === 'apache') {
    add('httpd.conf', 'base', 'ServerRoot "/usr/local/apache2"\nInclude conf.d/*.conf\n');
    add('conf.d/devstack.conf', 'generated', '# Generated by devstack. Do not edit.\nListen 80\n');
  } else if (service.id === 'mysql' || service.id === 'mariadb') {
    add('conf.d/devstack.cnf', 'generated', '[mysqld]\nbind-address = 127.0.0.1\n');
    add('my.cnf', 'base', '[client]\ndefault-character-set = utf8mb4\n');
  } else if (service.id === 'postgresql') {
    add('postgresql.conf', 'base', 'listen_addresses = \'127.0.0.1\'\n');
    add('pg_hba.conf', 'base', 'host all all 127.0.0.1/32 scram-sha-256\n');
  } else if (service.id === 'redis') {
    add('redis.conf', 'base', 'bind 127.0.0.1\nprotected-mode yes\n');
  } else {
    add('service.conf', 'base', `# ${service.display_name} managed configuration\n`);
  }
  return files;
}

function configFor(id: string): Record<string, MockConfigFile> {
  const service = findService(id);
  if (!service.installed) {
    throw new ApiError('SERVICE_CONFLICT', `${service.display_name} is not installed.`, 409);
  }
  if (!mockConfigs[id]) mockConfigs[id] = seedConfigFiles(service);
  return mockConfigs[id];
}

function configFileView(path: string, file: MockConfigFile): ServiceConfigFile {
  return {
    path,
    class: file.class,
    generated: file.generated,
    modified: file.modified,
    detached: file.detached,
    size: new TextEncoder().encode(file.content).length,
    conflict: file.detached
      ? {
          active: true,
          resolution_required: true,
          reason: 'Local edits are detached from generated output.',
          allowed_resolutions: ['keep', 'use', 'merge'],
        }
      : { active: false, resolution_required: false },
  };
}

const mockMaintenanceHTML =
  '<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Site unavailable - devstack</title><body><h1>Site unavailable</h1><p>This site is stopped. Start it from the devstack dashboard or CLI, then reload the page.</p></body></html>';

const mockMaxBodyBytes = 1_073_741_824;

function nginxValue(value: string): string {
  if (/^[A-Za-z0-9/._~:+-]+$/.test(value)) return value;
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

function apacheQuote(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

function mockDocumentRoot(site: Site): string {
  const root = site.document_root.trim();
  if (!root || root === '.') return site.backend_path;
  if (root.startsWith('/')) return root;
  return `${site.backend_path.replace(/\/+$/, '')}/${root.replace(/^\/+/, '')}`;
}

function nginxTLS(certPath: string, keyPath: string): string {
  return `    ssl_certificate     ${nginxValue(certPath)};
    ssl_certificate_key ${nginxValue(keyPath)};
    ssl_protocols TLSv1.2 TLSv1.3;
`;
}

function nginxServer(
  listen: string,
  serverNames: string,
  certPath: string | null,
  keyPath: string | null,
  body: string,
): string {
  return `server {
    listen ${listen};
    server_name ${serverNames};
${certPath && keyPath ? nginxTLS(certPath, keyPath) : ''}${body}}
`;
}

function nginxStaticBody(root: string, spa: boolean): string {
  return `
    client_max_body_size ${mockMaxBodyBytes};

    root ${nginxValue(root)};
    index index.html;
    autoindex off;
    add_header Cache-Control "no-store" always;

    location / {
        try_files $uri $uri/ ${spa ? '/index.html' : '=404'};
    }

    location ~ /\\. {
        deny all;
    }

    location ^~ /_devstack {
        deny all;
    }
`;
}

function nginxNodeBody(port: number): string {
  return `
    client_max_body_size ${mockMaxBodyBytes};

    error_page 502 503 504 =503 /_devstack/503.html;

    location / {
        proxy_pass http://127.0.0.1:${port};
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_read_timeout 120s;
        proxy_intercept_errors on;
    }

    location = /_devstack/503.html {
        internal;
        default_type text/html;
        return 200 ${nginxValue(mockMaintenanceHTML)};
    }

    location ~ /\\. {
        deny all;
    }

    location ^~ /_devstack {
        deny all;
    }
`;
}

function nginxServingBody(site: Site, root: string): string {
  return site.kind === 'node'
    ? nginxNodeBody(site.http_port || 41_000)
    : nginxStaticBody(root, site.spa_fallback);
}

function nginxStoppedBody(): string {
  return `
    default_type text/html;

    return 503 ${nginxValue(mockMaintenanceHTML)};
`;
}

function nginxRedirectBody(scheme: 'http' | 'https'): string {
  return `
    return 308 ${scheme}://$host$request_uri;
`;
}

function apacheTLS(certPath: string, keyPath: string): string {
  return `    SSLEngine on
    SSLCertificateFile ${apacheQuote(certPath)}
    SSLCertificateKeyFile ${apacheQuote(keyPath)}
    SSLProtocol -all +TLSv1.2 +TLSv1.3
`;
}

function apacheVHost(
  port: number,
  site: Site,
  certPath: string | null,
  keyPath: string | null,
  body: string,
): string {
  return `<VirtualHost *:${port}>
    ServerName ${site.domain}
${site.www ? `    ServerAlias www.${site.domain}\n` : ''}${certPath && keyPath ? apacheTLS(certPath, keyPath) : ''}
${body}</VirtualHost>
`;
}

function apacheStaticBody(root: string, spa: boolean): string {
  return `    LimitRequestBody ${mockMaxBodyBytes}

    DocumentRoot ${apacheQuote(root)}
    <Directory ${apacheQuote(root)}>
        Options -Indexes
        AllowOverride None
        Require all granted
${spa ? '        FallbackResource /index.html\n' : ''}    </Directory>

    <FilesMatch "^\\.">
        Require all denied
    </FilesMatch>

    <LocationMatch "^/_devstack(/|$)">
        Require all denied
    </LocationMatch>

    Header always set Cache-Control "no-store"
`;
}

function apacheNodeBody(port: number, scheme: 'http' | 'https'): string {
  const upstream = `http://127.0.0.1:${port}/`;
  return `    LimitRequestBody ${mockMaxBodyBytes}

    ProxyPreserveHost On
    ProxyAddHeaders Off
    ProxyPass "/" ${apacheQuote(upstream)} upgrade=websocket connectiontimeout=5 timeout=120
    ProxyPassReverse "/" ${apacheQuote(upstream)}

    RewriteEngine On
    RewriteCond %{HTTP_HOST} ^(.+)$
    RewriteRule ^ - [E=DEVSTACK_HOST:%1]
    RewriteCond %{REMOTE_ADDR} ^(.+)$
    RewriteRule ^ - [E=DEVSTACK_REMOTE:%1]

    RequestHeader set Host "%{DEVSTACK_HOST}e"
    RequestHeader set X-Forwarded-For "%{DEVSTACK_REMOTE}e"
    RequestHeader set X-Forwarded-Proto ${apacheQuote(scheme)}
    RequestHeader set X-Forwarded-Host "%{DEVSTACK_HOST}e"

    ErrorDocument 503 ${apacheQuote(mockMaintenanceHTML)}

    <LocationMatch "^/_devstack(/|$)">
        Require all denied
    </LocationMatch>
`;
}

function apacheStoppedBody(): string {
  return `    ErrorDocument 503 ${apacheQuote(mockMaintenanceHTML)}
    RewriteEngine On
    RewriteRule ^ - [R=503,L]
`;
}

function apacheRedirectBody(scheme: 'http' | 'https'): string {
  return `    RewriteEngine On
    RewriteRule ^ ${scheme}://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]
`;
}

function renderVhost(site: Site, server: 'nginx' | 'apache'): string {
  const serverNames = [site.domain, ...(site.www ? [`www.${site.domain}`] : [])].join(' ');
  const owner = site.id || site.name;
  const siteCert = `/etc/devstack/tls/${owner}.crt`;
  const siteKey = `/etc/devstack/tls/${owner}.key`;
  const dashboardCert = '/etc/devstack/tls/__dashboard__.crt';
  const dashboardKey = '/etc/devstack/tls/__dashboard__.key';
  const certPath = site.tls ? siteCert : dashboardCert;
  const keyPath = site.tls ? siteKey : dashboardKey;
  const root = mockDocumentRoot(site);
  const stopped = site.desired_state === 'stopped';

  if (server === 'nginx') {
    const blocks: string[] = [];
    const servingBody = nginxServingBody(site, root);
    if (stopped) {
      blocks.push(nginxServer('80', serverNames, null, null, nginxStoppedBody()));
      blocks.push(nginxServer('443 ssl http2', serverNames, certPath, keyPath, nginxStoppedBody()));
    } else if (site.tls && site.force_https) {
      blocks.push(nginxServer('80', serverNames, null, null, nginxRedirectBody('https')));
      blocks.push(nginxServer('443 ssl http2', serverNames, certPath, keyPath, servingBody));
    } else if (site.tls) {
      blocks.push(nginxServer('80', serverNames, null, null, servingBody));
      blocks.push(nginxServer('443 ssl http2', serverNames, certPath, keyPath, servingBody));
    } else {
      blocks.push(nginxServer('80', serverNames, null, null, servingBody));
      blocks.push(
        nginxServer('443 ssl http2', serverNames, dashboardCert, dashboardKey, nginxRedirectBody('http')),
      );
    }
    return `# Generated by devstack. Do not edit.\n\n${blocks.join('\n')}`;
  }

  const servingBody = (scheme: 'http' | 'https'): string =>
    site.kind === 'node'
      ? apacheNodeBody(site.http_port || 41_000, scheme)
      : apacheStaticBody(root, site.spa_fallback);
  const blocks: string[] = [];
  if (stopped) {
    blocks.push(apacheVHost(80, site, null, null, apacheStoppedBody()));
    blocks.push(apacheVHost(443, site, certPath, keyPath, apacheStoppedBody()));
  } else if (site.tls && site.force_https) {
    blocks.push(apacheVHost(80, site, null, null, apacheRedirectBody('https')));
    blocks.push(apacheVHost(443, site, certPath, keyPath, servingBody('https')));
  } else if (site.tls) {
    blocks.push(apacheVHost(80, site, null, null, servingBody('http')));
    blocks.push(apacheVHost(443, site, certPath, keyPath, servingBody('https')));
  } else {
    blocks.push(apacheVHost(80, site, null, null, servingBody('http')));
    blocks.push(apacheVHost(443, site, dashboardCert, dashboardKey, apacheRedirectBody('http')));
  }
  return `# Generated by devstack. Do not edit.\n\n${blocks.join('\n')}`;
}

const directories: Record<string, string[]> = {
  '/': ['home', 'opt', 'tmp', 'var'],
  '/home': ['dev'],
  '/home/dev': ['.devstack', 'Documents', 'Downloads', 'projects'],
  '/home/dev/.devstack': ['logs', 'runtimes'],
  '/home/dev/.devstack/logs': ['api', 'blog', 'shop'],
  '/home/dev/.devstack/logs/api': [],
  '/home/dev/.devstack/logs/blog': [],
  '/home/dev/.devstack/logs/shop': [],
  '/home/dev/.devstack/runtimes': ['node'],
  '/home/dev/.devstack/runtimes/node': ['22.20.0', '24.9.0', '24.11.0'],
  '/home/dev/.devstack/runtimes/node/22.20.0': [],
  '/home/dev/.devstack/runtimes/node/24.9.0': [],
  '/home/dev/.devstack/runtimes/node/24.11.0': [],
  '/home/dev/Documents': ['notes', 'specs'],
  '/home/dev/Documents/notes': [],
  '/home/dev/Documents/specs': [],
  '/home/dev/Downloads': [],
  '/home/dev/projects': ['api', 'blog', 'design-system', 'scratch', 'shop'],
  '/home/dev/projects/api': ['src', 'test'],
  '/home/dev/projects/api/src': ['handlers', 'middleware'],
  '/home/dev/projects/api/src/handlers': [],
  '/home/dev/projects/api/src/middleware': [],
  '/home/dev/projects/api/test': [],
  '/home/dev/projects/blog': ['public', 'src'],
  '/home/dev/projects/blog/public': ['assets'],
  '/home/dev/projects/blog/public/assets': [],
  '/home/dev/projects/blog/src': [],
  '/home/dev/projects/design-system': ['pages'],
  '/home/dev/projects/design-system/pages': [],
  '/home/dev/projects/shop': ['node_modules', 'public', 'src'],
  '/home/dev/projects/shop/node_modules': ['.bin'],
  '/home/dev/projects/shop/node_modules/.bin': [],
  '/home/dev/projects/shop/public': [],
  '/home/dev/projects/shop/src': ['components', 'routes'],
  '/home/dev/projects/shop/src/components': [],
  '/home/dev/projects/shop/src/routes': [],
  '/home/dev/projects/scratch': ['public'],
  '/home/dev/projects/scratch/public': [],
  '/opt': ['devstack'],
  '/opt/devstack': ['bin'],
  '/opt/devstack/bin': [],
  '/tmp': ['devstack-demo'],
  '/tmp/devstack-demo': ['public'],
  '/tmp/devstack-demo/public': [],
  '/var': ['log'],
  '/var/log': [],
};

function findSite(ref: string): Site {
  const decoded = ref;
  const site = sites.find((entry) => entry.id === decoded || entry.name === decoded);
  if (!site) throw new ApiError('NOT_FOUND', `No site named “${decoded}”.`, 404);
  return site;
}

function nodeMajorOf(version: string): number {
  return Number.parseInt(version.split('.')[0] ?? '0', 10);
}

function exactNodeVersion(value: string): string {
  return value.trim().replace(/^node@/, '').replace(/-linux-x64$/, '');
}

function findNodeBuild(value: string): RuntimeBuild | undefined {
  const wanted = exactNodeVersion(value);
  return runtimes.find(
    (entry) =>
      entry.kind === 'node' &&
      (entry.version === wanted || entry.id === value || exactNodeVersion(entry.id) === wanted),
  );
}

function newestNodeBuild(): RuntimeBuild | undefined {
  return runtimes
    .filter((entry) => entry.kind === 'node')
    .slice()
    .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true }))[0];
}

function nodeDefaultBuild(): RuntimeBuild | undefined {
  // Mirror ResolveNodeDefaultStatus: a configured default that is not
  // installed reports no build instead of falling back to the newest one.
  if (!nodeDefaultConfigured) return newestNodeBuild();
  return findNodeBuild(nodeDefaultConfigured);
}

function nodeDefaultStatus(): NodeDefaultStatus {
  const build = nodeDefaultBuild();
  return {
    configured: nodeDefaultConfigured || undefined,
    build: build ? clone(build) : undefined,
    shim_dir: '/home/dev/.devstack/tools/node-default/bin',
    shim_installed: Boolean(build),
    source: nodeDefaultConfigured ? 'setting' : 'newest',
  };
}

/** Test fixture hook for the configured default, including stale values. */
export function setMockNodeDefaultForTests(value: string): void {
  nodeDefaultConfigured = value.trim();
}

function nodeToolchainFor(site: Site): NodeToolchain | undefined {
  const pin = site.node_version.trim();
  if (pin) {
    const build = findNodeBuild(pin);
    if (build) {
      return {
        version: build.version,
        build_id: build.id,
        source: 'site',
        installed: true,
      };
    }
    return { version: exactNodeVersion(pin), source: 'site', installed: false };
  }
  if (site.kind === 'node') return undefined;
  const build = nodeDefaultBuild();
  if (!build) return undefined;
  return { version: build.version, build_id: build.id, source: 'global', installed: true };
}

/** Sites carry a response-only toolchain summary, like the real daemon. */
function cloneSite(site: Site): Site {
  const copy = clone(site);
  copy.node_toolchain = nodeToolchainFor(site);
  return copy;
}

function nodeReleaseEntry(version: string): CatalogEntry | undefined {
  const wanted = exactNodeVersion(version);
  const release = nodeReleases.find((entry) => entry.version === wanted);
  if (!release) return undefined;
  return {
    id: `node@${release.version}`,
    kind: 'node',
    version: release.version,
    build: 'linux-x64',
    url: `https://nodejs.org/dist/v${release.version}/node-v${release.version}-linux-x64.tar.xz`,
    sha256: mockHash(`node-${release.version}`),
    size: 50_000_000 + nodeMajorOf(release.version) * 100_000,
  };
}

function nodeReleaseSnapshot(all = false, major?: number): NodeReleaseSnapshot {
  const selected = major
    ? nodeReleases.filter((entry) => nodeMajorOf(entry.version) === major)
    : nodeReleases;
  const groups = new Map<number, NodeRelease[]>();
  for (const release of selected) {
    const key = nodeMajorOf(release.version);
    groups.set(key, [...(groups.get(key) ?? []), release]);
  }
  const majors = Array.from(groups.entries())
    .sort(([a], [b]) => b - a)
    .map(([key, releases]) => ({
      major: key,
      latest: releases[0],
      releases: all ? releases : [releases[0]],
    }));
  return {
    majors,
    all: selected,
    source: 'cache',
    cached_at: iso(-1000 * 60 * 12),
    stale: false,
  };
}

function findService(id: string): ServiceView {
  const service = mockServices.find((entry) => entry.id === id);
  if (!service) throw new ApiError('NOT_FOUND', `service “${id}” not found`, 404);
  return service;
}

function serviceDisplayName(service: ServiceView): string {
  return service.display_name || service.id;
}

function newestPHPBuild(): RuntimeBuild | undefined {
  return runtimes
    .filter((entry) => entry.kind === 'php')
    .slice()
    .sort(
      (a, b) =>
        b.version.localeCompare(a.version, undefined, { numeric: true }) ||
        phpRevisionOf(b.id) - phpRevisionOf(a.id),
    )[0];
}

function phpDefaultBuild(): RuntimeBuild | undefined {
  if (!phpDefaultConfigured) return newestPHPBuild();
  return (
    runtimes.find((entry) => entry.kind === 'php' && entry.id === phpDefaultConfigured) ??
    newestPHPBuild()
  );
}

function phpDefaultStatus(): PHPDefaultStatus {
  const build = phpDefaultBuild();
  return {
    configured: phpDefaultConfigured || undefined,
    build: build ? clone(build) : undefined,
    global_ini: '/home/dev/.devstack/tools/php-default/php.ini',
    shim_dir: '/home/dev/.devstack/tools/php-default/bin',
    shim_installed: toolStatuses.find((entry) => entry.id === 'php-default')?.installed ?? false,
  };
}

function portsFor(site: Site, index: number): { http: number; fastcgi: number } {
  if (site.kind === 'php') return { http: 0, fastcgi: 43_000 + index };
  return { http: 41_000 + index + 1, fastcgi: 0 };
}

function setStatus(site: Site, status: SiteStatus): void {
  site.status = status;
  site.desired_state = status === 'stopped' ? 'stopped' : 'running';
  site.updated_at = new Date().toISOString();
  if (status === 'running') {
    const index = sites.findIndex((entry) => entry.id === site.id);
    const ports = portsFor(site, Math.max(index, 0));
    site.http_port = ports.http;
    site.fastcgi_port = ports.fastcgi;
  } else if (status === 'stopped' || status === 'crashed' || status === 'error') {
    site.http_port = 0;
    site.fastcgi_port = 0;
  }
}

const logTemplates: Record<string, Omit<LogLine, 'time'>[]> = {
  blog: [
    { source: 'proxy', text: 'GET / 200 — 4.1ms — 127.0.0.1' },
    { source: 'proxy', text: 'GET /assets/app.css 200 — 1.2ms' },
    { source: 'app', text: 'Serving public/ with cache-control: no-store' },
    { source: 'supervisor', text: 'heartbeat ok · pid 48213 · uptime 2h 14m' },
  ],
  shop: [
    { source: 'app', text: 'VITE v6.1.0  ready in 412 ms' },
    { source: 'app', text: '->  Local:   http://127.0.0.1:41002/' },
    { source: 'proxy', text: 'GET /api/products 200 — 18.7ms' },
    { source: 'app', text: 'hmr update /src/routes/+page.svelte' },
    { source: 'supervisor', text: 'heartbeat ok · pid 49307 · uptime 46m' },
  ],
  api: [
    { source: 'supervisor', text: 'process exited with code 1; restarting in 8s (attempt 4/5)' },
    { source: 'app', text: 'Error: connect ECONNREFUSED 127.0.0.1:5432' },
    { source: 'app', text: '    at TCPConnectWrap.afterConnect [as oncomplete] (node:net:1611:16)' },
    { source: 'supervisor', text: 'readiness probe failed after 60s; marking site crashed' },
  ],
};

let logCursor: Record<string, number> = {};

export const mockApi = {
  async health(): Promise<Health> {
    await wait(30, 80);
    return {
      status: 'ok',
      version: settings.version ?? '0.1.0',
      helper: 'ok',
      helper_version: '0.1.0',
      ca_fingerprint: '8F:2A:9C:71:D4:E0:63:1B:B8:05:AE:47:22:9D:6C:F1',
    };
  },

  async getSession(): Promise<Session> {
    await wait(20, 60);
    return clone(sessions);
  },

  async listSites(): Promise<Site[]> {
    await wait();
    return sites.map((site) => cloneSite(site));
  },

  async getSite(ref: string): Promise<Site> {
    await wait();
    return cloneSite(findSite(ref));
  },

  async createSite(request: CreateSiteRequest): Promise<Site> {
    await wait();
    const name = request.name.trim();
    if (!name) throw new ApiError('INVALID_REQUEST', 'Site name is required.', 400);
    if (sites.some((site) => site.name === name)) {
      throw new ApiError('DOMAIN_CONFLICT', `A site named “${name}” already exists.`, 409);
    }
    const sourceMode = request.source_mode ?? 'local';
    const projectPath = request.path.trim();
    const validPath = sourceMode === 'local'
      ? projectPath.startsWith('/')
      : sourceMode === 'windows'
        ? /^[cC]:[\\/]/.test(projectPath)
        : /^\\\\wsl(?:\.localhost|\$)\\[^\\]+\\.+/.test(projectPath);
    if (!validPath) {
      throw new ApiError('INVALID_REQUEST', 'Project path must be absolute and match the selected source mode.', 400);
    }
    if (sourceMode === 'local' && !directories[projectPath]) {
      throw new ApiError('INVALID_REQUEST', 'Project path must be an existing readable directory.', 400);
    }
    let phpBuild: RuntimeBuild | undefined;
    let nodePin = '';
    if (request.node_version?.trim()) {
      const runtime = findNodeBuild(request.node_version);
      if (!runtime) {
        throw new ApiError(
          'NODE_VERSION_NOT_INSTALLED',
          `Node ${exactNodeVersion(request.node_version)} is not installed. Install it first.`,
          409,
        );
      }
      nodePin = runtime.version;
    }
    if (request.kind === 'node' && !nodePin) {
      throw new ApiError(
        'NODE_VERSION_REQUIRED',
        'Node sites require an explicit Node version.',
        400,
      );
    }
    if (request.kind === 'php') {
      phpBuild = request.php_build
        ? runtimes.find((entry) => entry.kind === 'php' && entry.id === request.php_build)
        : runtimes
            .filter(
              (entry) =>
                entry.kind === 'php' &&
                (!request.php_version || phpLineOf(entry.version) === request.php_version),
            )
            .sort(
              (a, b) =>
                b.version.localeCompare(a.version, undefined, { numeric: true }) ||
                phpRevisionOf(b.id) - phpRevisionOf(a.id),
            )[0];
      if (!phpBuild) {
        throw new ApiError(
          'RUNTIME_MISSING',
          `PHP ${request.php_version ?? ''} is not installed. Install a PHP runtime first.`,
          409,
        );
      }
    }
    if (request.build) {
      if (!request.build.node_version?.trim() || !request.build.command?.trim()) {
        throw new ApiError(
          'INVALID_REQUEST',
          'sites: build command and node version are required together (invalid build profile)',
          400,
        );
      }
      if (!request.build.output?.trim()) {
        throw new ApiError(
          'INVALID_REQUEST',
          'sites: build output is required for a build profile',
          400,
        );
      }
    }
    const buildOutput =
      request.kind === 'static' && request.build?.output?.trim()
        ? `${projectPath.replace(/\/+$/, '')}/${request.build.output.trim().replace(/^\/+/, '')}`
        : '';
    const buildOutputMissing = Boolean(
      buildOutput && !directories[buildOutput],
    );
    const phpVersion = phpBuild ? phpLineOf(phpBuild.version) : '';
    const id = `s_${name.replace(/[^a-z0-9]/g, '')}_${sites.length + 1}`;
    const site: Site = {
      id,
      name,
      domain: `${name}.test`,
      www: request.www ?? false,
      backend: 'local',
      source_mode: sourceMode,
      host_path: projectPath,
      backend_path: sourceMode === 'windows'
        ? `/mnt/c/${projectPath.slice(3).replace(/\\/g, '/')}`
        : sourceMode === 'wsl'
          ? `/${projectPath.replace(/\\/g, '/').split('/').slice(4).join('/')}`
          : projectPath,
      kind: request.kind,
      php_version: phpVersion,
      php_build: phpBuild?.id ?? '',
      php_extensions:
        request.kind === 'php' ? { ...(request.php_extensions ?? {}) } : undefined,
      php_ini: request.kind === 'php' ? { ...(request.php_ini ?? {}) } : undefined,
      php_ini_raw: request.kind === 'php' ? (request.php_ini_raw ?? '') : '',
      php_config_hash:
        request.kind === 'php' ? mockHash(`${phpBuild?.id ?? ''}${request.php_ini_raw ?? ''}`) : '',
      node_version: nodePin,
      command: request.kind === 'node' ? (request.command ?? '') : '',
      env: request.env ?? {},
      document_root: request.document_root ?? '',
      front_controller: request.front_controller ?? request.kind === 'php',
      front_controller_file: request.front_controller_file ?? 'index.php',
      front_controller_mode: request.front_controller_mode ?? 'auto',
      apache_allow_override: request.apache_allow_override ?? 'all',
      spa_fallback: request.spa_fallback ?? false,
      spa_fallback_file: request.spa_fallback_file ?? 'index.html',
      build: request.build,
      tls: request.tls ?? true,
      force_https: request.force_https ?? false,
      desired_state: buildOutputMissing ? 'stopped' : 'running',
      status: buildOutputMissing ? 'stopped' : 'starting',
      http_port: 0,
      fastcgi_port: 0,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      certificate: request.tls
        ? {
            not_after: iso(days(90)),
            domains: [`${name}.test`, ...(request.www ? [`www.${name}.test`] : [])],
            serial: '7c:11:8e:3a:bd:02',
          }
        : undefined,
    };
    if (request.database) {
      await mockDatabaseApi.createSiteDatabase(id, request.database);
    }
    sites.push(site);
    emitEvent(
      'site.created',
      site.id,
      buildOutputMissing
        ? `${site.name}: build output ${request.build?.output} is missing; site created stopped`
        : `${site.name}.test was added`,
    );
    if (!buildOutputMissing) {
      setTimeout(() => {
        const current = sites.find((entry) => entry.id === id);
        if (current) {
          setStatus(current, 'running');
          emitEvent('site.running', current.id, `${current.name} is running`);
        }
      }, 900);
    }
    return cloneSite(site);
  },

  async updateSite(ref: string, request: UpdateSiteRequest): Promise<Site> {
    await wait();
    const site = findSite(ref);
    if (request.node_version !== undefined) {
      const pin = (request.node_version ?? '').trim();
      if (!pin && site.kind === 'node') {
        throw new ApiError('INVALID_REQUEST', 'Node sites require an explicit Node version.', 400);
      }
      if (pin && !findNodeBuild(pin)) {
        throw new ApiError(
          'NODE_VERSION_NOT_INSTALLED',
          `Node ${exactNodeVersion(pin)} is not installed. Install it first.`,
          409,
        );
      }
    }
    Object.assign(site, request, { updated_at: new Date().toISOString() });
    if (request.node_version !== undefined) {
      const pin = (request.node_version ?? '').trim();
      site.node_version = pin ? (findNodeBuild(pin)?.version ?? pin) : '';
    }
    emitEvent('site.updated', site.id, `${site.name} was updated`);
    return cloneSite(site);
  },

  async changeSiteSource(ref: string, mode: SourceMode, path: string): Promise<Site> {
    await wait();
    const site = findSite(ref);
    if (site.status === 'running') {
      throw new ApiError('CONFLICT', 'Stop the site before changing source modes.', 409);
    }
    if (!path.trim()) {
      throw new ApiError('INVALID_REQUEST', 'Destination path is required.', 400);
    }
    site.source_mode = mode;
    if (mode === 'wsl') {
      site.backend_path = path.trim();
    } else if (mode === 'windows') {
      site.host_path = path.trim();
    }
    site.updated_at = new Date().toISOString();
    emitEvent('site.updated', site.id, `${site.name} source mode changed`);
    return cloneSite(site);
  },

  async deleteSite(ref: string): Promise<void> {
    await wait();
    const site = findSite(ref);
    const { links } = await mockDatabaseApi.listSiteDatabases(site.id);
    for (const link of links) {
      await mockDatabaseApi.unlinkSiteDatabase(site.id, link.id);
    }
    const index = sites.findIndex((entry) => entry.id === site.id);
    if (index >= 0) sites.splice(index, 1);
    delete logTemplates[site.name];
    emitEvent('site.deleted', site.id, `${site.name} was removed`);
  },

  async startSite(ref: string): Promise<void> {
    await wait();
    const site = findSite(ref);
    if (site.status === 'running') return;
    setStatus(site, 'running');
    emitEvent('site.started', site.id, `${site.name} is running`);
  },

  async stopSite(ref: string): Promise<void> {
    await wait();
    const site = findSite(ref);
    setStatus(site, 'stopped');
    emitEvent('site.stopped', site.id, `${site.name} was stopped`);
  },

  async restartSite(ref: string): Promise<void> {
    await wait();
    const site = findSite(ref);
    site.status = 'starting';
    site.updated_at = new Date().toISOString();
    setStatus(site, 'running');
    emitEvent('site.restarted', site.id, `${site.name} was restarted`);
  },

  async listRuntimes(): Promise<RuntimeBuild[]> {
    await wait();
    return clone(runtimes);
  },

  async availableRuntimes(): Promise<CatalogEntry[]> {
    await wait();
    return clone(catalog);
  },

  async listNodeReleases(query: {
    major?: number;
    all?: boolean;
    offline?: boolean;
  } = {}): Promise<NodeReleaseSnapshot> {
    await wait();
    return clone(nodeReleaseSnapshot(query.all === true, query.major));
  },

  async getNodeRelease(version: string): Promise<NodeReleasePreflight> {
    await wait();
    const wanted = exactNodeVersion(version);
    const entry = nodeReleaseEntry(wanted);
    const release = nodeReleases.find((item) => item.version === wanted);
    if (!entry || !release) {
      throw new ApiError('NOT_FOUND', `Node ${wanted} is not a known release.`, 404);
    }
    return clone({ entry, release });
  },

  async getNodeDefault(): Promise<NodeDefaultStatus> {
    await wait();
    return clone(nodeDefaultStatus());
  },

  async refreshNodeReleases(): Promise<NodeReleaseSnapshot> {
    await wait(250, 450);
    return clone({
      ...nodeReleaseSnapshot(false),
      source: 'live',
      cached_at: new Date().toISOString(),
      stale: false,
      warning: undefined,
    });
  },

  async setNodeDefault(value: string, _force = false): Promise<NodeDefaultStatus> {
    await wait();
    const wanted = value.trim();
    if (!wanted) {
      nodeDefaultConfigured = '';
      return clone(nodeDefaultStatus());
    }
    const build = findNodeBuild(wanted);
    if (!build) {
      throw new ApiError(
        'NODE_DEFAULT_NOT_INSTALLED',
        `Node ${exactNodeVersion(wanted)} is not installed.`,
        409,
      );
    }
    nodeDefaultConfigured = wanted;
    return clone(nodeDefaultStatus());
  },

  async clearNodeDefault(): Promise<NodeDefaultStatus> {
    await wait();
    nodeDefaultConfigured = '';
    return clone(nodeDefaultStatus());
  },

  async installRuntime(id: string): Promise<RuntimeBuild> {
    await wait(250, 450);
    const entry = catalog.find((item) => item.id === id) ?? nodeReleaseEntry(id);
    if (!entry) throw new ApiError('NOT_FOUND', `Runtime “${id}” is not in the catalog.`, 404);
    const installed =
      entry.kind === 'php'
        ? runtimes.some((item) => item.id === entry.id)
        : runtimes.some((item) => item.kind === entry.kind && item.version === entry.version);
    if (installed) {
      throw new ApiError(
        'RUNTIME_ALREADY_INSTALLED',
        `${entry.kind === 'node' ? 'Node' : 'PHP'} ${entry.version} is already installed.`,
        409,
      );
    }
    runtimes.push({
      id: entry.id,
      kind: entry.kind,
      version: entry.version,
      backend: 'local',
      path:
        entry.kind === 'php'
          ? `/home/dev/.devstack/runtimes/php/${entry.version}-r${entry.revision ?? 1}`
          : `/home/dev/.devstack/runtimes/${entry.kind}/${entry.version}`,
      checksum: entry.sha256,
      installed_at: new Date().toISOString(),
      size: entry.size,
    });
    emitEvent(
      'runtime.installed',
      undefined,
      `${entry.kind === 'node' ? 'Node' : 'PHP'} ${entry.version} was installed`,
    );
    return clone(runtimes[runtimes.length - 1]);
  },

  async removeRuntime(buildID: string): Promise<void> {
    await wait();
    const index = runtimes.findIndex((item) => item.id === buildID);
    if (index < 0) throw new ApiError('NOT_FOUND', 'Runtime build was not found.', 404);
    const runtime = runtimes[index];
    if (runtime.kind === 'php') {
      const line = phpLineOf(runtime.version);
      const usedBy = sites
        .filter(
          (site) =>
            site.kind === 'php' &&
            (site.php_build === runtime.id ||
              (!site.php_build && site.php_version === line)),
        )
        .map((site) => site.name)
        .sort();
      if (usedBy.length > 0) {
        throw new ApiError(
          'PHP_VERSION_IN_USE',
          `PHP ${runtime.version} is used by ${usedBy.join(', ')}.`,
          409,
          { build: runtime.id, sites: usedBy },
        );
      }
    } else {
      const usedBy = sites.filter(
        (site) => site.kind === 'node' && site.node_version === runtime.version,
      );
      if (usedBy.length > 0) {
        throw new ApiError(
          'RUNTIME_IN_USE',
          `Node ${runtime.version} is used by ${usedBy.map((site) => site.name).join(', ')}.`,
          409,
        );
      }
    }
    runtimes.splice(index, 1);
    emitEvent(
      'runtime.removed',
      undefined,
      `${runtime.kind === 'php' ? 'PHP' : 'Node'} ${runtime.version} was removed`,
    );
  },

  async getPHPInfo(ref: string): Promise<PHPInfo> {
    await wait();
    const site = findSite(ref);
    if (site.kind !== 'php') {
      throw new ApiError('INVALID_REQUEST', `${site.name} is not a PHP site.`, 400);
    }
    const build = runtimes.find((entry) => entry.kind === 'php' && entry.id === site.php_build);
    if (!build) {
      throw new ApiError(
        'RUNTIME_MISSING',
        `PHP build ${site.php_build || site.php_version} is not installed.`,
        409,
      );
    }
    const line = phpLineOf(build.version);
    const defaults = phpDefaults[line] ?? phpDefaults['8.4'];
    const builtins = new Set(phpBuiltinsFor(line));
    const available = phpCatalogExtensions
      .filter((extension) => extension.versions.includes(line) && !builtins.has(extension.name))
      .map((extension) => clone(extension));
    const generated = renderPHPIni(site, defaults);
    return {
      site: clone(site),
      build: clone(build),
      defaults: clone(defaults),
      extensions: resolvePHPExtensions(
        phpLineOf(build.version),
        defaults.extensions,
        site.php_extensions,
      ),
      available_extensions: available,
      builtins: phpBuiltinsFor(line),
      ini: clone(site.php_ini ?? {}),
      ini_raw: site.php_ini_raw,
      generated_ini: generated,
      fpm_config: `[global]\nerror_log = /proc/self/fd/2\n\n[www]\nlisten = 127.0.0.1:${site.fastcgi_port || 43000}\nuser = dev\npm = ondemand\npm.max_children = 5\nchdir = ${site.backend_path}/${site.document_root || '.'}\n`,
      config_hash: site.php_config_hash || mockHash(generated),
    };
  },

  async applyPHP(ref: string, change: PHPChange): Promise<Site> {
    await wait(250, 450);
    const site = findSite(ref);
    if (site.kind !== 'php') {
      throw new ApiError('INVALID_REQUEST', `${site.name} is not a PHP site.`, 400);
    }
    const targetBuild = change.build
      ? runtimes.find((entry) => entry.kind === 'php' && entry.id === change.build)
      : undefined;
    if (change.build && !targetBuild) {
      throw new ApiError('RUNTIME_MISSING', `PHP build ${change.build} is not installed.`, 409);
    }
    if (targetBuild?.id === FAILING_PHP_BUILD) {
      throw new ApiError(
        'PHP_CONFIG_INVALID',
        `PHP ${targetBuild.version} failed validation; the site stayed on PHP ${site.php_version}.`,
        422,
        {
          line: 4,
          field: 'build',
          message: 'The staged FPM master exited before it became ready.',
          output: `php-fpm: failed to load ${targetBuild.path}/etc/php.ini (line 4)\nphp-fpm: test failed; previous master kept`,
        },
      );
    }
    if (change.version) site.php_version = change.version;
    if (targetBuild) {
      site.php_version = phpLineOf(targetBuild.version);
      site.php_build = targetBuild.id;
    }
    if (change.extensions !== undefined) site.php_extensions = { ...change.extensions };
    if (change.ini !== undefined) site.php_ini = { ...change.ini };
    if (change.ini_raw !== undefined) site.php_ini_raw = change.ini_raw;
    const defaults = phpDefaults[site.php_version] ?? phpDefaults['8.4'];
    site.php_config_hash = mockHash(
      `${site.php_build}|${site.php_ini_raw}|${JSON.stringify(site.php_ini ?? {})}|${JSON.stringify(site.php_extensions ?? {})}|${JSON.stringify(defaults)}`,
    );
    site.updated_at = new Date().toISOString();
    emitEvent('site.updated', site.id, `${site.name} PHP configuration was applied`);
    return clone(site);
  },

  async phpExtensions(version: string): Promise<PHPExtension[]> {
    await wait(80, 180);
    if (!PHP_LINES.includes(version)) {
      throw new ApiError('INVALID_REQUEST', `Unsupported PHP version “${version}”.`, 400);
    }
    const builtins = new Set(phpBuiltinsFor(version));
    return clone(
      phpCatalogExtensions
        .filter((extension) => extension.versions.includes(version) && !builtins.has(extension.name))
        .sort((a, b) => a.name.localeCompare(b.name)),
    );
  },

  async getPHPDefaults(version: string): Promise<PHPDefaults> {
    await wait(80, 180);
    const defaults = phpDefaults[version];
    if (!defaults) {
      throw new ApiError('NOT_FOUND', `No PHP ${version} defaults are available.`, 404);
    }
    return clone(defaults);
  },

  async savePHPDefaults(
    version: string,
    change: Pick<PHPDefaults, 'ini' | 'extensions'>,
  ): Promise<PHPDefaultsResult> {
    await wait(250, 450);
    const current = phpDefaults[version];
    if (!current) {
      throw new ApiError('NOT_FOUND', `No PHP ${version} defaults are available.`, 404);
    }
    const saved: PHPDefaults = {
      version,
      ini: { ...current.ini, ...(change.ini ?? {}) },
      extensions: { ...current.extensions, ...(change.extensions ?? {}) },
    };
    phpDefaults = { ...phpDefaults, [version]: saved };
    const affected = sites.filter(
      (site) => site.kind === 'php' && site.php_version === version,
    );
    const results: PHPMigrationResult[] = affected.map((site) => {
      site.php_config_hash = mockHash(`${site.id}|${JSON.stringify(saved)}`);
      return { site: site.name, ok: true };
    });
    emitEvent('php.defaults', undefined, `PHP ${version} defaults were saved`);
    return { defaults: clone(saved), sites: results };
  },

  async migratePHP(request: PHPMigrationRequest): Promise<PHPMigrationResult[]> {
    await wait(250, 450);
    const target = runtimes.find(
      (entry) => entry.kind === 'php' && entry.id === request.to_build,
    );
    if (!target) {
      throw new ApiError('RUNTIME_MISSING', `PHP build ${request.to_build} is not installed.`, 409);
    }
    const affected = sites.filter(
      (site) => site.kind === 'php' && site.php_version === request.from_version,
    );
    if (target.id === FAILING_PHP_BUILD) {
      return affected.map((site) => ({
        site: site.name,
        ok: false,
        error: `php.ini failed validation on line 4 for ${site.name}`,
      }));
    }
    return affected.map((site) => {
      site.php_version = phpLineOf(target.version);
      site.php_build = target.id;
      site.php_config_hash = mockHash(`${site.id}|${target.id}`);
      site.updated_at = new Date().toISOString();
      return { site: site.name, ok: true };
    });
  },

  async phpExec(ref: string, argv: string[]): Promise<PHPExecResult> {
    await wait(80, 200);
    const site = findSite(ref);
    if (site.kind !== 'php') {
      throw new ApiError('INVALID_REQUEST', `${site.name} is not a PHP site.`, 400);
    }
    if (argv.includes('--broken')) {
      return { exit_code: 1, stdout: '', stderr: 'PHP Fatal error: broken snippet\n' };
    }
    const build = runtimes.find((entry) => entry.kind === 'php' && entry.id === site.php_build);
    return {
      exit_code: 0,
      stdout: `PHP ${build?.version ?? site.php_version} (cli) (built: devstack mock)\nCopyright (c) The PHP Group\n`,
      stderr: '',
    };
  },

  async buildSite(ref: string): Promise<BuildResult> {
    await wait(500, 800);
    const site = findSite(ref);
    if (site.kind !== 'static') {
      throw new ApiError(
        'INVALID_REQUEST',
        `build: invalid build setup: site “${site.name}” is not a static site`,
        400,
      );
    }
    if (!site.build?.command) {
      throw new ApiError(
        'INVALID_REQUEST',
        `build: invalid build setup: site “${site.name}” has no build profile`,
        400,
      );
    }
    const outputPath = site.build.output
      ? `${site.host_path.replace(/\/+$/, '')}/${site.build.output.replace(/^\/+/, '')}`
      : site.host_path;
    emitEvent('site.built', site.id, `${site.name} build completed`);
    return {
      site: site.name,
      success: true,
      command: site.build.command,
      duration_ms: 4280,
      log_path: `/home/dev/.devstack/logs/builds/${site.name}/build.log`,
      output_path: outputPath,
      restored: false,
      snapshot_skipped: false,
    };
  },

  async getBuildLogs(ref: string, tail = 200): Promise<BuildLogs> {
    await wait(120, 220);
    const site = findSite(ref);
    const lines = [
      `> ${site.name}@1.0.0 build`,
      `> ${site.build?.command ?? 'npm run build'}`,
      '',
      'vite v6.1.0 building for production...',
      '✓ 42 modules transformed.',
      'dist/index.html                  0.48 kB',
      'dist/assets/index-7c1a2f.css      9.14 kB',
      'dist/assets/index-3b5e8d.js      48.22 kB',
      '✓ built in 4.28s',
    ];
    const content = lines.slice(Math.max(0, lines.length - Math.max(tail, 1))).join('\n');
    return {
      site: site.name,
      path: `/home/dev/.devstack/logs/builds/${site.name}/build.log`,
      content,
    };
  },

  async listTools(): Promise<ToolStatus[]> {
    await wait(80, 160);
    return clone(toolStatuses);
  },

  async installTool(id: string, _force = false): Promise<ToolStatus> {
    await wait(300, 500);
    if (id !== 'php-default' && id !== 'composer') {
      throw new ApiError('INVALID_REQUEST', `tooling: unsupported tool: “${id}”`, 400);
    }
    let status = toolStatuses.find((entry) => entry.id === id);
    if (!status) {
      status = { id, installed: false, managed: true };
      toolStatuses.push(status);
    }
    status.installed = true;
    status.managed = true;
    status.conflict = undefined;
    status.backup = undefined;
    status.wrapper_path =
      id === 'composer' ? '/usr/local/bin/composer' : '/usr/local/bin/php';
    if (id === 'composer') {
      status.target = '/home/dev/.devstack/tools/composer/composer.phar';
      status.version = '2.8.4';
    } else {
      const build = phpDefaultBuild();
      status.target = `${build?.path ?? '/home/dev/.devstack/runtimes/php/8.4.4-r1'}/bin/php`;
      status.version = build?.version ?? '8.4.4';
    }
    emitEvent('tool.installed', undefined, `${id} was installed`);
    return clone(status);
  },

  async removeTool(id: string): Promise<ToolStatus> {
    await wait(240, 420);
    const status = toolStatuses.find((entry) => entry.id === id);
    if (!status) throw new ApiError('NOT_FOUND', `tool “${id}” is not managed`, 404);
    status.installed = false;
    status.managed = false;
    status.wrapper_path = undefined;
    status.target = undefined;
    status.version = undefined;
    emitEvent('tool.removed', undefined, `${id} was removed`);
    return clone(status);
  },

  async getPHPDefault(): Promise<PHPDefaultStatus> {
    await wait(80, 160);
    return clone(phpDefaultStatus());
  },

  async setPHPDefault(value: string, _force = false): Promise<PHPDefaultStatus> {
    await wait(300, 500);
    const match = runtimes.find(
      (entry) =>
        entry.kind === 'php' &&
        (entry.id === value || phpLineOf(entry.version) === value),
    );
    if (!match) {
      throw new ApiError(
        'RUNTIME_MISSING',
        `tooling: PHP default “${value}” is not installed`,
        409,
      );
    }
    phpDefaultConfigured = match.id;
    const status = toolStatuses.find((entry) => entry.id === 'php-default');
    if (status) {
      status.installed = true;
      status.managed = true;
      status.target = `${match.path}/bin/php`;
      status.version = match.version;
    }
    emitEvent('php.default', undefined, `PHP ${match.version} is the CLI default`);
    return clone(phpDefaultStatus());
  },

  async clearPHPDefault(): Promise<PHPDefaultStatus> {
    await wait(260, 440);
    phpDefaultConfigured = '';
    emitEvent('php.default', undefined, 'The PHP CLI default was cleared');
    return clone(phpDefaultStatus());
  },

  async getGlobalIni(): Promise<GlobalIni> {
    await wait(80, 160);
    return {
      path: '/home/dev/.devstack/tools/php-default/php.ini',
      content: globalIniContent,
      build_id: phpDefaultBuild()?.id,
    };
  },

  async saveGlobalIni(content: string): Promise<GlobalIni> {
    await wait(220, 380);
    globalIniContent = content;
    emitEvent('php.default.ini', undefined, 'The global CLI php.ini was saved');
    return {
      path: '/home/dev/.devstack/tools/php-default/php.ini',
      content,
      build_id: phpDefaultBuild()?.id,
    };
  },

  async doctor(): Promise<DoctorReport> {
    await wait();
    return {
      checks: clone(doctorChecks),
      ok: !doctorChecks.some((check) => check.status === 'fail'),
      last_run: lastDoctorRun,
    };
  },

  async runDoctor(): Promise<DoctorReport> {
    await wait(350, 600);
    lastDoctorRun = new Date().toISOString();
    emitEvent('doctor.updated', undefined, 'Doctor checks completed');
    return {
      checks: clone(doctorChecks),
      ok: !doctorChecks.some((check) => check.status === 'fail'),
      last_run: lastDoctorRun,
    };
  },

  async repairDoctor(): Promise<DoctorReport> {
    await wait(450, 750);
    doctorChecks = doctorChecks.map((check) => {
      if (check.status === 'skipped') return check;
      if (check.id === 'trust') {
        return {
          ...check,
          status: 'ok',
          detail: 'No Firefox profile is installed; every available trust target has the CA.',
          remediation: '',
        };
      }
      return { ...check, status: 'ok', remediation: '' };
    });
    lastDoctorRun = new Date().toISOString();
    trustTargets = trustTargets.map((target) =>
      target.name === 'Firefox'
        ? { ...target, ok: true, detail: 'Firefox is not installed; nothing to configure.' }
        : target,
    );
    emitEvent('doctor.repaired', undefined, 'Doctor repair completed');
    return {
      checks: clone(doctorChecks),
      ok: true,
      last_run: lastDoctorRun,
    };
  },

  async trust(): Promise<TrustTarget[]> {
    await wait();
    return clone(trustTargets);
  },

  async repairTrust(): Promise<TrustTarget[]> {
    await wait(300, 500);
    trustTargets = trustTargets.map((target) =>
      target.name === 'Firefox'
        ? { ...target, ok: true, detail: 'Firefox is not installed; nothing to configure.' }
        : { ...target, ok: true },
    );
    return clone(trustTargets);
  },

  async removeTrust(): Promise<TrustTarget[]> {
    await wait(250, 450);
    trustTargets = trustTargets.map((target) => ({
      ...target,
      ok: false,
      detail: 'The devstack CA is not installed for this target.',
    }));
    emitEvent('trust.removed', undefined, 'The devstack CA was removed from trust stores');
    return clone(trustTargets);
  },

  async browseFs(path: string, _mode?: 'wsl' | 'windows' | 'local'): Promise<FsListing> {
    await wait(120, 260);
    let normalized = path.trim();
    if (!normalized) {
      throw new ApiError('INVALID_REQUEST', 'A directory path is required.', 400);
    }
    if (normalized.length > 1) normalized = normalized.replace(/\/+$/, '');
    if (!normalized.startsWith('/')) {
      throw new ApiError('INVALID_REQUEST', 'Directory path must be absolute.', 400);
    }
    const children = directories[normalized];
    if (!children) throw new ApiError('NOT_FOUND', `Directory “${normalized}” is not readable.`, 404);
    return {
      path: normalized,
      entries: children.map((name) => ({
        name,
        path: normalized === '/' ? `/${name}` : `${normalized}/${name}`,
      })),
    };
  },

  async getVhostStatus(): Promise<VhostStatus> {
    await wait(80, 180);
    return clone(vhostStatus);
  },

  async setServingMode(mode: ServingMode): Promise<VhostStatus> {
    await wait(250, 450);
    const lastSync = new Date().toISOString();
    if (mode === 'builtin') {
      vhostStatus = {
        mode: 'builtin',
        server: '',
        server_version: '',
        bridge_path: '',
        include_present: false,
        current_release: '',
        config_ok: true,
        last_sync: lastSync,
      };
    } else if (mode === 'nginx' || mode === 'apache') {
      vhostStatus = {
        mode,
        server: mode,
        server_version: mode === 'nginx' ? '1.28.0' : '2.4.62',
        bridge_path: `/etc/${mode}/devstack-vhosts.conf`,
        include_present: true,
        current_release: `/home/dev/.devstack/vhosts/${mode}/releases/20261023T120000Z`,
        config_ok: true,
        last_sync: lastSync,
      };
    } else {
      throw new ApiError('INVALID_REQUEST', 'Mode must be builtin, nginx, or apache.', 400);
    }
    settings.serving_mode = mode;
    return clone(vhostStatus);
  },

  async syncVhosts(): Promise<VhostStatus> {
    await wait(300, 500);
    vhostStatus = {
      ...vhostStatus,
      config_ok: true,
      last_error: undefined,
      last_sync: new Date().toISOString(),
    };
    emitEvent('vhost.synced', undefined, `Vhosts synced in ${vhostStatus.mode} mode`);
    return clone(vhostStatus);
  },

  async previewVhost(site: string, server: 'nginx' | 'apache'): Promise<VhostPreview> {
    await wait(180, 320);
    const found = findSite(site);
    return { server, site, content: renderVhost(found, server) };
  },

  async settings(): Promise<Settings> {
    await wait();
    return clone(settings);
  },

  async createTerminalSession(_ref: string): Promise<TerminalSession> {
    await wait(40, 90);
    return {
      ticket: `mock-terminal-${Math.random().toString(36).slice(2)}`,
      expires_at: new Date(Date.now() + 30_000).toISOString(),
    };
  },

  async openTerminal(ref: string, handlers: TerminalHandlers): Promise<TerminalConnection> {
    const site = findSite(ref);
    let closed = false;
    let line = '';
    let timer: ReturnType<typeof setTimeout> | undefined;
    const write = (text: string) => {
      if (!closed) handlers.onOutput(text);
    };
    const prompt = () => write('\r\n\x1b[36mdevstack\x1b[0m:\x1b[34m' + site.host_path + '\x1b[0m$ ');
    handlers.onStatus('connecting');
    timer = setTimeout(() => {
      if (closed) return;
      handlers.onStatus('connected');
      write('\x1b[2mMock site shell — site PHP/Node shims and cwd are simulated.\x1b[0m');
      prompt();
    }, 80);
    const connection: TerminalConnection = {
      send(data: string) {
        if (closed) return;
        for (const char of data) {
          if (char === '\r' || char === '\n') {
            const command = line.trim();
            line = '';
            if (command === 'clear') {
              write('\x1b[2J\x1b[H');
              prompt();
            } else if (command === 'exit') {
              write('\r\nlogout\r\n');
              closed = true;
              if (timer) clearTimeout(timer);
              handlers.onExit(0);
              handlers.onStatus('closed');
            } else if (command === 'pwd') {
              write(`\r\n${site.host_path}`);
              prompt();
            } else if (command === 'ls') {
              write('\r\nindex.html  package.json  src');
              prompt();
            } else if (command === 'php -v') {
              write('\r\nPHP 8.4.4 (cli) (built: Oct 20 2026)');
              prompt();
            } else if (command === 'node -v') {
              write('\r\nv22.20.0');
              prompt();
            } else if (command === '') {
              prompt();
            } else {
              write(`\r\nbash: ${command}: command not found`);
              prompt();
            }
          } else if (char === '\x7f') {
            if (line.length > 0) {
              line = line.slice(0, -1);
              write('\b \b');
            }
          } else if (char === '\x03') {
            line = '';
            write('^C');
            prompt();
          } else if (char >= ' ') {
            line += char;
            write(char);
          }
        }
      },
      resize() {
        // The mock terminal has no real PTY geometry.
      },
      close() {
        if (closed) return;
        closed = true;
        if (timer) clearTimeout(timer);
        handlers.onStatus('closed');
      },
    };
    return connection;
  },

  async listServiceConfig(id: string): Promise<ServiceConfigList> {
    await wait(90, 170);
    const files = configFor(id);
    return {
      installed: true,
      files: Object.entries(files)
        .map(([path, file]) => configFileView(path, file))
        .sort((a, b) => a.path.localeCompare(b.path)),
    };
  },

  async getServiceConfigContent(id: string, path: string): Promise<ServiceConfigContent> {
    await wait(70, 140);
    const file = configFor(id)[path];
    if (!file) throw new ApiError('NOT_FOUND', `Config file ${path} was not found.`, 404);
    return {
      path,
      content: file.content,
      size: new TextEncoder().encode(file.content).length,
    };
  },

  async diffServiceConfig(id: string, path: string, content: string): Promise<ServiceConfigDiff> {
    await wait(100, 180);
    const file = configFor(id)[path];
    if (!file) throw new ApiError('NOT_FOUND', `Config file ${path} was not found.`, 404);
    const before = file.content.split('\n');
    const after = content.split('\n');
    let diff = `--- a/${path}\n+++ b/${path}\n`;
    const max = Math.max(before.length, after.length);
    for (let i = 0; i < max; i += 1) {
      if (before[i] === after[i]) {
        if (before[i] !== undefined) diff += ` ${before[i]}\n`;
        continue;
      }
      if (before[i] !== undefined) diff += `-${before[i]}\n`;
      if (after[i] !== undefined) diff += `+${after[i]}\n`;
    }
    return {
      path,
      diff,
      conflict: {
        active: file.detached,
        resolution_required: file.detached,
        reason: file.detached ? 'This file has detached local edits.' : undefined,
        allowed_resolutions: ['keep', 'use', 'merge'],
      },
    };
  },

  async applyServiceConfig(
    id: string,
    body: ServiceConfigApplyRequest,
  ): Promise<ServiceConfigResult> {
    await wait(220, 380);
    const service = findService(id);
    const file = configFor(id)[body.path];
    if (!file) throw new ApiError('NOT_FOUND', `Config file ${body.path} was not found.`, 404);
    if (body.resolution === 'keep') {
      return {
        changed: false,
        action: 'apply',
        path: body.path,
        message: 'Local edits kept; no write was performed.',
      };
    }
    if (file.detached && !body.resolution) {
      throw new ApiError(
        'CONFIG_CONFLICT',
        'This file has tracked local edits; choose keep, use, or merge.',
        409,
        {
          path: body.path,
          reason: 'This file has detached local edits.',
          conflict: {
            active: true,
            resolution_required: true,
            reason: 'This file has detached local edits.',
            allowed_resolutions: ['keep', 'use', 'merge'],
          },
        },
      );
    }
    const backup = `/home/dev/.devstack/services/${id}/config/backups/config/20261026T120000Z/${body.path}`;
    file.content = body.content;
    file.modified = false;
    file.detached = body.resolution === 'merge';
    file.class = body.resolution === 'use' || body.resolution === 'merge' ? 'generated' : file.class;
    return {
      service: id,
      path: body.path,
      action: 'apply',
      changed: true,
      backup_path: backup,
      live: service.active,
      reloaded: service.active,
      validation_pending: !service.active,
      restart_required: !service.active,
      message: !service.active
        ? 'Saved. Validation is pending until the service is activated.'
        : file.detached
          ? 'Merged content saved and marked detached from future generated writes.'
          : 'Configuration validated and reloaded.',
    };
  },

  async resetServiceConfig(id: string, path: string): Promise<ServiceConfigResult> {
    await wait(180, 320);
    const service = findService(id);
    const file = configFor(id)[path];
    if (!file) throw new ApiError('NOT_FOUND', `Config file ${path} was not found.`, 404);
    file.content = file.seeded;
    file.modified = false;
    file.detached = false;
    return {
      service: id,
      path,
      action: 'reset',
      changed: true,
      live: service.active,
      backup_path: `/home/dev/.devstack/services/${id}/backups/config/20261026T120500Z/${path}`,
      message: 'Restored the last known backup for this file.',
    };
  },

  async getPerformance(): Promise<PerformanceState> {
    await wait(100, 200);
    return clone(mockPerformance);
  },

  async setPerformanceProfile(
    profile: PerformanceProfile,
    custom?: PerformanceValues,
  ): Promise<PerformanceState> {
    await wait(120, 220);
    if (profile === 'custom' && !custom) {
      throw new ApiError('INVALID_REQUEST', 'Custom profile values are required.', 400);
    }
    if (profile !== 'balanced' && profile !== 'throughput' && profile !== 'custom') {
      throw new ApiError('INVALID_REQUEST', 'Profile must be balanced, throughput, or custom.', 400);
    }
    mockPerformance = {
      ...mockPerformance,
      profile,
      custom: profile === 'custom' ? clone(custom) : undefined,
      values: profile === 'custom' ? clone(custom ?? {}) : mockPerformanceValues(profile),
      applied: undefined,
    };
    return clone(mockPerformance);
  },

  async previewPerformance(): Promise<PerformancePreview> {
    await wait(160, 280);
    const values = clone(mockPerformance.values ?? mockPerformanceValues(mockPerformance.profile));
    return {
      profile: mockPerformance.profile,
      values,
      warnings: [
        'Host tuning changes are global to this machine and require root through the helper.',
        'Apply reloads only nginx/Apache and PHP-FPM; no arbitrary service is restarted.',
      ],
      managed_files: [
        {
          path: '/home/dev/.devstack/services/nginx/config/conf.d/performance/main.conf',
          content: 'worker_processes 4;\nworker_rlimit_nofile 8192;\n',
          active: true,
        },
        {
          path: '/home/dev/.devstack/services/nginx/config/conf.d/performance/events.conf',
          content: 'events {\n    worker_connections 4096;\n}\n',
          active: true,
        },
        {
          path: '/home/dev/.devstack/services/apache/config/conf.d/performance.conf',
          content: 'ThreadsPerChild 25\nServerLimit 16\n',
          active: false,
        },
      ],
      host_tuning: {
        opt_in: false,
        files: [
          {
            path: '/etc/sysctl.d/99-devstack-performance.conf',
            exists: false,
            current: '',
            proposed: 'fs.file-max = 100000\nnet.core.somaxconn = 4096\n',
            diff: '+fs.file-max = 100000\n+net.core.somaxconn = 4096\n',
          },
          {
            path: '/etc/security/limits.d/99-devstack-performance.conf',
            exists: false,
            current: '',
            proposed: 'dev soft nofile 20000\ndev hard nofile 40000\n',
            diff: '+dev soft nofile 20000\n+dev hard nofile 40000\n',
          },
        ],
        warnings: ['Host tuning changes are global to this machine and require root through the helper.'],
      },
    };
  },

  async applyPerformance(hostTuning: boolean): Promise<PerformanceApplyResult> {
    await wait(420, 700);
    const appliedAt = new Date().toISOString();
    mockPerformance = {
      ...mockPerformance,
      applied: {
        profile: mockPerformance.profile,
        values: mockPerformance.values,
        applied_at: appliedAt,
        host_tuning: hostTuning,
        backup_dir: '/home/dev/.devstack/backups/performance/20261026T120000Z',
      },
      host_tuning: hostTuning
        ? {
            ...mockPerformance.host_tuning,
            managed: true,
            applied: true,
            applied_at: appliedAt,
            profile: mockPerformance.profile,
            error: undefined,
          }
        : mockPerformance.host_tuning,
    };
    return {
      applied: true,
      profile: mockPerformance.profile,
      applied_at: appliedAt,
      host_tuning: hostTuning,
      backup_dir: '/home/dev/.devstack/backups/performance/20261026T120000Z',
      steps: [
        { name: 'write nginx performance includes', status: 'ok' },
        { name: 'write Apache performance include', status: 'skipped', detail: 'inactive server only' },
        { name: 'write PHP-FPM pool budgets', status: 'ok' },
        ...(hostTuning
          ? [
              { name: 'apply host sysctl/limits', status: 'ok', detail: 'helper verified values' },
              { name: 'reload nginx', status: 'ok' },
              { name: 'reload PHP-FPM', status: 'ok' },
            ]
          : [
              { name: 'reload nginx', status: 'ok' },
              { name: 'reload PHP-FPM', status: 'ok' },
            ]),
      ],
    };
  },

  async resetPerformance(): Promise<PerformanceResetResult> {
    await wait(320, 520);
    mockPerformance = {
      ...mockPerformance,
      applied: undefined,
      host_tuning: { ...mockPerformance.host_tuning, applied: false, managed: false, error: undefined },
    };
    return {
      reset: true,
      backup_dir: '/home/dev/.devstack/backups/performance/20261026T120500Z',
      steps: [
        { name: 'restore host tuning', status: 'ok' },
        { name: 'write safe nginx includes', status: 'ok' },
        { name: 'reload nginx', status: 'ok' },
      ],
    };
  },

  async listBenchmarks(): Promise<BenchmarkRun[]> {
    await wait(80, 160);
    return clone(mockBenchmarks).sort((a, b) => b.started_at.localeCompare(a.started_at));
  },

  async getBenchmark(id: string): Promise<BenchmarkRun> {
    await wait(60, 120);
    const run = mockBenchmarks.find((entry) => entry.id === id);
    if (!run) throw new ApiError('NOT_FOUND', `Benchmark ${id} was not found.`, 404);
    return clone(run);
  },

  async runBenchmark(request: BenchmarkRequest): Promise<BenchmarkRun> {
    await wait(650, 950);
    const targetSite = findSite(request.site);
    const duration = request.duration_s ?? 10;
    const connections = request.connections ?? 32;
    const seed = 900 + connections * 7 + duration * 3;
    const started = new Date();
    const directory = `/home/dev/.devstack/logs/performance/${started.toISOString().replaceAll(':', '-')}`;
    const run: BenchmarkRun = {
      id: `bench-${started.getTime().toString(36)}`,
      site: targetSite.name,
      tool: 'devstack-go-http',
      tool_version: 'built-in (mock)',
      target: `http://127.0.0.1:${targetSite.http_port || 41001}/`,
      profile: mockPerformance.profile,
      status: 'completed',
      started_at: started.toISOString(),
      finished_at: new Date(started.getTime() + duration * 1000).toISOString(),
      duration_s: duration,
      connections,
      measured: {
        requests: seed * duration,
        errors: 0,
        requests_per_second: seed,
        p50_ms: (1000 / seed) * 5,
        p95_ms: (1000 / seed) * 11,
        p99_ms: (1000 / seed) * 18,
        duration_seconds: duration,
      },
      limitations: ['Mock measurement for dashboard tests; not a host benchmark.'],
      directory,
      raw_file: `${directory}/raw.log`,
    };
    mockBenchmarks = [...mockBenchmarks, run];
    return clone(run);
  },

  async updateSettings(update: SettingsUpdate): Promise<SettingsUpdateResult> {
    await wait(120, 220);
    const next = {
      http_port: update.http_port ?? settings.http_port,
      https_port: update.https_port ?? settings.https_port,
      ui_port: update.ui_port ?? settings.ui_port,
    };
    for (const [field, port] of Object.entries(next)) {
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        throw new ApiError('INVALID_REQUEST', `${field} must be between 1 and 65535.`, 400);
      }
      if (port >= 41_000 && port <= 43_999) {
        throw new ApiError('INVALID_REQUEST', `${field} is inside the reserved backend range.`, 400);
      }
    }
    if (new Set(Object.values(next)).size !== 3) {
      throw new ApiError('INVALID_REQUEST', 'HTTP, HTTPS, and UI ports must differ.', 400);
    }
    const changed =
      next.http_port !== settings.http_port ||
      next.https_port !== settings.https_port ||
      next.ui_port !== settings.ui_port;
    settings = { ...settings, ...next, restart_required: changed };
    return {
      ...clone(settings),
      warnings: changed
        ? ['Run `devstack daemon restart` to move the listeners.']
        : undefined,
    };
  },

  async listServices(): Promise<ServiceView[]> {
    await wait();
    return clone(mockServices);
  },

  async getService(id: string): Promise<ServiceView> {
    await wait();
    return clone(findService(id));
  },

  async installService(id: string): Promise<void> {
    await wait(350, 600);
    const service = findService(id);
    if (service.installed) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} is already installed.`,
        409,
      );
    }
    service.installed = true;
    service.state = 'installed';
    service.active = false;
    service.health = '';
    service.last_error = undefined;
    service.container_id = `mock${id}`.slice(0, 12).padEnd(12, '0');
    emitEvent('service.installed', undefined, `${serviceDisplayName(service)} was installed`);
  },

  async activateService(id: string): Promise<void> {
    await wait(400, 650);
    const service = findService(id);
    if (!service.installed) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} must be installed before it can be activated.`,
        409,
      );
    }
    if (service.active) return;
    if (service.group === 'web' || service.group === 'mysql-family') {
      const active = mockServices.find(
        (entry) => entry.id !== id && entry.group === service.group && entry.active,
      );
      if (active) {
        throw new ApiError(
          'SERVICE_CONFLICT',
          `cannot activate ${serviceDisplayName(service)}: ${serviceDisplayName(active)} is active in the ${service.group} group. Deactivate it first.`,
          409,
        );
      }
    }
    service.active = true;
    service.state = 'active';
    service.health = 'healthy';
    service.last_error = undefined;
    emitEvent('service.activated', undefined, `${serviceDisplayName(service)} was activated`);
  },

  async deactivateService(id: string): Promise<void> {
    await wait(300, 550);
    const service = findService(id);
    if (!service.installed) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} is not installed.`,
        409,
      );
    }
    service.active = false;
    service.state = service.installed ? 'installed' : 'not_installed';
    service.health = '';
    service.last_error = undefined;
    emitEvent('service.deactivated', undefined, `${serviceDisplayName(service)} was deactivated`);
  },

  async restartService(id: string): Promise<void> {
    await wait(350, 600);
    const service = findService(id);
    if (!service.installed) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} is not installed.`,
        409,
      );
    }
    if (!service.active) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} is not active. Activate it before restarting.`,
        409,
      );
    }
    service.health = 'healthy';
    service.last_error = undefined;
    emitEvent('service.restarted', undefined, `${serviceDisplayName(service)} was restarted`);
  },

  async uninstallService(id: string, deleteData: boolean): Promise<ServiceUninstallResult> {
    await wait(400, 700);
    const service = findService(id);
    if (service.active) {
      throw new ApiError(
        'SERVICE_CONFLICT',
        `${serviceDisplayName(service)} is active. Deactivate it before uninstalling.`,
        409,
      );
    }
    const index = mockServices.findIndex((entry) => entry.id === id);
    if (index >= 0) mockServices.splice(index, 1);
    emitEvent('service.uninstalled', undefined, `${serviceDisplayName(service)} was uninstalled`);
    return deleteData
      ? { backup_path: `/data/devstack/backups/services/${id}.tar.gz` }
      : {};
  },

  async serviceLogs(id: string, tail = 200): Promise<string> {
    await wait(120, 240);
    const service = findService(id);
    const lines = [
      `[${service.id}] container ${service.container_id || 'not created'}`,
      `[${service.id}] state=${service.state} active=${service.active} health=${service.health || 'unknown'}`,
      `[${service.id}] published ports: ${
        service.ports?.map((port) => `${port.host}:${port.container}`).join(', ') || 'none'
      }`,
      `[${service.id}] config=${service.config_path ?? '—'}`,
      `[${service.id}] data=${service.data_path ?? '—'}`,
      service.last_error ? `[${service.id}] last error: ${service.last_error}` : '',
    ].filter(Boolean);
    return lines.slice(Math.max(0, lines.length - Math.max(tail, 1))).join('\n');
  },

  async revealServiceCredentials(id: string): Promise<ServiceCredentials> {
    await wait(250, 450);
    const service = findService(id);
    if (service.group !== 'mysql-family' && id !== 'postgresql') {
      throw new ApiError(
        'INVALID_REQUEST',
        'service credentials are not supported: service has no database account',
        400,
      );
    }
    const engine = id === 'postgresql' ? 'postgres' : id === 'mariadb' ? 'mariadb' : 'mysql';
    return {
      engine,
      username: 'devstack',
      password: 'mock_7f3c9a2e5b8d1a4c6e9f0b2d',
      database: 'devstack',
      host: '127.0.0.1',
      port: engine === 'postgres' ? 5432 : 3306,
    };
  },

  async rotateServiceCredentials(id: string): Promise<ServiceCredentials> {
    await wait(250, 450);
    findService(id);
    throw new ApiError(
      'CREDENTIAL_ROTATION_UNAVAILABLE',
      'database credential rotation is not available until Wave 2',
      501,
    );
  },

  openLogStream(
    ref: string,
    onLine: (line: LogLine) => void,
    onStatus?: (status: 'live' | 'error') => void,
  ): () => void {
    const site = findSite(ref);
    const templates = logTemplates[site.name] ?? logTemplates.blog;
    let cursor = logCursor[site.name] ?? 0;
    let closed = false;
    const initialTimers: ReturnType<typeof setTimeout>[] = [];

    const emit = () => {
      if (closed) return;
      const template = templates[cursor % templates.length];
      cursor += 1;
      logCursor[site.name] = cursor;
      onLine({
        time: new Date().toISOString(),
        source: template.source,
        text: template.text,
      });
    };

    for (let index = 0; index < 3; index += 1) {
      initialTimers.push(setTimeout(() => {
        emit();
        if (index === 2) onStatus?.('live');
      }, 90 + index * 90));
    }

    const interval = setInterval(emit, 700);
    return () => {
      closed = true;
      initialTimers.forEach(clearTimeout);
      clearInterval(interval);
    };
  },

  openEvents(onEvent: (event: DaemonEvent) => void): () => void {
    eventListeners.add(onEvent);
    return () => eventListeners.delete(onEvent);
  },
};
