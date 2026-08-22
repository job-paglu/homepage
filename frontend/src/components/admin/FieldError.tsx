/**
 * A field-level validation message.
 *
 * Renders nothing when there is no message, so a caller can always mount it and
 * keep the `aria-describedby` id stable — pointing at a missing element is
 * harmless, whereas conditionally changing the attribute is easy to get wrong.
 *
 * No `role="alert"`: these appear while the user types, and an assertive live
 * region on every keystroke is unusable. The association is made by
 * `aria-describedby` + `aria-invalid` on the input instead, which is what a
 * screen reader reads when focus lands on the field.
 */
interface FieldErrorProps {
  /** Must match the input's `aria-describedby`. */
  id: string;
  message?: string | null | undefined;
}

export default function FieldError({ id, message }: FieldErrorProps) {
  if (!message) return null;
  return (
    <p className="jpa-error" id={id}>
      {/* An icon as well as colour: the error must survive greyscale. */}
      <span aria-hidden="true">!</span>
      <span>{message}</span>
    </p>
  );
}
