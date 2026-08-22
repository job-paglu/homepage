/**
 * Job list: filters, pagination, row actions.
 *
 * Deliberately not optimistic. A publish can legitimately fail with 422 — a job
 * whose apply link is not a usable https URL must not become publicly
 * applicable — so flipping the badge before the server agrees would display a
 * state that does not exist. Every mutation replaces the row with the record
 * the server returned.
 *
 * Markup for the toolbar, pager and the three data states lives in
 * `JobsTableChrome.tsx`.
 */
import { useCallback, useEffect, useState } from 'react';
import { ApiError } from '@lib/api';
import { deleteJob, listAdminJobs, publishJob, unpublishJob } from '@lib/adminApi';
import { isNotAuthenticated, loadIdentity } from '@lib/adminIdentity';
import type { JobSummary, Pagination } from '@lib/types';
import AccessNotice from './AccessNotice';
import ConfirmDialog from './ConfirmDialog';
import JobsTableRow from './JobsTableRow';
import {
  JobsEmpty,
  JobsError,
  JobsLoading,
  JobsPager,
  JobsToolbar,
} from './JobsTableChrome';
import type { StatusFilter } from './JobsTableChrome';
import { ToastStack, useToasts } from './Toast';
import styles from '@styles/admin-table.module.css';

const PER_PAGE = 20;
const SEARCH_DEBOUNCE_MS = 350;

/**
 * `signed-out` is its own phase, not an error message.
 *
 * "Access has not authenticated you" is not a failure of the request, it is a
 * different situation with a different remedy, and the remedy is a reload rather
 * than a retry. Collapsing it into `error` would offer a "Try again" button that
 * cannot possibly work.
 */
type Phase = 'loading' | 'ready' | 'error' | 'signed-out';

