/**
 * SEO builders: page titles, meta descriptions, and JobPosting JSON-LD.
 *
 * The governing rule for structured data is *never fabricate a field*. Search
 * engines penalise structured data that disagrees with the visible page, and an
 * invented `hiringOrganization` or a guessed `baseSalary` is worse than an
 * absent one — it is a false statement about a real employer. Every optional
 * property below is therefore emitted only when the record actually carries it.
 */

import type { Job } from './types';
import { copy } from './copy';
import { truncateOnWord } from './format';

// ---------------------------------------------------------------- page titles

/** Longer than this and search results truncate it mid-word anyway. */
const MAX_TITLE_LENGTH = 62;
const MAX_DESCRIPTION_LENGTH = 158;

/**
 * `<page> | Job Paglu`. The brand suffix is dropped when the page text already
 * ends with it, so a page that needs full control of its own title can pass one.
 */
export function pageTitle(text: string): string {
  const brand = copy.site.name;
  const trimmed = text.trim();
  if (trimmed.length === 0) return brand;
  if (trimmed === brand || trimmed.endsWith(`| ${brand}`)) return trimmed;

  const suffix = ` | ${brand}`;
  const room = MAX_TITLE_LENGTH - suffix.length;
  const head = trimmed.length > room ? truncateOnWord(trimmed, room) : trimmed;
  return `${head}${suffix}`;
}

/** Clamp a description to a length search engines will actually display. */
export function metaDescription(text: string | null | undefined, fallback: string): string {
  const candidate = typeof text === 'string' ? text.trim() : '';
  return truncateOnWord(candidate.length > 0 ? candidate : fallback, MAX_DESCRIPTION_LENGTH);
}

/** Detail-page description: prefer the server excerpt, then the full description. */
export function jobMetaDescription(job: Pick<Job, 'title' | 'excerpt' | 'description' | 'location'>): string {
  const lead = job.excerpt.trim().length > 0 ? job.excerpt : job.description;
  return truncateOnWord(`${job.title} — ${job.location}. ${lead}`, MAX_DESCRIPTION_LENGTH);
}

// -------------------------------------------------------------------- JSON-LD

/**
 * `employmentType` is a closed schema.org vocabulary, so an unknown `JobType`
 * (the contract allows the API to add one before the frontend knows about it)
 * must be omitted rather than passed through — an invalid enum value invalidates
 * the whole entity in a rich-results parser, while a missing optional property
 * does not.
 */
const EMPLOYMENT_TYPES: Readonly<Record<string, string>> = {
  'full-time': 'FULL_TIME',
  'part-time': 'PART_TIME',
  contract: 'CONTRACTOR',
  freelance: 'CONTRACTOR',
  internship: 'INTERN',
  temporary: 'TEMPORARY',
};

export interface JobPostingOptions {
  /** Absolute canonical URL of the detail page. */
  url: string;
}

/**
 * Build a schema.org `JobPosting` from a real record.
 *
 * Deliberately omitted:
 *
 * - `baseSalary`. `salary` is free text by contract ("$140k - $175k", "DOE",
 *   "competitive"). Turning that into `MonetaryAmount` + currency + `unitText`
 *   means guessing the currency and the pay period, and a wrong salary in
 *   structured data is a materially misleading claim about a job. The visible
 *   page shows the admin's exact string; the markup stays silent.
 * - `experienceRequirements`. The schema type wants a month count;
 *   `experience_level` is free text ("5+ years", "senior"). Same reasoning.
 * - `applicantLocationRequirements`. Not a field we hold.
 */
