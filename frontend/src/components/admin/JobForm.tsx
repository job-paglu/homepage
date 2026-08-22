/**
 * Create / edit a job.
 *
 * This is the one place in Job Paglu where a React island earns its bytes: a
 * live slug preview, debounced apply-URL validation and an unsaved-changes
 * guard all have to react to typing, and none of them survives being turned
 * into a server round trip without making the form feel broken.
 *
 * Slug policy (contract section 8, and the `PUT` rules in section 4):
 *   - create: the slug is NOT sent. The server derives it from the title and
 *     de-duplicates, so an auto-slug can never 409. The preview is advisory.
 *   - edit: the slug is NOT sent either, unless the admin explicitly opts in.
 *     Omitting it is what keeps a live URL stable when a title is corrected.
 *
 * Form shape lives in `JobFormFields.tsx`, state transforms in
 * `jobFormState.ts`, the URL panel in `JobSlugPanel.tsx`. What is left here is
 * only behaviour.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { ApiError } from '@lib/api';
import { createJob, getAdminJob, updateJob } from '@lib/adminApi';
import { ACCESS_NOTICE_UNSAVED, isNotAuthenticated, loadIdentity } from '@lib/adminIdentity';
import AccessNotice from './AccessNotice';
import ConfirmDialog from './ConfirmDialog';
import JobFormFields, { fieldDomId } from './JobFormFields';
import type { JobFormState } from './JobFormFields';
import JobSlugPanel, { SLUG_INPUT_ID } from './JobSlugPanel';
import { EMPTY_JOB_FORM, localJobErrors, stateFromJob, toJobInput } from './jobFormState';
import { JOB_FIELD_ORDER, deriveSlug, firstFieldInOrder, isValidSlug, validateApplyUrl } from './jobFields';
import styles from '@styles/admin-form.module.css';

const APPLY_URL_DEBOUNCE_MS = 400;
const LIST_PATH = '/admin/jobs';

/** `signed-out` is separate from `load-error` for the reason given in
 *  `adminIdentity.ts`: the remedy is a reload, not a retry. */
type Phase = 'loading' | 'ready' | 'load-error' | 'signed-out';

interface JobFormProps {
  /** Present for edit, absent for create. */
  jobId?: string | undefined;
}

