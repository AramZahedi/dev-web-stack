import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import RedisTab from './RedisTab.svelte';
import { resetMockDatabaseStateForTests } from '../../databaseMock';
import type { DatabaseUser } from '../../databaseTypes';
import type { ServiceView } from '../../types';

// Exercise the real databaseApi -> databaseMock path in mock mode. Only the
// transport module is mocked so `isMockMode` is true.
vi.mock('../../api', () => ({
  isMockMode: true,
  API_BASE: '/api/v1',
  getSession: vi.fn(async () => ({ csrf_token: 'test' })),
  health: vi.fn(),
  request: vi.fn(),
  servicePath: (id: string) => `/services/${encodeURIComponent(id)}`,
  sitePath: (ref: string) => `/sites/${encodeURIComponent(ref)}`,
}));

const REDIS_SERVICE: ServiceView = {
  id: 'redis',
  display_name: 'Redis 8.8',
  group: 'standalone',
  state: 'active',
  active: true,
  installed: true,
  health: 'healthy',
  container_id: 'b1b2c3d4e5f6',
  image: 'redis:8.8.3',
  ports: [{ host: 6379, container: 6379, protocol: 'tcp', loopback: true }],
  config_path: '/data/devstack/services/redis/config',
  data_path: '/data/devstack/services/redis/data',
};

const REDIS_USERS: DatabaseUser[] = [
  {
    id: 'usr_cache',
    service_id: 'redis',
    engine: 'redis',
    username: 'cache',
    enabled: true,
    managed: true,
    password_managed: true,
    access_level: 'read-only',
    grants: [],
    linked_sites: [],
  },
  {
    id: 'usr_default',
    service_id: 'redis',
    engine: 'redis',
    username: 'default',
    enabled: true,
    managed: false,
    password_managed: true,
    access_level: 'full',
    grants: [],
    linked_sites: [],
  },
];

beforeEach(() => {
  resetMockDatabaseStateForTests();
});

describe('RedisTab in mock mode', () => {
  it('resets the discovered default user through the generic password route', async () => {
    render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /reset default password/i }));

    expect(await screen.findByText('mock_db_password')).toBeTruthy();
  });

  it('reveals the discovered default user through the audited generic route', async () => {
    render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    expect(screen.queryByText('mock_db_password')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reveal default password/i }));

    expect(await screen.findByText('mock_db_password')).toBeTruthy();
  });
});
