/**
 * Cloudflare Access JWT verification.
 *
 * ###########################################################################
 * #  READ THIS BEFORE CHANGING ANYTHING IN THIS FILE                        #
 * #                                                                         #
 * #  Cloudflare Access puts a signed JWT in the `Cf-Access-Jwt-Assertion`   #
 * #  header (and the `CF_Authorization` cookie). It is an ORDINARY REQUEST   #
 * #  HEADER. Anyone who can reach this Worker can send one saying anything   #
 * #  they like.                                                             #
 * #                                                                         #
 * #  Trusting the header without verifying its signature is the single most  #
 * #  dangerous mistake possible in an Access-protected app: it turns the     #
 * #  entire admin API into an unauthenticated one, and it looks like working #
 * #  code because the happy path is identical. Decoding the payload to read  #
 * #  `email` is NOT verification. Checking that the header is present is NOT #
 * #  verification.                                                          #
 * #                                                                         #
 * #  Verification here means, in order, all of:                             #
 * #    1. three well-formed base64url segments                               #
 * #    2. header `alg` is exactly RS256 (never `none`, never a caller's      #
 * #       choice of algorithm)                                               #
 * #    3. RSASSA-PKCS1-v1_5 / SHA-256 signature verified with the public key #
 * #       whose `kid` the header names, fetched from the team's JWKS         #
 * #    4. `iss` equals https://<team domain>                                 #
 * #    5. `aud` contains ACCESS_AUD  <- stops a token minted for a DIFFERENT #
 * #       application in the same Access team from working here             #
 * #    6. `exp` in the future, `iat`/`nbf` not in the future                 #
 * #                                                                         #
 * #  Do not add a "trust the header when the JWKS fetch fails" fallback.     #
 * #  Failing closed on an unreachable JWKS is correct.                      #
 * ###########################################################################
 */

import { isDevelopment, type RuntimeEnv } from './env';
import { errors } from './respond';

/** The verified identity. `sub` is Access's stable per-user id. */
export interface AccessIdentity {
  readonly email: string;
  readonly sub: string;
}

const ACCESS_HEADER = 'Cf-Access-Jwt-Assertion';
const ACCESS_COOKIE = 'CF_Authorization';

/**
 * How long a fetched key set is reused.
 *
 * Module scope means the cache is shared by every request an isolate serves, so
 * a busy deployment fetches the JWKS a few times an hour instead of on every
 * admin call. Ten minutes is short enough that a revoked signing key stops being
 * accepted quickly, and long enough that the fetch is not on the hot path.
 * An unrecognised `kid` also forces a refresh (see `keyForKid`), so ordinary key
 * rotation does not wait out the TTL.
 */
const JWKS_TTL_MS = 10 * 60 * 1000;

/** Tolerance for clock skew between Access and this Worker. */
const CLOCK_SKEW_SECONDS = 60;

interface KeyCache {
  readonly teamDomain: string;
  readonly keys: Map<string, CryptoKey>;
  readonly fetchedAt: number;
}

let keyCache: KeyCache | null = null;
/** Collapses concurrent misses into one fetch instead of a thundering herd. */
let inFlight: Promise<KeyCache> | null = null;

// ------------------------------------------------------------------ utilities

/**
 * Decode a base64url JWT segment to bytes.
 *
 * The return type is `Uint8Array<ArrayBuffer>`, not the default
 * `Uint8Array<ArrayBufferLike>`. That distinction is load-bearing:
 * `crypto.subtle.verify` takes a `BufferSource`, which requires an
 * `ArrayBufferView<ArrayBuffer>`, and `ArrayBufferLike` also admits
 * `SharedArrayBuffer` — which WebCrypto cannot accept, because a buffer another
 * thread can mutate mid-verification would be a real soundness hole rather than
 * a type-checker nicety.
 *
 * Allocating the ArrayBuffer explicitly and typing it satisfies that without an
 * `as` cast, so the guarantee is checked rather than asserted.
 */
