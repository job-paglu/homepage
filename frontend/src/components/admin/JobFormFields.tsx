/**
 * Field layout for the job form.
 *
 * Split out of `JobForm.tsx` so the form's *behaviour* (submit, guards, error
 * mapping) stays readable next to itself rather than buried under 150 lines of
 * markup.
 *
 * The optional fields sit in a `<details>` collapsed by default. Seven required
 * fields is the whole job of this form; showing fourteen at once makes the
 * required set harder to find, and the API has defaults for every optional one.
 */
import type { ReactNode } from 'react';
import { JOB_TYPES, REMOTE_TYPES } from '@lib/types';
import { CheckboxField, SelectField, TextArea, TextField } from './AdminField';
import { FIELD_LIMITS, jobTypeLabel } from './jobFields';
import styles from '@styles/admin-form.module.css';

/** Field ids are deterministic so the 422 handler can focus one by name. */
export function fieldDomId(name: string): string {
  return `job-${name}`;
}

export interface JobFormState {
  title: string;
  description: string;
  salary: string;
  qualification: string;
  job_type: string;
  location: string;
  apply_url: string;
  company: string;
  country: string;
  remote_type: string;
  experience_level: string;
  category: string;
  is_featured: boolean;
  /** `datetime-local` wall-clock value; converted to RFC 3339 on submit. */
  expires_at: string;
}

const JOB_TYPE_OPTIONS = JOB_TYPES.map((value) => ({ value, label: jobTypeLabel(value) }));
const REMOTE_TYPE_OPTIONS = REMOTE_TYPES.map((value) => ({ value, label: jobTypeLabel(value) }));

interface JobFormFieldsProps {
  state: JobFormState;
  errors: Readonly<Record<string, string>>;
  disabled: boolean;
  onChange: <K extends keyof JobFormState>(key: K, value: JobFormState[K]) => void;
  /** Slug preview / rename control, rendered directly under the title. */
  slugPanel: ReactNode;
}

export default function JobFormFields({
  state,
  errors,
  disabled,
  onChange,
  slugPanel,
}: JobFormFieldsProps) {
  /** Every field except the one boolean carries a string, so one adapter serves all. */
  const text =
    (name: Exclude<keyof JobFormState, 'is_featured'>) =>
    (value: string) =>
      onChange(name, value);

  return (
    <>
      <div className={styles.group}>
        <TextField
          id={fieldDomId('title')}
          label="Job title"
          value={state.title}
          onChange={text('title')}
          error={errors['title']}
          required
          disabled={disabled}
          maxLength={FIELD_LIMITS['title']?.max}
          placeholder="Senior Cloud Engineer"
        />
        {slugPanel}

        <TextArea
          id={fieldDomId('description')}
          label="Job description"
          value={state.description}
          onChange={text('description')}
          error={errors['description']}
          required
          disabled={disabled}
          maxLength={FIELD_LIMITS['description']?.max}
          hint="Plain text. Blank lines become paragraphs on the public page; HTML is not rendered."
          rows={10}
        />

        <div className={styles.pair}>
          <TextField
            id={fieldDomId('salary')}
            label="Salary"
            value={state.salary}
            onChange={text('salary')}
            error={errors['salary']}
            required
            disabled={disabled}
            maxLength={FIELD_LIMITS['salary']?.max}
            placeholder="$140k – $175k"
          />
          <SelectField
            id={fieldDomId('job_type')}
            label="Job type"
            value={state.job_type}
            onChange={text('job_type')}
            error={errors['job_type']}
            required
            disabled={disabled}
            options={JOB_TYPE_OPTIONS}
            emptyLabel="Choose a job type"
          />
        </div>

        <TextField
          id={fieldDomId('qualification')}
          label="Qualification"
          value={state.qualification}
          onChange={text('qualification')}
          error={errors['qualification']}
          required
          disabled={disabled}
          maxLength={FIELD_LIMITS['qualification']?.max}
          placeholder="B.Sc. Computer Science or equivalent experience"
        />

        <TextField
          id={fieldDomId('location')}
          label="Location"
          value={state.location}
          onChange={text('location')}
          error={errors['location']}
          required
          disabled={disabled}
          maxLength={FIELD_LIMITS['location']?.max}
          placeholder="Berlin, Germany"
        />

        <TextField
          id={fieldDomId('apply_url')}
          label="Apply link"
          type="url"
          value={state.apply_url}
          onChange={text('apply_url')}
          error={errors['apply_url']}
          required
          disabled={disabled}
          maxLength={FIELD_LIMITS['apply_url']?.max}
          placeholder="https://example.com/careers/senior-cloud-engineer"
          hint="Visitors are sent here unchanged. Must be an absolute https:// URL."
        />
      </div>

      <details className={styles.details}>
        <summary className={styles.summary}>
          Optional details
          <span className={styles.summaryHint}>company, country, remote, expiry</span>
        </summary>
        <div className={styles.detailsBody}>
          <div className={styles.pair}>
            <TextField
              id={fieldDomId('company')}
              label="Company"
              value={state.company}
              onChange={text('company')}
              error={errors['company']}
              disabled={disabled}
              maxLength={FIELD_LIMITS['company']?.max}
            />
            <TextField
              id={fieldDomId('country')}
              label="Country"
              value={state.country}
              onChange={text('country')}
              error={errors['country']}
              disabled={disabled}
              maxLength={FIELD_LIMITS['country']?.max}
            />
          </div>
          <div className={styles.pair}>
            <SelectField
              id={fieldDomId('remote_type')}
              label="Remote type"
              value={state.remote_type}
              onChange={text('remote_type')}
              error={errors['remote_type']}
              disabled={disabled}
              options={REMOTE_TYPE_OPTIONS}
              emptyLabel="Not specified"
            />
            <TextField
              id={fieldDomId('experience_level')}
              label="Experience level"
              value={state.experience_level}
              onChange={text('experience_level')}
              error={errors['experience_level']}
              disabled={disabled}
              maxLength={FIELD_LIMITS['experience_level']?.max}
              placeholder="5+ years"
            />
          </div>
          <div className={styles.pair}>
            <TextField
              id={fieldDomId('category')}
              label="Category"
              value={state.category}
              onChange={text('category')}
              error={errors['category']}
              disabled={disabled}
              maxLength={FIELD_LIMITS['category']?.max}
              placeholder="Engineering"
            />
            <TextField
              id={fieldDomId('expires_at')}
              label="Expires at"
              type="datetime-local"
              value={state.expires_at}
              onChange={text('expires_at')}
              error={errors['expires_at']}
              disabled={disabled}
              hint="Your local time. After this moment the job disappears from the public site."
            />
          </div>
          <CheckboxField
            id={fieldDomId('is_featured')}
            label="Feature this job"
            checked={state.is_featured}
            onChange={(checked) => onChange('is_featured', checked)}
            disabled={disabled}
            hint="Featured jobs are listed ahead of the rest."
          />
        </div>
      </details>
    </>
  );
}
