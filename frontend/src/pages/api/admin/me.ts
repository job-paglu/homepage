/**
 * GET /api/admin/me — who is signed in?
 *
 * The frontend's way to answer "am I still authenticated?" after a reload.
 *
 * Response: `{"data":{"admin":{"email":…,"sub":…}}}`
 *
 * NO `csrf_token`, and that is a deliberate contract change, not an oversight.
 * The Go API issued a CSRF token because it authenticated with a cookie IT set,
 * and a cookie the browser attaches automatically can be exercised by a hostile
 * page. Authentication is now Cloudflare Access: the credential is a JWT that
 * Access injects at the edge for an authenticated user, this application sets no
 * cookie of its own, and there is nothing for a cross-site request to forge. A
 * CSRF token defending nothing is a token that gets copied into the next project
 * as though it did something.
 *
 * `sub` replaces the Go `Admin` shape's `id`/`name`/`last_login_at`: Access is
 * the identity provider now, there is no `admin_users` row, and inventing a
 * fake `id` would imply a record that does not exist.
 */

import type { APIRoute } from 'astro';
import { requireAdmin } from '@lib/server/access';
import { getRuntimeEnv } from '@lib/server/env';
import { CACHE, jsonData, methodNotAllowed, respond } from '@lib/server/respond';

export const prerender = false;

export const GET: APIRoute = ({ request, locals }) =>
  respond(request, async (requestId) => {
    // Verification first, always. `requireAdmin` throws UNAUTHORIZED rather than
    // returning a falsy value, so a route that forgot to check the result would
    // still not proceed unauthenticated.
    const admin = await requireAdmin(request, getRuntimeEnv(locals));

    return jsonData({ admin }, requestId, { headers: { 'Cache-Control': CACHE.none } });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
