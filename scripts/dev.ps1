#Requires -Version 5.1
<#
.SYNOPSIS
    Bring up the Job Paglu local development database, migrate it, and print the
    two commands that run the backend and the frontend.

.DESCRIPTION
    What this script DOES:
      1. Checks the prerequisites and names what to install if one is missing.
      2. Creates .env from .env.example on first run (never overwrites).
      3. Starts only the `db` service from docker-compose.yml.
      4. Waits for Postgres's own healthcheck - not a fixed sleep.
      5. Applies migrations by delegating to scripts/migrate.ps1.
      6. Optionally loads scripts/seed-dev.sql (-Seed), refusing unless
         APP_ENV=development.
      7. Prints the two long-running commands for you to run yourself.

    What this script deliberately does NOT do: orchestrate three long-running
    processes. A wrapper that owns the backend and the Astro dev server has to
    reinvent log multiplexing, Ctrl-C handling and per-process restart, and the
    first thing it costs you is a readable stack trace. Two terminals is not a
    limitation - it is the thing that makes a crash legible.

.PARAMETER Seed
    After migrating, load scripts/seed-dev.sql. Refuses unless APP_ENV is
    exactly 'development'.

.PARAMETER SkipMigrate
    Start and wait for the database, then stop. Useful when you only want a
    Postgres to point psql at.

.PARAMETER Recreate
    Destroy the database volume first, for a genuinely empty database.
    This deletes local data and asks for confirmation.

.EXAMPLE
    .\scripts\dev.ps1

.EXAMPLE
    .\scripts\dev.ps1 -Seed

.NOTES
    Windows-first counterpart of scripts/dev.sh. The two are kept behaviourally
    identical; see scripts/README.md for the differences that are unavoidable.
