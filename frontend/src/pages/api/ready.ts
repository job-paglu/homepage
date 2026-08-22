/**
 * GET /api/ready — readiness.
 *
 * Unlike `/api/health`, this one is SUPPOSED to fail when the database is
 * unreachable: it answers "can this instance serve a request right now?", and it
 * runs the cheapest statement that proves the D1 binding round-trips.
 *
 * 200 `{"data":{"status":"ready"}}` or 503 `SERVICE_UNAVAILABLE`.
 *
 * PATH NOTE: served at the root (`/ready`) by the Go API; now `/api/ready` for
 * the same reason as `/api/health`.
 */

import type { APIRoute } from 'astro';
import { getRuntimeEnv } from '@lib/server/env';
import { JobsRepository } from '@lib/server/jobs.repo';
import { CACHE, errors, jsonData, methodNotAllowed, respond } from '@lib/server/respond';

export const prerender = false;

export const GET: APIRoute = ({ request, locals }) =>
  respond(request, async (requestId) => {
    try {
      // The binding lookup is inside the try on purpose: a missing binding means
      // this instance cannot serve, which is a 503, not a 500.
      await new JobsRepository(getRuntimeEnv(locals).DB).ping();
    } catch (cause) {
      // Deliberately 503 and not 500: this is a retryable condition, and the
      // frontend renders the two differently.
      throw errors.serviceUnavailable(cause);
    }
    return jsonData({ status: 'ready' }, requestId, {
      headers: { 'Cache-Control': CACHE.none },
    });
  });

export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
