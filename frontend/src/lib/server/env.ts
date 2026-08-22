/**
 * Typed access to the Cloudflare Workers runtime environment.
 *
 * On Cloudflare Pages, bindings and variables are NOT ambient globals and are
 * NOT `import.meta.env`: they arrive per-request on `locals.runtime.env`. Every
 * server module therefore takes an env object rather than reaching for a global,
 * which is also what makes the modules unit-testable with a fake D1.
 *
 * The D1 types below are declared locally and deliberately minimal.
 * `@cloudflare/workers-types` is not a dependency of this project (package.json
 * is out of scope), and a hand-written structural subset is enough: the real
 * `D1Database` is assignable to it, so adding the package later needs no change
 * here.
 */

// ------------------------------------------------------------------ D1 shapes

/** The subset of D1's prepared-statement API this codebase uses. */
export interface D1PreparedStatement {
  /**
   * Binds positional `?` parameters, in order. This is the ONLY way a value
   * reaches a query anywhere in `src/lib/server/**` — see jobs.repo.ts.
   */
  bind(...values: readonly unknown[]): D1PreparedStatement;
  /** First row, or null when the statement returned none. */
  first<T>(): Promise<T | null>;
  /** All rows. */
  all<T>(): Promise<{ readonly results: T[] }>;
}

/** The subset of the D1 binding this codebase uses. */
export interface D1Database {
  prepare(query: string): D1PreparedStatement;
}

// -------------------------------------------------------------- runtime shape

/**
 * Everything the API reads at runtime.
 *
 * `DB` is required; the string variables are optional so a missing one is a
 * handled configuration error with a useful message rather than a type lie.
 */
export interface RuntimeEnv {
  readonly DB: D1Database;
  readonly APP_ENV?: string;
  readonly ACCESS_TEAM_DOMAIN?: string;
  readonly ACCESS_AUD?: string;
  readonly ACCESS_DEV_BYPASS_EMAIL?: string;
  readonly ALLOW_HTTP_APPLY_URLS?: string;
}

/** Thrown when a binding or variable the request needs is absent. */
export class ConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ConfigError';
  }
}

// ------------------------------------------------------------------ accessors

interface MaybeRuntimeLocals {
  runtime?: { env?: unknown };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function hasPrepare(value: unknown): value is D1Database {
  return isRecord(value) && typeof value['prepare'] === 'function';
}

function readString(source: Record<string, unknown>, key: string): string | undefined {
  const value = source[key];
  if (typeof value !== 'string') return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

/**
 * Extract the runtime env from an Astro `APIContext.locals`.
 *
 * `locals` is typed `App.Locals`, which this project does not augment (that is
 * the adapter's business, and `src/env.d.ts` is not in this module's scope), so
 * the parameter is `unknown` and narrowed here. One cast, in one place, with the
 * shape checked — rather than an `as` at every call site.
 *
 * Build-time `import.meta.env` is used as a FALLBACK for the string variables
 * only. That matters for `astro dev` without the Cloudflare platform proxy,
 * where `.env` is present but `locals.runtime` is not. The D1 binding has no
 * fallback: without a real database there is nothing sensible to degrade to.
 */
export function getRuntimeEnv(locals: unknown): RuntimeEnv {
  const runtimeEnv = isRecord(locals) ? (locals as MaybeRuntimeLocals).runtime?.env : undefined;
  const source = isRecord(runtimeEnv) ? runtimeEnv : {};

  const db = source['DB'];
  if (!hasPrepare(db)) {
    throw new ConfigError(
      'The D1 binding "DB" is not available on locals.runtime.env. Check the ' +
        '[[d1_databases]] block in wrangler.toml, and run local development ' +
        'through the Cloudflare adapter so the binding is injected.',
    );
  }

  const build = import.meta.env;

  return {
    DB: db,
    APP_ENV: readString(source, 'APP_ENV') ?? build.APP_ENV,
    ACCESS_TEAM_DOMAIN: readString(source, 'ACCESS_TEAM_DOMAIN'),
    ACCESS_AUD: readString(source, 'ACCESS_AUD'),
    ACCESS_DEV_BYPASS_EMAIL: readString(source, 'ACCESS_DEV_BYPASS_EMAIL'),
    ALLOW_HTTP_APPLY_URLS: readString(source, 'ALLOW_HTTP_APPLY_URLS'),
  };
}

/**
 * True only for an exact `development` match.
 *
 * Deliberately not "anything that is not production": a typo like `APP_ENV=dev`
 * must fall on the SAFE side, because this flag unlocks the Access bypass and
 * the `http://` apply-URL allowance.
 */
export function isDevelopment(env: RuntimeEnv): boolean {
  return env.APP_ENV === 'development';
}

/** Policy for the apply-URL allowlist (contract section 7). */
export interface ApplyUrlPolicy {
  readonly allowHttp: boolean;
  readonly allowLocalhost: boolean;
}

/**
 * Resolve the apply-URL policy.
 *
 * `ALLOW_HTTP_APPLY_URLS` must be the exact string `true` to enable `http://`;
 * every other value, including `"1"` and `"yes"`, leaves it off. A security
 * switch that turns on for anything truthy turns on by accident.
 *
 * The two switches are independent, exactly as contract section 7 has them:
 * `http://` is gated on the flag alone (so it is off by default even locally),
 * and a dotless `localhost` host is gated on development alone. A developer who
 * wants `http://localhost:3000/apply` needs both, which is the honest answer —
 * that URL is two separate departures from the production rule.
 */
export function applyUrlPolicy(env: RuntimeEnv): ApplyUrlPolicy {
  return {
    allowHttp: env.ALLOW_HTTP_APPLY_URLS === 'true',
    allowLocalhost: isDevelopment(env),
  };
}
