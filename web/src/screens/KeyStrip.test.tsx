import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import KeyStrip, { ctrlLetter, stripBytes } from './KeyStrip';

describe('stripBytes', () => {
  it('sends escape, tab, arrows and enter', () => {
    expect(stripBytes('esc', false).bytes).toEqual([0x1b]);
    expect(stripBytes('tab', false).bytes).toEqual([0x09]);
    expect(stripBytes('up', false).bytes).toEqual([0x1b, 0x5b, 0x41]);
    expect(stripBytes('enter', false).bytes).toEqual([0x0d]);
  });

  it('sends control arrows under an armed Ctrl and always disarms', () => {
    expect(stripBytes('up', true)).toEqual({ bytes: [0x1b, 0x5b, 0x31, 0x3b, 0x35, 0x41], keepArmed: false });
    expect(stripBytes('enter', true).bytes).toEqual([0x0d]);
    expect(stripBytes('enter', true).keepArmed).toBe(false);
  });

  it('maps typed letters to control codes', () => {
    expect(ctrlLetter('c')).toBe(3);
    expect(ctrlLetter('Z')).toBe(26);
    expect(ctrlLetter('[')).toBeNull();
    expect(ctrlLetter('ab')).toBeNull();
  });
});

describe('KeyStrip', () => {
  it('renders every key with a label and a 44px target', () => {
    render(<KeyStrip ctrlArmed={false} onToggleCtrl={vi.fn()} onKey={vi.fn()} />);
    for (const label of ['Control (sticky modifier)', 'Escape', 'Tab', 'Left arrow', 'Up arrow', 'Down arrow', 'Right arrow', 'Enter']) {
      const button = screen.getByRole('button', { name: label });
      expect(button.className).toContain('min-h-11');
      expect(button.className).toContain('min-w-11');
    }
  });

  it('toggles the sticky Ctrl and sends strip keys', async () => {
    const user = userEvent.setup();
    const onToggleCtrl = vi.fn();
    const onKey = vi.fn();
    render(<KeyStrip ctrlArmed={false} onToggleCtrl={onToggleCtrl} onKey={onKey} />);
    await user.click(screen.getByRole('button', { name: 'Control (sticky modifier)' }));
    expect(onToggleCtrl).toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Enter' }));
    expect(onKey).toHaveBeenCalledWith('enter');
  });

  it('marks an armed Ctrl as pressed', () => {
    render(<KeyStrip ctrlArmed={true} onToggleCtrl={vi.fn()} onKey={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Control (sticky modifier)' }).getAttribute('aria-pressed')).toBe('true');
  });
});
