/**
 * Labelled form controls for the admin forms.
 *
 * One place that wires up the four things every field must get right — a real
 * `<label for>`, `aria-invalid`, `aria-describedby` pointing at both the hint
 * and the error, and a character counter — so no individual field can forget
 * one. `id` is caller-supplied and deterministic (rather than `useId`) because
 * the 422 handler focuses fields by id after mapping server errors.
 */
import type { ReactNode } from 'react';
import FieldError from './FieldError';
import styles from '@styles/admin-form.module.css';

/** Counter appears only in the last fifth of the budget: quiet until it matters. */
const COUNTER_VISIBLE_FRACTION = 0.8;

interface BaseProps {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  error?: string | undefined;
  hint?: string | undefined;
  required?: boolean;
  maxLength?: number | undefined;
  disabled?: boolean;
  autoComplete?: string;
  placeholder?: string;
}

function fieldIds(id: string) {
  return { hintId: `${id}-hint`, errorId: `${id}-error` };
}

function describedBy(hint: string | undefined, error: string | undefined, id: string) {
  const { hintId, errorId } = fieldIds(id);
  const parts: string[] = [];
  if (hint) parts.push(hintId);
  if (error) parts.push(errorId);
  return parts.length > 0 ? parts.join(' ') : undefined;
}

function Counter({ length, maxLength }: { length: number; maxLength: number | undefined }) {
  if (maxLength === undefined) return null;
  if (length < maxLength * COUNTER_VISIBLE_FRACTION) return null;
  const over = length > maxLength;
  const className = over ? `${styles.counter} ${styles.counterOver}` : `${styles.counter} ${styles.counterNear}`;
  return (
    <span className={className} aria-live="polite">
      {length} / {maxLength}
    </span>
  );
}

function Label({
  id,
  label,
  required,
  value,
  maxLength,
}: Pick<BaseProps, 'id' | 'label' | 'required' | 'value' | 'maxLength'>) {
  return (
    <div className={styles.labelRow}>
      <label className="jpa-label" htmlFor={id}>
        {label}
        {required && (
          <>
            {' '}
            <span className="jpa-req" aria-hidden="true">
              *
            </span>
            <span className="jpa-visually-hidden">(required)</span>
          </>
        )}
      </label>
      <Counter length={value.length} maxLength={maxLength} />
    </div>
  );
}

function Shell({
  id,
  hint,
  error,
  children,
}: {
  id: string;
  hint: string | undefined;
  error: string | undefined;
  children: ReactNode;
}) {
  const { hintId, errorId } = fieldIds(id);
  return (
    <div className={styles.field}>
      {children}
      {hint && (
        <p className="jpa-hint" id={hintId}>
          {hint}
        </p>
      )}
      <FieldError id={errorId} message={error} />
    </div>
  );
}

export function TextField({ type = 'text', ...props }: BaseProps & { type?: string }) {
  const { id, label, value, onChange, error, hint, required, maxLength, disabled } = props;
  return (
    <Shell id={id} hint={hint} error={error}>
      <Label id={id} label={label} required={required} value={value} maxLength={maxLength} />
      <input
        className="jpa-input"
        id={id}
        name={id}
        type={type}
        value={value}
        required={required}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(hint, error, id)}
        {...(maxLength === undefined ? {} : { maxLength })}
        {...(props.autoComplete === undefined ? {} : { autoComplete: props.autoComplete })}
        {...(props.placeholder === undefined ? {} : { placeholder: props.placeholder })}
        onChange={(event) => onChange(event.target.value)}
      />
    </Shell>
  );
}

export function TextArea({ rows = 8, ...props }: BaseProps & { rows?: number }) {
  const { id, label, value, onChange, error, hint, required, maxLength, disabled } = props;
  return (
    <Shell id={id} hint={hint} error={error}>
      <Label id={id} label={label} required={required} value={value} maxLength={maxLength} />
      <textarea
        className="jpa-textarea"
        id={id}
        name={id}
        rows={rows}
        value={value}
        required={required}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(hint, error, id)}
        {...(maxLength === undefined ? {} : { maxLength })}
        {...(props.placeholder === undefined ? {} : { placeholder: props.placeholder })}
        onChange={(event) => onChange(event.target.value)}
      />
    </Shell>
  );
}

export interface SelectOption {
  value: string;
  label: string;
}

export function SelectField(
  props: Omit<BaseProps, 'maxLength' | 'autoComplete' | 'placeholder'> & {
    options: readonly SelectOption[];
    /** Rendered as an empty first option. Omit to force an explicit choice. */
    emptyLabel?: string;
  },
) {
  const { id, label, value, onChange, error, hint, required, disabled, options, emptyLabel } = props;
  return (
    <Shell id={id} hint={hint} error={error}>
      <Label id={id} label={label} required={required} value={value} maxLength={undefined} />
      <select
        className="jpa-select"
        id={id}
        name={id}
        value={value}
        required={required}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(hint, error, id)}
        onChange={(event) => onChange(event.target.value)}
      >
        {emptyLabel !== undefined && <option value="">{emptyLabel}</option>}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </Shell>
  );
}

export function CheckboxField({
  id,
  label,
  checked,
  onChange,
  hint,
  disabled,
}: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  hint?: string | undefined;
  disabled?: boolean;
}) {
  const { hintId } = fieldIds(id);
  return (
    <div className={styles.field}>
      <div className="jpa-checkbox">
        <input
          id={id}
          name={id}
          type="checkbox"
          checked={checked}
          disabled={disabled}
          aria-describedby={hint ? hintId : undefined}
          onChange={(event) => onChange(event.target.checked)}
        />
        <label htmlFor={id}>{label}</label>
      </div>
      {hint && (
        <p className="jpa-hint" id={hintId}>
          {hint}
        </p>
      )}
    </div>
  );
}
