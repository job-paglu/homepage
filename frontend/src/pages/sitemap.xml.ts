/**
 * sitemap.xml — server-rendered, so a job published in the admin dashboard is
 * in the sitemap on the next crawl rather than on the next deploy.
 *
 * **This is the route that most needed the rewrite.** It used to fetch
 * `/api/jobs` over HTTP and swallow the failure, so a misconfigured base URL
 * produced a syntactically perfect sitemap containing only the four static URLs
 * — no error, no log line, and no job ever submitted for indexing. It now walks
 * the job service in-process, which cannot fail for that reason at all. Walking
 * 20 pages would also have spent 20 of the free plan's 50 subrequests per
 * request; in-process it spends none.
 *
 * The failure mode still matters more than the happy path: if D1 is unreachable
 * this returns the four static URLs rather than a 500. A search engine that gets
 * a 500 may drop the sitemap entirely; one that gets a short sitemap simply keeps
 * the job URLs it already knows. The difference from before is that the reason is
 * now logged instead of silently discarded.
 */

import type { APIRoute } from 'astro';
import { getRuntimeEnv, listPublishedJobs, MAX_PER_PAGE, type RuntimeEnv } from '@lib/publicJobs';
import { absoluteUrl, jobPath, ROUTES } from '@lib/urls';
import type { JobSummary } from '@lib/types';

export const prerender = false;

/** The service clamps `per_page` to this, so pages are walked rather than
 *  requested oversized and silently truncated. */
const PER_PAGE = MAX_PER_PAGE;
/**
 * Hard cap. A sitemap is limited to 50,000 URLs by the protocol anyway, but the
 * real reason for a small cap is that this runs inside a request: an unbounded
 * loop over a growing table would hold the Worker open until it is killed.
 */
const MAX_PAGES = 20;

interface StaticEntry {
  path: string;
  changefreq: string;
  priority: string;
}

const STATIC_ENTRIES: readonly StaticEntry[] = [
  { path: ROUTES.home, changefreq: 'daily', priority: '1.0' },
  { path: ROUTES.jobs, changefreq: 'hourly', priority: '0.9' },
  { path: ROUTES.privacy, changefreq: 'yearly', priority: '0.2' },
  { path: ROUTES.terms, changefreq: 'yearly', priority: '0.2' },
];

/**
 * XML has five predefined entities and no others. Slugs are `[a-z0-9-]` by
 * contract so nothing here should need escaping — which is exactly why it is
 * escaped: the day the slug rules change, this must not start emitting broken
 * XML.
 */
function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

/** `lastmod` accepts a full W3C datetime; the records already hold RFC 3339 UTC. */
function lastmodOf(job: JobSummary): string {
  return job.updated_at || job.published_at || job.created_at;
}

async function collectJobs(env: RuntimeEnv): Promise<JobSummary[]> {
  const collected: JobSummary[] = [];
  let page = 1;

  while (page <= MAX_PAGES) {
    const result = await listPublishedJobs(env, { page, per_page: PER_PAGE });
    collected.push(...result.jobs);
    if (result.jobs.length === 0 || page >= result.pagination.total_pages) break;
    page += 1;
  }

  return collected;
}

export const GET: APIRoute = async ({ site, locals }) => {
  let jobs: JobSummary[] = [];
  try {
    jobs = await collectJobs(getRuntimeEnv(locals));
  } catch (error) {
    // Degrade, do not 500 — but say so. A silent `catch {}` here is what hid the
    // original bug for an entire e2e run.
    console.error('sitemap: job collection failed, serving static URLs only', {
      cause: error instanceof Error ? error.stack : String(error),
    });
    jobs = [];
  }

  const entries = [
    ...STATIC_ENTRIES.map(
      (entry) =>
        `  <url>\n` +
        `    <loc>${escapeXml(absoluteUrl(entry.path, site))}</loc>\n` +
        `    <changefreq>${entry.changefreq}</changefreq>\n` +
        `    <priority>${entry.priority}</priority>\n` +
        `  </url>`,
    ),
    ...jobs.map(
      (job) =>
        `  <url>\n` +
        `    <loc>${escapeXml(absoluteUrl(jobPath(job.slug), site))}</loc>\n` +
        `    <lastmod>${escapeXml(lastmodOf(job))}</lastmod>\n` +
        `    <changefreq>weekly</changefreq>\n` +
        `    <priority>0.8</priority>\n` +
        `  </url>`,
    ),
  ];

  const xml =
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n` +
    `${entries.join('\n')}\n` +
    `</urlset>\n`;

  return new Response(xml, {
    status: 200,
    headers: {
      'Content-Type': 'application/xml; charset=utf-8',
      // Longer than the job list's 60s: a crawler does not need minute-level
      // freshness, and this endpoint walks every page of jobs.
      'Cache-Control': 'public, max-age=0, s-maxage=3600, stale-while-revalidate=86400',
    },
  });
};
