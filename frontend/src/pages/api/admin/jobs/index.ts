/**
 * /api/admin/jobs — the admin collection (contract section 4).
 *
 *   GET   all statuses, expiry ignored, soft-deleted rows excluded unless
 *         `include_deleted=true`. `per_page` clamps to 1..100 here, not 1..50.
 *         Ordered `updated_at DESC`.
 *   POST  create. `status` defaults to `draft`; an omitted `slug` is derived from
 *         the title and de-duplicated, so an auto-derived slug never 409s.
 *
 * Both verify the Cloudflare Access JWT before anything else, and both send
 * `Cache-Control: no-store` — an admin list cached anywhere, browser or CDN or
 * corporate proxy, is both a staleness bug and a disclosure.
 */

import type { APIRoute } from 'astro';
import { requireAdmin } from '@lib/server/access';
import { getRuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import {
  CACHE,
  jsonCollection,
  jsonData,
  methodNotAllowed,
  readJsonBody,
  respond,
} from '@lib/server/respond';
import { parseListQuery } from '@lib/server/validate';

export const prerender = false;

const NO_STORE = { 'Cache-Control': CACHE.none } as const;

export const GET: APIRoute = ({ request, locals, url }) =>
  respond(request, async (requestId) => {
    const env = getRuntimeEnv(locals);
    await requireAdmin(request, env);

    const query = parseListQuery(url.searchParams, true);
    const { data, meta } = await createJobService(env).listAdmin(query);

    return jsonCollection(data, meta, requestId, { headers: NO_STORE });
  });

export const POST: APIRoute = ({ request, locals }) =>
  respond(request, async (requestId) => {
    const env = getRuntimeEnv(locals);
    await requireAdmin(request, env);

    const body = await readJsonBody(request);
    const job = await createJobService(env).create(body);

    return jsonData(job, requestId, {
      status: 201,
      // The contract's Location header, so a client can follow the created
      // resource without reconstructing the path itself.
      headers: { ...NO_STORE, Location: `/api/admin/jobs/${job.id}` },
    });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
