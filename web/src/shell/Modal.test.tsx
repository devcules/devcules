import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import Modal from './Modal';

function Harness({ onClose = vi.fn(), autoFocusSecond = false }: { onClose?: () => void; autoFocusSecond?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ transform: 'translateZ(0)' }} data-testid="host">
      <button type="button" onClick={() => setOpen(true)}>open</button>
      {open && (
        <Modal label="thing" onClose={() => { onClose(); setOpen(false); }} className="fixed inset-0">
          <form>
            <input aria-label="first" />
            <input aria-label="second" autoFocus={autoFocusSecond} />
            <button type="button" disabled>off</button>
            <button type="submit">save</button>
          </form>
        </Modal>
      )}
    </div>
  );
}

function openIt() {
  const opener = screen.getByRole('button', { name: 'open' });
  opener.focus();
  fireEvent.click(opener);
  return opener;
}

describe('Modal', () => {
  it('is an aria-modal dialog portalled out of a transformed host', () => {
    render(<Harness />);
    openIt();
    const dlg = screen.getByRole('dialog', { name: 'thing' });
    expect(dlg.getAttribute('aria-modal')).toBe('true');
    expect(dlg.parentElement).toBe(document.body);
    expect(screen.getByTestId('host').contains(dlg)).toBe(false);
  });

  it('focuses the first control, or the autoFocus one, on open', () => {
    const { unmount } = render(<Harness />);
    openIt();
    expect(document.activeElement).toBe(screen.getByLabelText('first'));
    unmount();
    render(<Harness autoFocusSecond />);
    openIt();
    expect(document.activeElement).toBe(screen.getByLabelText('second'));
  });

  it('Escape closes, stops at the dialog, and gives focus back to the opener', () => {
    const onClose = vi.fn();
    const onWin = vi.fn();
    window.addEventListener('keydown', onWin);
    render(<Harness onClose={onClose} />);
    const opener = openIt();
    fireEvent.keyDown(screen.getByLabelText('first'), { key: 'Escape' });
    window.removeEventListener('keydown', onWin);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onWin).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it('Tab and Shift+Tab wrap inside the dialog, skipping disabled controls', () => {
    render(<Harness />);
    openIt();
    const first = screen.getByLabelText('first');
    const save = screen.getByRole('button', { name: 'save' });
    save.focus();
    fireEvent.keyDown(save, { key: 'Tab' });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(save);
  });

  it('closes on a backdrop click but not on a click inside, or a drag out of a field', () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);
    openIt();
    const dlg = screen.getByRole('dialog');
    const field = screen.getByLabelText('first');
    fireEvent.mouseDown(field); fireEvent.click(field);
    // A selection that starts in the field and ends on the backdrop.
    fireEvent.mouseDown(field); fireEvent.click(dlg);
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.mouseDown(dlg); fireEvent.click(dlg);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
  // B14: a control with tabindex=-1 (the palette's rows) is not a Tab stop;
  // counted as the last stop, it let Tab leave the dialog.
  it('skips tabindex=-1 controls when trapping Tab', () => {
    render(
      <Modal label="rows" onClose={vi.fn()} className="fixed inset-0">
        <input aria-label="only" autoFocus />
        <button type="button" tabIndex={-1}>row</button>
      </Modal>,
    );
    const only = screen.getByLabelText('only');
    expect(document.activeElement).toBe(only);
    // The trap takes the Tab (default prevented) and keeps focus on the one stop.
    expect(fireEvent.keyDown(only, { key: 'Tab' })).toBe(false);
    expect(document.activeElement).toBe(only);
    expect(fireEvent.keyDown(only, { key: 'Tab', shiftKey: true })).toBe(false);
    expect(document.activeElement).toBe(only);
  });
});
