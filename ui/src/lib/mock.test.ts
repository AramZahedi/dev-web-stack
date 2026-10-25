import { afterEach, describe, expect, it } from 'vitest';
import { mockApi, resetMockServiceStateForTests } from './mock';

const BLOG = 's_blog';

function count(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1;
}

afterEach(async () => {
  await mockApi.updateSite(BLOG, {
    tls: true,
    force_https: true,
    document_root: 'public',
    www: false,
    spa_fallback: false,
  });
  await mockApi.startSite(BLOG);
});

describe('mock vhost previews', () => {
  it('serves both HTTP and HTTPS when TLS is on without force HTTPS', async () => {
    await mockApi.updateSite(BLOG, { tls: true, force_https: false, www: true });

    const nginx = (await mockApi.previewVhost('blog', 'nginx')).content;
    expect(count(nginx, 'listen 80;')).toBe(1);
    expect(count(nginx, 'listen 443 ssl http2;')).toBe(1);
    expect(count(nginx, 'root /home/dev/projects/blog/public;')).toBe(2);
    expect(nginx).toContain('server_name blog.test www.blog.test;');
    expect(nginx).not.toContain('return 308');

    const apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(count(apache, '<VirtualHost *:80>')).toBe(1);
    expect(count(apache, '<VirtualHost *:443>')).toBe(1);
    expect(apache).toContain('ServerAlias www.blog.test');
    expect(apache).not.toContain('R=308');
  });

  it('redirects HTTPS to HTTP with the dashboard certificate when TLS is off', async () => {
    await mockApi.updateSite(BLOG, { tls: false, force_https: false });

    const nginx = (await mockApi.previewVhost('blog', 'nginx')).content;
    expect(count(nginx, 'listen 80;')).toBe(1);
    expect(nginx).toContain('listen 443 ssl http2;');
    expect(nginx).toContain('return 308 http://$host$request_uri;');
    expect(nginx).toContain('/etc/devstack/tls/__dashboard__.crt;');
    expect(nginx).not.toContain('/etc/devstack/tls/s_blog.crt;');

    const apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(apache).toContain(
      'RewriteRule ^ http://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]',
    );
    expect(apache).toContain('SSLCertificateFile "/etc/devstack/tls/__dashboard__.crt"');
    expect(apache).not.toContain('Redirect permanent');
  });

  it('uses a path-preserving Apache 308 rewrite for force HTTPS', async () => {
    await mockApi.updateSite(BLOG, { tls: true, force_https: true });

    const apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(apache).toContain(
      'RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=308,L]',
    );
    expect(apache).not.toContain('Redirect permanent');
    expect(count(apache, 'SSLCertificateFile')).toBe(1);
  });

  it('keeps absolute document roots and joins relative roots to the backend path', async () => {
    await mockApi.updateSite(BLOG, { document_root: '/srv/www/blog/public' });
    let nginx = (await mockApi.previewVhost('blog', 'nginx')).content;
    expect(nginx).toContain('root /srv/www/blog/public;');
    expect(nginx).not.toContain('/home/dev/projects/blog/srv/www');
    let apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(apache).toContain('DocumentRoot "/srv/www/blog/public"');

    await mockApi.updateSite(BLOG, { document_root: 'public' });
    nginx = (await mockApi.previewVhost('blog', 'nginx')).content;
    expect(nginx).toContain('root /home/dev/projects/blog/public;');
    apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(apache).toContain('DocumentRoot "/home/dev/projects/blog/public"');
  });

  it('renders branded 503 vhosts for stopped sites', async () => {
    await mockApi.stopSite(BLOG);

    const nginx = (await mockApi.previewVhost('blog', 'nginx')).content;
    expect(count(nginx, 'return 503')).toBe(2);
    expect(count(nginx, 'listen 443 ssl http2;')).toBe(1);
    expect(nginx).toContain('/etc/devstack/tls/s_blog.crt;');
    expect(nginx).not.toContain('root /home/dev/projects/blog/public;');

    const apache = (await mockApi.previewVhost('blog', 'apache')).content;
    expect(count(apache, 'ErrorDocument 503')).toBe(2);
    expect(count(apache, '[R=503,L]')).toBe(2);
    expect(apache).toContain('SSLCertificateFile "/etc/devstack/tls/s_blog.crt"');
  });

  it('proxies Node sites through the allocated backend port', async () => {
    const nginx = (await mockApi.previewVhost('shop', 'nginx')).content;
    expect(nginx).toContain('proxy_pass http://127.0.0.1:41002;');
    expect(nginx).toContain('proxy_set_header Upgrade $http_upgrade;');

    const apache = (await mockApi.previewVhost('shop', 'apache')).content;
    expect(apache).toContain('ProxyPass "/" "http://127.0.0.1:41002/"');
    expect(apache).toContain('ProxyPassReverse "/" "http://127.0.0.1:41002/"');
  });
});

