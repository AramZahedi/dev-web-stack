import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import Toggle from './Toggle.svelte';

describe('Toggle', () => {
  it('updates its bindable value when no onchange handler is provided', async () => {
    render(Toggle, { props: { checked: false, label: 'SPA fallback' } });

    const toggle = screen.getByRole('switch', { name: 'SPA fallback' });
    expect(toggle.getAttribute('aria-checked')).toBe('false');

    await fireEvent.click(toggle);

    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });

  it('updates its value and notifies a change handler', async () => {
    const onchange = vi.fn();
    render(Toggle, {
      props: {
        checked: false,
        label: 'Serve over HTTPS',
        description: 'Issue a certificate from the local CA.',
        onchange,
      },
    });

    const toggle = screen.getByRole('switch', { name: 'Serve over HTTPS' });
    await fireEvent.click(toggle);

    expect(onchange).toHaveBeenCalledWith(true);
    expect(toggle.getAttribute('aria-checked')).toBe('true');
  });

  it('does not change while disabled', async () => {
    const onchange = vi.fn();
    render(Toggle, {
      props: { checked: false, label: 'Force HTTPS', disabled: true, onchange },
    });

    const toggle = screen.getByRole('switch', { name: 'Force HTTPS' });
    await fireEvent.click(toggle);

    expect(onchange).not.toHaveBeenCalled();
    expect(toggle.getAttribute('aria-checked')).toBe('false');
  });
});
