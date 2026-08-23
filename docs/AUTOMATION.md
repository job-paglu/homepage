# Posting jobs without the admin page

There are two ways a job reaches the site:

1. **The admin dashboard** at `/admin` — a human signs in through Cloudflare
   Access and fills the form.
2. **The webhook API** — a machine posts a job with a bearer key, no login.
   This is the one to automate against.

This document is the automation guide. The normative spec is
[`API-CONTRACT.md` §11](API-CONTRACT.md).

---

## The endpoint

```
POST https://home.jobpaglu.eu.cc/api/webhook/jobs
Authorization: Bearer <WEBHOOK_API_KEY>
Content-Type: application/json
```

- **`Content-Type: application/json` is required.** A form content type is
  rejected by the origin check; JSON is not. Every example below sends JSON.
- The key is a secret. It is **not** in this repository. It was set on the
  Cloudflare Pages project with
  `wrangler pages secret put WEBHOOK_API_KEY` and is held locally in the
  workspace's git-ignored `.env.deploy`. To rotate it, run that command again
  with a new value.

---

## The smallest possible call

```bash
curl -X POST https://home.jobpaglu.eu.cc/api/webhook/jobs \
  -H "Authorization: Bearer $WEBHOOK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "external_id": "ATS-1042",
    "title": "Senior Cloud Engineer",
    "description": "Own the platform that serves millions of requests a day. At least 20 characters.",
    "salary": "$140k - $175k",
    "qualification": "B.Sc. Computer Science or equivalent",
    "job_type": "full-time",
    "location": "Berlin, Germany",
    "apply_url": "https://employer.example.com/careers/123",
    "status": "published"
  }'
```

Required: `title`, `description` (>= 20 chars), `salary`, `qualification`,
`job_type`, `location`, `apply_url` (`https://` only). Optional: `company`,
`country`, `remote_type` (`remote|hybrid|onsite`), `experience_level`,
`category`, `is_featured`, `expires_at`, `external_id`, `status`.

**`status` defaults to `draft`.** Send `"status": "published"` to put it live.

---

## `external_id` — set it, always

`external_id` is your own id for the posting (the ATS id, the row id, the
source URL — anything stable). It makes the call **idempotent**:

- First call with `ATS-1042` -> creates the job.
- Any later call with `ATS-1042` -> **updates that same job**, never a duplicate.

Every webhook system retries on a timeout. Without `external_id`, a retry
creates a second copy of the job. With it, a retry is harmless. This is the
single most important field for automation.

On an update, omitting `status` keeps the job's current status — re-syncing a
published job without a `status` field will not silently unpublish it.

---

## Batches

Send up to 50 at once:

```json
{ "jobs": [ { "external_id": "…", "title": "…", … }, { … } ] }
```

The response reports each item. One bad row does not fail the others:

```json
{ "data": {
  "created": 2, "updated": 1, "failed": 1,
  "results": [
    { "index": 0, "action": "created", "slug": "senior-cloud-engineer", "external_id": "ATS-1042", "status": "published" },
    { "index": 3, "action": "failed", "external_id": "ATS-9", "error": { "code": "VALIDATION_FAILED", "message": "…", "details": { "apply_url": "…" } } }
  ]
} }
```

A processed batch returns `200` even with per-item failures — so a sender does
not blindly retry the whole batch over one bad row. Only auth/config problems
short-circuit: `401` (bad/missing key), `503` (key not configured), `413` (body
over 256 KiB), `422` (more than 50 items, or a non-array `jobs`).

---

## Wiring it to a no-code tool

Any tool that can send an HTTP POST works. In each, add a header
`Authorization: Bearer YOUR_KEY` and `Content-Type: application/json`, and a
JSON body as above.

- **Zapier** — "Webhooks by Zapier" -> Custom Request -> POST, the URL above,
  the two headers, and the job JSON as the data. Map fields from the trigger.
- **Make (Integromat)** — an HTTP "Make a request" module, same settings.
- **n8n** — an HTTP Request node, method POST, "Header Auth" credential holding
  the bearer token.
- **Google Sheets** — an Apps Script that reads a row and posts it:

  ```javascript
  function postJob() {
    const key = PropertiesService.getScriptProperties().getProperty('WEBHOOK_API_KEY');
    UrlFetchApp.fetch('https://home.jobpaglu.eu.cc/api/webhook/jobs', {
      method: 'post',
      contentType: 'application/json',
      headers: { Authorization: 'Bearer ' + key },
      payload: JSON.stringify({
        external_id: 'SHEET-' + rowId,
        title, description, salary, qualification,
        job_type: jobType, location, apply_url: applyUrl,
        status: 'published',
      }),
    });
  }
  ```

  Store the key in **Project Settings -> Script Properties**, never in the code.

---

## From this workspace (Windows / any shell)

Two helper scripts live in the project's `tools/` directory. They read the key
from `.env.deploy` themselves, so you never handle it:

```powershell
# one job
.\tools\post-job.ps1 -Title "Senior Cloud Engineer" `
  -Description "Own the platform that serves millions of requests a day..." `
  -Salary '$140k - $175k' -Qualification "B.Sc. or equivalent" `
  -JobType full-time -Location "Berlin, Germany" `
  -ApplyUrl "https://employer.com/careers/123" -ExternalId "ATS-1042" -Publish
```

```bash
# a job or a batch from a JSON file
WEBHOOK_API_KEY=… ./tools/post-job.sh job.json
```

Both create a `draft` unless you publish; both print the resulting job URL.

---

## Security notes

- The key is the only thing protecting this endpoint (it is deliberately not
  behind Cloudflare Access, because a webhook cannot do an interactive login).
  Treat it like a password: if it leaks, rotate it immediately with
  `wrangler pages secret put WEBHOOK_API_KEY`.
- `apply_url` is validated with the same allowlist as the admin path, so a
  caller cannot post a `javascript:` or `data:` apply link even with a valid
  key.
- `status` defaults to `draft`, so a leaked key cannot spam live listings
  without explicitly publishing.
- There is no per-key rate limit today beyond the 50-item batch cap and D1's
  free-tier write budget. If this endpoint is ever exposed more widely, add one.