export function buildJobPostingJsonLd(job: Job, options: JobPostingOptions): Record<string, unknown> {
  const node: Record<string, unknown> = {
    '@context': 'https://schema.org',
    '@type': 'JobPosting',
    title: job.title,
    description: job.description,
    url: options.url,
    // Applications always happen on the employer's own site, never in-page here.
    directApply: false,
    identifier: {
      '@type': 'PropertyValue',
      name: copy.site.name,
      value: job.id,
    },
  };

  if (job.published_at) node.datePosted = job.published_at;
  // Omitted when null: an absent `validThrough` means "still open", whereas a
  // guessed one would silently expire a live listing in search results.
  if (job.expires_at) node.validThrough = job.expires_at;

  const employmentType = EMPLOYMENT_TYPES[job.job_type];
  if (employmentType) node.employmentType = employmentType;

  const company = job.company === null ? '' : job.company.trim();
  // No company on the record means no organisation in the markup. Substituting
  // "Job Paglu" here would claim we are the employer, which we are not.
  if (company) {
    node.hiringOrganization = { '@type': 'Organization', name: company };
  }

  // `location` is free text ("Berlin, Germany") and is used verbatim as
  // addressLocality rather than split on a comma: splitting would invent a
  // structure the admin never entered. `addressCountry` comes only from the
  // dedicated `country` column.
  const locality = job.location.trim();
  const country = job.country === null ? '' : job.country.trim();
  if (locality || country) {
    const address: Record<string, unknown> = { '@type': 'PostalAddress' };
    if (locality) address.addressLocality = locality;
    if (country) address.addressCountry = country;
    node.jobLocation = { '@type': 'Place', address };
  }

  // Only a genuinely remote role gets TELECOMMUTE. 'hybrid' and 'onsite' both
  // require presence, and mislabelling them is the single most common
  // JobPosting error.
  if (job.remote_type === 'remote') node.jobLocationType = 'TELECOMMUTE';

  const qualification = job.qualification.trim();
  if (qualification) node.qualifications = qualification;

  return node;
}

// -------------------------------------------------------------- serialisation

/** U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR, referenced by code. */
const LINE_SEPARATOR = 0x2028;
const PARAGRAPH_SEPARATOR = 0x2029;

/**
 * True for any character that must not appear raw inside a `<script>` element.
 *
 * `<` and `>` because `</script>` anywhere in a job description would close the
 * element and let the rest of the JSON be parsed as markup. `&` because a
 * consumer that HTML-decodes the block first must not be able to rebuild a `<`
 * out of `&lt;`. U+2028/U+2029 are valid JSON but are JavaScript line
 * terminators, so a raw one is a syntax error inside an inline script.
 */
function mustEscapeInScript(char: string): boolean {
  if (char === '<' || char === '>' || char === '&') return true;
  const code = char.codePointAt(0);
  return code === LINE_SEPARATOR || code === PARAGRAPH_SEPARATOR;
}

/**
 * Serialise a JSON-LD node for embedding in `<script type="application/ld+json">`.
 *
 * Escapes are JSON `\uXXXX` sequences, not HTML entities: `JSON.parse` restores
 * the original character, so a consumer still reads exactly the text the admin
 * wrote, while the bytes in the document can no longer terminate the element.
 *
 * Written as an explicit scan rather than a regex containing literal separator
 * characters — those are invisible in a source file and get mangled by the next
 * editor that touches the line.
 *
 * The result is emitted with `set:html`, because Astro would otherwise escape
 * the quotes and produce invalid JSON. That is only safe because of this
 * function; the two must be changed together.
 */
export function serialiseJsonLd(value: unknown): string {
  const json = JSON.stringify(value) ?? 'null';
  let out = '';
  for (const char of json) {
    if (mustEscapeInScript(char)) {
      out += '\\u' + (char.codePointAt(0) ?? 0).toString(16).padStart(4, '0');
    } else {
      out += char;
    }
  }
  return out;
}

/** Build and serialise a JobPosting in one step. */
export function jobPostingScript(job: Job, options: JobPostingOptions): string {
  return serialiseJsonLd(buildJobPostingJsonLd(job, options));
}

/** `WebSite` node for the home page. Small, true, and cheap to keep accurate. */
export function buildWebSiteJsonLd(origin: string): Record<string, unknown> {
  return {
    '@context': 'https://schema.org',
    '@type': 'WebSite',
    name: copy.site.name,
    url: origin,
    description: copy.home.metaDescription,
  };
}
