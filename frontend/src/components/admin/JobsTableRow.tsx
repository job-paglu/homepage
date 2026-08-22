/**
 * One job row.
 *
 * The `role` attributes duplicate the implicit table semantics on purpose:
 * `admin-table.module.css` flips these elements to `display: block` below 700px
 * to build the card layout, and Chromium/WebKit drop the implicit table roles
 * when `display` changes. Stating them keeps the row a row for assistive tech in
 * both layouts. In the wide layout they are redundant no-ops.
 *
 * Each cell also carries its own label element, hidden on wide screens. A
 * CSS-generated `content: attr(data-label)` would be invisible to some screen
 * readers, so the label is real DOM.
 */
import type { ReactNode } from 'react';
import type { JobSummary } from '@lib/types';
import StatusBadge from './StatusBadge';
import { formatDateTime, jobTypeLabel } from './jobFields';
import styles from '@styles/admin-table.module.css';

interface JobsTableRowProps {
  job: JobSummary;
  busy: boolean;
  onPublish: (job: JobSummary) => void;
  onUnpublish: (job: JobSummary) => void;
  onDelete: (job: JobSummary) => void;
}

function Cell({
  label,
  extraClass,
  children,
}: {
  label: string;
  extraClass?: string | undefined;
  children: ReactNode;
}) {
  return (
    <td className={extraClass ? `${styles.cell} ${extraClass}` : styles.cell} role="cell">
      <span className={styles.cellLabel}>{label}</span>
      <span>{children}</span>
    </td>
  );
}

export default function JobsTableRow({
  job,
  busy,
  onPublish,
  onUnpublish,
  onDelete,
}: JobsTableRowProps) {
  const isPublished = job.status === 'published';

  return (
    <tr className={styles.row} role="row" data-busy={busy ? 'true' : 'false'}>
      <Cell label="Job" extraClass={styles.cellTitle}>
        {isPublished ? (
          <a
            className={`${styles.title} ${styles.titleLink}`}
            href={`/jobs/${job.slug}`}
            target="_blank"
            rel="noopener noreferrer"
          >
            {job.title}
            <span className="jpa-visually-hidden"> (opens the public page in a new tab)</span>
          </a>
        ) : (
          <span className={styles.title}>{job.title}</span>
        )}
        <span className={styles.slug}>/{job.slug}</span>
      </Cell>

      <Cell label="Status">
        <StatusBadge status={job.status} />
      </Cell>

      <Cell label="Type">
        <span className={styles.muted}>{jobTypeLabel(job.job_type)}</span>
      </Cell>

      <Cell label="Location">{job.location}</Cell>

      <Cell label="Updated">
        <span className={styles.muted}>{formatDateTime(job.updated_at)}</span>
      </Cell>

      <td className={`${styles.cell} ${styles.cellActions}`} role="cell">
        <span className={styles.cellLabel}>Actions</span>
        <span className={styles.actions}>
          <a className="jpa-btn jpa-btn--sm" href={`/admin/jobs/${job.id}/edit`}>
            Edit<span className="jpa-visually-hidden"> {job.title}</span>
          </a>
          {isPublished ? (
            <button
              type="button"
              className="jpa-btn jpa-btn--sm"
              disabled={busy}
              onClick={() => onUnpublish(job)}
            >
              Unpublish<span className="jpa-visually-hidden"> {job.title}</span>
            </button>
          ) : (
            <button
              type="button"
              className="jpa-btn jpa-btn--sm jpa-btn--primary"
              disabled={busy}
              onClick={() => onPublish(job)}
            >
              Publish<span className="jpa-visually-hidden"> {job.title}</span>
            </button>
          )}
          <button
            type="button"
            className="jpa-btn jpa-btn--sm jpa-btn--danger"
            disabled={busy}
            onClick={() => onDelete(job)}
          >
            Delete<span className="jpa-visually-hidden"> {job.title}</span>
          </button>
        </span>
      </td>
    </tr>
  );
}
