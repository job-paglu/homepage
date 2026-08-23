import { describe, expect, it } from 'vitest';

import {
  buildJobPostingJsonLd,
  jobMetaDescription,
  jobPostingScript,
  metaDescription,
  pageTitle,
  serialiseJsonLd,
} from '../seo';
import type { Job } from '../types';

const URL_UNDER_TEST = 'https://jobpaglu.com/jobs/senior-cloud-engineer';

/**
 * A complete record, mirroring the example in API-CONTRACT.md section 10. Tests
 * override single fields from it, so a field added to `Job` shows up as one
 * compile error here rather than as a silently untested branch.
 */
function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: '8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33',
    slug: 'senior-cloud-engineer',
    title: 'Senior Cloud Engineer',
    excerpt: 'Own the platform that serves millions of requests a day.',
    description: 'Own the platform.\n\nYou will work across three regions.',
    salary: '$140k - $175k',
    qualification: 'B.Sc. Computer Science or equivalent',
    job_type: 'full-time',
    location: 'Berlin, Germany',
    company: 'Acme GmbH',
    country: 'Germany',
    remote_type: 'hybrid',
    experience_level: '5+ years',
    category: 'Engineering',
    is_featured: true,
    apply_url: 'https://example.com/careers/sce',
    status: 'published',
    external_id: null,
    published_at: '2026-08-20T11:00:00Z',
    expires_at: null,
    created_at: '2026-08-20T10:58:12Z',
    updated_at: '2026-08-20T11:00:00Z',
    ...overrides,
  };
}

describe('buildJobPostingJsonLd — required shape', () => {
  it('emits the entity type, url and directApply', () => {
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(node['@context']).toBe('https://schema.org');
    expect(node['@type']).toBe('JobPosting');
    expect(node.title).toBe('Senior Cloud Engineer');
    expect(node.url).toBe(URL_UNDER_TEST);
    // Applications happen on the employer's site, never in-page here.
    expect(node.directApply).toBe(false);
  });

  it('carries the job id as the identifier', () => {
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(node.identifier).toEqual({
      '@type': 'PropertyValue',
      name: 'Job Paglu',
      value: '8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33',
    });
  });

  it('never emits baseSalary, because `salary` is free text', () => {
    // "$140k - $175k" cannot be turned into MonetaryAmount + currency + period
    // without guessing, and a wrong salary in structured data is a materially
    // misleading claim about a real job.
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('baseSalary');
    expect(JSON.stringify(node)).not.toContain('140k');
  });
});

