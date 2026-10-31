import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import Section from './Section';

// The disclosure contract: chevron + title as one button, region
// hidden when folded (mounted, as folded Azure blades are), the
// summary line off the page once open.
describe('Section', () => {
  it('closed: button with aria-expanded false, content hidden', () => {
    render(
      <Section id="x" title="Blade" summary="2 registered" open={false} onToggle={() => {}}>
        <p>inside</p>
      </Section>,
    );
    const btn = screen.getByRole('button', { name: /Blade/ });
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    expect(screen.getByText('2 registered')).toBeTruthy();
    expect(screen.getByText('inside').closest('[hidden]')).toBeTruthy();
  });

  it('open: content visible, summary gone', () => {
    render(
      <Section id="x" title="Blade" summary="2 registered" open={true} onToggle={() => {}}>
        <p>inside</p>
      </Section>,
    );
    const btn = screen.getByRole('button', { name: /Blade/ });
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(screen.queryByText('2 registered')).toBeNull();
    expect(screen.getByText('inside').closest('[hidden]')).toBeNull();
  });

  it('toggle calls back with the id of the folded section', async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <Section id="providers" title="Providers" open={false} onToggle={onToggle}>
        <p>inside</p>
      </Section>,
    );
    await user.click(screen.getByRole('button', { name: /Providers/ }));
    expect(onToggle).toHaveBeenCalledWith('providers');
  });
});
