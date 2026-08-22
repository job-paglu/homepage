/**
 * Who is signed in to the dashboard, client-side.
 *
 * This replaces the old `adminSession.ts`. Everything that file existed for is
 * gone: there is no login form, no password, no session cookie of ours, no CSRF
 * token to cache or rotate, and no `?next=` parameter to sanitise. Cloudflare
 * Access authenticates at the edge *before* a request reaches this project, so
 * the only question left is "who does the API say I am?", and there is exactly
 * one way to ask: `GET /api/admin/me`.
 *
 * The answer is cached in module memory for the lifetime of the page. It is
 * never written to `localStorage`, `sessionStorage`, a cookie, a URL, or the DOM
 * — persisting an identity buys nothing when one request can re-derive it.
 *
 * ---------------------------------------------------------------------------
 * A 401 must NOT trigger a redirect. This is the important part.
 * ---------------------------------------------------------------------------
 * The old code sent an unauthenticated browser to `/admin/login`. That page no
 * longer exists, and re-creating it would be worse than useless: with Access in
 * front, any URL we could redirect to is *itself* behind the same gate. So the
 * failure mode of "redirect on 401" is a loop — Access bounces the browser to
 * its own login, the browser lands back on an admin page, the island probes
 * `/api/admin/me`, gets 401 again because the Access session genuinely is not
 * established, and redirects again. A user watching that sees a flickering tab
 * and no explanation, and any error state the page wanted to show is destroyed
 * by the navigation before it can be read.
 *
 * So: on 401 we RENDER something. A short explanation, and a link that reloads
 * the page — one deliberate navigation, taken by the user, which is what gives
 * Access the chance to run its own flow and hand back a `CF_Authorization`
 * cookie. See `components/admin/AccessNotice.tsx`.
 *
 * This module is not an access control and never was. The security boundary is
 * `requireAdmin` in `src/lib/server/access.ts`, which verifies the Access JWT's
 * signature on every `/api/admin/*` request. Anyone may request an admin page
 * shell and receive HTML; they receive no data.
 */

import { ApiError } from './api';
import { getMe } from './adminApi';
import type { AdminIdentity } from './adminApi';

export type { AdminIdentity };

/**
 * Cloudflare Access's own sign-out endpoint. Same-origin, served by Cloudflare
 * ahead of this Worker, so it exists on every deployment that has Access in
 * front of it and needs nothing from us. It clears `CF_Authorization`; we have
 * no session of our own to destroy alongside it.
 */
export const ACCESS_LOGOUT_PATH = '/cdn-cgi/access/logout';

/** Copy for the 401 state, in one place so the header and both islands agree. */
export const ACCESS_NOTICE_TITLE = 'You are not signed in';
export const ACCESS_NOTICE_BODY =
  'Cloudflare Access has not authenticated this browser, so the dashboard cannot load any data. ' +
  'Reload this page to start the Access sign-in flow, then try again.';
export const ACCESS_NOTICE_ACTION = 'Reload this page';

/**
 * The same condition, worded for a form that holds unsaved work.
 *
 * Deliberately does NOT tell the admin to reload: a reload here would throw away
 * everything they typed. Signing in through Access in a second tab sets the
 * `CF_Authorization` cookie for this origin, after which "Save" simply works —
 * so that is the instruction, and the form stays on screen with its contents
 * intact.
 */
export const ACCESS_NOTICE_UNSAVED =
  'Cloudflare Access has signed you out, so this job could not be saved. Your changes are still ' +
  'here — sign in through Access in another tab, then press Save again. Do not reload this page.';

let identity: AdminIdentity | null = null;
/** Deduplicates concurrent probes: the header and an island both want this. */
let inflight: Promise<AdminIdentity> | null = null;

/** The cached identity, or null if `loadIdentity` has not succeeded yet. */
export function currentIdentity(): AdminIdentity | null {
  return identity;
}

/**
 * Resolve the current identity, hitting `GET /api/admin/me` at most once unless
 * `force` is set. Throws `ApiError`; a 401 means Access did not authenticate the
 * browser, and the caller renders `AccessNotice` rather than navigating.
 */
export function loadIdentity(options: { force?: boolean } = {}): Promise<AdminIdentity> {
  const force = options.force === true;
  if (!force) {
    if (identity !== null) return Promise.resolve(identity);
    if (inflight !== null) return inflight;
  }

  const request = getMe().then(
    (next) => {
      identity = next;
      inflight = null;
      return next;
    },
    (error: unknown) => {
      inflight = null;
      if (error instanceof ApiError && error.isUnauthorized) identity = null;
      throw error;
    },
  );
  inflight = request;
  return request;
}

export function clearIdentity(): void {
  identity = null;
  inflight = null;
}

/**
 * Does this error mean "Access has not authenticated this browser"?
 *
 * Callers use it to choose between the sign-in notice and an ordinary error
 * panel. It deliberately does not act — no navigation, no state mutation beyond
 * dropping the stale cache — because the caller owns what the user sees.
 */
export function isNotAuthenticated(error: unknown): boolean {
  if (error instanceof ApiError && error.isUnauthorized) {
    clearIdentity();
    return true;
  }
  return false;
}

/**
 * Reload the current URL.
 *
 * `location.reload()` rather than `assign('/admin/...')`: the browser re-requests
 * the page it is already on, Access intercepts that request at the edge and runs
 * its flow if needed, and the user ends up back where they were. No path is
 * hard-coded, so there is nothing to keep in sync and no open-redirect surface —
 * which is why the old `isSafeNextPath` allowlist is gone rather than ported.
 */
export function reloadPage(): void {
  if (typeof window !== 'undefined') window.location.reload();
}
