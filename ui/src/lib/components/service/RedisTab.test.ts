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
    enableRedisACLFile: vi.fn(),
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
  vi.mocked(db.enableRedisACLFile).mockResolvedValue({ enabled: true });
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

  it('resets the default Redis password through the discovered user id', async () => {
    render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    expect(screen.queryByText('newredis')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reset default password/i }));

    await waitFor(() =>
      expect(db.setServiceUserPassword).toHaveBeenCalledWith('redis', 'usr_default'),
    );
    expect(await screen.findByText('newredis')).toBeTruthy();
  });

  it('reveals the default Redis password only through the audited endpoint', async () => {
    render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    expect(screen.queryByText('redissecret')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reveal default password/i }));

    expect(await screen.findByText('redissecret')).toBeTruthy();
    expect(db.revealServiceUserPassword).toHaveBeenCalledWith('redis', 'usr_default');
  });

  it('shows an unavailable state when no default ACL user is discovered', () => {
    render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: [REDIS_USERS[0]],
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    expect(screen.getByText(/default ACL user was not discovered/i)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /reset default password/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /reveal default password/i })).toBeNull();
    expect(db.setServiceUserPassword).not.toHaveBeenCalled();
    expect(db.revealServiceUserPassword).not.toHaveBeenCalled();
  });

  it('clears a revealed default password when the user inventory refreshes', async () => {
    const { rerender } = render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /reveal default password/i }));
    expect(await screen.findByText('redissecret')).toBeTruthy();

    rerender({
      service: REDIS_SERVICE,
      users: REDIS_USERS.map((user) => ({ ...user })),
      onRefresh: vi.fn(),
      section: 'password',
    });

    await waitFor(() => expect(screen.queryByText('redissecret')).toBeNull());
  });

  it('keeps a freshly reset password visible through its own refresh', async () => {
    const { rerender } = render(RedisTab, {
      props: {
        service: REDIS_SERVICE,
        users: REDIS_USERS,
        onRefresh: vi.fn(),
        section: 'password',
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: /reset default password/i }));
    expect(await screen.findByText('newredis')).toBeTruthy();

    rerender({
      service: REDIS_SERVICE,
      users: REDIS_USERS.map((user) => ({ ...user })),
      onRefresh: vi.fn(),
      section: 'password',
    });

    expect(screen.getByText('newredis')).toBeTruthy();
  });

  it('enables Redis ACL persistence only after explicit confirmation', async () => {
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    await fireEvent.click(screen.getByRole('button', { name: /enable acl persistence/i }));
    expect(screen.getAllByText(/restarts redis once/i).length).toBeGreaterThan(0);
    expect(db.enableRedisACLFile).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole('button', { name: /enable and restart/i }));
    await waitFor(() => expect(db.enableRedisACLFile).toHaveBeenCalledWith('redis', true));
  });

  it('shows an ACL enable failure inside the open confirmation dialog', async () => {
    vi.mocked(db.enableRedisACLFile).mockRejectedValueOnce(
      new Error('Could not restart Redis.'),
    );
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    await fireEvent.click(screen.getByRole('button', { name: /enable acl persistence/i }));
    await fireEvent.click(screen.getByRole('button', { name: /enable and restart/i }));

    expect(await screen.findByText('Could not restart Redis.')).toBeTruthy();
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(screen.queryByText(/redis acl persistence enabled/i)).toBeNull();
  });

  it('does not duplicate the default password card on the users tab', () => {
    render(RedisTab, {
      props: { service: REDIS_SERVICE, users: REDIS_USERS, onRefresh: vi.fn() },
    });

    expect(screen.getByText('cache')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /reset default password/i })).toBeNull();
  });
});
