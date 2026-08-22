/**
 * POST /api/admin/jobs/{id}/publish (contract section 4).
 *
 * Sets `status = 'published'` and `published_at = now` ONLY IF it was null, so
 * re-publishing does not rewrite the original publication date.
 *
 * Rejects with 422 `VALIDATION_FAILED` if the stored row would be invalid when
 * public — and EVERY field is re-checked, not just `apply_url`, against the
 * policy this deployment is running with right now. Two reasons: a row can reach
 * an invalid state through a direct `wrangler d1 execute`, and validating
 * `apply_url` against the *current* `ALLOW_HTTP_APPLY_URLS` avoids the trap where
 * a value accepted on write is rejected at publish time, or worse the reverse.
 *
 * A job must never become publicly applicable without a usable apply link.
 *
 * No request body is read. Publishing is a transition, not an edit.
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

    const job = await createJobService(env).publish(id);
    return jsonData(job, requestId, { headers: { 'Cache-Control': CACHE.none } });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
