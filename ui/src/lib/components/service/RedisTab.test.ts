import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import RedisTab from './RedisTab.svelte';
import * as db from '../../databaseApi';
import type { DatabaseUser } from '../../databaseTypes';
import type { ServiceView } from '../../types';

vi.mock('../../databaseApi', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../databaseApi')>();
  return {
    ...actual,
    setServiceUserPassword: vi.fn(),
    revealServiceUserPassword: vi.fn(),
  };
});

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
  vi.mocked(db.setServiceUserPassword).mockResolvedValue({ value: 'newredis' });
  vi.mocked(db.revealServiceUserPassword).mockResolvedValue({ value: 'redissecret' });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('RedisTab', () => {
  it('shows Redis users and password without database tabs', () => {
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    expect(screen.getByText('cache')).toBeTruthy();
    expect(screen.getByText('read-only')).toBeTruthy();
    expect(screen.queryByText(/databases/i)).toBeNull();
    expect(screen.getByText(/full means all data commands/i)).toBeTruthy();
    expect(screen.queryByText(/superuser/i)).toBeNull();
  });

  it('resets the default Redis password through the service user endpoint', async () => {
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    expect(screen.queryByText('newredis')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reset default password/i }));

    await waitFor(() =>
      expect(db.setServiceUserPassword).toHaveBeenCalledWith('redis', 'default'),
    );
    expect(await screen.findByText('newredis')).toBeTruthy();
  });

  it('reveals the default Redis password only through the audited endpoint', async () => {
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    expect(screen.queryByText('redissecret')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reveal default password/i }));

    expect(await screen.findByText('redissecret')).toBeTruthy();
    expect(db.revealServiceUserPassword).toHaveBeenCalledWith('redis', 'default');
  });
});
