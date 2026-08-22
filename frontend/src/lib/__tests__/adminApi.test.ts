/**
 * Tests for the browser fetch core, the admin API wrappers, and the identity
 * policy that replaced the old session/CSRF machinery.
 *
 * What these assert that a reader should care about:
 *   - every request path is site-relative, so there is no base URL to get wrong;
 *   - no `X-CSRF-Token` is sent, and no `credentials: 'include'` either;
 *   - `/api/admin/me` is probed once per page and its 401 is reported as "not
 *     authenticated" rather than acted on.
 *
 * Relative imports on purpose: there is no `vitest.config.ts` in this project, so
 * the `@lib/*` aliases from `tsconfig.json` are not wired into the Vitest
 * resolver. A test that only passes because of a config file nobody has written
 * yet is worse than a test with slightly uglier imports.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, apiFetch } from '../api';
import {
  createJob,
  deleteJob,
  getMe,
  listAdminJobs,
  publishJob,
  updateJob,
} from '../adminApi';
import {
  clearIdentity,
  currentIdentity,
  isNotAuthenticated,
  loadIdentity,
} from '../adminIdentity';
import type { JobInput } from '../types';

// ---------------------------------------------------------------- test doubles

interface StubResponse {
  status: number;
  body?: unknown;
  headers?: Record<string, string>;
}

function respond({ status, body, headers = {} }: StubResponse): Response {
  const init: ResponseInit = {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  };
  if (status === 204 || body === undefined) return new Response(null, init);
  return new Response(JSON.stringify(body), init);
}

/** The `/api/admin/me` envelope: `{data:{admin:{email,sub}}}` — no name, no
 *  `last_login_at`, no `csrf_token`. Access is the identity provider. */
const ME_BODY = { data: { admin: { email: 'admin@example.com', sub: 'access|abc123' } } };

const fetchMock = vi.fn<(input: string | URL | Request, init?: RequestInit) => Promise<Response>>();

/**
 * Always answer with a FRESH `Response`.
 *
 * `mockResolvedValue(respond(...))` evaluates `respond` once and hands the same
 * object to every call — and a `Response` body can only be read once, so the
 * second call fails with a `TypeError` that has nothing to do with the code under
 * test. Any test that expects more than one request must use this.
 */
function alwaysRespond(stub: StubResponse): void {
  fetchMock.mockImplementation(() => Promise.resolve(respond(stub)));
}

interface RecordedCall {
  url: string;
  method: string;
  credentials: RequestCredentials | undefined;
  headers: Record<string, string>;
  body: BodyInit | null | undefined;
}

function calls(): RecordedCall[] {
  return fetchMock.mock.calls.map(([url, init]) => ({
    url: String(url),
    method: init?.method ?? 'GET',
    credentials: init?.credentials,
    headers: (init?.headers as Record<string, string> | undefined) ?? {},
    body: init?.body,
  }));
}

function firstCall(): RecordedCall {
  const call = calls()[0];
  if (call === undefined) throw new Error('fetch was never called');
  return call;
}

const VALID_INPUT: JobInput = {
  title: 'Senior Cloud Engineer',
  description: 'A description long enough to satisfy the twenty character minimum.',
  salary: '$140k',
  qualification: 'B.Sc.',
  job_type: 'full-time',
  location: 'Berlin, Germany',
  apply_url: 'https://example.com/apply',
};

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  fetchMock.mockReset();
  clearIdentity();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// ------------------------------------------------------------ same-origin core

describe('apiFetch', () => {
  it('sends the path verbatim, with no base URL prefixed', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 200, body: { data: [] } }));

    await apiFetch('/api/jobs?page=2');

    expect(firstCall().url).toBe('/api/jobs?page=2');
  });

  it('uses same-origin credentials and never `include`', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 200, body: { data: {} } }));

    await apiFetch('/api/admin/me');

    expect(firstCall().credentials).toBe('same-origin');
  });

  /**
   * The guard that keeps "there is no base URL" true. A protocol-relative or
   * absolute target would send the Access cookie somewhere else entirely, so it
   * must fail before `fetch` is reached — not merely be discouraged by a comment.
   */
  const offOrigin = ['https://evil.example/api/admin/jobs', '//evil.example/api', 'api/jobs'];
  for (const path of offOrigin) {
    it(`refuses ${JSON.stringify(path)} without calling fetch`, async () => {
      await expect(apiFetch(path)).rejects.toBeInstanceOf(ApiError);
      expect(fetchMock).not.toHaveBeenCalled();
    });
  }

  it('returns undefined for a 204 rather than trying to parse a body', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 204 }));

    await expect(deleteJob('j1')).resolves.toBeUndefined();
  });
});

