/**
 * Publication status.
 *
 * An unknown status is rendered verbatim rather than dropped or mapped to a
 * default: contract section 2.4 allows the API to add an enum member before the
 * frontend knows about it, and silently showing "draft" for a `scheduled` job
 * would be a lie about whether it is public.
 */
const KNOWN: Readonly<Record<string, { label: string; className: string }>> = {
  draft: { label: 'Draft', className: 'jpa-badge--draft' },
  published: { label: 'Published', className: 'jpa-badge--published' },
  unpublished: { label: 'Unpublished', className: 'jpa-badge--unpublished' },
};

interface StatusBadgeProps {
  status: string;
}

export default function StatusBadge({ status }: StatusBadgeProps) {
  const known = KNOWN[status];
  return (
    <span className={known ? `jpa-badge ${known.className}` : 'jpa-badge'}>
      {known ? known.label : status}
    </span>
  );
}
