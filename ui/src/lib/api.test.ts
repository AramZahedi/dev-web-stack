import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createSite,
  getVhostStatus,
  listSites,
  previewVhost,
  resetApiClientForTests,
  restartSite,
  setServingMode,
  startSite,
  stopSite,
  syncVhosts,
} from './api';
import { ApiError } from './errors';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function emptyResponse(status: number): Response {
  return new Response(null, { status });
}

describe('api client', () => {
  beforeEach(() => {
    resetApiClientForTests();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    resetApiClientForTests();
  });

  it('fetches a session and sends its CSRF token with same-origin credentials', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-123' }))
      .mockResolvedValueOnce(jsonResponse({ id: 's1', name: 'blog', domain: 'blog.test' }, 201));
    vi.stubGlobal('fetch', fetchMock);

    await createSite({ name: 'blog', path: '/tmp/blog', kind: 'static' });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [sessionUrl, sessionInit] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(sessionUrl).toBe('/api/v1/session');
    expect(sessionInit.credentials).toBe('same-origin');

    const [createUrl, createInit] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(createUrl).toBe('/api/v1/sites');
    expect(createInit.method).toBe('POST');
    expect(createInit.credentials).toBe('same-origin');
    const headers = new Headers(createInit.headers);
    expect(headers.get('X-CSRF-Token')).toBe('csrf-123');
    expect(headers.get('Content-Type')).toBe('application/json');
    expect(JSON.parse(String(createInit.body))).toMatchObject({
      name: 'blog',
      path: '/tmp/blog',
      kind: 'static',
    });
  });

  it('reuses the CSRF token for subsequent mutations', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-abc' }))
      .mockResolvedValueOnce(jsonResponse({ id: 's1' }, 201))
      .mockResolvedValue(jsonResponse({ id: 's2' }, 201));
    vi.stubGlobal('fetch', fetchMock);

    await createSite({ name: 'one', path: '/tmp/one', kind: 'static' });
    await createSite({ name: 'two', path: '/tmp/two', kind: 'static' });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    const secondHeaders = new Headers((fetchMock.mock.calls[2][1] as RequestInit).headers);
    expect(secondHeaders.get('X-CSRF-Token')).toBe('csrf-abc');
  });

  it('accepts a 202 Accepted response with no body for lifecycle actions', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-lifecycle' }))
      .mockResolvedValueOnce(emptyResponse(202));
    vi.stubGlobal('fetch', fetchMock);

    await expect(startSite('s_blog')).resolves.toBeUndefined();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe('/api/v1/sites/s_blog/start');
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-lifecycle');
  });

  it('accepts 204 and empty 200 bodies for void endpoints', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-lifecycle' }))
      .mockResolvedValueOnce(emptyResponse(204))
      .mockResolvedValueOnce(emptyResponse(200));
    vi.stubGlobal('fetch', fetchMock);

    await expect(stopSite('s_blog')).resolves.toBeUndefined();
    await expect(restartSite('s_blog')).resolves.toBeUndefined();

    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('does not accept an HTML 200 response for a void endpoint', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-lifecycle' }))
      .mockResolvedValueOnce(
        new Response('<html>SPA fallback</html>', {
          status: 200,
          headers: { 'Content-Type': 'text/html' },
        }),
      );
    vi.stubGlobal('fetch', fetchMock);

    await expect(stopSite('s_blog')).rejects.toMatchObject({
      name: 'ApiError',
      code: 'INVALID_RESPONSE',
      status: 200,
    });
  });

  it('sends the CSRF token and mode when switching serving mode', async () => {
    const status = {
      mode: 'nginx',
      server: 'nginx',
      server_version: '1.28.0',
      bridge_path: '/etc/nginx/devstack-vhosts.conf',
      include_present: true,
      current_release: '/etc/devstack/vhosts/nginx/releases/20261023T120000Z',
      config_ok: true,
      last_sync: '2026-10-23T12:00:00Z',
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-vhost' }))
      .mockResolvedValueOnce(jsonResponse(status));
    vi.stubGlobal('fetch', fetchMock);

    await expect(setServingMode('nginx')).resolves.toMatchObject({ mode: 'nginx' });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe('/api/v1/vhosts/mode');
    expect(init.method).toBe('POST');
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-vhost');
    expect(JSON.parse(String(init.body))).toEqual({ mode: 'nginx' });
  });

  it('loads vhost status and renders a preview with an encoded query', async () => {
    const status = { mode: 'builtin', server: '', config_ok: true };
    const preview = {
      server: 'apache',
      site: 'blog',
      content: '<VirtualHost *:443>\n    ServerName blog.test\n</VirtualHost>\n',
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(status))
      .mockResolvedValueOnce(jsonResponse(preview));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getVhostStatus()).resolves.toMatchObject({ mode: 'builtin' });
    await expect(previewVhost('blog', 'apache')).resolves.toEqual(preview);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [statusUrl, statusInit] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(statusUrl).toBe('/api/v1/vhosts/status');
    expect(statusInit.method ?? 'GET').toBe('GET');
    expect(new Headers(statusInit.headers).get('X-CSRF-Token')).toBeNull();

    const [previewUrl, previewInit] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(previewUrl).toBe('/api/v1/vhosts/preview?site=blog&server=apache');
    expect(previewInit.method ?? 'GET').toBe('GET');
    expect(new Headers(previewInit.headers).get('X-CSRF-Token')).toBeNull();
    expect(previewInit.credentials).toBe('same-origin');
    expect(JSON.parse(JSON.stringify(preview.content))).toContain('blog.test');
  });

  it('syncs vhosts with CSRF and parses the refreshed status', async () => {
    const status = {
      mode: 'nginx',
      server: 'nginx',
      server_version: '1.28.0',
      include_present: true,
      config_ok: true,
      last_sync: '2026-10-23T12:05:00Z',
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-sync' }))
      .mockResolvedValueOnce(jsonResponse(status));
    vi.stubGlobal('fetch', fetchMock);

    await expect(syncVhosts()).resolves.toMatchObject({ last_sync: '2026-10-23T12:05:00Z' });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect(url).toBe('/api/v1/vhosts/sync');
    expect(init.method).toBe('POST');
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-sync');
    expect(init.body).toBeUndefined();
  });

  it('maps an error envelope to ApiError with code, message and details', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          {
            code: 'HOSTS_SYNC_FAILED',
            message: 'Could not update the hosts block.',
            details: { path: '/etc/hosts' },
          },
          500,
        ),
      ),
    );

    await expect(listSites()).rejects.toMatchObject({
      name: 'ApiError',
      code: 'HOSTS_SYNC_FAILED',
      message: 'Could not update the hosts block.',
      status: 500,
      details: { path: '/etc/hosts' },
    });

    await expect(listSites()).rejects.toBeInstanceOf(ApiError);
  });

  it('wraps network failures in a stable error code', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(listSites()).rejects.toMatchObject({
      name: 'ApiError',
      code: 'NETWORK_ERROR',
    });
  });
});
