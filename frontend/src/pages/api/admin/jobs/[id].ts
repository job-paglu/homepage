/**
 * /api/admin/jobs/{id} — single admin resource (contract section 4).
 *
 *   GET     by UUID, not slug, so an admin can still load a job after a slug
 *           change.
 *   PUT     full replacement of the mutable fields, with two stability rules the
 *           service enforces: an absent `slug` keeps the existing slug, and an
 *           absent `status` keeps the existing status. The second one matters
 *           most — defaulting to `draft` would mean a dashboard "Save" silently
 *           unpublishes a live job.
 *   DELETE  SOFT delete. 204, the row survives, and the slug becomes reusable
 *           immediately because the unique index is partial on `deleted_at`.
 */

import type { APIRoute } from 'astro';
import { requireAdmin } from '@lib/server/access';
import { getRuntimeEnv, type RuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import {
  CACHE,
  errors,
  jsonData,
  methodNotAllowed,
  noContent,
  readJsonBody,
  respond,
} from '@lib/server/respond';
import { isUuid } from '@lib/server/validate';

export const prerender = false;

const NO_STORE = { 'Cache-Control': CACHE.none } as const;

/**
 * Verify the caller, then the id shape.
 *
 * The shape check happens before any query so an unparseable id is a 400 from
 * the handler rather than a database error, and it happens AFTER authentication
 * so an anonymous caller cannot use the difference between 400 and 401 to learn
 * anything (contract section 6).
 */
async function gate(
  request: Request,
  locals: unknown,
  id: string | undefined,
): Promise<{ env: RuntimeEnv; id: string }> {
  const env = getRuntimeEnv(locals);
  await requireAdmin(request, env);
  if (id === undefined || !isUuid(id)) throw errors.badRequest('The job id must be a UUID.');
  return { env, id };
}

export const GET: APIRoute = ({ request, locals, params }) =>
  respond(request, async (requestId) => {
    const { env, id } = await gate(request, locals, params.id);
    const job = await createJobService(env).getAdmin(id);
    return jsonData(job, requestId, { headers: NO_STORE });
  });

export const PUT: APIRoute = ({ request, locals, params }) =>
  respond(request, async (requestId) => {
    const { env, id } = await gate(request, locals, params.id);
    const body = await readJsonBody(request);
    const job = await createJobService(env).update(id, body);
    return jsonData(job, requestId, { headers: NO_STORE });
  });

export const DELETE: APIRoute = ({ request, locals, params }) =>
  respond(request, async (requestId) => {
    const { env, id } = await gate(request, locals, params.id);
    await createJobService(env).remove(id);
    // 204 carries no body at all (contract section 6).
    return noContent(requestId, NO_STORE);
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
