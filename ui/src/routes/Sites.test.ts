import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import Sites from './Sites.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('Sites', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders a mobile loading list alongside the desktop table skeleton', () => {
    const resolvers: Array<(response: Response) => void> = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            resolvers.push(resolve);
          }),
      ),
    );

    const { container } = render(Sites);

    expect(container.querySelector('.loading-state')).not.toBeNull();
    expect(container.querySelector('.table-card')).not.toBeNull();
    expect(container.querySelector('.mobile-list.loading-cards')).not.toBeNull();

    resolvers.forEach((resolve) => resolve(jsonResponse([])));
  });

  it('keeps the empty state outside the desktop-only table card', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => jsonResponse([])),
    );

    const { container } = render(Sites);

    const title = await screen.findByText('No local sites yet');
    expect(title.closest('.state-card')).not.toBeNull();
    expect(title.closest('.table-card')).toBeNull();
    expect(screen.getByRole('button', { name: 'Add your first site' })).toBeTruthy();

    await waitFor(() => {
      expect(container.querySelector('.loading-state')).toBeNull();
    });
  });

  it('keeps sites visible when the runtime inventory is unavailable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/v1/runtimes')) {
          return jsonResponse(
            { code: 'INTERNAL', message: 'WSL agent not connected', details: null },
            500,
          );
        }
        if (url.includes('/api/v1/sites')) {
          return jsonResponse([
            {
              id: 'mysite',
              name: 'My Site',
              domain: 'mysite.test',
              status: 'running',
              kind: 'static',
              host_path: 'C:\\Projects\\mysite',
              source_mode: 'windows',
            },
          ]);
        }
        return jsonResponse([]);
      }),
    );

    render(Sites);
    expect((await screen.findAllByText('My Site')).length).toBeGreaterThan(0);
    expect(screen.getAllByText('Runtime inventory unavailable').length).toBeGreaterThan(0);
    expect(screen.queryByText('Could not load sites')).toBeNull();
  });

  it('displays source mode badge when present', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/v1/sites')) {
          return jsonResponse([
            {
              id: 'mysite',
              name: 'My Site',
              domain: 'mysite.test',
              status: 'running',
              kind: 'php',
              host_path: 'C:\\Projects\\mysite',
              source_mode: 'windows',
            },
          ]);
        }
        return jsonResponse([]);
      }),
    );

    render(Sites);
    const badges = await screen.findAllByText('Windows drive');
    expect(badges.length).toBeGreaterThan(0);
  });
});
