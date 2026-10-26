import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ProjectSourceMode from './ProjectSourceMode.svelte';

describe('ProjectSourceMode', () => {
  it('shows accessible Windows and WSL choices plus the NTFS performance warning', async () => {
    const onmodechange = vi.fn();
    render(ProjectSourceMode, {
      props: { mode: 'windows', kind: 'static', isWindows: true, onmodechange },
    });

    expect(screen.getByRole('group', { name: 'Project source' })).toBeTruthy();
    expect(screen.getByRole('radio', { name: /Windows drive/ })).toBeTruthy();
    expect(screen.getByRole('radio', { name: /WSL distro/ })).toBeTruthy();
    expect(screen.getByRole('status').textContent).toMatch(/Windows.*\/mnt\/c.*NTFS.*file watching/i);
    expect(screen.getByText(/verified copy.*original source is kept/i)).toBeTruthy();

    await fireEvent.click(screen.getByRole('radio', { name: /WSL distro/ }));
    expect(onmodechange).toHaveBeenCalledWith('wsl');
  });

  it('limits Linux projects to local source', () => {
    render(ProjectSourceMode, {
      props: { mode: 'local', kind: 'php', isWindows: false, onmodechange: vi.fn() },
    });

    expect(screen.getByRole('radio', { name: /Local Linux/ })).toBeTruthy();
    expect(screen.queryByRole('radio', { name: /Windows drive/ })).toBeNull();
    expect(screen.queryByRole('radio', { name: /WSL distro/ })).toBeNull();
  });
});
