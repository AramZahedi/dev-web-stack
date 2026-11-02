import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import PathBrowser from './PathBrowser.svelte';
import { browseFs, mkdirFs } from '../api';
import { ApiError } from '../errors';

vi.mock('../api', () => ({
  browseFs: vi.fn(),
  mkdirFs: vi.fn(),
}));

const mockBrowse = vi.mocked(browseFs);
const mockMkdir = vi.mocked(mkdirFs);

const WSL_ROOT = '/data/devstack/projects';

function renderBrowser(overrides: Partial<{
  initialPath: string;
  rootPath: string;
  rootLabel: string;
}> = {}) {
  return render(PathBrowser, {
    props: {
      open: true,
      mode: 'wsl',
      initialPath: overrides.initialPath ?? WSL_ROOT,
      rootPath: overrides.rootPath ?? WSL_ROOT,
      rootLabel: overrides.rootLabel ?? 'WSL Projects',
      onselect: vi.fn(),
      onclose: vi.fn(),
    },
  });
}

beforeEach(() => {
  mockBrowse.mockReset();
  mockMkdir.mockReset();
});

describe('PathBrowser managed roots', () => {
  it('disables Up when the current directory is the projects root', async () => {
    mockBrowse.mockResolvedValue({
      path: WSL_ROOT,
      entries: [{ name: 'scratch', path: `${WSL_ROOT}/scratch` }],
    });

    renderBrowser();

    await screen.findByRole('button', { name: 'scratch' });
    expect(mockBrowse).toHaveBeenCalledWith(WSL_ROOT, 'wsl');
    expect(
      (screen.getByRole('button', { name: 'Go up one level' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it('shows the root label instead of a house or drive root', async () => {
    mockBrowse.mockResolvedValue({ path: WSL_ROOT, entries: [] });

    renderBrowser();

    const rootCrumb = await screen.findByRole('button', { name: 'WSL Projects' });
    expect(rootCrumb).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Root' })).toBeNull();
    expect(screen.queryByText('Root')).toBeNull();
    expect(screen.queryByRole('button', { name: 'projects' })).toBeNull();
  });

  it('only renders crumbs below the root', async () => {
    const current = `${WSL_ROOT}/app/src`;
    mockBrowse.mockResolvedValue({ path: current, entries: [] });

    renderBrowser({ initialPath: current });

    await screen.findByRole('button', { name: 'WSL Projects' });
    expect(await screen.findByRole('button', { name: 'app' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'src' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'projects' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'data' })).toBeNull();
  });

  it('browses the clamped initial path when the initial path is outside the root', async () => {
    mockBrowse.mockResolvedValue({ path: WSL_ROOT, entries: [] });

    renderBrowser({ initialPath: '/home/dev/.devstack/projects/app' });

    await waitFor(() =>
      expect(mockBrowse).toHaveBeenCalledWith(WSL_ROOT, 'wsl'),
    );
    expect(await screen.findByText(WSL_ROOT)).toBeTruthy();
  });

  it('offers the projects root action when the root itself cannot be read', async () => {
    mockBrowse.mockRejectedValue(
      new ApiError('FORBIDDEN', 'Directory is outside the managed projects root.', 403),
    );

    renderBrowser();

    expect(await screen.findByText('Could not read that folder')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Go to projects root' })).toBeTruthy();
  });

  it('clamps back into the new root when the mode and root change while open', async () => {
    mockBrowse.mockImplementation(async (path) => ({ path, entries: [] }));

    const view = renderBrowser({ initialPath: `${WSL_ROOT}/app` });
    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(`${WSL_ROOT}/app`, 'wsl'));

    mockBrowse.mockClear();
    const windowsRoot = 'C:\\Users\\Aram\\.devstack\\projects';
    await view.rerender({
      open: true,
      mode: 'windows',
      initialPath: `${WSL_ROOT}/app`,
      rootPath: windowsRoot,
      rootLabel: 'Windows Projects',
      onselect: vi.fn(),
      onclose: vi.fn(),
    });

    await waitFor(() =>
      expect(mockBrowse).toHaveBeenCalledWith(windowsRoot, 'windows'),
    );
    expect(await screen.findByRole('button', { name: 'Windows Projects' })).toBeTruthy();
  });

  it('never falls back above the root when a child is missing', async () => {
    mockBrowse.mockImplementation(async (path) => {
      if (path === `${WSL_ROOT}/app`) {
        throw new ApiError('NOT_FOUND', 'Directory is not readable.', 404);
      }
      return { path, entries: [] };
    });

    renderBrowser({ initialPath: `${WSL_ROOT}/app` });

    await waitFor(() => expect(mockBrowse).toHaveBeenCalledWith(WSL_ROOT, 'wsl'));
    expect(mockBrowse.mock.calls.some(([path]) => path === '/data/devstack')).toBe(false);
  });
});
