import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Services from './Services.svelte';
import * as api from '../lib/api';
import { ApiError } from '../lib/errors';
import type { ServiceView } from '../lib/types';

vi.mock('../lib/api', () => ({
  isMockMode: false,
  listServices: vi.fn(),
  installService: vi.fn(),
  activateService: vi.fn(),
  deactivateService: vi.fn(),
  restartService: vi.fn(),
  uninstallService: vi.fn(),
  serviceLogs: vi.fn(),
  revealServiceCredentials: vi.fn(),
  rotateServiceCredentials: vi.fn(),
}));

const NGINX: ServiceView = {
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
};

const APACHE: ServiceView = {
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
};

const MYSQL: ServiceView = {
  id: 'mysql',
  display_name: 'MySQL 8.4',
  group: 'mysql-family',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'a1b2c3d4e5f6',
  image: 'mysql:8.4.13',
  ports: [{ host: 3306, container: 3306, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/mysql/config',
  data_path: '/data/devstack/services/mysql/data',
};

const REDIS: ServiceView = {
  id: 'redis',
  display_name: 'Redis 8.8',
  group: 'standalone',
  state: 'not_installed',
  active: false,
  installed: false,
  health: '',
  container_id: '',
  image: 'redis:8.8.3',
  ports: [{ host: 6379, container: 6379, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/redis/config',
  data_path: '/data/devstack/services/redis/data',
};

const PHPMYADMIN: ServiceView = {
  id: 'phpmyadmin',
  display_name: 'phpMyAdmin',
  group: 'standalone',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'e1f2a3b4c5d6',
  image: 'phpmyadmin:5.2',
  ports: [{ host: 8082, container: 80, protocol: 'http', loopback: true }],
  config_path: '/data/devstack/services/phpmyadmin/config',
  data_path: '/data/devstack/services/phpmyadmin/data',
};

const mockApi = vi.mocked(api);
const BASE = [NGINX, APACHE, MYSQL, REDIS, PHPMYADMIN];

async function cardFor(name: string): Promise<HTMLElement> {
  const heading = await screen.findByRole('heading', { name });
  const card = heading.closest('article');
  if (!(card instanceof HTMLElement)) throw new Error(`no service card for ${name}`);
  return card;
}

beforeEach(() => {
  mockApi.listServices.mockResolvedValue(BASE.map((service) => ({ ...service })));
  mockApi.installService.mockResolvedValue(undefined);
  mockApi.activateService.mockResolvedValue(undefined);
  mockApi.deactivateService.mockResolvedValue(undefined);
  mockApi.restartService.mockResolvedValue(undefined);
  mockApi.uninstallService.mockResolvedValue({});
  mockApi.serviceLogs.mockResolvedValue('[mysql] ready for connections\n[mysql] port 3306');
  mockApi.revealServiceCredentials.mockResolvedValue({
    engine: 'mysql',
    username: 'devstack',
    password: 'super-secret-password',
    database: 'devstack',
    host: '127.0.0.1',
    port: 3306,
  });
  mockApi.rotateServiceCredentials.mockRejectedValue(
    new ApiError(
      'CREDENTIAL_ROTATION_UNAVAILABLE',
      'database credential rotation is not available until Wave 2',
      501,
    ),
  );
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('Services', () => {
  it('groups cards and shows installed, active, lifecycle and health badges', async () => {
    render(Services);

    const nginx = await cardFor('Nginx Web Server');
    expect(nginx.textContent).toContain('Installed');
    expect(nginx.textContent).toContain('Active');
    expect(nginx.textContent).toContain('Health: healthy');
    expect(nginx.textContent).toContain('80:80');
    expect(nginx.textContent).toContain('443:443');

    expect(screen.getByRole('heading', { name: 'Web servers' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'MySQL family' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Standalone' })).toBeTruthy();

    const mysql = await cardFor('MySQL 8.4');
    expect(mysql.textContent).toContain('3306:3306');
    expect(mysql.closest('section')?.querySelector('h2')?.textContent).toBe('MySQL family');

    const redis = await cardFor('Redis 8.8');
    expect(redis.textContent).toContain('Not installed');
    expect(within(redis).getByRole('button', { name: 'Install' })).toBeTruthy();
    expect(within(redis).queryByRole('button', { name: 'Logs' })).toBeNull();
  });

  it('uses distinct action colors for Install and Activate', async () => {
    render(Services);

    const apache = await cardFor('Apache HTTP Server');
    const activate = within(apache).getByRole('button', { name: 'Activate' });
    const redis = await cardFor('Redis 8.8');
    const install = within(redis).getByRole('button', { name: 'Install' });

    expect(activate.className).toContain('primary');
    expect(install.className).toContain('info');
    expect(install.className).not.toContain('primary');
  });

  it('opens phpMyAdmin only for an active phpMyAdmin service', async () => {
    render(Services);

    const link = await screen.findByRole('link', { name: /Open phpMyAdmin/ });
    expect(link.getAttribute('href')).toBe('http://127.0.0.1:8082/');
    expect(link.getAttribute('target')).toBe('_blank');

    mockApi.listServices.mockResolvedValue([{ ...PHPMYADMIN, active: false, state: 'installed' }]);
    window.dispatchEvent(new Event('devstack:refresh'));
    await waitFor(() => {
      expect(screen.queryByRole('link', { name: /Open phpMyAdmin/ })).toBeNull();
    });
  });

  it('does not show optimistic active state while activation is in flight', async () => {
    let resolveActivation: (() => void) | undefined;
    mockApi.activateService.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveActivation = resolve;
        }),
    );
    mockApi.listServices
      .mockResolvedValueOnce(BASE.map((service) => ({ ...service })))
      .mockResolvedValueOnce(
        BASE.map((service) =>
          service.id === 'apache'
            ? { ...service, active: true, state: 'active' as const, health: 'healthy' }
            : service,
        ),
      );

    render(Services);
    const apache = await cardFor('Apache HTTP Server');
    await fireEvent.click(within(apache).getByRole('button', { name: 'Activate' }));

    expect(mockApi.activateService).toHaveBeenCalledWith('apache');
    expect(apache.textContent).toContain('Inactive');

    resolveActivation?.();
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Apache HTTP Server' }).closest('article')?.textContent).toContain('Active');
    });
    expect(mockApi.listServices).toHaveBeenCalledTimes(2);
  });

  it('installs, deactivates and restarts through the API then refreshes', async () => {
    render(Services);

    const redis = await cardFor('Redis 8.8');
    await fireEvent.click(within(redis).getByRole('button', { name: 'Install' }));
    await waitFor(() => expect(mockApi.installService).toHaveBeenCalledWith('redis'));

    const nginx = await cardFor('Nginx Web Server');
    await fireEvent.click(within(nginx).getByRole('button', { name: 'Restart' }));
    await waitFor(() => expect(mockApi.restartService).toHaveBeenCalledWith('nginx'));

    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Deactivate' }));
    await waitFor(() => expect(mockApi.deactivateService).toHaveBeenCalledWith('mysql'));

    expect(mockApi.listServices.mock.calls.length).toBeGreaterThanOrEqual(4);
  });

  it('shows a service conflict verbatim and keeps the previous state', async () => {
    mockApi.activateService.mockRejectedValue(
      new ApiError(
        'SERVICE_CONFLICT',
        'cannot activate Apache HTTP Server: Nginx Web Server is active in the web group. Deactivate it first.',
        409,
      ),
    );

    render(Services);
    const apache = await cardFor('Apache HTTP Server');
    await fireEvent.click(within(apache).getByRole('button', { name: 'Activate' }));

    const alert = await within(apache).findByRole('alert');
    expect(alert.textContent).toContain('SERVICE_CONFLICT');
    expect(alert.textContent).toContain(
      'cannot activate Apache HTTP Server: Nginx Web Server is active in the web group. Deactivate it first.',
    );
    expect((await cardFor('Apache HTTP Server')).textContent).toContain('Inactive');
  });

  it('loads logs in a modal and refreshes the tail on demand', async () => {
    render(Services);
    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Logs' }));

    const dialog = await screen.findByRole('dialog', { name: 'MySQL 8.4 logs' });
    expect(await within(dialog).findByText(/ready for connections/)).toBeTruthy();
    expect(mockApi.serviceLogs).toHaveBeenCalledWith('mysql', 200);

    fireEvent.change(within(dialog).getByLabelText('Lines'), { target: { value: '500' } });
    await waitFor(() => expect(mockApi.serviceLogs).toHaveBeenCalledWith('mysql', 500));

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(mockApi.serviceLogs).toHaveBeenCalledTimes(3));
  });

  it('reveals credentials and shows the 501 rotation message from the daemon', async () => {
    render(Services);
    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Credentials' }));

    const dialog = await screen.findByRole('dialog', { name: 'MySQL 8.4 credentials' });
    expect(await within(dialog).findAllByText('devstack')).toHaveLength(2);
    expect(within(dialog).getByText('••••••••••••')).toBeTruthy();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Reveal password' }));
    expect(within(dialog).getByText('super-secret-password')).toBeTruthy();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rotate credentials' }));
    expect(within(dialog).getByText('Rotate these credentials now?')).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rotate credentials' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toContain('CREDENTIAL_ROTATION_UNAVAILABLE');
    expect(alert.textContent).toContain(
      'database credential rotation is not available until Wave 2',
    );
    expect(alert.textContent).toContain('Credential rotation is not available yet');
    expect(within(dialog).getByText(/no credentials were changed/)).toBeTruthy();
  });

  it('shows generic rotation failures without claiming they are the Wave 2 boundary', async () => {
    mockApi.rotateServiceCredentials.mockRejectedValue(
      new ApiError('HELPER_UNAVAILABLE', 'service helper is unavailable', 503),
    );

    render(Services);
    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Credentials' }));
    const dialog = await screen.findByRole('dialog', { name: 'MySQL 8.4 credentials' });

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rotate credentials' }));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Rotate credentials' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert.textContent).toContain('HELPER_UNAVAILABLE');
    expect(alert.textContent).toContain('service helper is unavailable');
    expect(alert.textContent).toContain('Credential rotation failed');
    expect(alert.textContent).not.toContain('Wave 2');
    expect(alert.textContent).toContain('No credentials were changed.');
  });

  it('uninstalls with data kept by default', async () => {
    mockApi.listServices
      .mockResolvedValueOnce(BASE.map((service) => ({ ...service })))
      .mockResolvedValueOnce(BASE.filter((service) => service.id !== 'mysql'));

    render(Services);
    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Uninstall' }));

    const dialog = await screen.findByRole('dialog', { name: 'Uninstall MySQL 8.4?' });
    const keep = within(dialog).getByRole('radio', { name: /Keep data/ }) as HTMLInputElement;
    expect(keep.checked).toBe(true);
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Uninstall service' }));

    await waitFor(() => expect(mockApi.uninstallService).toHaveBeenCalledWith('mysql', false));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Uninstall MySQL/ })).toBeNull());
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'MySQL 8.4' })).toBeNull());
  });

  it('requires an explicit deletion choice and typed service id before deleting data', async () => {
    render(Services);
    const mysql = await cardFor('MySQL 8.4');
    await fireEvent.click(within(mysql).getByRole('button', { name: 'Uninstall' }));

    const dialog = await screen.findByRole('dialog', { name: 'Uninstall MySQL 8.4?' });
    await fireEvent.click(within(dialog).getByRole('radio', { name: /Delete data permanently/ }));

    const confirm = within(dialog).getByRole('button', {
      name: 'Uninstall service',
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(within(dialog).getByText(/Deleting data is irreversible/)).toBeTruthy();

    const typed = within(dialog).getByLabelText('Type “mysql” to confirm') as HTMLInputElement;
    await fireEvent.input(typed, { target: { value: 'mysql' } });
    await waitFor(() => expect(confirm.disabled).toBe(false));
    await fireEvent.click(confirm);

    await waitFor(() => expect(mockApi.uninstallService).toHaveBeenCalledWith('mysql', true));
  });

  it('labels missing config and performance APIs as Wave 2 backend pending', async () => {
    render(Services);
    expect(await screen.findByText('Configuration & performance')).toBeTruthy();
    expect(screen.getByText('Service config editing')).toBeTruthy();
    expect(screen.getByText('Performance profiles')).toBeTruthy();
    expect(screen.getAllByText('Wave 2 backend pending').length).toBeGreaterThan(0);
    expect(screen.getByText(/Nothing is editable yet/)).toBeTruthy();
    expect(screen.getByText(/No profile is applied/)).toBeTruthy();
  });
});
