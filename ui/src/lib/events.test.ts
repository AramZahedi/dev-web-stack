import { describe, expect, it } from 'vitest';
import { isRefreshEvent } from './events';

describe('isRefreshEvent', () => {
  it.each([
    'site.updated',
    'runtime.installed',
    'doctor.updated',
    'trust.updated',
    'service.activated',
    'database.created',
    'database.job.updated',
    'database.backup.restored',
    'redis.user.created',
    'redis.password.changed',
  ])('refreshes for %s', (type) => {
    expect(isRefreshEvent(type)).toBe(true);
  });

  it.each(['log.line', 'terminal.output', 'database', 'redisson.updated'])(
    'ignores %s',
    (type) => {
      expect(isRefreshEvent(type)).toBe(false);
    },
  );
});
