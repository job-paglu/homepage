/**
 * Pure state transforms for the job form: form <-> API shapes, plus the fast
 * local validity check.
 *
 * Separated from `JobForm.tsx` so the component file is only about behaviour
 * (submit, guards, error mapping) and these functions can be reasoned about — or
 * tested — without mounting anything.
 */
import type { Job, JobInput, JobType, RemoteType } from '@lib/types';
import type { JobFormState } from './JobFormFields';
import { fromDateTimeLocal, toDateTimeLocal, validateApplyUrl } from './jobFields';

export const EMPTY_JOB_FORM: JobFormState = {
  title: '',
  description: '',
  salary: '',
  qualification: '',
  job_type: '',
  location: '',
  apply_url: '',
  company: '',
  country: '',
  remote_type: '',
  experience_level: '',
  category: '',
  is_featured: false,
  expires_at: '',
};

/** Nullable API fields become empty strings, because inputs cannot hold null. */
export function stateFromJob(job: Job): JobFormState {
  return {
    title: job.title,
    description: job.description,
    salary: job.salary,
    qualification: job.qualification,
    job_type: job.job_type,
    location: job.location,
    apply_url: job.apply_url,
    company: job.company ?? '',
    country: job.country ?? '',
    remote_type: job.remote_type ?? '',
    experience_level: job.experience_level ?? '',
    category: job.category ?? '',
    is_featured: job.is_featured,
    expires_at: toDateTimeLocal(job.expires_at),
  };
}

/** Blank optional field means "clear it", which the API spells as `null`. */
function optional(value: string): string | null {
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed;
}

/**
 * Build the request body. `slug` is included **only** when explicitly supplied:
 * omitting it is what makes the server keep the existing slug on update and
 * derive-and-deduplicate one on create (contract sections 4 and 8).
 */
export function toJobInput(state: JobFormState, slug: string | undefined): JobInput {
  return {
    title: state.title.trim(),
    description: state.description.trim(),
    salary: state.salary.trim(),
    qualification: state.qualification.trim(),
    job_type: state.job_type as JobType,
    location: state.location.trim(),
    apply_url: state.apply_url.trim(),
    company: optional(state.company),
    country: optional(state.country),
    remote_type: optional(state.remote_type) as RemoteType | null,
    experience_level: optional(state.experience_level),
    category: optional(state.category),
    is_featured: state.is_featured,
    expires_at: fromDateTimeLocal(state.expires_at),
    ...(slug === undefined ? {} : { slug }),
  };
}

/**
 * Fast local check against contract section 9, so an obviously incomplete form
 * does not cost a round trip. The server repeats all of it and has the last
 * word; this must never be the only check.
 */
export function localJobErrors(state: JobFormState): Record<string, string> {
  const found: Record<string, string> = {};
  if (state.title.trim().length < 3) found['title'] = 'Must be at least 3 characters.';
  if (state.description.trim().length < 20) {
    found['description'] = 'Must be at least 20 characters.';
  }
  if (state.salary.trim() === '') found['salary'] = 'Required.';
  if (state.qualification.trim() === '') found['qualification'] = 'Required.';
  if (state.job_type === '') found['job_type'] = 'Choose a job type.';
  if (state.location.trim() === '') found['location'] = 'Required.';

  const applyIssue = validateApplyUrl(state.apply_url);
  if (applyIssue) found['apply_url'] = applyIssue;

  const expires = fromDateTimeLocal(state.expires_at);
  if (expires !== null && new Date(expires).getTime() <= Date.now()) {
    found['expires_at'] = 'Must be in the future.';
  }
  return found;
}
