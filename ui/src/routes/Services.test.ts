import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import Services from './Services.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('Services', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders a list of services with name, group, status and ports', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.includes('/api/v1/session')) {
          return jsonResponse({ csrf_token: 'test-token' });
        }
        if (url.includes('/api/v1/services')) {
          return jsonResponse({
            services: [
              {
                id: 'nginx',
                display_name: 'Nginx Web Server',
                group: 'web',
                state: 'active',
                active: true,
                image: 'fixture-nginx:latest',
                ports: [{ host: 80, container: 80, protocol: 'http', loopback: true }],
                config_path: '/data/devstack/services/nginx/config',
              },
            ],
          });
        }
        return jsonResponse({});
      }),
    );

    render(Services);

    const name = await screen.findByText('Nginx Web Server');
    expect(name).toBeTruthy();
    expect(screen.getByText('web')).toBeTruthy();
    expect(screen.getByText('active')).toBeTruthy();
    expect(screen.getByText('80:80')).toBeTruthy();
  });

  it('renders an empty state when no services are available', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.includes('/api/v1/session')) {
          return jsonResponse({ csrf_token: 'test-token' });
        }
        if (url.includes('/api/v1/services')) {
          return jsonResponse({ services: [] });
        }
        return jsonResponse({});
      }),
    );

    render(Services);

    const empty = await screen.findByText('No services configured');
    expect(empty).toBeTruthy();
  });
});
