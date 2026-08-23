#!/usr/bin/env bash
# Post a job onto Job Paglu with no admin login.
#   ./post-job.sh path/to/job.json      # one job, or {"jobs":[...]} for a batch
#   cat job.json | ./post-job.sh -      # from stdin
# Reads WEBHOOK_API_KEY from $WEBHOOK_API_KEY, else from .env.deploy above this
# script. The key is sent in the header only, never printed.
set -euo pipefail
ENDPOINT="${JP_WEBHOOK_ENDPOINT:-https://home.jobpaglu.eu.cc/api/webhook/jobs}"

key="${WEBHOOK_API_KEY:-}"
if [ -z "$key" ]; then
  d="$(cd "$(dirname "$0")" && pwd)"
  while [ -n "$d" ] && [ ! -f "$d/.env.deploy" ]; do
    p="$(dirname "$d")"; [ "$p" = "$d" ] && break; d="$p"
  done
  [ -f "$d/.env.deploy" ] && key="$(grep -E '^\s*WEBHOOK_API_KEY\s*=' "$d/.env.deploy" | head -1 | cut -d= -f2- | tr -d '\r\n ')"
fi
[ -n "$key" ] || { echo "WEBHOOK_API_KEY not found (env var or .env.deploy)." >&2; exit 1; }

src="${1:?usage: post-job.sh <job.json | -> }"
body="$( [ "$src" = "-" ] && cat || cat "$src" )"

curl -sS -X POST "$ENDPOINT" \
  -H "Authorization: Bearer $key" \
  -H "Content-Type: application/json" \
  -d "$body"
echo
