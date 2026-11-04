import { act, render, screen, fireEvent } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import CommandPalette, { PENDING_MS, deskEvent, listenDesk, modKeyLabel, score, toDesk, type DeskAction } from './CommandPalette';
import { isPresenting, setPresenting } from './present';

describe('score', () => {
  it('ranks substrings above scattered letters and rejects misses', () => {
    expect(score('Go to Desk', 'desk')).toBeGreaterThan(score('Go to Desk', 'gtd'));
    expect(score('Go to Desk', 'gtd')).toBeGreaterThan(0);
    expect(score('Go to Desk', 'zzz')).toBe(-1);
    expect(score('anything', '')).toBe(0);
  });
});

const realPlatform = Object.getOwnPropertyDescriptor(window.navigator, 'platform');
function setPlatform(p: string) {
  Object.defineProperty(window.navigator, 'platform', { value: p, configurable: true });
}

type Nav = Array<{ id: string; label: string; key?: string; go: () => void }>;
const twoNav = (go = vi.fn(), home = vi.fn()): Nav => [
  { id: 'desk', label: 'Desk', key: 'd', go },
  { id: 'home', label: 'Home', key: 'h', go: home },
];

describe('CommandPalette', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });
  afterEach(() => {
    if (isPresenting()) act(() => setPresenting(false));
    if (realPlatform) Object.defineProperty(window.navigator, 'platform', realPlatform);
    else delete (window.navigator as { platform?: string }).platform;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('opens on ⌘K, filters, and runs the picked command on Enter', () => {
    const go = vi.fn();
    render(<CommandPalette nav={twoNav(go)} />);
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    const box = screen.getByLabelText('Command');
    fireEvent.change(box, { target: { value: 'go to desk' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(go).toHaveBeenCalledOnce();
    expect(screen.queryByLabelText('Command')).toBeNull();
  });

  it('jumps with a g-chord but ignores keys typed into a field', () => {
    const go = vi.fn();
    render(<><input aria-label="field" /><CommandPalette nav={[{ id: 'home', label: 'Home', key: 'h', go }]} /></>);
    const field = screen.getByLabelText('field');
    fireEvent.keyDown(field, { key: 'g' });
    fireEvent.keyDown(field, { key: 'h' });
    expect(go).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'h' });
    expect(go).toHaveBeenCalledOnce();
  });

  // DESK-22: p and r used to be checked before the chord.
  it('the key after g is a jump, never a single-key shortcut', () => {
    const home = vi.fn();
    const sync = vi.fn();
    window.addEventListener('devcules:desk', sync);
    render(<CommandPalette nav={twoNav(vi.fn(), home)} />);
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'p' });
    expect(isPresenting()).toBe(false);
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'r' });
    expect(sync).not.toHaveBeenCalled();
    // The chord is spent: the next h is not a jump.
    fireEvent.keyDown(window, { key: 'h' });
    expect(home).not.toHaveBeenCalled();
    // g g h: a second g re-arms.
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'h' });
    expect(home).toHaveBeenCalledOnce();
    window.removeEventListener('devcules:desk', sync);
  });

  it('a chord older than 1.2 s has expired', () => {
    const now = vi.spyOn(Date, 'now').mockReturnValue(10_000);
    const home = vi.fn();
    render(<CommandPalette nav={twoNav(vi.fn(), home)} />);
    fireEvent.keyDown(window, { key: 'g' });
    now.mockReturnValue(11_500);
    fireEvent.keyDown(window, { key: 'h' });
    expect(home).not.toHaveBeenCalled();
  });

  it('single keys: p presents, Esc leaves, r refreshes the desk, / opens', () => {
    const seen: string[] = [];
    const on = (e: Event) => seen.push((e as CustomEvent<{ action: string }>).detail.action);
    window.addEventListener('devcules:desk', on);
    render(<CommandPalette nav={twoNav()} />);
    fireEvent.keyDown(window, { key: 'p' });
    expect(isPresenting()).toBe(true);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(isPresenting()).toBe(false);
    fireEvent.keyDown(window, { key: 'r' });
    expect(seen).toEqual(['sync']);
    // Modifier and Alt combos are left to the browser.
    fireEvent.keyDown(window, { key: 'p', altKey: true });
    fireEvent.keyDown(window, { key: 'p', ctrlKey: true });
    expect(isPresenting()).toBe(false);
    fireEvent.keyDown(window, { key: '/' });
    expect(screen.getByLabelText('Command')).toBeTruthy();
    // Single keys do nothing while the palette is open.
    fireEvent.keyDown(window, { key: 'p' });
    expect(isPresenting()).toBe(false);
    // ⌘K toggles it shut again.
    fireEvent.keyDown(window, { key: 'K', ctrlKey: true });
    expect(screen.queryByLabelText('Command')).toBeNull();
    window.removeEventListener('devcules:desk', on);
  });

  // DESK-22: the label follows the platform.
  it('labels the palette key Ctrl+K on Linux and ⌘K on a Mac', () => {
    setPlatform('Linux x86_64');
    expect(modKeyLabel()).toBe('Ctrl+K');
    const { unmount } = render(<CommandPalette nav={twoNav()} />);
    const btn = screen.getByRole('button', { name: 'Open the command palette' });
    expect(btn.textContent).toContain('Ctrl+K');
    expect(btn.getAttribute('title')).toBe('Command palette (Ctrl+K)');
    fireEvent.keyDown(window, { key: '?' });
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' }).textContent).toContain('Ctrl+K');
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' }).textContent).not.toContain('⌘K');
    unmount();
    setPlatform('MacIntel');
    expect(modKeyLabel()).toBe('⌘K');
    render(<CommandPalette nav={twoNav()} />);
    expect(screen.getByRole('button', { name: 'Open the command palette' }).textContent).toContain('⌘K');
  });

  // DESK-14: the active row stays in view and is announced.
  it('arrow selection scrolls the active row into view and wires aria-activedescendant', () => {
    render(<CommandPalette nav={twoNav()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Open the command palette' }));
    const box = screen.getByRole('combobox', { name: 'Command' });
    const list = screen.getByRole('listbox');
    expect(box.getAttribute('aria-controls')).toBe(list.id);
    const scroll = vi.mocked(Element.prototype.scrollIntoView);
    scroll.mockClear();
    fireEvent.keyDown(box, { key: 'ArrowDown' });
    fireEvent.keyDown(box, { key: 'ArrowDown' });
    const opts = screen.getAllByRole('option');
    expect(opts[2].getAttribute('aria-selected')).toBe('true');
    expect(box.getAttribute('aria-activedescendant')).toBe(opts[2].id);
    expect(scroll).toHaveBeenLastCalledWith({ block: 'nearest' });
    expect(scroll.mock.contexts.at(-1)).toBe(opts[2]);
    fireEvent.keyDown(box, { key: 'ArrowUp' });
    expect(box.getAttribute('aria-activedescendant')).toBe(opts[1].id);
    // Clamped at both ends.
    fireEvent.keyDown(box, { key: 'ArrowUp' });
    fireEvent.keyDown(box, { key: 'ArrowUp' });
    expect(opts[0].getAttribute('aria-selected')).toBe('true');
    for (let i = 0; i < opts.length + 3; i++) fireEvent.keyDown(box, { key: 'ArrowDown' });
    expect(screen.getAllByRole('option').at(-1)?.getAttribute('aria-selected')).toBe('true');
    // Hovering a row selects it.
    fireEvent.mouseEnter(screen.getAllByRole('option')[1].querySelector('button')!);
    expect(screen.getAllByRole('option')[1].getAttribute('aria-selected')).toBe('true');
  });

  // DESK-14: Tab stays inside the palette, which is a real modal.
  it('is aria-modal and keeps Tab inside', () => {
    render(<><button type="button">behind</button><CommandPalette nav={twoNav()} /></>);
    fireEvent.keyDown(window, { key: '/' });
    const dlg = screen.getByRole('dialog', { name: 'Command palette' });
    expect(dlg.getAttribute('aria-modal')).toBe('true');
    const box = screen.getByLabelText('Command');
    expect(document.activeElement).toBe(box);
    // The rows are not Tab stops (aria-activedescendant walks them), so
    // the trap takes every Tab and focus stays on the input.
    expect(fireEvent.keyDown(box, { key: 'Tab' })).toBe(false);
    expect(document.activeElement).toBe(box);
    expect(fireEvent.keyDown(box, { key: 'Tab', shiftKey: true })).toBe(false);
    expect(document.activeElement).toBe(box);
    // Esc closes it and does not leave presentation behind it.
    act(() => setPresenting(true));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).toBeNull();
    expect(isPresenting()).toBe(true);
  });

  it('a click on the backdrop closes; a click on a row runs it; no match says so', () => {
    const home = vi.fn();
    render(<CommandPalette nav={twoNav(vi.fn(), home)} />);
    fireEvent.keyDown(window, { key: '/' });
    const dlg = screen.getByRole('dialog', { name: 'Command palette' });
    fireEvent.mouseDown(dlg);
    fireEvent.click(dlg);
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.keyDown(window, { key: '/' });
    fireEvent.change(screen.getByLabelText('Command'), { target: { value: 'zzzzqx' } });
    expect(screen.getByText(/nothing matches/)).toBeTruthy();
    // Enter with nothing shown is a no-op and keeps the palette open.
    fireEvent.keyDown(screen.getByLabelText('Command'), { key: 'Enter' });
    expect(screen.getByRole('dialog', { name: 'Command palette' })).toBeTruthy();

    fireEvent.change(screen.getByLabelText('Command'), { target: { value: 'home' } });
    fireEvent.click(screen.getByRole('button', { name: /Go to Home/ }));
    expect(home).toHaveBeenCalledOnce();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('runs the demo and theme commands', () => {
    const party = vi.fn();
    window.addEventListener('devcules:confetti', party);
    render(<CommandPalette nav={twoNav()} />);
    const pick = (q: string) => {
      fireEvent.keyDown(window, { key: '/' });
      const box = screen.getByLabelText('Command');
      fireEvent.change(box, { target: { value: q } });
      fireEvent.keyDown(box, { key: 'Enter' });
    };
    pick('celebrate');
    expect(party).toHaveBeenCalledOnce();
    pick('start presentation');
    expect(isPresenting()).toBe(true);
    // The palette reads the live state when it opens.
    fireEvent.keyDown(window, { key: '/' });
    expect(screen.getByText('Exit presentation mode')).toBeTruthy();
    fireEvent.keyDown(screen.getByLabelText('Command'), { key: 'Escape' });
    pick('exit presentation');
    expect(isPresenting()).toBe(false);
    pick('theme holo');
    expect(document.documentElement.dataset.theme).toBe('holo');
    pick('theme vibe');
    expect(document.documentElement.dataset.theme).toBe('vibe');
    pick('keyboard shortcuts');
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeTruthy();
    window.removeEventListener('devcules:confetti', party);
  });

  // DESK-24: the ? sheet is a real dialog: focus, ✕, Esc.
  it('the shortcut sheet takes focus, closes on ✕ and Esc, and lists the jumps', () => {
    render(<CommandPalette nav={twoNav()} />);
    fireEvent.keyDown(window, { key: '?' });
    const sheet = screen.getByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(sheet.getAttribute('aria-modal')).toBe('true');
    const close = screen.getByRole('button', { name: 'Close shortcuts' });
    expect(document.activeElement).toBe(close);
    expect(sheet.textContent).toContain('g d');
    expect(sheet.textContent).toContain('g h');
    // ? again does not stack or toggle; p does nothing behind it.
    fireEvent.keyDown(close, { key: '?' });
    fireEvent.keyDown(close, { key: 'p' });
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(isPresenting()).toBe(false);
    fireEvent.click(close);
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.keyDown(window, { key: '?' });
    fireEvent.keyDown(screen.getByRole('button', { name: 'Close shortcuts' }), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();

    // Esc reaching the window (focus outside) closes it too.
    fireEvent.keyDown(window, { key: '?' });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();

    // ⌘K from the sheet swaps it for the palette.
    fireEvent.keyDown(window, { key: '?' });
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    expect(screen.queryByRole('dialog', { name: 'Keyboard shortcuts' })).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Command palette' })).toBeTruthy();
  });

  // DESK-13: another dialog owns the keyboard.
  it('ignores ?, p, /, g, ⌘K and Esc while another aria-modal is open', () => {
    const home = vi.fn();
    const { rerender } = render(<><div role="dialog" aria-modal="true" aria-label="Add a widget" /><CommandPalette nav={twoNav(vi.fn(), home)} /></>);
    act(() => setPresenting(true));
    fireEvent.keyDown(window, { key: '?' });
    fireEvent.keyDown(window, { key: '/' });
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'h' });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(home).not.toHaveBeenCalled();
    expect(isPresenting()).toBe(true);
    fireEvent.keyDown(window, { key: 'p' });
    expect(isPresenting()).toBe(true);
    // Once it closes, the keys work again.
    rerender(<><CommandPalette nav={twoNav(vi.fn(), home)} /></>);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(isPresenting()).toBe(false);
    fireEvent.keyDown(window, { key: '?' });
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeTruthy();
  });

  it('an open header popup (the theme picker) holds the keys like a dialog', () => {
    const home = vi.fn();
    const picker = (open: boolean) => (
      <>
        <button type="button" aria-haspopup="listbox" aria-expanded={open} aria-label="Theme">theme</button>
        {open && <div role="listbox" aria-label="Themes" />}
        <CommandPalette nav={twoNav(vi.fn(), home)} />
      </>
    );
    const { rerender } = render(picker(true));
    act(() => setPresenting(true));
    // The picker's own Escape closes it; presentation stays on.
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(isPresenting()).toBe(true);
    fireEvent.keyDown(window, { key: '?' });
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.keyDown(window, { key: 'g' });
    fireEvent.keyDown(window, { key: 'h' });
    expect(home).not.toHaveBeenCalled();
    rerender(picker(false));
    fireEvent.keyDown(window, { key: '?' });
    expect(screen.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeTruthy();
  });

  it('never fires inside a terminal or an editable region', () => {
    render(<><div className="xterm"><textarea aria-label="term" /></div><div contentEditable aria-label="ed" /><CommandPalette nav={twoNav()} /></>);
    fireEvent.keyDown(screen.getByLabelText('term'), { key: 'p' });
    const ed = screen.getByLabelText('ed');
    Object.defineProperty(ed, 'isContentEditable', { value: true });
    fireEvent.keyDown(ed, { key: 'p' });
    expect(isPresenting()).toBe(false);
  });
});

// DESK-06: a desk command run from another screen used to be lost, because
// the Desk was not mounted yet when the event fired.
describe('desk actions', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('a palette desk command from another screen is held for the Desk mount', () => {
    let mounted: (() => void) | null = null;
    const got: Array<[DeskAction, boolean]> = [];
    const goDesk = vi.fn(() => { mounted = listenDesk((a, held) => got.push([a, held])); });
    render(<CommandPalette nav={[{ id: 'desk', label: 'Desk', key: 'd', go: () => { /* tab switch: the Desk mounts later */ } }]} />);
    fireEvent.keyDown(window, { key: '/' });
    const box = screen.getByLabelText('Command');
    fireEvent.change(box, { target: { value: 'add a widget' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(got).toEqual([]);
    goDesk(); // the Desk mounts
    expect(got).toEqual([['add', true]]);
    // Taken once: a second mount gets nothing.
    (mounted as unknown as () => void)();
    const off = listenDesk((a, held) => got.push([a, held]));
    expect(got).toHaveLength(1);
    off();
  });

  it('runs at once when the Desk is already listening', () => {
    const got: Array<[DeskAction, boolean]> = [];
    const off = listenDesk((a, held) => got.push([a, held]));
    const go = vi.fn();
    toDesk(go, 'arrange');
    toDesk(go, 'sources');
    toDesk(go, 'sync');
    expect(go).toHaveBeenCalledTimes(3);
    expect(got).toEqual([['arrange', false], ['sources', false], ['sync', false]]);
    off();
    // Nothing is left held for a later mount.
    const later: DeskAction[] = [];
    const off2 = listenDesk((a) => later.push(a));
    expect(later).toEqual([]);
    off2();
  });

  it('a held action goes stale after PENDING_MS', () => {
    const now = vi.spyOn(Date, 'now').mockReturnValue(50_000);
    toDesk(undefined, 'add');
    now.mockReturnValue(50_000 + PENDING_MS + 1);
    const got: DeskAction[] = [];
    const off = listenDesk((a) => got.push(a));
    expect(got).toEqual([]);
    off();
  });

  it('deskEvent reaches a listener; an empty event is ignored', () => {
    const got: DeskAction[] = [];
    const off = listenDesk((a) => got.push(a));
    deskEvent('sync');
    window.dispatchEvent(new CustomEvent('devcules:desk', { detail: {} }));
    off();
    deskEvent('sync');
    expect(got).toEqual(['sync']);
  });
});
