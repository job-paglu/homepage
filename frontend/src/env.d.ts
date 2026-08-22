/// <reference types="astro/client" />

/**
 * Cloudflare bindings and runtime variables.
 *
 * On Cloudflare there is no separate API service: the API is Astro routes in
 * this same Pages project, and they reach D1 through `locals.runtime.env`. That
 * object is supplied by `@astrojs/cloudflare`, which is why swapping the adapter
 * back to Vercel would break every route under `src/lib/server/`.
 *
 * These are Worker *bindings and vars*, not `import.meta.env`. Vite substitutes
 * `import.meta.env` at build time; bindings are injected per request at runtime.
 * The two are not interchangeable, and confusing them is the most common way to
 * get `undefined` for a value that is plainly set in the dashboard.
 */
interface Env {
  /** D1 database binding. Declared in wrangler.toml as `DB`. */
  DB: import('@cloudflare/workers-types').D1Database;

  /** 'development' | 'production'. Anything but the exact string 'development' is treated as production. */
  APP_ENV?: string;

  /**
   * Cloudflare Access team domain, e.g. `yourteam.cloudflareaccess.com`.
   * Used to build the JWKS URL and to check the `iss` claim.
   */
  ACCESS_TEAM_DOMAIN?: string;

  /**
   * Access application AUD tag. Checking this is what stops a token minted for
   * a *different* application in the same Access team from being accepted here.
   */
  ACCESS_AUD?: string;

  /**
   * Development-only identity bypass. Read ONLY when APP_ENV === 'development',
   * because there is no Access edge in front of `wrangler dev`. Must never be
   * set in production — wrangler.toml omits it entirely on purpose.
   */
  ACCESS_DEV_BYPASS_EMAIL?: string;

  /** Allow `http://` apply URLs. Default false. Must stay false in production. */
  ALLOW_HTTP_APPLY_URLS?: string;
}

type CloudflareRuntime = import('@astrojs/cloudflare').Runtime<Env>;

declare namespace App {
  interface Locals extends CloudflareRuntime {}
}

/**
 * Build-time variables, substituted by Vite. Everything here is public by
 * definition once the bundle ships — never put a secret in this interface.
 */
interface ImportMetaEnv {
  /**
   * Absolute public origin of this site. Used for canonicals, Open Graph URLs
   * and sitemap.xml.
   *
   * NOTE: there is deliberately no `PUBLIC_API_BASE_URL` any more. On Cloudflare
   * the API is same-origin — the browser calls `/api/...` as a relative path —
   * so there is no base URL to configure and no way to point the frontend at
   * the wrong backend.
   */
  readonly SITE_URL?: string;

  /** 'development' | 'production'. Drives dev-only affordances in the UI. */
  readonly APP_ENV?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