describe('mock PHP 8.5 built-ins', () => {
  it('treats OPcache as built in and never emits opcache.so', async () => {
    const site = await mockApi.createSite({
      name: 'php85-check',
      path: '/home/dev/projects/scratch',
      kind: 'php',
      php_version: '8.5',
      php_build: 'php@8.5.0-r1',
    });
    try {
      const info = await mockApi.getPHPInfo(site.id);
      expect(info.builtins).toContain('opcache');
      expect(info.available_extensions.map((extension) => extension.name)).not.toContain('opcache');
      expect(info.generated_ini).not.toContain('opcache.so');

      const extensions = await mockApi.phpExtensions('8.5');
      expect(extensions.map((extension) => extension.name)).not.toContain('opcache');
      expect((await mockApi.getPHPDefaults('8.5')).extensions.opcache).toBeUndefined();
    } finally {
      await mockApi.deleteSite(site.id);
    }
  });
});

describe('mock services', () => {
  it('returns mock services list and allows getting a service by id', async () => {
    const list = await mockApi.listServices();
    expect(list.length).toBeGreaterThan(0);
    const nginx = list.find((s) => s.id === 'nginx');
    expect(nginx).toBeDefined();
    expect(nginx?.display_name).toBe('Nginx Web Server');

    const fetched = await mockApi.getService('nginx');
    expect(fetched.id).toBe('nginx');
    expect(fetched.state).toBe('active');
    expect(fetched.installed).toBe(true);
    expect(fetched.health).toBeTruthy();
    expect(fetched.container_id).toBeTruthy();

    await expect(mockApi.getService('nonexistent')).rejects.toMatchObject({
      code: 'NOT_FOUND',
      status: 404,
    });
  });

  it('reports the serving provider in mock settings', async () => {
    const settings = await mockApi.settings();
    expect(settings.serving_provider).toBe('native');
  });
});

