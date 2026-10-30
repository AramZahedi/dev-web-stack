import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import NodeVersionPicker from './NodeVersionPicker.svelte';
import * as api from '../api';
import type { NodeRelease, NodeReleaseSnapshot, RuntimeBuild } from '../types';

vi.mock('../api', () => ({
  isMockMode: false,
  listNodeReleases: vi.fn(),
}));

function release(version: string, npm = '11.6.0', lts = 'Krypton'): NodeRelease {
  return {
    version,
    date: '2026-10-28',
    lts,
    security: false,
    npm,
    modules: '137',
    file: 'linux-x64',
  };
}

const BUILDS: RuntimeBuild[] = [
  {
    id: 'node@24.9.0',
    kind: 'node',
    version: '24.9.0',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/node/24.9.0',
    checksum: 'a'.repeat(64),
    installed_at: '2026-10-20T10:00:00Z',
  },
  {
    id: 'node@22.20.0',
    kind: 'node',
    version: '22.20.0',
    backend: 'local',
    path: '/home/dev/.devstack/runtimes/node/22.20.0',
    checksum: 'b'.repeat(64),
    installed_at: '2026-09-20T10:00:00Z',
  },
];

const SNAPSHOT: NodeReleaseSnapshot = {
  source: 'cache',
  cached_at: '2026-10-30T09:00:00Z',
  stale: false,
  majors: [
    {
      major: 24,
      latest: release('24.9.0'),
      releases: [release('24.9.0'), release('24.8.1', '11.5.2'), release('24.8.0'), release('24.1.2')],
    },
    {
      major: 22,
      latest: release('22.20.0'),
      releases: [release('22.20.0'), release('22.19.0')],
    },
  ],
  all: [
    release('24.9.0'),
    release('24.8.1', '11.5.2'),
    release('24.8.0'),
    release('24.1.2'),
    release('22.20.0'),
    release('22.19.0'),
  ],
};

const mockApi = vi.mocked(api);

beforeEach(() => {
  mockApi.listNodeReleases.mockResolvedValue(SNAPSHOT);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('NodeVersionPicker', () => {
  it('groups installed builds by major and offers inherit', async () => {
    const onchange = vi.fn();
    render(NodeVersionPicker, {
      props: {
        id: 'node-version',
        label: 'Node toolchain',
        value: '',
        builds: BUILDS,
        inheritLabel: 'Inherit global default (v24.9.0)',
        onchange,
      },
    });

    const picker = screen.getByLabelText(/Node toolchain/) as HTMLSelectElement;
    expect(picker.value).toBe('');
    expect(
      screen.getByRole('option', { name: 'Inherit global default (v24.9.0)' }),
    ).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Node 24' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Node 22' })).toBeTruthy();

    await fireEvent.change(picker, { target: { value: '22.20.0' } });
    expect(onchange).toHaveBeenCalledWith('22.20.0');
  });

  it('expands a major to reveal older minor and patch versions', async () => {
    render(NodeVersionPicker, {
      props: {
        id: 'node-version',
        label: 'Node toolchain',
        value: '',
        builds: BUILDS,
        inheritLabel: 'Inherit global default (v24.9.0)',
        onchange: vi.fn(),
      },
    });

    expect(screen.getByLabelText(/Node toolchain/)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Install another version' }));

    await waitFor(() => expect(mockApi.listNodeReleases).toHaveBeenCalledWith({ all: true }));
    expect(await screen.findByText('Node 24.9.0')).toBeTruthy();
    expect(screen.queryByText('Node 24.8.1')).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'Show all Node 24 versions' }));

    expect(screen.getByText('Node 24.8.1')).toBeTruthy();
    expect(screen.getByText('Node 24.8.0')).toBeTruthy();
    expect(screen.getByText('Node 24.1.2')).toBeTruthy();
  });

  it('offers install-first when the selected version is not installed', async () => {
    const oninstall = vi.fn().mockResolvedValue(undefined);
    render(NodeVersionPicker, {
      props: {
        id: 'node-version',
        label: 'Node toolchain',
        value: '22.11.0',
        builds: BUILDS,
        inheritLabel: 'Inherit global default (v24.9.0)',
        onchange: vi.fn(),
        oninstall,
      },
    });

    expect(screen.getByText(/Node 22\.11\.0 is not installed/)).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: 'Install Node 22.11.0' }));
    await waitFor(() => expect(oninstall).toHaveBeenCalledWith('22.11.0'));
  });
});
