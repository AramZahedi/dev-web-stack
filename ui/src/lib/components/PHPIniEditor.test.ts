import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import PHPIniEditor from './PHPIniEditor.svelte';
import { ApiError } from '../errors';

const GENERATED = `; BEGIN devstack version defaults
memory_limit = 256M
; END devstack version defaults

; BEGIN devstack site overrides
memory_limit = 512M
; END devstack site overrides

[PHP]
; BEGIN devstack extensions (managed)
extension=bcmath.so
zend_extension=opcache.so
; END devstack extensions (managed)
`;

function props(overrides: Record<string, unknown> = {}) {
  return {
    ini: { memory_limit: '256M' },
    raw: 'memory_limit = 512M\n',
    generated: GENERATED,
    onApply: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe('PHPIniEditor', () => {
  it('keeps the generated preview read-only and the managed block out of the raw editor', () => {
    render(PHPIniEditor, { props: props() });

    const preview = screen.getByLabelText('Generated php.ini preview');
    expect(preview.tagName).toBe('PRE');
    expect(preview.getAttribute('contenteditable')).toBeNull();
    expect(preview.textContent).toContain('devstack extensions (managed)');
    expect(preview.textContent).toContain('zend_extension=opcache.so');

    const raw = screen.getByLabelText('Site php.ini section') as HTMLTextAreaElement;
    expect(raw.tagName).toBe('TEXTAREA');
    expect(raw.value).toBe('memory_limit = 512M\n');
    expect(raw.value).not.toContain('devstack extensions (managed)');
  });

  it('applies only the edited override map and raw site section', async () => {
    const onApply = vi.fn().mockResolvedValue(undefined);
    render(PHPIniEditor, { props: props({ onApply }) });

    const raw = screen.getByLabelText('Site php.ini section') as HTMLTextAreaElement;
    await fireEvent.input(raw, {
      target: { value: 'memory_limit = 512M\ndisplay_errors = On\n' },
    });

    const value = screen.getByLabelText('memory_limit value') as HTMLInputElement;
    await fireEvent.input(value, { target: { value: '768M' } });

    await fireEvent.click(screen.getByRole('button', { name: 'Apply PHP settings' }));

    await waitFor(() => expect(onApply).toHaveBeenCalledTimes(1));
    const change = onApply.mock.calls[0][0] as Record<string, unknown>;
    expect(Object.keys(change).sort()).toEqual(['ini', 'ini_raw']);
    expect(change.ini).toEqual({ memory_limit: '768M' });
    expect(change.ini_raw).toBe('memory_limit = 512M\ndisplay_errors = On\n');
    expect(JSON.stringify(change)).not.toContain('devstack extensions (managed)');
  });

  it('shows the reported line, code, message, and output for a rejected apply', async () => {
    const onApply = vi.fn().mockRejectedValue(
      new ApiError('PHP_CONFIG_INVALID', 'php.ini could not be validated.', 422, {
        line: 4,
        field: 'ini_raw',
        message: 'unexpected token "memory_limit"',
        output: 'PHP Parse error: syntax error on line 4',
      }),
    );
    render(PHPIniEditor, { props: props({ onApply }) });

    await fireEvent.click(screen.getByRole('button', { name: 'Apply PHP settings' }));

    expect(await screen.findByText('PHP_CONFIG_INVALID')).toBeTruthy();
    expect(screen.getByText(/Line 4/)).toBeTruthy();
    expect(screen.getByText(/unexpected token/)).toBeTruthy();
    expect(screen.getByText(/PHP Parse error/)).toBeTruthy();
    expect(screen.getByRole('alert')).toBeTruthy();
  });

  it('does not render Line 0 for line-less validation errors', async () => {
    const onApply = vi.fn().mockRejectedValue(
      new ApiError('PHP_CONFIG_INVALID', 'php.ini could not be validated.', 422, {
        line: 0,
        message: 'no line information',
      }),
    );
    render(PHPIniEditor, { props: props({ onApply }) });

    await fireEvent.click(screen.getByRole('button', { name: 'Apply PHP settings' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('PHP_CONFIG_INVALID');
    expect(alert.textContent).not.toContain('Line 0');
  });
});
