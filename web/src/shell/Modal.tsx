// Modal is the one dialog frame: it portals to <body> (so a transformed
// ancestor such as a grid tile cannot clip its backdrop), marks itself
// aria-modal, moves focus in on open, keeps Tab inside, closes on Escape
// or a click on the backdrop, and hands focus back on close.
//
// Keys are handled on the dialog itself, not on the window: focus is
// trapped inside, so the dialog sees its own keys first, and an Escape it
// consumes stops there instead of also reaching the global shortcuts.

import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

const FOCUSABLE = [
  'a[href]', 'button:not([disabled])', 'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])', 'textarea:not([disabled])', '[tabindex]:not([tabindex="-1"])',
].join(',');

function focusables(root: HTMLElement): HTMLElement[] {
  // A control taken out of the Tab order (tabindex="-1", such as the
  // palette's rows behind aria-activedescendant) is not a stop: counting
  // it as the last one let Tab walk out of the dialog.
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
    .filter((el) => !el.closest('[inert]') && el.getAttribute('tabindex') !== '-1');
}

export default function Modal({ label, onClose, className, children }: {
  // The accessible name of the dialog.
  label: string;
  onClose: () => void;
  // Classes for the backdrop, which is also the dialog element (layout,
  // z-index, padding); the panel is the child.
  className: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // Captured during the first render, before any autoFocus inside runs.
  const [opener] = useState(() => (typeof document !== 'undefined' ? document.activeElement as HTMLElement | null : null));
  const downOnBackdrop = useRef(false);

  useEffect(() => {
    const root = ref.current;
    // An autoFocus field inside has already taken focus; otherwise land
    // on the first control, or on the dialog itself. preventScroll keeps a
    // long sheet (Add a widget on a phone) at its top, heading in view.
    if (root && !root.contains(document.activeElement)) (focusables(root)[0] ?? root).focus({ preventScroll: true });
    return () => {
      if (opener && opener.isConnected && typeof opener.focus === 'function') opener.focus();
    };
  }, [opener]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== 'Tab' || !ref.current) return;
    const list = focusables(ref.current);
    if (list.length === 0) { e.preventDefault(); ref.current.focus(); return; }
    const first = list[0];
    const last = list[list.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || active === ref.current || !ref.current.contains(active))) {
      e.preventDefault(); last.focus();
    } else if (!e.shiftKey && (active === last || !ref.current.contains(active))) {
      e.preventDefault(); first.focus();
    }
  };

  // Close only on a press that both starts and ends on the backdrop, so a
  // text selection dragged out of a field does not dismiss the dialog.
  const onMouseDown = (e: MouseEvent<HTMLDivElement>) => { downOnBackdrop.current = e.target === e.currentTarget; };
  const onClick = (e: MouseEvent<HTMLDivElement>) => {
    if (downOnBackdrop.current && e.target === e.currentTarget) onClose();
    downOnBackdrop.current = false;
  };

  return createPortal(
    <div
      ref={ref}
      role="dialog"
      aria-modal="true"
      aria-label={label}
      tabIndex={-1}
      className={`outline-none ${className}`}
      onKeyDown={onKeyDown}
      onMouseDown={onMouseDown}
      onClick={onClick}
    >
      {children}
    </div>,
    document.body,
  );
}
