/**
 * GET /api/jobs/{slug} — the public job detail (contract section 3).
 *
 * Returns `Job`: every summary field plus the full `description`.
 *
 * 404 `JOB_NOT_FOUND` covers "no such slug" AND "draft / unpublished /
 * soft-deleted / expired", deliberately indistinguishably. An admin's draft slug
 * must not be discoverable by probing this endpoint for a different error, which
 * is why the repository's visibility predicate returns nothing rather than
 * returning the row for the handler to filter.
 */

import type { APIRoute } from 'astro';
import { getRuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import { CACHE, errors, jsonData, methodNotAllowed, respond } from '@lib/server/respond';

export const prerender = false;

export const GET: APIRoute = ({ request, locals, params }) =>
  respond(request, async (requestId) => {
    const slug = params.slug?.trim() ?? '';
    // An empty slug is 404 rather than 400: `/api/jobs/` is a job that does not
    // exist, not a malformed request, and the client renders the same page.
    if (slug === '') throw errors.jobNotFound();

    const env = getRuntimeEnv(locals);
    const job = await createJobService(env).getPublic(slug);

    return jsonData(job, requestId, { headers: { 'Cache-Control': CACHE.detail } });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
