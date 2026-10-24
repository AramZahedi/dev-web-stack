import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/svelte';
import ExtensionToggles from './ExtensionToggles.svelte';
import type { PHPExtension } from '../types';

const EXTENSIONS: PHPExtension[] = [
  {
    name: 'xdebug',
    type: 'zend_extension',
    so: 'xdebug.so',
    default: false,
    standard: false,
    versions: ['8.4'],
  },
  {
    name: 'opcache',
    type: 'zend_extension',
    so: 'opcache.so',
    default: true,
    standard: true,
    versions: ['8.4'],
  },
  {
    name: 'redis',
    type: 'extension',
    so: 'redis.so',
    default: false,
    standard: false,
    versions: ['8.4'],
    requires: ['igbinary'],
  },
  {
    name: 'mbstring',
    type: 'extension',
    so: 'mbstring.so',
    default: true,
    standard: true,
    versions: ['8.4'],
  },
];

function renderToggles(value: Record<string, boolean>, onchange = vi.fn()) {
  render(ExtensionToggles, {
    props: {
      extensions: EXTENSIONS,
      builtins: ['core', 'json'],
      value,
      onchange,
    },
  });
  return onchange;
}

describe('ExtensionToggles', () => {
  it('shows the inherited catalog default and the zend extension badge', () => {
    renderToggles({});

    const xdebug = screen.getByRole('group', { name: 'xdebug' });
    expect(within(xdebug).getByText('Inherit (off)')).toBeTruthy();
    expect(within(xdebug).getByRole('button', { name: 'Inherit xdebug' })).toBeTruthy();

    const opcache = screen.getByRole('group', { name: 'opcache' });
    expect(within(opcache).getByText('Inherit (on)')).toBeTruthy();
    expect(within(opcache).getByText('zend_extension')).toBeTruthy();
    expect(within(screen.getByRole('group', { name: 'redis' })).queryByText('zend_extension')).toBeNull();
  });

  it('uses the edited version defaults for inherited labels', () => {
    render(ExtensionToggles, {
      props: {
        extensions: EXTENSIONS,
        builtins: [],
        value: { mbstring: true },
        defaults: { mbstring: false, opcache: false, xdebug: true },
        onchange: vi.fn(),
      },
    });

    const mbstring = screen.getByRole('group', { name: 'mbstring' });
    // An explicit site override wins over the edited baseline.
    expect(within(mbstring).getByText('Enabled for this site')).toBeTruthy();

    const opcache = screen.getByRole('group', { name: 'opcache' });
    expect(within(opcache).getByText('Inherit (off)')).toBeTruthy();

    const xdebug = screen.getByRole('group', { name: 'xdebug' });
    expect(within(xdebug).getByText('Inherit (on)')).toBeTruthy();
  });

  it('removes the override when inherit is chosen', async () => {
    const onchange = renderToggles({ xdebug: true, opcache: false });

    await fireEvent.click(screen.getByRole('button', { name: 'Inherit xdebug' }));

    expect(onchange).toHaveBeenCalledWith({ opcache: false });
  });

  it('sets explicit on and off overrides', async () => {
    const onchange = renderToggles({});

    await fireEvent.click(screen.getByRole('button', { name: 'Enable xdebug' }));
    expect(onchange).toHaveBeenLastCalledWith({ xdebug: true });

    await fireEvent.click(screen.getByRole('button', { name: 'Disable opcache' }));
    expect(onchange).toHaveBeenLastCalledWith({ opcache: false });
  });

  it('keeps built-ins always on and disabled', () => {
    renderToggles({});

    const core = screen.getByRole('switch', { name: 'core built-in extension' });
    expect(core.getAttribute('aria-checked')).toBe('true');
    expect((core as HTMLButtonElement).disabled).toBe(true);
  });
});