export default function JobForm({ jobId }: JobFormProps) {
  const editing = jobId !== undefined && jobId !== '';
  const [phase, setPhase] = useState<Phase>(editing ? 'loading' : 'ready');
  const [loadError, setLoadError] = useState<string | null>(null);
  const [state, setState] = useState<JobFormState>(EMPTY_JOB_FORM);
  const [baseline, setBaseline] = useState<JobFormState>(EMPTY_JOB_FORM);
  const [existingSlug, setExistingSlug] = useState('');
  const [renaming, setRenaming] = useState(false);
  const [slugDraft, setSlugDraft] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [applyIssue, setApplyIssue] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [leaveOpen, setLeaveOpen] = useState(false);
  /** Disarmed just before a deliberate navigation, so saving does not warn. */
  const guardArmed = useRef(true);

  const dirty = useMemo(
    () => JSON.stringify(state) !== JSON.stringify(baseline) || renaming,
    [state, baseline, renaming],
  );

  useEffect(() => {
    if (jobId === undefined || jobId === '') return;
    const controller = new AbortController();
    let cancelled = false;
    void (async () => {
      try {
        await loadIdentity();
        const job = await getAdminJob(jobId, { signal: controller.signal });
        if (cancelled) return;
        const next = stateFromJob(job);
        setState(next);
        setBaseline(next);
        setExistingSlug(job.slug);
        setSlugDraft(job.slug);
        setPhase('ready');
      } catch (caught) {
        if (cancelled) return;
        if (isNotAuthenticated(caught)) {
          setPhase('signed-out');
          return;
        }
        setLoadError(caught instanceof ApiError ? caught.message : 'This job could not be loaded.');
        setPhase('load-error');
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [jobId]);

  // Debounced so the message does not flicker while a URL is being typed.
  useEffect(() => {
    if (state.apply_url.trim() === '') {
      setApplyIssue(null);
      return;
    }
    const timer = setTimeout(
      () => setApplyIssue(validateApplyUrl(state.apply_url)),
      APPLY_URL_DEBOUNCE_MS,
    );
    return () => clearTimeout(timer);
  }, [state.apply_url]);

  // Browser-level guard for real navigations. Leaving via the Cancel button uses
  // the dialog below instead, which can explain itself; `beforeunload` cannot.
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!guardArmed.current) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);

  const setField = useCallback(<K extends keyof JobFormState>(key: K, value: JobFormState[K]) => {
    setState((current) => ({ ...current, [key]: value }));
    // Clear the field's error the moment it is touched, so a stale message never
    // sits under a value the admin has already fixed.
    setErrors((current) => {
      if (!(key in current)) return current;
      const next = { ...current };
      delete next[key as string];
      return next;
    });
  }, []);

  const leave = useCallback((target: string) => {
    guardArmed.current = false;
    window.location.assign(target);
  }, []);

  /**
   * Map errors onto inputs, then move focus to the first invalid one. Without
   * the focus move a keyboard or screen-reader user is told something is wrong
   * and left to hunt for it.
   */
  const applyErrors = useCallback((details: Record<string, string>, message: string) => {
    setErrors(details);

    // A `details` key with no matching input — the API validating a field this
    // form does not expose, or a rename after a contract change — would vanish
    // silently. Fold those into the form-level message instead.
    const orphans = Object.entries(details)
      .filter(([field]) => !(JOB_FIELD_ORDER as readonly string[]).includes(field))
      .map(([field, text]) => `${field}: ${text}`);
    setFormError(orphans.length > 0 ? `${message} ${orphans.join(' ')}` : message);

    const target = firstFieldInOrder(Object.keys(details));
    if (target === undefined) return;
    // After paint: the input has to exist and be enabled before it can be focused.
    requestAnimationFrame(() => {
      document.getElementById(target === 'slug' ? SLUG_INPUT_ID : fieldDomId(target))?.focus();
    });
  }, []);

  const previewSlug = useMemo(() => deriveSlug(state.title), [state.title]);
  const effectiveSlug = editing && renaming ? slugDraft.trim() : undefined;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting) return; // second guard; the disabled button is the first

    const found = localJobErrors(state);
    if (effectiveSlug !== undefined && !isValidSlug(effectiveSlug)) {
      found['slug'] = 'Lower-case letters, digits and single hyphens only, up to 100 characters.';
    }
    if (Object.keys(found).length > 0) {
      applyErrors(found, 'Some fields need attention before this job can be saved.');
      return;
    }

    setSubmitting(true);
    setFormError(null);
    setErrors({});
    try {
      const input = toJobInput(state, effectiveSlug);
      if (jobId !== undefined && jobId !== '') {
        await updateJob(jobId, input);
      } else {
        await createJob(input);
      }
      // No success toast: the list is the confirmation, and it orders by
      // `updated_at DESC`, so the job just saved is the first row.
      leave(LIST_PATH);
      return; // stays disabled through the navigation
    } catch (caught) {
      setSubmitting(false);
      // A 401 while saving must NOT swap the form out for the sign-in notice:
      // unmounting the form would discard everything the admin typed. Keep the
      // form, explain, and let them re-submit once Access has authenticated them
      // in another tab.
      if (isNotAuthenticated(caught)) {
        setFormError(ACCESS_NOTICE_UNSAVED);
        return;
      }
      if (!(caught instanceof ApiError)) {
        setFormError('Saving failed for an unexpected reason. Please try again.');
        return;
      }
      if (caught.code === 'VALIDATION_FAILED') {
        applyErrors(caught.details, caught.message);
      } else if (caught.code === 'SLUG_CONFLICT') {
        applyErrors(
          { slug: 'That URL is already used by another job. Choose a different one.' },
          caught.message,
        );
      } else {
        setFormError(caught.message);
      }
    }
  }

  if (phase === 'loading') {
    return (
      <div className="jpa-state" role="status">
        <span className="jpa-spinner" aria-hidden="true"></span>
        <span>Loading job…</span>
      </div>
    );
  }

  // Only reachable from the initial load of an EXISTING job, where there is
  // nothing typed to lose — the save path above deliberately handles 401
  // differently.
  if (phase === 'signed-out') {
    return <AccessNotice />;
  }

  if (phase === 'load-error') {
    return (
      <div className="jpa-state">
        <strong>This job could not be loaded.</strong>
        <span>{loadError}</span>
        <a className="jpa-btn" href={LIST_PATH}>
          Back to all jobs
        </a>
      </div>
    );
  }

  return (
    <>
      {formError && (
        <div className="jpa-alert jpa-alert--error" role="alert">
          <span aria-hidden="true">!</span>
          <span>{formError}</span>
        </div>
      )}

      <form className={`jpa-panel ${styles.form}`} onSubmit={onSubmit} noValidate>
        <JobFormFields
          state={state}
          // The debounced apply-URL check overlays the server's message, so
          // typing a fix clears the 422 without another submit.
          errors={{ ...errors, ...(applyIssue ? { apply_url: applyIssue } : {}) }}
          disabled={submitting}
          onChange={setField}
          slugPanel={
            <JobSlugPanel
              mode={editing ? 'edit' : 'create'}
              previewSlug={previewSlug}
              existingSlug={existingSlug}
              renaming={renaming}
              draft={slugDraft}
              error={errors['slug']}
              disabled={submitting}
              onToggleRename={(next) => {
                setRenaming(next);
                if (!next) setSlugDraft(existingSlug);
              }}
              onDraftChange={setSlugDraft}
            />
          }
        />

        <div className={styles.footer}>
          <p className={styles.footerNote}>
            {editing
              ? 'Saving does not change whether the job is public. Use Publish or Unpublish in the job list.'
              : 'New jobs are saved as drafts. Publish them from the job list when they are ready.'}
          </p>
          <button
            type="button"
            className="jpa-btn"
            disabled={submitting}
            onClick={() => (dirty ? setLeaveOpen(true) : leave(LIST_PATH))}
          >
            Cancel
          </button>
          <button type="submit" className="jpa-btn jpa-btn--primary" disabled={submitting}>
            {submitting ? 'Saving…' : editing ? 'Save changes' : 'Create job'}
          </button>
        </div>
      </form>

      <ConfirmDialog
        open={leaveOpen}
        title="Discard your changes?"
        confirmLabel="Discard and leave"
        cancelLabel="Keep editing"
        danger
        onConfirm={() => leave(LIST_PATH)}
        onCancel={() => setLeaveOpen(false)}
      >
        <p>This form has unsaved changes. Leaving now throws them away.</p>
      </ConfirmDialog>
    </>
  );
}
