// @ts-check
import { defineConfig } from 'astro/config';
import cloudflare from '@astrojs/cloudflare';
import react from '@astrojs/react';

/**
 * Job Paglu frontend.
 *
 * Deployment target: **Cloudflare only** (docs/DECISIONS.md D-21).
 *   A single Cloudflare Pages project serves the site AND the API. The API is
 *   Astro routes under src/pages/api/, backed by D1 through
 *   `locals.runtime.env.DB`. There is no second service, no cross-origin call,
 *   and therefore no CORS and no proxy: `/api/*` is same-origin by
 *   construction.
 *
 *   Admin auth is Cloudflare Access, not passwords — the Workers free plan caps
 *   CPU at 10ms per invocation and Argon2id needs 50-150ms, so a password hash
 *   cannot run there at all. Access authenticates at the edge before the
 *   request reaches this code.
 *
 *   The Go backend in ../backend/ is retained as the reference implementation
 *   and is NOT deployed. See docs/DECISIONS.md D-21 for what that trade bought.
 *
 * Rendering model (see docs/DECISIONS.md D-2):
 *   output: 'server' makes SSR the default, and individual pages opt back into
 *   build-time rendering with `export const prerender = true`. A job published
 *   in the admin dashboard is then public immediately, with no rebuild, while
 *   pages that never change (home shell, 404, legal) still ship as static HTML.
 *
 * React (D-4) is registered but deliberately used on `/admin` only. No public
 * page mounts an island, so the public bundle contains no framework JavaScript.
 */
export default defineConfig({
  // Absolute site URL. Drives canonical links, Open Graph URLs and sitemap.xml.
  // Override per environment with SITE_URL so preview deployments emit their own
  // canonicals instead of pointing at production.
  site: process.env.SITE_URL ?? 'https://jobpaglu.com',

  output: 'server',
  adapter: cloudflare({
    // The API lives in this same project as Astro routes under src/pages/api/,
    // and it reads its D1 binding from `locals.runtime.env`. That is what the
    // Cloudflare adapter provides and the Vercel adapter does not — swapping
    // this line back would break every route in src/lib/server/.
    //
    // 'advanced' rather than 'directory': Pages Functions from a single
    // _worker.js, which is what `wrangler pages dev` and Pages both expect for
    // an Astro SSR build.
    platformProxy: {
      // Makes `wrangler`'s local D1 (real SQLite via workerd) available during
      // `astro dev`, so the API can be exercised without a deploy. This is the
      // whole reason the stack can be tested end to end for free.
      enabled: true,
    },

    // Cloudflare Images is a paid add-on and this site has no raster assets --
    // the only thing in public/ is an SVG favicon and job records carry no
    // image field. 'compile' keeps image processing at build time and adds no
    // runtime dependency.
    imageService: 'compile',
  }),

  // Deliberately NOT set: any adapter-level caching option. Cache-Control is
  // set per-response, because API-CONTRACT.md fixes the s-maxage values so the
  // API and the pages cannot disagree. Two caching mechanisms with two sources
  // of truth is how a published job becomes visible at one URL and not another.
  //
  // NOTE ON CPU TIME: the Workers *free* plan allows 10ms CPU per invocation.
  // That is ample for these handlers -- CPU time excludes waiting on D1 -- but
  // it is why admin auth is Cloudflare Access rather than a password hash:
  // Argon2id needs 50-150ms of pure CPU and cannot fit. See docs/SECURITY.md.

  // CSRF. Explicit, because this is now a load-bearing security control and not
  // a default worth inheriting silently.
  //
  // I previously reasoned that CSRF was not applicable here because the app sets
  // no cookie of its own. That was WRONG: Cloudflare Access authenticates with
  // the `CF_Authorization` **cookie**, which a browser attaches to cross-site
  // requests just like any other. So a state-changing request forged from
  // another origin would arrive already authenticated.
  //
  // Astro's origin check is what closes that. It rejects state-changing requests
  // whose `Origin` does not match the host, which is exactly the old
  // `X-CSRF-Token` guarantee obtained without a token to mint, store or rotate.
  //
  // Consequence for API clients: a non-browser caller must send `Origin`, or use
  // a JSON content type, or it gets a 403. That is the intended trade -- see
  // docs/SECURITY.md.
  security: {
    checkOrigin: true,
  },

  integrations: [react()],

  // Trailing-slash-free URLs: /jobs/senior-cloud-engineer, never .../
  trailingSlash: 'never',

  build: {
    // Emit /jobs/foo.html rather than /jobs/foo/index.html for prerendered pages,
    // which keeps the canonical URL and the file path in agreement.
    format: 'file',
    inlineStylesheets: 'auto',
  },

  prefetch: {
    // Prefetch on hover only. `viewport` would fire dozens of requests on a long
    // job list, which costs the visitor bandwidth to save a few ms.
    prefetchAll: false,
    defaultStrategy: 'hover',
  },

  vite: {
    build: {
      // Small, predictable chunks; nothing here is big enough to need splitting
      // heuristics tuned for a large SPA.
      cssCodeSplit: true,
    },
  },

  devToolbar: {
    enabled: false,
  },
});
