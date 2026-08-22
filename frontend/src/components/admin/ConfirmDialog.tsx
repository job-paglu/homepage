/**
 * Modal confirmation.
 *
 * `requireText` exists because "Are you sure?" is a button people click without
 * reading. Making the admin retype the job title turns the confirmation into an
 * act of identification: it is very hard to delete the wrong row by accident
 * when you have to name the right one.
 */
import { useEffect, useId, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import styles from '@styles/admin-dialog.module.css';

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  /** When set, confirm stays disabled until the user types this exactly. */
  requireText?: string | undefined;
  requireTextLabel?: string | undefined;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export default function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  cancelLabel = 'Cancel',
  requireText,
  requireTextLabel = 'Type the job title to confirm',
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const headingId = useId();
  const bodyId = useId();
  const confirmFieldId = useId();
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const restoreFocusTo = useRef<HTMLElement | null>(null);
  const [typed, setTyped] = useState('');
  /**
   * Read inside the keydown listener. A ref rather than a dependency: adding
   * `busy` to the listener effect would re-run its focus setup and its
   * restore-focus cleanup every time a request starts or finishes.
   */
  const busyRef = useRef(busy);
  useEffect(() => {
    busyRef.current = busy;
  }, [busy]);

  // Reset the acknowledgement whenever the dialog reopens, so a previous
  // confirmation can never carry over to a different row.
  useEffect(() => {
    if (open) setTyped('');
  }, [open]);

  useEffect(() => {
    if (!open) return;

    restoreFocusTo.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    document.documentElement.dataset['jpaModal'] = 'open';

    const node = dialogRef.current;
    node?.querySelector<HTMLElement>(FOCUSABLE)?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        // Cancelling mid-request would leave the caller's in-flight mutation
        // running with no UI attached to its result.
        if (!busyRef.current) onCancel();
        return;
      }
      if (event.key !== 'Tab' || !node) return;

      const items = Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE));
      const first = items[0];
      const last = items[items.length - 1];
      if (!first || !last) return;

      const active = document.activeElement;
      // Focus escaping the dialog entirely (browser chrome, a stray click) is
      // treated as "wrap to the far end" rather than ignored.
      if (event.shiftKey && (active === first || !node.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (active === last || !node.contains(active))) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('keydown', onKeyDown, true);
      delete document.documentElement.dataset['jpaModal'];
      restoreFocusTo.current?.focus();
    };
  }, [open, onCancel]);

  if (!open) return null;

  const acknowledged = requireText === undefined || typed.trim() === requireText.trim();

  return (
    <div
      className={styles.overlay}
      // A click on the backdrop cancels; a click inside must not bubble out and
      // be read as a backdrop click. Keyboard users get Escape, handled above,
      // so no keyboard handler belongs on this decorative element.
      onClick={() => {
        if (!busy) onCancel();
      }}
    >
      <div
        className={styles.dialog}
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingId}
        aria-describedby={bodyId}
        onClick={(event) => event.stopPropagation()}
      >
        <h2 className={styles.dialogTitle} id={headingId}>
          {title}
        </h2>
        <div className={styles.dialogBody} id={bodyId}>
          {children}
        </div>

        {requireText !== undefined && (
          <div className={styles.dialogConfirmField}>
            <label className="jpa-label" htmlFor={confirmFieldId}>
              {requireTextLabel}
            </label>
            <input
              className="jpa-input"
              id={confirmFieldId}
              type="text"
              value={typed}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setTyped(event.target.value)}
            />
          </div>
        )}

        <div className={styles.dialogActions}>
          <button type="button" className="jpa-btn" onClick={onCancel} disabled={busy}>
            {cancelLabel}
          </button>
          <button
            type="button"
            className={danger ? 'jpa-btn jpa-btn--danger' : 'jpa-btn jpa-btn--primary'}
            onClick={onConfirm}
            disabled={busy || !acknowledged}
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
