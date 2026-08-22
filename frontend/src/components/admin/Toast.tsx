/**
 * Transient status messages.
 *
 * Two live regions, not one: successes are `polite` so they wait for a pause in
 * speech, errors are `assertive` because an admin who does not hear that a
 * publish failed will assume it succeeded. Mixing both tones into one region
 * would force a single politeness setting on both.
 *
 * Errors also never auto-dismiss. A missed error is worse than clutter, and the
 * only thing that clears one is the user acknowledging it.
 */
import { useCallback, useMemo, useRef, useState } from 'react';
import styles from '@styles/admin-dialog.module.css';

export type ToastTone = 'success' | 'error' | 'info';

export interface ToastItem {
  id: number;
  tone: ToastTone;
  message: string;
}

const AUTO_DISMISS_MS = 6000;

export interface ToastController {
  toasts: ToastItem[];
  push: (tone: ToastTone, message: string) => void;
  dismiss: (id: number) => void;
}

export function useToasts(): ToastController {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextId = useRef(0);

  const dismiss = useCallback((id: number) => {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const push = useCallback(
    (tone: ToastTone, message: string) => {
      nextId.current += 1;
      const id = nextId.current;
      setToasts((current) => [...current, { id, tone, message }]);
      if (tone !== 'error') {
        setTimeout(() => dismiss(id), AUTO_DISMISS_MS);
      }
    },
    [dismiss],
  );

  // Memoised so callers can list the controller in a `useCallback` dependency
  // array without rebuilding every handler on each render.
  return useMemo(() => ({ toasts, push, dismiss }), [toasts, push, dismiss]);
}

const TONE_CLASS: Readonly<Record<ToastTone, string>> = {
  success: styles.toastSuccess ?? '',
  error: styles.toastError ?? '',
  info: styles.toastInfo ?? '',
};

interface ToastStackProps {
  toasts: ToastItem[];
  onDismiss: (id: number) => void;
}

function Group({
  toasts,
  onDismiss,
  live,
  label,
}: ToastStackProps & { live: 'polite' | 'assertive'; label: string }) {
  return (
    <div className={styles.toastGroup} role="status" aria-live={live} aria-label={label}>
      {toasts.map((toast) => (
        <div key={toast.id} className={`${styles.toast} ${TONE_CLASS[toast.tone]}`}>
          <span className={styles.toastText}>{toast.message}</span>
          <button
            type="button"
            className={`jpa-btn jpa-btn--ghost jpa-btn--sm ${styles.toastDismiss}`}
            onClick={() => onDismiss(toast.id)}
          >
            <span aria-hidden="true">x</span>
            <span className="jpa-visually-hidden">Dismiss this message</span>
          </button>
        </div>
      ))}
    </div>
  );
}

export function ToastStack({ toasts, onDismiss }: ToastStackProps) {
  const errors = toasts.filter((toast) => toast.tone === 'error');
  const rest = toasts.filter((toast) => toast.tone !== 'error');

  // Both regions are mounted even when empty. A live region added to the DOM at
  // the same moment as its content is unreliably announced; one that already
  // exists always is.
  return (
    <div className={styles.toastRegion}>
      <Group toasts={errors} onDismiss={onDismiss} live="assertive" label="Errors" />
      <Group toasts={rest} onDismiss={onDismiss} live="polite" label="Notifications" />
    </div>
  );
}

export default ToastStack;
