import { afterEach, describe, expect, it } from 'vitest';
import { mockApi } from './mock';

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
