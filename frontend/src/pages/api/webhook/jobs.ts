/**
 * POST /api/webhook/jobs — machine-to-machine job ingestion.
 *
 * This is how automation (Zapier, n8n, a cron, a scraper) inserts or upserts
 * jobs with no human logging in. It accepts one job object or `{ "jobs": [...] }`
 * (a batch, capped at 50) and is idempotent on the caller's `external_id`.
 *
 * ###########################################################################
 * #  AUTH IS A BEARER KEY, AND IT IS NOT CLOUDFLARE ACCESS.                  #
 * #                                                                         #
 * #  Cloudflare Access guards `admin*` only. This path — `webhook*` — does   #
 * #  NOT match that rule, so it is reachable from the public internet with   #
 * #  no Access JWT in front of it. That is intentional: automation cannot    #
 * #  complete an interactive Access login. Therefore this route must NEVER   #
 * #  call the Access verifier (`requireAdmin`) — its ONLY authentication is  #
 * #  the bearer key checked below.                                          #
 * #                                                                         #
 * #  FAIL CLOSED. If `WEBHOOK_API_KEY` is unset or empty, the endpoint       #
 * #  answers 503 and ingests nothing. An unconfigured key must never mean an #
 * #  OPEN endpoint — that is the single most dangerous mistake possible in   #
 * #  this file. `getRuntimeEnv` already maps an empty/whitespace value to    #
 * #  `undefined`, so both "unset" and "blank" land on the 503 path.         #
 * ###########################################################################
 *
 * CSRF: not applicable. There is no cookie auth on this route — the only
 * credential is the `Authorization` header, which a browser does not attach
 * automatically to a cross-site request. A forged cross-origin request cannot
 * carry the bearer key, so there is nothing for a CSRF token to add. (Astro's
 * `security.checkOrigin` behaviour for a header-authenticated JSON POST with no
 * Origin is flagged in the delivery notes as a runtime thing to verify.)
 */

import type { APIRoute } from 'astro';
import { getRuntimeEnv } from '@lib/server/env';
import { createJobService } from '@lib/server/jobs.service';
import { CACHE, errors, jsonData, methodNotAllowed, readJsonBody, respond } from '@lib/server/respond';
import {
  MAX_WEBHOOK_BODY_BYTES,
  bearerToken,
  constantTimeEqual,
  ingestionDisabled,
  parseWebhookBody,
} from '@lib/server/webhook';

export const prerender = false;

/** Every response on this route is uncacheable — it is a write endpoint, and its
 *  auth outcome must never be served from a cache. */
const NO_STORE = { 'Cache-Control': CACHE.none } as const;

export const POST: APIRoute = ({ request, locals }) =>
  respond(request, async (requestId) => {
    const env = getRuntimeEnv(locals);

    // --- FAIL CLOSED -------------------------------------------------------
    // No key configured => the endpoint is OFF. Checked before anything is
    // read from the request, so an unconfigured deployment does no work and
    // cannot be probed for behaviour.
    const configuredKey = env.WEBHOOK_API_KEY;
    if (configuredKey === undefined || configuredKey === '') {
      throw ingestionDisabled();
    }

    // --- Authenticate ------------------------------------------------------
    // Always run the compare, even for a missing/blank header (bearerToken
    // returns ''), so "no header" and "wrong key" are indistinguishable in both
    // timing and response. Never `===` on the raw keys — see constantTimeEqual.
    const presented = bearerToken(request);
    const authorised = await constantTimeEqual(presented, configuredKey);
    if (!authorised) {
      // Identical body regardless of which of {missing, malformed, wrong} it was.
      throw errors.unauthorized();
    }

    // --- Parse -------------------------------------------------------------
    // readJsonBody enforces the 256 KiB cap (413), the application/json type,
    // and the "must be a JSON object" rule (400) — shared with every other JSON
    // route. parseWebhookBody then splits single vs batch and enforces the cap.
    const body = await readJsonBody(request, MAX_WEBHOOK_BODY_BYTES);
    const items = parseWebhookBody(body);

    // --- Ingest ------------------------------------------------------------
    const summary = await createJobService(env).ingest(items);

    // Structured log: counts + the external_ids, so a delivery is traceable.
    // NEVER the key and NEVER the job bodies — those would put a secret, or
    // arbitrary caller content, into the log stream.
    console.info('webhook ingest', {
      request_id: requestId,
      total: items.length,
      created: summary.created,
      updated: summary.updated,
      failed: summary.failed,
      external_ids: summary.results.map((result) => result.external_id),
    });

    // Always 200 for a processed batch, even with per-item failures: the sender
    // must not retry the whole batch over one bad row. Auth/config failures
    // (401/503) and a malformed body (400/413) short-circuit above.
    return jsonData(summary, requestId, { headers: NO_STORE });
  });

/** Any method other than POST is 405 — never 404, so a caller learns the path
 *  exists but used the wrong verb. */
export const ALL: APIRoute = ({ request }) => methodNotAllowed(request);