describe('mock service actions', () => {
  afterEach(() => {
    resetMockServiceStateForTests();
  });

  it('refuses two active services in an exclusive group without changing state', async () => {
    await expect(mockApi.activateService('apache')).rejects.toMatchObject({
      code: 'SERVICE_CONFLICT',
      status: 409,
    });
    const list = await mockApi.listServices();
    expect(list.find((service) => service.id === 'nginx')?.active).toBe(true);
    expect(list.find((service) => service.id === 'apache')?.active).toBe(false);
  });

  it('installs, activates, restarts and reads service logs', async () => {
    await mockApi.installService('mariadb');
    expect((await mockApi.getService('mariadb')).state).toBe('installed');

    await expect(mockApi.activateService('mariadb')).rejects.toMatchObject({
      code: 'SERVICE_CONFLICT',
    });

    await mockApi.deactivateService('mysql');
    await mockApi.activateService('mariadb');
    const active = await mockApi.getService('mariadb');
    expect(active.active).toBe(true);
    expect(active.health).toBe('healthy');

    await mockApi.restartService('mariadb');
    const logs = await mockApi.serviceLogs('mariadb', 50);
    expect(logs).toContain('state=active');
    expect(logs).toContain('3306:3306');
  });

  it('keeps data by default and returns a backup path only when deleting data', async () => {
    await mockApi.deactivateService('nginx');
    await expect(mockApi.uninstallService('nginx', false)).resolves.toEqual({});
    expect((await mockApi.listServices()).some((service) => service.id === 'nginx')).toBe(false);

    resetMockServiceStateForTests();
    await mockApi.deactivateService('nginx');
    const result = await mockApi.uninstallService('nginx', true);
    expect(result.backup_path).toContain('/backups/services/nginx');
  });

  it('reveals database credentials and reports the rotation boundary', async () => {
    const credentials = await mockApi.revealServiceCredentials('mysql');
    expect(credentials).toMatchObject({
      engine: 'mysql',
      username: 'devstack',
      host: '127.0.0.1',
      port: 3306,
    });
    expect(credentials.password.length).toBeGreaterThan(16);

    await expect(mockApi.rotateServiceCredentials('mysql')).rejects.toMatchObject({
      code: 'CREDENTIAL_ROTATION_UNAVAILABLE',
      status: 501,
      message: 'database credential rotation is not available until Wave 2',
    });
    await expect(mockApi.revealServiceCredentials('redis')).rejects.toMatchObject({
      code: 'INVALID_REQUEST',
    });
  });

  it('manages the global PHP default, ini and Composer status', async () => {
    const initial = await mockApi.getPHPDefault();
    expect(initial.configured).toBe('php@8.4.4-r1');
    expect(initial.build?.version).toBe('8.4.4');
    expect(initial.shim_installed).toBe(true);

    const switched = await mockApi.setPHPDefault('8.3');
    expect(switched.configured).toBe('php@8.3.14-r2');
    expect(switched.build?.version).toBe('8.3.14');

    await expect(mockApi.setPHPDefault('9.9')).rejects.toMatchObject({
      code: 'RUNTIME_MISSING',
      status: 409,
    });

    const cleared = await mockApi.clearPHPDefault();
    expect(cleared.configured).toBeUndefined();
    expect(cleared.build?.version).toBe('8.5.0');

    const ini = await mockApi.getGlobalIni();
    expect(ini.content).toContain('memory_limit');
    const saved = await mockApi.saveGlobalIni('[PHP]\nmemory_limit = 1G\n');
    expect(saved.content).toContain('1G');
    expect((await mockApi.getGlobalIni()).content).toContain('1G');

    const tools = await mockApi.listTools();
    expect(tools.map((tool) => tool.id)).toEqual(['php-default', 'composer']);
    const removed = await mockApi.removeTool('composer');
    expect(removed.installed).toBe(false);
    const installed = await mockApi.installTool('composer');
    expect(installed.installed).toBe(true);
    expect(installed.version).toBe('2.8.4');
    await expect(mockApi.installTool('unknown-tool')).rejects.toMatchObject({
      code: 'INVALID_REQUEST',
    });
  });

  it('requires build output and creates stopped when the output directory is missing', async () => {
    await expect(
      mockApi.createSite({
        name: 'build-empty-output',
        path: '/home/dev/projects/scratch',
        kind: 'static',
        build: {
          node_version: '24.9.0',
          command: 'npm run build',
          output: '',
        },
      }),
    ).rejects.toMatchObject({
      code: 'INVALID_REQUEST',
      status: 400,
      message: 'sites: build output is required for a build profile',
    });

    const stopped = await mockApi.createSite({
      name: 'build-missing-output',
      path: '/home/dev/projects/scratch',
      kind: 'static',
      build: {
        node_version: '24.9.0',
        command: 'npm run build',
        output: 'dist',
      },
    });
    expect(stopped.status).toBe('stopped');
    expect(stopped.desired_state).toBe('stopped');
    await mockApi.deleteSite(stopped.id);

    const ready = await mockApi.createSite({
      name: 'build-ready-output',
      path: '/home/dev/projects/scratch',
      kind: 'static',
      build: {
        node_version: '24.9.0',
        command: 'npm run build',
        output: 'public',
      },
    });
    expect(ready.status).toBe('starting');
    await mockApi.deleteSite(ready.id);
  });

  it('returns a build result and build log for a static profile', async () => {
    const result = await mockApi.buildSite('blog');
    expect(result.success).toBe(true);
    expect(result.duration_ms).toBeGreaterThan(0);
    expect(result.output_path).toBe('/home/dev/projects/blog/public');

    const logs = await mockApi.getBuildLogs('blog', 5);
    expect(logs.content).toContain('built in');
    expect(logs.path).toContain('/logs/builds/blog/build.log');

    await expect(mockApi.buildSite('api')).rejects.toMatchObject({
      code: 'INVALID_REQUEST',
    });
  });
});
