/**
 * Presentational pieces around the jobs table: the filter toolbar, the
 * pagination footer, and the three data states.
 *
 * Extracted from `JobsTable.tsx` so that file reads as the data and mutation
 * logic it actually is, instead of that logic buried under markup.
 */
import type { ApiError } from '@lib/api';
import styles from '@styles/admin-table.module.css';

export type StatusFilter = 'all' | 'draft' | 'published' | 'unpublished';

const STATUS_OPTIONS: ReadonlyArray<{ value: StatusFilter; label: string }> = [
  { value: 'all', label: 'All statuses' },
  { value: 'draft', label: 'Draft' },
  { value: 'published', label: 'Published' },
  { value: 'unpublished', label: 'Unpublished' },
];

interface ToolbarProps {
  search: string;
  status: StatusFilter;
  onSearchChange: (value: string) => void;
  onStatusChange: (value: StatusFilter) => void;
}

export function JobsToolbar({ search, status, onSearchChange, onStatusChange }: ToolbarProps) {
  return (
    <div className="jpa-toolbar">
      <div>
        <label className="jpa-label" htmlFor="jobs-search">
          Search
        </label>
        <input
          className="jpa-input"
          id="jobs-search"
          type="search"
          value={search}
          placeholder="Title, company, location…"
          onChange={(event) => onSearchChange(event.target.value)}
        />
      </div>
      <div>
        <label className="jpa-label" htmlFor="jobs-status">
          Status
        </label>
        <select
          className="jpa-select"
          id="jobs-status"
          value={status}
          onChange={(event) => onStatusChange(event.target.value as StatusFilter)}
        >
          {STATUS_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </div>
      <div className="jpa-toolbar__actions">
        <a className="jpa-btn jpa-btn--primary" href="/admin/jobs/new">
          New job
        </a>
      </div>
    </div>
  );
}

interface PagerProps {
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
  onPageChange: (page: number) => void;
}

export function JobsPager({ page, perPage, total, totalPages, onPageChange }: PagerProps) {
  const from = (page - 1) * perPage + 1;
  const to = Math.min(page * perPage, total);
  const summary =
    total === 0
      ? 'No jobs match these filters.'
      : `Showing ${from}-${to} of ${total} job${total === 1 ? '' : 's'}.`;

  return (
    <div className={styles.pager}>
      {/* Polite, not assertive: the count changing is context, not an alert. */}
      <p className={styles.pagerCount} aria-live="polite">
        {summary}
      </p>
      <div className={styles.pagerControls}>
        <button
          type="button"
          className="jpa-btn jpa-btn--sm"
          disabled={page <= 1}
          onClick={() => onPageChange(Math.max(1, page - 1))}
        >
          Previous
        </button>
        <button
          type="button"
          className="jpa-btn jpa-btn--sm"
          disabled={totalPages === 0 || page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          Next
        </button>
      </div>
    </div>
  );
}

export function JobsLoading() {
  return (
    <div className="jpa-state" role="status">
      <span className="jpa-spinner" aria-hidden="true"></span>
      <span>Loading jobs…</span>
    </div>
  );
}

/** A retry button is offered only for failures that can plausibly succeed next time. */
export function JobsError({ error, onRetry }: { error: ApiError | null; onRetry: () => void }) {
  return (
    <div className="jpa-state">
      <strong>The job list could not be loaded.</strong>
      <span>{error?.message ?? 'An unexpected error occurred.'}</span>
      {error?.requestId && <span className="jpa-hint">Reference: {error.requestId}</span>}
      {(error === null || error.isRetryable) && (
        <button type="button" className="jpa-btn" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function JobsEmpty({ filtered }: { filtered: boolean }) {
  if (filtered) {
    return (
      <div className="jpa-state">
        <strong>No jobs match these filters</strong>
        <span>Try a broader search, or clear the status filter.</span>
      </div>
    );
  }
  return (
    <div className="jpa-state">
      <strong>No jobs yet</strong>
      <span>Every listing on the public site comes from this table.</span>
      <a className="jpa-btn jpa-btn--primary" href="/admin/jobs/new">
        Create the first job
      </a>
    </div>
  );
}