// ------------------------------------------------------------------- adminApi

describe('adminApi', () => {
  it('serialises AdminJobListQuery and drops empty values', async () => {
    fetchMock.mockResolvedValueOnce(
      respond({
        status: 200,
        body: { data: [], meta: { page: 2, per_page: 20, total: 0, total_pages: 0 } },
      }),
    );

    const result = await listAdminJobs({
      page: 2,
      per_page: 20,
      q: '',
      status: 'draft',
      include_deleted: false,
    });

    const call = firstCall();
    expect(call.url.startsWith('/api/admin/jobs?')).toBe(true);
    // A second argument is required: the URL is relative and has no origin.
    const query = new URL(call.url, 'https://site.test').searchParams;
    expect(query.get('page')).toBe('2');
    expect(query.get('per_page')).toBe('20');
    expect(query.get('status')).toBe('draft');
    expect(query.get('include_deleted')).toBe('false');
    // An empty `q` must not be sent: the API treats a present-but-empty search
    // differently from an absent one.
    expect(query.has('q')).toBe(false);
    expect(result.pagination.page).toBe(2);
  });

  it('posts JSON with no CSRF header at all', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 201, body: { data: { id: 'j1' } } }));

    await createJob(VALID_INPUT);

    const call = firstCall();
    expect(call.method).toBe('POST');
    expect(call.headers['Content-Type']).toBe('application/json');
    // Not merely "empty" — absent. The token is gone, and `checkOrigin` plus the
    // CORS preflight requirement are what replaced it (see adminApi.ts).
    expect('X-CSRF-Token' in call.headers).toBe(false);
    expect(call.credentials).toBe('same-origin');
  });

  it('takes no csrf argument on a write, and still reaches the right path', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 200, body: { data: { id: 'j1' } } }));

    await updateJob('j 1/../x', VALID_INPUT);

    // The id is percent-encoded, so a hostile id cannot climb out of the route.
    expect(firstCall().url).toBe('/api/admin/jobs/j%201%2F..%2Fx');
  });

  it('throws ApiError carrying code and details for a 422', async () => {
    fetchMock.mockResolvedValueOnce(
      respond({
        status: 422,
        body: {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'One or more fields are invalid.',
            details: { apply_url: 'Must be an absolute https:// URL.' },
          },
        },
      }),
    );

    await expect(publishJob('j1')).rejects.toMatchObject({
      code: 'VALIDATION_FAILED',
      details: { apply_url: 'Must be an absolute https:// URL.' },
    });
  });

  it('unwraps `data.admin` from /api/admin/me', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 200, body: ME_BODY }));

    await expect(getMe()).resolves.toEqual({ email: 'admin@example.com', sub: 'access|abc123' });
    expect(firstCall().url).toBe('/api/admin/me');
  });
});

// -------------------------------------------------------------- identity policy

describe('adminIdentity', () => {
  it('probes /api/admin/me once and shares the result', async () => {
    alwaysRespond({ status: 200, body: ME_BODY });

    const [first, second] = await Promise.all([loadIdentity(), loadIdentity()]);

    expect(first?.email).toBe('admin@example.com');
    expect(second).toBe(first);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(currentIdentity()).toBe(first);
  });

  it('re-probes when forced', async () => {
    alwaysRespond({ status: 200, body: ME_BODY });

    await loadIdentity();
    await loadIdentity({ force: true });

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('reports a 401 as "not authenticated" and drops the cached identity', async () => {
    fetchMock.mockResolvedValueOnce(respond({ status: 200, body: ME_BODY }));
    await loadIdentity();
    expect(currentIdentity()).not.toBeNull();

    fetchMock.mockResolvedValueOnce(
      respond({ status: 401, body: { error: { code: 'UNAUTHORIZED', message: 'not signed in' } } }),
    );

    const caught = await loadIdentity({ force: true }).catch((error: unknown) => error);

    expect(isNotAuthenticated(caught)).toBe(true);
    expect(currentIdentity()).toBeNull();
  });

  it('does not classify a non-401 failure as an auth problem', async () => {
    const failure = new ApiError({ code: 'SERVICE_UNAVAILABLE', message: 'down', status: 503 });

    expect(isNotAuthenticated(failure)).toBe(false);
  });
});