export default function JobsTable() {
  const [status, setStatus] = useState<StatusFilter>('all');
  const [searchInput, setSearchInput] = useState('');
  const [q, setQ] = useState('');
  const [page, setPage] = useState(1);

  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [meta, setMeta] = useState<Pagination | null>(null);
  const [phase, setPhase] = useState<Phase>('loading');
  const [error, setError] = useState<ApiError | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<JobSummary | null>(null);
  const [reloadSeq, setReloadSeq] = useState(0);
  const toasts = useToasts();

  // Typing must not fire a request per keystroke. The page resets too: "page 4
  // of the previous query" is never the right landing place for a new one.
  useEffect(() => {
    const timer = setTimeout(() => {
      setQ(searchInput.trim());
      setPage(1);
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    void (async () => {
      setPhase('loading');
      setError(null);
      try {
        // Identity probe first, so a browser Access has not authenticated is
        // told exactly that, rather than shown a generic error about a list it
        // was never entitled to see.
        await loadIdentity();
        const result = await listAdminJobs(
          {
            page,
            per_page: PER_PAGE,
            ...(q === '' ? {} : { q }),
            ...(status === 'all' ? {} : { status }),
          },
          { signal: controller.signal },
        );
        if (cancelled) return;
        setJobs(result.jobs);
        setMeta(result.pagination);
        setPhase('ready');
      } catch (caught) {
        if (cancelled) return;
        if (isNotAuthenticated(caught)) {
          setPhase('signed-out');
          return;
        }
        setError(caught instanceof ApiError ? caught : null);
        setPhase('error');
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [page, q, status, reloadSeq]);

  const replaceRow = useCallback((updated: JobSummary) => {
    setJobs((current) => current.map((row) => (row.id === updated.id ? updated : row)));
  }, []);

  const reportFailure = useCallback(
    (caught: unknown, fallback: string) => {
      // A mutation that comes back 401 means the Access session lapsed mid-session.
      // Replacing the whole table with the notice is right: every other action on
      // this page would fail the same way, and a toast that scrolls away would
      // leave the admin clicking buttons that silently do nothing.
      if (isNotAuthenticated(caught)) {
        setPendingDelete(null);
        setPhase('signed-out');
        return;
      }
      toasts.push('error', caught instanceof ApiError ? caught.message : fallback);
    },
    [toasts],
  );

  const runPublish = useCallback(
    async (job: JobSummary) => {
      setBusyId(job.id);
      try {
        replaceRow(await publishJob(job.id));
        toasts.push('success', `“${job.title}” is now public.`);
      } catch (caught) {
        // 422 here is a content problem, not a transport problem: say what to fix.
        if (caught instanceof ApiError && caught.code === 'VALIDATION_FAILED') {
          const detail = Object.values(caught.details)[0] ?? caught.message;
          toasts.push(
            'error',
            `“${job.title}” cannot go public yet. ${detail} Edit the job, fix the apply link, then publish again.`,
          );
        } else {
          reportFailure(caught, 'Publishing failed. Please try again.');
        }
      } finally {
        setBusyId(null);
      }
    },
    [replaceRow, reportFailure, toasts],
  );

  const runUnpublish = useCallback(
    async (job: JobSummary) => {
      setBusyId(job.id);
      try {
        replaceRow(await unpublishJob(job.id));
        toasts.push('success', `“${job.title}” is no longer public.`);
      } catch (caught) {
        reportFailure(caught, 'Unpublishing failed. Please try again.');
      } finally {
        setBusyId(null);
      }
    },
    [replaceRow, reportFailure, toasts],
  );

  const runDelete = useCallback(async () => {
    const job = pendingDelete;
    if (!job) return;
    setBusyId(job.id);
    try {
      await deleteJob(job.id);
      setPendingDelete(null);
      toasts.push('success', `“${job.title}” was deleted.`);
      // Refetch rather than splice: dropping a row locally would leave the
      // pagination totals lying about how many jobs exist.
      setReloadSeq((value) => value + 1);
    } catch (caught) {
      reportFailure(caught, 'Deleting failed. Please try again.');
    } finally {
      setBusyId(null);
    }
  }, [pendingDelete, reportFailure, toasts]);

  const filtered = q !== '' || status !== 'all';

  return (
    <>
      <JobsToolbar
        search={searchInput}
        status={status}
        onSearchChange={setSearchInput}
        onStatusChange={(next) => {
          setStatus(next);
          setPage(1);
        }}
      />

      {phase === 'signed-out' && <AccessNotice />}
      {phase === 'loading' && <JobsLoading />}
      {phase === 'error' && (
        <JobsError error={error} onRetry={() => setReloadSeq((value) => value + 1)} />
      )}
      {phase === 'ready' && jobs.length === 0 && <JobsEmpty filtered={filtered} />}

      {phase === 'ready' && jobs.length > 0 && (
        <>
          <div className={styles.wrap}>
            <table className={styles.table} role="table">
              <caption className={styles.caption}>
                All job listings, most recently changed first. Drafts and unpublished jobs are not
                visible to the public.
              </caption>
              <thead className={styles.head} role="rowgroup">
                <tr role="row">
                  {['Job', 'Status', 'Type', 'Location', 'Updated'].map((heading) => (
                    <th key={heading} className={styles.th} scope="col" role="columnheader">
                      {heading}
                    </th>
                  ))}
                  <th className={styles.th} scope="col" role="columnheader">
                    <span className="jpa-visually-hidden">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody className={styles.body} role="rowgroup">
                {jobs.map((job) => (
                  <JobsTableRow
                    key={job.id}
                    job={job}
                    busy={busyId === job.id}
                    onPublish={(target) => void runPublish(target)}
                    onUnpublish={(target) => void runUnpublish(target)}
                    onDelete={setPendingDelete}
                  />
                ))}
              </tbody>
            </table>
          </div>

          <JobsPager
            page={page}
            perPage={PER_PAGE}
            total={meta?.total ?? 0}
            totalPages={meta?.total_pages ?? 0}
            onPageChange={setPage}
          />
        </>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title="Delete this job?"
        confirmLabel="Delete job"
        requireText={pendingDelete?.title}
        requireTextLabel="Type the job title to confirm"
        danger
        busy={pendingDelete !== null && busyId === pendingDelete.id}
        onConfirm={() => void runDelete()}
        onCancel={() => setPendingDelete(null)}
      >
        <p>
          You are about to delete <strong>{pendingDelete?.title}</strong>. It disappears from the
          public site and from this list immediately.
        </p>
        <p>
          This is a soft delete: the row is kept in the database and can be restored by someone
          with database access. It cannot, however, be restored from this dashboard.
        </p>
      </ConfirmDialog>

      <ToastStack toasts={toasts.toasts} onDismiss={toasts.dismiss} />
    </>
  );
}
