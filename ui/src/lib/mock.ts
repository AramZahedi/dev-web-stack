import { ApiError } from './errors';
import type {
  CatalogEntry,
  CreateSiteRequest,
  DaemonEvent,
  DoctorCheck,
  DoctorReport,
  FsListing,
  Health,
  LogLine,
  PHPChange,
  PHPDefaults,
  PHPDefaultsResult,
  PHPExecResult,
  PHPExtension,
  PHPInfo,
  PHPMigrationRequest,
  PHPMigrationResult,
  PHPResolvedExtension,
  RuntimeBuild,
  Session,
  Settings,
  ServingMode,
  Site,
  SiteStatus,
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
  return Object.fromEntries(
    PHP_LINES.map((version) => [
      version,
      { version, ini: { ...phpDefaultIni }, extensions: { ...extensions } },
    ]),
  );
}

let phpDefaults = seedPHPDefaults();

function resolvePHPExtensions(
  version: string,
  defaults: Record<string, boolean>,
  overrides: Record<string, boolean> | undefined,
): PHPResolvedExtension[] {
  const catalog = new Map(phpCatalogExtensions.map((extension) => [extension.name, extension]));
  const selected = new Map<string, PHPResolvedExtension>();
  for (const extension of phpCatalogExtensions) {
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
    },
    document_root: 'public',
    front_controller: false,
    spa_fallback: false,
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
    spa_fallback: false,
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
    spa_fallback: false,
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

const settings: Settings = {
  home: '/home/dev/.devstack',
  user_home: '/home/dev',
  http_port: 8080,
  https_port: 8443,
  ui_port: 7070,
  serving_mode: 'builtin',
  wsl_distro: 'devstack',
  auto_start_sites: true,
  graceful_stop_seconds: 10,
  max_request_body_mb: 1024,
  log_max_mb: 10,
  log_keep: 5,
  version: '0.1.0',
  socket_path: '/home/dev/.devstack/devstack.sock',
  pid_path: '/home/dev/.devstack/devstack.pid',
};

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
  const decoded = decodeURIComponent(ref);
  const site = sites.find((entry) => entry.id === decoded || entry.name === decoded);
  if (!site) throw new ApiError('NOT_FOUND', `No site named “${decoded}”.`, 404);
  return site;
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
    return clone(sites);
  },

  async getSite(ref: string): Promise<Site> {
    await wait();
    return clone(findSite(ref));
  },

  async createSite(request: CreateSiteRequest): Promise<Site> {
    await wait();
    const name = request.name.trim();
    if (!name) throw new ApiError('INVALID_REQUEST', 'Site name is required.', 400);
    if (sites.some((site) => site.name === name)) {
      throw new ApiError('DOMAIN_CONFLICT', `A site named “${name}” already exists.`, 409);
    }
    if (!request.path?.startsWith('/')) {
      throw new ApiError('INVALID_REQUEST', 'Project path must be absolute.', 400);
    }
    const projectPath = request.path.length > 1 ? request.path.replace(/\/+$/, '') : request.path;
    if (!directories[projectPath]) {
      throw new ApiError('INVALID_REQUEST', 'Project path must be an existing readable directory.', 400);
    }
    let phpBuild: RuntimeBuild | undefined;
    if (request.kind === 'node') {
      const runtime = runtimes.find(
        (entry) => entry.kind === 'node' && entry.version === request.node_version,
      );
      if (!runtime) {
        throw new ApiError(
          'RUNTIME_MISSING',
          `Node ${request.node_version ?? ''} is not installed. Install it from the Runtimes page first.`,
          409,
        );
      }
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
    const phpVersion = phpBuild ? phpLineOf(phpBuild.version) : '';
    const id = `s_${name.replace(/[^a-z0-9]/g, '')}_${sites.length + 1}`;
    const site: Site = {
      id,
      name,
      domain: `${name}.test`,
      www: request.www ?? false,
      backend: 'local',
      host_path: projectPath,
      backend_path: projectPath,
      kind: request.kind,
      php_version: phpVersion,
      php_build: phpBuild?.id ?? '',
      php_extensions:
        request.kind === 'php' ? { ...(request.php_extensions ?? {}) } : undefined,
      php_ini: request.kind === 'php' ? { ...(request.php_ini ?? {}) } : undefined,
      php_ini_raw: request.kind === 'php' ? (request.php_ini_raw ?? '') : '',
      php_config_hash:
        request.kind === 'php' ? mockHash(`${phpBuild?.id ?? ''}${request.php_ini_raw ?? ''}`) : '',
      node_version: request.kind === 'node' ? (request.node_version ?? '') : '',
      command: request.kind === 'node' ? (request.command ?? '') : '',
      env: request.env ?? {},
      document_root: request.document_root ?? '',
      front_controller: request.front_controller ?? request.kind === 'php',
      spa_fallback: request.spa_fallback ?? false,
      tls: request.tls ?? true,
      force_https: request.force_https ?? false,
      desired_state: 'running',
      status: 'starting',
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
    sites.push(site);
    emitEvent('site.created', site.id, `${site.name}.test was added`);
    setTimeout(() => {
      const current = sites.find((entry) => entry.id === id);
      if (current) {
        setStatus(current, 'running');
        emitEvent('site.running', current.id, `${current.name} is running`);
      }
    }, 900);
    return clone(site);
  },

  async updateSite(ref: string, request: UpdateSiteRequest): Promise<Site> {
    await wait();
    const site = findSite(ref);
    Object.assign(site, request, { updated_at: new Date().toISOString() });
    emitEvent('site.updated', site.id, `${site.name} was updated`);
    return clone(site);
  },

  async deleteSite(ref: string): Promise<void> {
    await wait();
    const site = findSite(ref);
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

  async installRuntime(id: string): Promise<RuntimeBuild> {
    await wait(250, 450);
    const entry = catalog.find((item) => item.id === id);
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
    const defaults = phpDefaults[phpLineOf(build.version)] ?? phpDefaults['8.4'];
    const available = phpCatalogExtensions
      .filter((extension) => extension.versions.includes(phpLineOf(build.version)))
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
      builtins: [...phpBuiltins],
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
    return clone(
      phpCatalogExtensions
        .filter((extension) => extension.versions.includes(version))
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

  async browseFs(path: string): Promise<FsListing> {
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
