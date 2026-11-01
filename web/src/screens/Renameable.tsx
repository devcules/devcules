import { useEffect, useRef, useState } from 'react';

// Renameable is the inline label-edit every terminal and assistant
// name uses: a right-click (or the accessible ✎ affordance) swaps the
// label for an input Commit is Enter/blur; Escape cancels. The parent
// owns persistence — onCommit resolves true when the server took it,
// and the label reverts otherwise. The label keeps its exact footprint
// while editing, so the row doesn't jump.
export default function Renameable({
  value,
  onCommit,
  className = '',
  inputClassName = '',
}: {
  value: string;
  // onCommit resolves true when the rename stuck; false reverts.
  onCommit: (next: string) => Promise<boolean> | boolean;
  className?: string;
  inputClassName?: string;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) {
      inputRef.current?.select();
    }
  }, [editing]);

  const commit = async () => {
    const next = draft.trim();
    if (busy) {
      return;
    }
    if (next === '' || next === value) {
      setDraft(value);
      setEditing(false);
      return;
    }
    setBusy(true);
    try {
      const ok = await onCommit(next);
      if (ok) {
        setEditing(false);
      } else {
        setDraft(value);
        setEditing(false);
      }
    } finally {
      setBusy(false);
    }
  };

  if (!editing) {
    return (
      <span
        // The label opens its editor on right-click (and the keyboard
        // via the button role); stopping propagation keeps the row's
        // own activate from firing as you ask to rename.
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setDraft(value);
          setEditing(true);
        }}
        title={`${value} — right-click to rename`}
        className={`min-w-0 cursor-context-menu truncate ${className}`}
      >
        {value}
      </span>
    );
  }
  return (
    <input
      ref={inputRef}
      value={draft}
      disabled={busy}
      aria-label="Rename"
      onChange={(e) => setDraft(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          void commit();
        } else if (e.key === 'Escape') {
          e.preventDefault();
          setDraft(value);
          setEditing(false);
        }
        e.stopPropagation();
      }}
      onBlur={() => void commit()}
      maxLength={40}
      className={`i-rename ${inputClassName}`}
    />
  );
}