describe('buildJobPostingJsonLd — hiringOrganization', () => {
  it('omits hiringOrganization entirely when company is null', () => {
    // Substituting "Job Paglu" here would claim we are the employer.
    const node = buildJobPostingJsonLd(makeJob({ company: null }), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('hiringOrganization');
  });

  it('omits it for a whitespace-only company', () => {
    const node = buildJobPostingJsonLd(makeJob({ company: '   ' }), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('hiringOrganization');
  });

  it('emits an Organization when company is present', () => {
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(node.hiringOrganization).toEqual({ '@type': 'Organization', name: 'Acme GmbH' });
  });
});

describe('buildJobPostingJsonLd — dates', () => {
  it('omits validThrough when expires_at is null', () => {
    // An absent validThrough means "still open". A guessed one would silently
    // expire a live listing in search results.
    const node = buildJobPostingJsonLd(makeJob({ expires_at: null }), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('validThrough');
    expect(node.datePosted).toBe('2026-08-20T11:00:00Z');
  });

  it('emits validThrough when expires_at is set', () => {
    const node = buildJobPostingJsonLd(makeJob({ expires_at: '2026-12-31T23:59:59Z' }), {
      url: URL_UNDER_TEST,
    });
    expect(node.validThrough).toBe('2026-12-31T23:59:59Z');
  });

  it('omits datePosted when published_at is null', () => {
    const node = buildJobPostingJsonLd(makeJob({ published_at: null }), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('datePosted');
  });
});

describe('buildJobPostingJsonLd — location', () => {
  it('sets TELECOMMUTE only for remote_type "remote"', () => {
    const remote = buildJobPostingJsonLd(makeJob({ remote_type: 'remote' }), { url: URL_UNDER_TEST });
    expect(remote.jobLocationType).toBe('TELECOMMUTE');

    for (const value of ['hybrid', 'onsite', null] as const) {
      const node = buildJobPostingJsonLd(makeJob({ remote_type: value }), { url: URL_UNDER_TEST });
      expect(node).not.toHaveProperty('jobLocationType');
    }
  });

  it('uses the real location and country fields, inventing nothing', () => {
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(node.jobLocation).toEqual({
      '@type': 'Place',
      address: {
        '@type': 'PostalAddress',
        // Verbatim: splitting "Berlin, Germany" on the comma would invent a
        // structure the admin never entered.
        addressLocality: 'Berlin, Germany',
        addressCountry: 'Germany',
      },
    });
  });

  it('omits addressCountry when country is null', () => {
    const node = buildJobPostingJsonLd(makeJob({ country: null }), { url: URL_UNDER_TEST });
    expect(node.jobLocation).toEqual({
      '@type': 'Place',
      address: { '@type': 'PostalAddress', addressLocality: 'Berlin, Germany' },
    });
  });
});

describe('buildJobPostingJsonLd — employmentType', () => {
  it('maps every documented JobType to the schema.org vocabulary', () => {
    const expected: Record<string, string> = {
      'full-time': 'FULL_TIME',
      'part-time': 'PART_TIME',
      contract: 'CONTRACTOR',
      freelance: 'CONTRACTOR',
      internship: 'INTERN',
      temporary: 'TEMPORARY',
    };
    for (const [input, output] of Object.entries(expected)) {
      const node = buildJobPostingJsonLd(makeJob({ job_type: input }), { url: URL_UNDER_TEST });
      expect(node.employmentType).toBe(output);
    }
  });

  it('omits employmentType for an unknown JobType rather than guessing', () => {
    // employmentType is a closed vocabulary: an invalid value invalidates the
    // whole entity, while a missing optional property does not.
    const node = buildJobPostingJsonLd(makeJob({ job_type: 'job-share' }), { url: URL_UNDER_TEST });
    expect(node).not.toHaveProperty('employmentType');
  });
});

describe('serialiseJsonLd', () => {
  it('escapes a description containing </script> so it cannot break out', () => {
    const job = makeJob({
      description: 'Great role.</script><img src=x onerror=alert(1)>',
    });
    const serialised = jobPostingScript(job, { url: URL_UNDER_TEST });

    expect(serialised).not.toContain('</script>');
    expect(serialised).not.toContain('<');
    expect(serialised).not.toContain('>');
    expect(serialised).toContain('\\u003c');

    // The escaping is JSON-level, not HTML-level: a consumer still parses out
    // exactly the text the admin typed.
    const parsed = JSON.parse(serialised) as { description: string };
    expect(parsed.description).toBe('Great role.</script><img src=x onerror=alert(1)>');
  });

  it('escapes a bare ampersand', () => {
    const serialised = serialiseJsonLd({ name: 'Ben & Jerry' });
    expect(serialised).not.toContain('&');
    expect(JSON.parse(serialised)).toEqual({ name: 'Ben & Jerry' });
  });

  it('escapes the JavaScript line terminators U+2028 and U+2029', () => {
    // Valid JSON, but a syntax error inside an inline <script>.
    const input = `line${String.fromCharCode(0x2028)}break${String.fromCharCode(0x2029)}end`;
    const serialised = serialiseJsonLd({ text: input });
    expect(serialised).toContain('\\u2028');
    expect(serialised).toContain('\\u2029');
    expect(JSON.parse(serialised)).toEqual({ text: input });
  });

  it('round-trips a full JobPosting unchanged', () => {
    const node = buildJobPostingJsonLd(makeJob(), { url: URL_UNDER_TEST });
    expect(JSON.parse(serialiseJsonLd(node))).toEqual(node);
  });
});

describe('pageTitle', () => {
  it('appends the brand', () => {
    expect(pageTitle('International jobs')).toBe('International jobs | Job Paglu');
  });

  it('does not double the brand', () => {
    expect(pageTitle('Privacy | Job Paglu')).toBe('Privacy | Job Paglu');
    expect(pageTitle('Job Paglu')).toBe('Job Paglu');
  });

  it('falls back to the brand alone for empty input', () => {
    expect(pageTitle('   ')).toBe('Job Paglu');
  });

  it('keeps the whole title inside the length search results display', () => {
    const long = 'Senior Staff Site Reliability Engineer for Global Payments Infrastructure';
    const result = pageTitle(long);
    expect(result.endsWith(' | Job Paglu')).toBe(true);
    expect(result.length).toBeLessThanOrEqual(63);
  });
});

describe('metaDescription / jobMetaDescription', () => {
  it('uses the fallback when the text is blank', () => {
    expect(metaDescription('   ', 'Fallback copy')).toBe('Fallback copy');
    expect(metaDescription(null, 'Fallback copy')).toBe('Fallback copy');
  });

  it('leads with the job title and location', () => {
    const description = jobMetaDescription(makeJob());
    expect(description.startsWith('Senior Cloud Engineer — Berlin, Germany.')).toBe(true);
    expect(description.length).toBeLessThanOrEqual(159);
  });

  it('falls back to the description when the excerpt is empty', () => {
    const description = jobMetaDescription(makeJob({ excerpt: '' }));
    expect(description).toContain('Own the platform.');
  });
});