function base64UrlToBytes(segment: string): Uint8Array<ArrayBuffer> {
  const base64 = segment.replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);
  const binary = atob(padded);
  const buffer = new ArrayBuffer(binary.length);
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function decodeJsonSegment(segment: string): unknown {
  return JSON.parse(new TextDecoder().decode(base64UrlToBytes(segment)));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function readStringClaim(claims: Record<string, unknown>, name: string): string | null {
  const value = claims[name];
  return typeof value === 'string' && value !== '' ? value : null;
}

function readNumberClaim(claims: Record<string, unknown>, name: string): number | null {
  const value = claims[name];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

// ---------------------------------------------------------------------- JWKS

async function fetchKeys(teamDomain: string): Promise<KeyCache> {
  const url = `https://${teamDomain}/cdn-cgi/access/certs`;
  const response = await fetch(url, { headers: { accept: 'application/json' } });
  if (!response.ok) {
    throw errors.serviceUnavailable(new Error(`Access JWKS fetch returned ${response.status}`));
  }

  const body: unknown = await response.json();
  const rawKeys = isRecord(body) ? body['keys'] : undefined;
  if (!Array.isArray(rawKeys)) {
    throw errors.serviceUnavailable(new Error('Access JWKS response had no "keys" array'));
  }

  const keys = new Map<string, CryptoKey>();
  for (const entry of rawKeys) {
    if (!isRecord(entry)) continue;
    const kid = entry['kid'];
    // Only RSA verification keys are usable here, and `alg` is pinned so a key
    // advertised for a different algorithm can never be pressed into RS256.
    if (typeof kid !== 'string' || entry['kty'] !== 'RSA') continue;
    if (typeof entry['alg'] === 'string' && entry['alg'] !== 'RS256') continue;
    if (typeof entry['n'] !== 'string' || typeof entry['e'] !== 'string') continue;

    const jwk: JsonWebKey = { kty: 'RSA', n: entry['n'], e: entry['e'], alg: 'RS256', ext: true };
    const key = await crypto.subtle.importKey(
      'jwk',
      jwk,
      { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' },
      false,
      ['verify'],
    );
    keys.set(kid, key);
  }

  if (keys.size === 0) {
    throw errors.serviceUnavailable(new Error('Access JWKS contained no usable RS256 keys'));
  }
  return { teamDomain, keys, fetchedAt: Date.now() };
}

function cacheIsFresh(cache: KeyCache | null, teamDomain: string): cache is KeyCache {
  return (
    cache !== null && cache.teamDomain === teamDomain && Date.now() - cache.fetchedAt < JWKS_TTL_MS
  );
}

async function loadKeys(teamDomain: string, force: boolean): Promise<KeyCache> {
  if (!force && cacheIsFresh(keyCache, teamDomain)) return keyCache;
  if (inFlight !== null && !force) return inFlight;

  const pending = fetchKeys(teamDomain)
    .then((fresh) => {
      keyCache = fresh;
      return fresh;
    })
    .finally(() => {
      if (inFlight === pending) inFlight = null;
    });
  inFlight = pending;
  return pending;
}

/**
 * Resolve a `kid` to a key, refreshing once on a miss.
 *
 * A `kid` this isolate has never seen is the normal signature of key rotation,
 * not of an attack, so one forced refresh is worth it. It cannot be used to
 * hammer the JWKS endpoint: a forged token still has to survive the signature
 * check, and a refreshed cache immediately serves subsequent requests.
 */
async function keyForKid(teamDomain: string, kid: string): Promise<CryptoKey> {
  const cached = await loadKeys(teamDomain, false);
  const key = cached.keys.get(kid);
  if (key !== undefined) return key;

  const refreshed = await loadKeys(teamDomain, true);
  const rotated = refreshed.keys.get(kid);
  if (rotated === undefined) throw errors.unauthorized();
  return rotated;
}

// -------------------------------------------------------------- verification

/**
 * Verify a token and return the identity it carries. Throws `UNAUTHORIZED` for
 * any failure — never a message that distinguishes "bad signature" from "wrong
 * audience", because that difference only helps whoever is probing.
 */
export async function verifyAccessJwt(
  token: string,
  config: { teamDomain: string; aud: string },
): Promise<AccessIdentity> {
  const segments = token.split('.');
  const [headerSegment, payloadSegment, signatureSegment] = segments;
  if (
    segments.length !== 3 ||
    headerSegment === undefined ||
    payloadSegment === undefined ||
    signatureSegment === undefined ||
    headerSegment === '' ||
    payloadSegment === '' ||
    signatureSegment === ''
  ) {
    throw errors.unauthorized();
  }

  let header: unknown;
  let claims: unknown;
  // Uint8Array<ArrayBuffer>, not the default ArrayBufferLike -- see
  // base64UrlToBytes for why crypto.subtle.verify requires the narrower type.
  let signature: Uint8Array<ArrayBuffer>;
  try {
    header = decodeJsonSegment(headerSegment);
    claims = decodeJsonSegment(payloadSegment);
    signature = base64UrlToBytes(signatureSegment);
  } catch {
    throw errors.unauthorized();
  }
  if (!isRecord(header) || !isRecord(claims)) throw errors.unauthorized();

  // `alg` comes from the token, so it is attacker-controlled and is checked
  // against a single allowed value rather than used to pick an algorithm.
  // Accepting `none`, or letting the token choose HS256 and treating the public
  // key as an HMAC secret, are the two classic JWT forgeries.
  if (header['alg'] !== 'RS256') throw errors.unauthorized();
  const kid = header['kid'];
  if (typeof kid !== 'string' || kid === '') throw errors.unauthorized();

  const key = await keyForKid(config.teamDomain, kid);
  const signedData = new TextEncoder().encode(`${headerSegment}.${payloadSegment}`);
  const valid = await crypto.subtle.verify(
    { name: 'RSASSA-PKCS1-v1_5' },
    key,
    signature,
    signedData,
  );
  if (!valid) throw errors.unauthorized();

  // Only now is anything in the payload meaningful.
  if (readStringClaim(claims, 'iss') !== `https://${config.teamDomain}`) throw errors.unauthorized();

  const audClaim = claims['aud'];
  const audiences = typeof audClaim === 'string' ? [audClaim] : audClaim;
  if (!Array.isArray(audiences) || !audiences.includes(config.aud)) throw errors.unauthorized();

  const nowSeconds = Math.floor(Date.now() / 1000);
  const exp = readNumberClaim(claims, 'exp');
  if (exp === null || exp <= nowSeconds - CLOCK_SKEW_SECONDS) throw errors.unauthorized();

  const iat = readNumberClaim(claims, 'iat');
  if (iat !== null && iat > nowSeconds + CLOCK_SKEW_SECONDS) throw errors.unauthorized();
  const nbf = readNumberClaim(claims, 'nbf');
  if (nbf !== null && nbf > nowSeconds + CLOCK_SKEW_SECONDS) throw errors.unauthorized();

  const email = readStringClaim(claims, 'email');
  const sub = readStringClaim(claims, 'sub');
  if (email === null || sub === null) throw errors.unauthorized();

  return { email, sub };
}

// ------------------------------------------------------------- token sourcing

function tokenFromCookie(request: Request): string | null {
  const cookieHeader = request.headers.get('Cookie');
  if (cookieHeader === null) return null;
  for (const part of cookieHeader.split(';')) {
    const separator = part.indexOf('=');
    if (separator < 0) continue;
    if (part.slice(0, separator).trim() !== ACCESS_COOKIE) continue;
    const value = part.slice(separator + 1).trim();
    if (value !== '') return value;
  }
  return null;
}

/**
 * The gate every `/api/admin/*` route calls first.
 *
 * Throws `UNAUTHORIZED` on any failure, so a route that forgets to check the
 * result still cannot proceed unauthenticated — the failure is an exception, not
 * a falsy return.
 */
export async function requireAdmin(request: Request, env: RuntimeEnv): Promise<AccessIdentity> {
  // ---------------------------------------------------------------------------
  // LOCAL/CI BYPASS.
  //
  // Fenced by TWO conditions, and structured so the second is unreachable
  // unless the first holds: with APP_ENV !== 'development' the body of this `if`
  // never runs, so ACCESS_DEV_BYPASS_EMAIL is not read, not compared, and
  // cannot be turned on by setting it. There is no code path from a deployed
  // configuration into this block.
  //
  // It exists because `wrangler pages dev` and CI have no Access edge in front
  // of them, so there is no JWT to verify and the admin API would otherwise be
  // untestable. Every use logs, so a bypassed request is never silent.
  // ---------------------------------------------------------------------------
  if (isDevelopment(env)) {
    const bypassEmail = env.ACCESS_DEV_BYPASS_EMAIL;
    if (bypassEmail !== undefined) {
      console.warn(
        '[access] DEVELOPMENT BYPASS IN USE — admin request accepted with no Access JWT. ' +
          'This is only possible with APP_ENV=development and ACCESS_DEV_BYPASS_EMAIL set. ' +
          `identity=${bypassEmail}`,
      );
      return { email: bypassEmail, sub: `dev-bypass:${bypassEmail}` };
    }
  }

  const { ACCESS_TEAM_DOMAIN: teamDomain, ACCESS_AUD: aud } = env;
  if (teamDomain === undefined || aud === undefined) {
    // A misconfigured deployment must fail CLOSED and loudly. Returning 401
    // would suggest the caller is at fault and hide a broken deployment.
    throw errors.serviceUnavailable(
      new Error('ACCESS_TEAM_DOMAIN and ACCESS_AUD must both be set to verify Access tokens'),
    );
  }

  const token = request.headers.get(ACCESS_HEADER) ?? tokenFromCookie(request);
  if (token === null || token.trim() === '') throw errors.unauthorized();

  return verifyAccessJwt(token.trim(), { teamDomain, aud });
}
