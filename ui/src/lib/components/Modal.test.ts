import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import Modal from './Modal.svelte';

function bodySnippet() {
  return createRawSnippet(() => ({ render: () => '<p>Modal body</p>' }));
}

const props = {
  title: 'Benchmark result',
  onclose: () => {},
  children: bodySnippet(),
};

describe('Modal', () => {
  afterEach(() => {
    cleanup();
    document.documentElement.style.overflow = '';
    document.body.style.overflow = '';
    document.body.style.overscrollBehavior = '';
  });

  it('portals the overlay to body and locks document scrolling', async () => {
    render(Modal, { ...props, open: true } as never);

    const dialog = await screen.findByRole('dialog');
    const overlay = dialog.parentElement;
    expect(overlay?.parentElement).toBe(document.body);
    expect(document.documentElement.style.overflow).toBe('hidden');
    expect(document.body.style.overflow).toBe('hidden');
    expect(document.body.style.overscrollBehavior).toBe('none');
  });

  it('restores document scrolling when closed', async () => {
    const { rerender } = render(Modal, { ...props, open: true } as never);
    await screen.findByRole('dialog');

    await rerender({ ...props, open: false } as never);

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.documentElement.style.overflow).toBe('');
    expect(document.body.style.overflow).toBe('');
    expect(document.body.style.overscrollBehavior).toBe('');
  });
});
