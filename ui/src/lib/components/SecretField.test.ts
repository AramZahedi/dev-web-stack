import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/svelte';
import SecretField from './SecretField.svelte';

describe('SecretField', () => {
  it('does not render the secret until revealed', async () => {
    const onreveal = vi.fn();
    const { rerender } = render(SecretField, {
      props: { label: 'Password', value: 's3cret', revealed: false, onreveal, onhide: vi.fn() },
    });
    expect(screen.queryByText('s3cret')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /reveal password/i }));
    expect(onreveal).toHaveBeenCalledTimes(1);
    await rerender({ label: 'Password', value: 's3cret', revealed: true, onreveal, onhide: vi.fn() });
    expect(screen.getByText('s3cret')).toBeTruthy();
  });

  it('hides the value again through the hide action', async () => {
    const onhide = vi.fn();
    render(SecretField, {
      props: { label: 'Password', value: 's3cret', revealed: true, onreveal: vi.fn(), onhide },
    });
    await fireEvent.click(screen.getByRole('button', { name: /hide password/i }));
    expect(onhide).toHaveBeenCalledTimes(1);
  });
});