#>
[CmdletBinding()]
param(
  [switch]$Seed,
  [switch]$SkipMigrate,
  [switch]$Recreate
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Repo root is the parent of scripts/. Resolved from the script's own location so
# the script works from any working directory (rule R11: no absolute paths).
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

function Write-Step  { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok    { param([string]$m) Write-Host "    $m" -ForegroundColor Green }
function Write-Warn2 { param([string]$m) Write-Host "    $m" -ForegroundColor Yellow }

function Fail {
  param([string]$Message, [string]$Fix)
  Write-Host ''
  Write-Host "ERROR: $Message" -ForegroundColor Red
  if ($Fix) {
    Write-Host ''
    Write-Host "  Fix: $Fix" -ForegroundColor Yellow
  }
  Write-Host ''
  exit 1
}

function Test-Tool {
  param([string]$Name)
  $cmd = Get-Command $Name -ErrorAction SilentlyContinue
  if ($null -eq $cmd) { return $null }
  return $cmd.Source
}

# ---------------------------------------------------------------------------
# 1. Prerequisites - fail fast, and say what to install.
# ---------------------------------------------------------------------------
Write-Step 'Checking prerequisites'

$missing = @()

if (-not (Test-Tool 'docker')) {
  $missing += 'docker  -> Docker Desktop for Windows: https://docs.docker.com/desktop/install/windows-install/'
}
if (-not (Test-Tool 'go')) {
  $missing += 'go      -> Go 1.24 or newer: https://go.dev/dl/  (winget install GoLang.Go)'
}
if (-not (Test-Tool 'node')) {
  $missing += 'node    -> Node.js 20.11 LTS or newer: https://nodejs.org/  (winget install OpenJS.NodeJS.LTS)'
}
if (-not (Test-Tool 'npm')) {
  $missing += 'npm     -> ships with Node.js; if node exists but npm does not, reinstall Node'
}

if ($missing.Count -gt 0) {
  Write-Host ''
  Write-Host 'ERROR: required tools are not on PATH:' -ForegroundColor Red
  foreach ($m in $missing) { Write-Host "  $m" -ForegroundColor Yellow }
  Write-Host ''
  Write-Host '  A tool installed in a shell opened BEFORE the install will not be' -ForegroundColor Yellow
  Write-Host '  visible here. Open a new terminal and try again.' -ForegroundColor Yellow
  Write-Host ''
  exit 1
}

# `docker compose` (v2 plugin), not `docker-compose` (v1, end of life). Probed
# rather than assumed, because the error otherwise surfaces much later as an
# unhelpful "unknown command".
& docker compose version *> $null
if ($LASTEXITCODE -ne 0) {
  Fail 'Docker is installed but `docker compose` is not available.' `
       'Install Docker Desktop 4.x or newer (it bundles the Compose v2 plugin). The retired `docker-compose` v1 binary is not supported by this repo.'
}

# The daemon, not just the CLI. On Windows this is the single most common cause
# of a confusing failure: Docker Desktop is installed but not started.
& docker info *> $null
if ($LASTEXITCODE -ne 0) {
  Fail 'The Docker CLI works but the Docker daemon is not reachable.' `
       'Start Docker Desktop and wait for the whale icon to stop animating, then re-run this script.'
}

Write-Ok "docker  $((& docker --version) -replace 'Docker version ', '')"
Write-Ok "go      $(((& go version) -split ' ')[2])"
Write-Ok "node    $(& node --version)"

# ---------------------------------------------------------------------------
# 2. .env
# ---------------------------------------------------------------------------
Write-Step 'Checking .env'

$envPath     = Join-Path $RepoRoot '.env'
$envExample  = Join-Path $RepoRoot '.env.example'
$feEnvPath   = Join-Path $RepoRoot 'frontend/.env'
$feEnvExample= Join-Path $RepoRoot 'frontend/.env.example'

if (-not (Test-Path $envPath)) {
  Copy-Item -Path $envExample -Destination $envPath
  Write-Warn2 'Created .env from .env.example.'
  Write-Warn2 'It contains PLACEHOLDERS. You must fill in at least:'
  Write-Warn2 '  ADMIN_SESSION_SECRET  (see the generator command in .env.example)'
  Write-Warn2 '  POSTGRES_PASSWORD     (any local-only value)'
  Write-Warn2 '  DATABASE_URL          (must match the POSTGRES_* values)'
} else {
  Write-Ok '.env exists (not modified).'
}

if (-not (Test-Path $feEnvPath)) {
  Copy-Item -Path $feEnvExample -Destination $feEnvPath
  Write-Warn2 'Created frontend/.env from frontend/.env.example (no secrets in it).'
} else {
  Write-Ok 'frontend/.env exists (not modified).'
}

# Read individual keys out of .env. Only the named key is ever returned, so a
# credential in an adjacent line cannot reach the terminal - the whole file is
# never echoed. Last occurrence wins, matching how dotenv loaders behave.
function Get-EnvValue {
  param([string]$Path, [string]$Key, [string]$Default)
  $value = $Default
  foreach ($line in (Get-Content -LiteralPath $Path)) {
    if ($line -match "^\s*$([regex]::Escape($Key))\s*=\s*(.*?)\s*$") {
      $raw = $Matches[1]
      if ($raw.Length -ge 2 -and (($raw[0] -eq '"' -and $raw[-1] -eq '"') -or ($raw[0] -eq "'" -and $raw[-1] -eq "'"))) {
        $raw = $raw.Substring(1, $raw.Length - 2)
      }
      if ($raw -ne '') { $value = $raw }
    }
  }
  return $value
}

$appEnv  = Get-EnvValue -Path $envPath -Key 'APP_ENV'       -Default 'production'
# These two must match the defaults in docker-compose.yml, because that is what
# actually created the role and the database inside the container.
$pgUser  = Get-EnvValue -Path $envPath -Key 'POSTGRES_USER' -Default 'jobpaglu'
$pgDb    = Get-EnvValue -Path $envPath -Key 'POSTGRES_DB'   -Default 'jobpaglu'

Write-Ok "APP_ENV = $appEnv"

# ---------------------------------------------------------------------------
# 3. Database
# ---------------------------------------------------------------------------
if ($Recreate) {
  Write-Step 'Destroying the existing database volume (-Recreate)'
  $answer = Read-Host 'This deletes all local database data. Type "yes" to continue'
  if ($answer -ne 'yes') { Fail 'Aborted by user.' '' }
  Push-Location $RepoRoot
  try { & docker compose down -v } finally { Pop-Location }
}

Write-Step 'Starting Postgres (docker compose up -d db)'
Push-Location $RepoRoot
try {
  & docker compose up -d db
  if ($LASTEXITCODE -ne 0) { Fail 'docker compose failed to start the db service.' 'Read the output above; `docker compose logs db` has more.' }
} finally {
  Pop-Location
}

Write-Step 'Waiting for the Postgres healthcheck'
# Poll the container's own healthcheck rather than sleeping. A fixed sleep is
# either too short (initdb has not finished, the backend dies on connect) or
# wastefully long, and it is wrong on a different machine either way.
Push-Location $RepoRoot
try {
  $containerId = (& docker compose ps -q db | Select-Object -First 1)
  if (-not $containerId) { Fail 'Could not find the db container after starting it.' 'Run `docker compose ps` and `docker compose logs db`.' }

  $deadline = (Get-Date).AddSeconds(120)
  $status = ''
  while ((Get-Date) -lt $deadline) {
    $status = (& docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' $containerId) 2>$null
    if ($status -eq 'healthy') { break }
    if ($status -eq 'none') { Fail 'The db container has no healthcheck.' 'docker-compose.yml defines one; you may be running a modified or stale compose file.' }
    Start-Sleep -Milliseconds 1500
  }
  if ($status -ne 'healthy') {
    Fail "Postgres did not become healthy within 120s (last status: '$status')." `
         'Run `docker compose logs db`. A port clash on 5432 is the usual cause - change POSTGRES_PORT in .env.'
  }
  Write-Ok 'Postgres is healthy.'
} finally {
  Pop-Location
}

# ---------------------------------------------------------------------------
# 4. Migrations
# ---------------------------------------------------------------------------
if ($SkipMigrate) {
  Write-Step 'Skipping migrations (-SkipMigrate)'
} else {
  Write-Step 'Applying migrations'
  & (Join-Path $RepoRoot 'scripts/migrate.ps1')
  if ($LASTEXITCODE -ne 0) { Fail 'Migrations failed. Nothing further was run.' 'See the migrate output above.' }
}

# ---------------------------------------------------------------------------
# 5. Seed data (opt-in, guarded)
# ---------------------------------------------------------------------------
if ($Seed) {
  Write-Step 'Loading development seed data'

  # Two independent guards, because one is not enough for a script that writes
  # rows: this one, and a SQL-level guard inside seed-dev.sql that aborts if the
  # database already holds any job whose apply_url is not on example.com.
  if ($appEnv -ne 'development') {
    Fail "Refusing to seed: APP_ENV is '$appEnv', not 'development'." `
         'Seed data is obviously-fake development content and must never reach a real database. Set APP_ENV=development in .env if this really is a local database.'
  }

  $seedSql = Join-Path $RepoRoot 'scripts/seed-dev.sql'
  if (-not (Test-Path $seedSql)) { Fail "Seed file not found: $seedSql" '' }

  # Piped through the container's own psql, so no local PostgreSQL client is
  # needed. -v ON_ERROR_STOP=1 makes the SQL-level guard fatal instead of a
  # warning buried in the output.
  Push-Location $RepoRoot
  try {
    Get-Content -LiteralPath $seedSql -Raw |
      & docker compose exec -T db psql -v ON_ERROR_STOP=1 -U $pgUser -d $pgDb -f -
    if ($LASTEXITCODE -ne 0) { Fail 'Seeding failed.' 'Read the psql output above - the guard in seed-dev.sql refuses to touch a database that already holds non-seed jobs.' }
  } finally {
    Pop-Location
  }
  Write-Ok 'Seed data loaded.'
}

# ---------------------------------------------------------------------------
# 6. Hand over
# ---------------------------------------------------------------------------
$sep = '-' * 74
Write-Host ''
Write-Host $sep -ForegroundColor DarkGray
Write-Host ' The database is up and migrated. Now run these in TWO separate terminals.' -ForegroundColor White
Write-Host $sep -ForegroundColor DarkGray
Write-Host ''
Write-Host ' Terminal 1 - Go API  (http://localhost:8080)' -ForegroundColor Cyan
Write-Host '   cd backend' -ForegroundColor White
Write-Host '   go run ./cmd/server' -ForegroundColor White
Write-Host ''
Write-Host ' Terminal 2 - Astro dev server  (http://localhost:4321)' -ForegroundColor Cyan
Write-Host '   cd frontend' -ForegroundColor White
Write-Host '   npm install      # first run only, and commit package-lock.json' -ForegroundColor DarkGray
Write-Host '   npm run dev' -ForegroundColor White
Write-Host ''
Write-Host ' First admin user (once, then delete ADMIN_BOOTSTRAP_PASSWORD from .env):' -ForegroundColor Cyan
Write-Host '   cd backend' -ForegroundColor White
Write-Host '   go run ./cmd/adminctl create' -ForegroundColor White
Write-Host ''
Write-Host $sep -ForegroundColor DarkGray
Write-Host ' Useful:' -ForegroundColor White
Write-Host '   docker compose logs -f db          database logs' -ForegroundColor DarkGray
Write-Host '   docker compose down                stop, keep the data' -ForegroundColor DarkGray
Write-Host '   docker compose down -v             stop and delete the data' -ForegroundColor DarkGray
Write-Host '   .\scripts\migrate.ps1              re-apply migrations' -ForegroundColor DarkGray
Write-Host '   .\scripts\dev.ps1 -Seed            load fake development jobs' -ForegroundColor DarkGray
Write-Host $sep -ForegroundColor DarkGray
Write-Host ''
