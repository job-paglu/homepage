/**
 * GET /api/jobs — the public job list (contract section 3).
 *
 * Published, non-deleted, non-expired jobs only. That guarantee is NOT enforced
 * here: it lives in the SQL of `JobsRepository.listPublic`, so this handler has
 * no way to leak a draft even by mistake. Its whole job is to parse the query
 * string, delegate, and set the cache header.
 *
 * Returns `JobSummary[]` — never the full `description`. An empty result is a
 * 200 with `data: []` and `total: 0`, not a 404.
 */

import type { APIRoute } from 'astro';
import { getRuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import { CACHE, jsonCollection, methodNotAllowed, respond } from '@lib/server/respond';
import { parseListQuery } from '@lib/server/validate';

export const prerender = false;

export const GET: APIRoute = ({ request, locals, url }) =>
  respond(request, async (requestId) => {
    const env = getRuntimeEnv(locals);
    const query = parseListQuery(url.searchParams, false);
    const { data, meta } = await createJobService(env).listPublic(query);

    return jsonCollection(data, meta, requestId, { headers: { 'Cache-Control': CACHE.list } });
  });

/** Without this, Astro answers a POST to a GET-only route with 404. The contract
 *  distinguishes "no such route" from "wrong method on a known route". */
export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
