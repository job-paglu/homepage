/**
 * POST /api/admin/jobs/{id}/unpublish (contract section 4).
 *
 * Sets `status = 'unpublished'` and leaves `published_at` INTACT, so a later
 * re-publish keeps the original publication date.
 *
 * The public detail route starts returning 404 immediately: the visibility
 * predicate is evaluated per request, and admin responses are `no-store`, so
 * nothing of ours caches the old answer. The public list and detail carry
 * `s-maxage`, so an edge copy can survive for up to its TTL — that is the
 * contract's own caching decision, not a bug in this endpoint.
 *
 * No request body is read.
 */

import type { APIRoute } from 'astro';
import { requireAdmin } from '@lib/server/access';
import { getRuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import { CACHE, errors, jsonData, methodNotAllowed, respond } from '@lib/server/respond';
import { isUuid } from '@lib/server/validate';

export const prerender = false;

export const POST: APIRoute = ({ request, locals, params }) =>
  respond(request, async (requestId) => {
    const env = getRuntimeEnv(locals);
    await requireAdmin(request, env);

    const id = params.id;
    if (id === undefined || !isUuid(id)) throw errors.badRequest('The job id must be a UUID.');

    const job = await createJobService(env).unpublish(id);
    return jsonData(job, requestId, { headers: { 'Cache-Control': CACHE.none } });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
