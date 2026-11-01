import { useCallback, type ReactNode } from 'react';

// Section is the Settings disclosure: a text-level chevron header —
// no card — so sections never nest surface on surface. A collapsed
// section is one quiet line (chevron + title + summary), still
// naming its state ("2 registered", "All off") the way an Azure
// blade header does. Several sections can be open at a time
// (Passkeys and Providers are edited together at onboarding);
// children stay mounted while hidden so a fold never loses a typed
// form field.

export interface SectionProps {
  /** key in the persisted fold record */
  id: string;
  title: string;
  /** one muted short clause shown only when collapsed */
  summary?: string;
  open: boolean;
  onToggle: (id: string) => void;
  children: ReactNode;
}

export default function Section({ id, title, summary, open, onToggle, children }: SectionProps) {
  // useCallback keeps the toggle stable for the memo-free child
  // sections this drives; a fresh arrow per render was the bug the
  // test for remount-on-every-keystroke caught.
  const toggle = useCallback(() => onToggle(id), [id, onToggle]);
  return (
    <section aria-label={title}>
      <h3>
        <button
          type="button"
          onClick={toggle}
          aria-expanded={open}
          aria-controls={`section-${id}`}
          data-expanded={open ? 'true' : undefined}
          className="i-control flex min-h-11 w-full items-start gap-3 px-3 py-2 text-left"
        >
          <span
            aria-hidden="true"
            className={`mt-1 inline-block text-muted transition-transform duration-150 ${
              open ? 'rotate-90' : ''
            }`}
          >
            ▸
          </span>
          <span className="min-w-0 flex-1">
            <span className="text-body font-semibold text-text">{title}</span>
            {!open && summary && <span className="block text-body text-muted">{summary}</span>}
          </span>
        </button>
      </h3>
      <div
        id={`section-${id}`}
        hidden={!open}
        // Indent the body to the label column so the open section
        // reads as one block without borders; the naked div keeps
        // `hidden` controlling display.
        className="ml-6 flex flex-col gap-3 pt-1"
      >
        {children}
      </div>
    </section>
  );
}
