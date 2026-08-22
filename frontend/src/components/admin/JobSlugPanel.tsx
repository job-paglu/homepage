/**
 * The public-URL panel that sits directly under the job title.
 *
 * Two different jobs, one component:
 *
 *   create — shows the slug the server will derive, live. Advisory only: the
 *            server de-duplicates, so the real URL may gain a `-2`.
 *   edit   — shows the *existing* slug and leaves it alone unless the admin
 *            ticks the box. Slug stability is a contract guarantee (section 4,
 *            `PUT`): correcting a typo in a title must not 404 every link that
 *            has already been shared.
 *
 * The warning is stated plainly rather than softened. "Changing this breaks
 * existing links" is the whole reason the opt-in exists.
 */
import styles from '@styles/admin-form.module.css';

/** Fixed ids: the 409/422 handler focuses the slug input by id. */
export const SLUG_INPUT_ID = 'job-slug';
const SLUG_ERROR_ID = 'job-slug-error';
const SLUG_WARNING_ID = 'job-slug-warning';

interface JobSlugPanelProps {
  mode: 'create' | 'edit';
  /** Live preview derived from the title. Create mode only. */
  previewSlug: string;
  existingSlug: string;
  renaming: boolean;
  draft: string;
  error?: string | undefined;
  disabled: boolean;
  onToggleRename: (renaming: boolean) => void;
  onDraftChange: (value: string) => void;
}

export default function JobSlugPanel({
  mode,
  previewSlug,
  existingSlug,
  renaming,
  draft,
  error,
  disabled,
  onToggleRename,
  onDraftChange,
}: JobSlugPanelProps) {
  if (mode === 'create') {
    return (
      <div className={styles.slugPanel}>
        <p className={styles.slugRow}>
          <span>Public URL will be</span>
          <span className={styles.slugValue}>/jobs/{previewSlug === '' ? 'job-...' : previewSlug}</span>
          <span>
            {previewSlug === ''
              ? '(this title has no Latin letters or digits, so the server assigns a URL)'
              : '(a number is appended if that URL is already taken)'}
          </span>
        </p>
      </div>
    );
  }

  return (
    <div className={styles.slugPanel}>
      <p className={styles.slugRow}>
        <span>Public URL</span>
        <span className={styles.slugValue}>/jobs/{renaming ? draft : existingSlug}</span>
      </p>

      <div className="jpa-checkbox">
        <input
          id="job-slug-rename"
          type="checkbox"
          checked={renaming}
          disabled={disabled}
          onChange={(event) => onToggleRename(event.target.checked)}
        />
        <label htmlFor="job-slug-rename">Change the URL of this job</label>
      </div>

      {renaming && (
        <>
          <label className="jpa-label" htmlFor={SLUG_INPUT_ID}>
            New URL segment
          </label>
          <input
            className="jpa-input"
            id={SLUG_INPUT_ID}
            type="text"
            value={draft}
            spellCheck={false}
            disabled={disabled}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? `${SLUG_ERROR_ID} ${SLUG_WARNING_ID}` : SLUG_WARNING_ID}
            onChange={(event) => onDraftChange(event.target.value)}
          />
          {error && (
            <p className="jpa-error" id={SLUG_ERROR_ID}>
              <span aria-hidden="true">!</span>
              <span>{error}</span>
            </p>
          )}
          <p className={styles.slugWarning} id={SLUG_WARNING_ID}>
            <span aria-hidden="true">!</span>
            <span>
              Changing this breaks every existing link to the job. Shared links, search results
              and bookmarks will all return a 404. Leave it alone unless the current URL is wrong.
            </span>
          </p>
        </>
      )}
    </div>
  );
}
