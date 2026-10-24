import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import StatusPill from './StatusPill.svelte';

describe('StatusPill', () => {
  it('renders a text label alongside the status dot', () => {
    const { container } = render(StatusPill, { props: { status: 'running' } });

    const pill = screen.getByLabelText('Status: Running');
    expect(pill.textContent).toContain('Running');
    expect(container.querySelector('.dot')).not.toBeNull();
  });

  it('labels a crashed site in text, not color alone', () => {
    render(StatusPill, { props: { status: 'crashed' } });
    expect(screen.getByLabelText('Status: Crashed').textContent).toContain('Crashed');
  });
});
