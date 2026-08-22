/**
 * GET /api/health — liveness.
 *
 * IT MUST NOT TOUCH D1. A liveness probe answers "is this worker running?", and
 * a probe that fails because a dependency is down causes the orchestrator to
 * recycle a perfectly healthy instance — turning a database blip into an outage.
 * Readiness is the endpoint that is allowed to care about the database; see
 * `ready.ts`.
 *
 * Nothing is imported from env.ts here either, so this route cannot fail on a
 * missing binding.
 *
 * PATH NOTE: the Go API served this at the ROOT (`/health`). On Cloudflare Pages
 * the API is a set of Astro routes under `/api`, and `/health` at the root would
 * collide with the site's page routing, so it lives at `/api/health`. Update any
 * uptime monitor accordingly — this is a deployment-shape change, not a
 * behaviour change.
 */

import type { APIRoute } from 'astro';
import { CACHE, jsonData, methodNotAllowed, respond } from '@lib/server/respond';

export const prerender = false;

export const GET: APIRoute = ({ request }) =>
  respond(request, (requestId) =>
    Promise.resolve(
      jsonData({ status: 'ok' }, requestId, { headers: { 'Cache-Control': CACHE.none } }),
    ),
  );

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
