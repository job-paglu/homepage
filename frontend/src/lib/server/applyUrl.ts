/**
 * The `apply_url` allowlist — contract section 7.
 *
 * This is the one field where a bad value becomes an attack on a visitor, so it
 * is checked on write, re-checked at publish time, and re-checked again on
 * render.
 *
 * RELATIONSHIP TO `src/lib/urls.ts#isSafeApplyUrl`
 * -----------------------------------------------
 * That function is the RENDER-time guard and is covered by an existing test
 * suite. The checks below are the same checks in the same order — the forbidden
 * character scan is its scan, character-for-character — with two deliberate
 * differences:
 *
 *   1. A policy object replaces its `import.meta.env.APP_ENV` read, because the
 *      server's `http://` allowance is `ALLOW_HTTP_APPLY_URLS` and its
 *      `localhost` allowance is `APP_ENV`, and those are two switches.
 *   2. It returns a per-field message instead of a boolean, because a 422 has to
 *      tell the admin which rule they broke.
 *
 * This side is also slightly STRICTER: it rejects hosts with a leading, trailing
 * or doubled dot, which `isSafeApplyUrl` accepts (`example..com` contains a dot,
 * so its dot test passes). Strictness in this direction is safe — everything the
 * server stores still passes the render guard. The reverse would not be: a value
 * accepted here and rejected there renders as a disabled button, which looks
 * like a bug in the page.
 *
 * KNOWN, PRE-EXISTING ASYMMETRY: with `ALLOW_HTTP_APPLY_URLS=true` in
 * production, this accepts `http://` while `isSafeApplyUrl` (which allows
 * `http:` only in development) rejects it at render. That is inherited from the
 * Go implementation and the contract, and it is why the flag should stay off in
 * production. Do not "fix" it by loosening the render guard.
 */

import type { ApplyUrlPolicy } from './env';

/** Contract section 7: length cap, in bytes-equivalent characters. */
export const MAX_APPLY_URL_LENGTH = 2048;

/** Messages, named so the tests assert on a constant rather than a literal. */
export const APPLY_URL_MESSAGES = {
  required: 'This field is required.',
  tooLong: `Must be at most ${MAX_APPLY_URL_LENGTH} characters.`,
  whitespace: 'Must not contain spaces, tabs or line breaks.',
  scheme: 'Must be an absolute https:// URL.',
  userinfo: 'Must not contain a username or password.',
  host: 'Must include a valid host name, for example example.com.',
} as const;

/**
 * Reject control characters and whitespace on the RAW string, before parsing.
 *
 * `new URL()` tolerates surrounding whitespace and strips tab/newline from
 * inside the scheme, which is exactly the trick used to smuggle a
 * `java\nscript:` value past a naive scheme check. Written as an explicit
 * code-point scan rather than a regex so there is no escaping subtlety and a
 * literal control character never has to appear in the source.
 *
 * Copied from `src/lib/urls.ts#hasForbiddenChars` on purpose: the two must agree
 * exactly, and a shared import would make the server module depend on a file
 * that also pulls in site-URL concerns.
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

/**
 * Validate an apply URL. Returns `null` when it is acceptable, or the message to
 * put in `details.apply_url`.
 *
 * The value is never rewritten. Whatever the admin stored is what the API
 * returns, byte for byte (contract section 7), so this function only ever says
 * yes or no.
 */
export function validateApplyUrl(raw: string, policy: ApplyUrlPolicy): string | null {
  if (raw.trim() === '') return APPLY_URL_MESSAGES.required;

  // Length before parsing: running the URL parser over a megabyte of text is
  // wasted work.
  if (raw.length > MAX_APPLY_URL_LENGTH) return APPLY_URL_MESSAGES.tooLong;

  // Refused, not cleaned. A leading space turns `  javascript:…` into something
  // some HTML parsers still execute, and an embedded newline is a log-injection
  // primitive. Cleaning would mean storing something the admin did not type.
  if (hasForbiddenChars(raw)) return APPLY_URL_MESSAGES.whitespace;

  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    // Covers `//evil.com/apply`, `/relative/path` and `example.com/jobs` — all
    // of which are relative references, not absolute URLs.
    return APPLY_URL_MESSAGES.scheme;
  }

  // THE ALLOWLIST. A denylist of dangerous schemes is always one exotic scheme
  // behind, so anything not named here is refused: javascript, data, vbscript,
  // file, blob, about, mailto, tel, ws, intent, view-source, and whatever is
  // invented next. `URL` lowercases the protocol, so `JavaScript:` lands here
  // too.
  if (parsed.protocol !== 'https:' && !(policy.allowHttp && parsed.protocol === 'http:')) {
    return APPLY_URL_MESSAGES.scheme;
  }

  // `https://attacker.example@real-employer.com/` reads as the employer in a
  // truncated status bar. Refuse rather than try to explain it to a visitor.
  if (parsed.username !== '' || parsed.password !== '') return APPLY_URL_MESSAGES.userinfo;

  const host = parsed.hostname; // no port, IPv6 brackets already stripped
  if (host === '') return APPLY_URL_MESSAGES.host;
  if (host.startsWith('.') || host.endsWith('.') || host.includes('..')) {
    return APPLY_URL_MESSAGES.host;
  }
  if (!host.includes('.')) {
    // A dotless host is either a bare intranet name, which cannot resolve for a
    // public visitor, or `localhost` — allowed in development only.
    const devLocalhost = policy.allowLocalhost && host.toLowerCase() === 'localhost';
    if (!devLocalhost) return APPLY_URL_MESSAGES.host;
  }

  return null;
}
