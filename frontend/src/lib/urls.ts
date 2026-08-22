/**
 * URL construction and the render-time apply-URL guard.
 *
 * Two jobs live here because they are the same concern seen from two sides:
 * every absolute URL we *emit* (canonical, Open Graph, sitemap) must agree with
 * the deployed origin, and every URL we *accept* from the database must be
 * proven safe before it reaches an `href`.
 */

/**
 * Used only when neither `Astro.site` nor `SITE_URL` is available — a
 * misconfiguration. A wrong-but-absolute canonical is recoverable; a relative
 * or empty one silently breaks Open Graph and the sitemap, so there is no
 * "throw" branch on a public page.
 */
export const FALLBACK_ORIGIN = 'https://jobpaglu.com';

/** Public routes, in one place so the sitemap and the nav cannot drift apart. */
export const ROUTES = {
  home: '/',
  jobs: '/jobs',
  privacy: '/privacy',
  terms: '/terms',
} as const;

// ------------------------------------------------------------------- emitting

/**
 * Resolve the site origin. `Astro.site` is the authority when present (it is
 * set from `SITE_URL` in astro.config.mjs, so a preview deployment emits its
 * own canonicals rather than pointing at production).
 */
export function siteOrigin(site?: URL | undefined): string {
  if (site) return site.origin;
  const configured = import.meta.env.SITE_URL;
  if (configured) {
    try {
      return new URL(configured).origin;
    } catch {
      // Fall through — a malformed SITE_URL must not blank out every canonical.
    }
  }
  return FALLBACK_ORIGIN;
}

/** Absolute URL for a site-relative path. Query strings are preserved. */
export function absoluteUrl(pathAndQuery: string, site?: URL | undefined): string {
  const origin = siteOrigin(site);
  const path = pathAndQuery.startsWith('/') ? pathAndQuery : `/${pathAndQuery}`;
  return `${origin}${normalisePath(path)}`;
}

/**
 * Canonical URL for the page currently being rendered.
 *
 * Query strings are dropped: pagination pages that genuinely need `?page=2` in
 * their canonical pass the path explicitly via `absoluteUrl`. Everything else
 * must not let an arbitrary `?utm_source=...` mint an infinite family of
 * canonical URLs for the same content.
 */
export function canonicalUrl(url: URL, site?: URL | undefined): string {
  return absoluteUrl(url.pathname, site);
}

/**
 * `trailingSlash: 'never'` in astro.config.mjs. Emitting `/jobs/` anywhere
 * would be a redirect at best and a duplicate-content signal at worst, so the
 * slash is stripped here rather than trusted at each call site. `/` is exempt.
 */
function normalisePath(path: string): string {
  const [pathnamePart = '/', ...rest] = path.split('?');
  const query = rest.length > 0 ? `?${rest.join('?')}` : '';
  const trimmed = pathnamePart.replace(/\/+$/, '');
  return `${trimmed === '' ? '/' : trimmed}${query}`;
}

/** Public detail path for a job. Slugs are `[a-z0-9-]` by contract, but encode anyway. */
export function jobPath(slug: string): string {
  return `${ROUTES.jobs}/${encodeURIComponent(slug)}`;
}

/** Job-list path for a page number. Page 1 has no query, so it has one canonical. */
export function jobListPath(page: number): string {
  return page > 1 ? `${ROUTES.jobs}?page=${page}` : ROUTES.jobs;
}

/**
 * Should `href` be marked `aria-current="page"`?
 *
 * A section link stays current on its children (`/jobs/some-role` keeps `/jobs`
 * highlighted), but `/` must match exactly or it would be current everywhere.
 */
export function isCurrentPath(currentPath: string, href: string): boolean {
  if (href === ROUTES.home) return currentPath === ROUTES.home;
  return currentPath === href || currentPath.startsWith(`${href}/`);
}

/** Read and clamp the `page` query parameter. Non-numeric input becomes page 1. */
export function readPageParam(url: URL, max = 10_000): number {
  const raw = url.searchParams.get('page');
  if (raw === null) return 1;
  const parsed = Number.parseInt(raw, 10);
  if (!Number.isFinite(parsed) || parsed < 1) return 1;
  return Math.min(parsed, max);
}

// ------------------------------------------------------------------- guarding

/** Contract section 7: length cap. Enforced on write *and* here on render. */
const MAX_APPLY_URL_LENGTH = 2048;

/**
 * Reject control characters and whitespace on the *raw* string, before parsing.
 *
 * `new URL()` tolerates surrounding whitespace and strips tab/newline from
 * inside the scheme, which is exactly the trick used to smuggle a
 * `java\nscript:` style value past a naive scheme check. Written as an explicit
 * code-point scan rather than a regex so the intent is auditable and there is no
 * escaping subtlety to get wrong.
 */
function hasForbiddenChars(value: string): boolean {
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code <= 0x20) return true; // C0 controls and the space itself
    if (code === 0x7f) return true; // DEL
    if (code >= 0x80 && code <= 0xa0) return true; // C1 controls and NBSP
    if (code === 0x2028 || code === 0x2029 || code === 0xfeff) return true;
  }
  return false;
}

function isDevelopment(): boolean {
  return import.meta.env.APP_ENV === 'development';
}

/**
 * Re-validate an `apply_url` immediately before it becomes an `href`.
 *
 * The backend already validates on write (contract section 7). This is the belt
 * to that braces: a row edited directly in the database, or restored from an
 * older backup, must not be able to turn into a live `javascript:` link. The
 * scheme test is an allowlist — a denylist of dangerous schemes is always one
 * exotic scheme behind.
 */
export function isSafeApplyUrl(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  if (value.length === 0 || value.length > MAX_APPLY_URL_LENGTH) return false;
  if (hasForbiddenChars(value)) return false;

  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return false;
  }

  const dev = isDevelopment();
  if (parsed.protocol !== 'https:' && !(dev && parsed.protocol === 'http:')) return false;

  // `https://attacker.example@real-employer.com/` reads as the employer in a
  // truncated status bar. Refuse rather than try to explain it to a visitor.
  if (parsed.username !== '' || parsed.password !== '') return false;

  if (parsed.hostname === '') return false;
  // A dotless host is either an intranet name or a typo; neither belongs on a
  // public apply link. `localhost` is allowed in development only.
  if (!parsed.hostname.includes('.') && !(dev && parsed.hostname === 'localhost')) return false;

  return true;
}

/** Host shown next to a valid apply link so the visitor knows where they land. */
export function applyUrlHost(value: string): string | null {
  try {
    return new URL(value).hostname.replace(/^www\./, '');
  } catch {
    return null;
  }
}
