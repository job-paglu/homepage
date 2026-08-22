#Requires -Version 5.1
<#
.SYNOPSIS
    Apply Job Paglu database migrations against DATABASE_URL.

.DESCRIPTION
    WHICH MECHANISM THIS USES, AND WHY

    It runs THE SERVER'S OWN MIGRATION PATH, not psql.

    The migration runner is not a separate tool. Decision D-7 embeds the .sql
    files in the binary with `embed.FS` and applies them from
    internal/database/migrate.go at startup, gated on RUN_MIGRATIONS_ON_START.
    That runner is also what maintains the `schema_migrations` bookkeeping table.

    So `psql -f backend/migrations/*.sql` would appear to work and would leave
    the database in a state the server disagrees with: the DDL applied, but
    `schema_migrations` empty. The next real startup would try to apply
    migration 001 again and fail on an existing table. Using the same code path
    the server uses is the only way to keep those two in agreement.

    The mechanics, therefore: build the server binary, start it once with
    RUN_MIGRATIONS_ON_START=true on a throwaway port, wait until /health answers
    - migrations run during startup and before the listener opens, so a reply on
    /health proves they finished - then stop it. The temporary server is not left
    running; scripts\dev.ps1 tells you to start the real one yourself.

    Manual escape hatch, for when you want to READ the SQL rather than run it:
        Get-Content backend\migrations\001_init.sql |
            docker compose exec -T db psql -U jobpaglu -d jobpaglu
    Only do that against a database you are about to throw away, for the
    bookkeeping reason above.

.PARAMETER Port
    Throwaway port for the temporary server. Default 8099 - deliberately not
    8080, so this cannot collide with a backend already running elsewhere.

.PARAMETER TimeoutSeconds
    How long to wait for /health. Default 90.

.EXAMPLE
    .\scripts\migrate.ps1

.EXAMPLE
    $env:DATABASE_URL = 'postgres://...'; .\scripts\migrate.ps1

.NOTES
    Behavioural twin of scripts/migrate.sh. Exit code 0 = applied (or already
    current), 1 = failed.
#>
[CmdletBinding()]
param(
  [int]$Port = 8099,
  [int]$TimeoutSeconds = 90
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$RepoRoot   = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$BackendDir = Join-Path $RepoRoot 'backend'
$BinDir     = Join-Path $BackendDir 'bin'

function Write-Step { param([string]$m) Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok   { param([string]$m) Write-Host "    $m" -ForegroundColor Green }

function Fail {
  param([string]$Message, [string]$Fix)
  Write-Host ''
  Write-Host "ERROR: $Message" -ForegroundColor Red
  if ($Fix) { Write-Host ''; Write-Host "  Fix: $Fix" -ForegroundColor Yellow }
  Write-Host ''
  exit 1
}

if ($null -eq (Get-Command go -ErrorAction SilentlyContinue)) {
  Fail 'Go is not on PATH.' 'Install Go 1.24 or newer from https://go.dev/dl/ (or `winget install GoLang.Go`), then open a NEW terminal so PATH is re-read.'
}
if (-not (Test-Path $BackendDir)) {
  Fail "backend\ not found at $BackendDir" 'Run this from inside the repository.'
}

# ---------------------------------------------------------------------------
# DATABASE_URL: environment wins, then .env. Never echoed in full.
# ---------------------------------------------------------------------------
$databaseUrl = $env:DATABASE_URL
if ([string]::IsNullOrWhiteSpace($databaseUrl)) {
  $envPath = Join-Path $RepoRoot '.env'
  if (-not (Test-Path $envPath)) {
    Fail 'DATABASE_URL is not set and .env does not exist.' `
         'Run .\scripts\dev.ps1 first, or copy .env.example to .env and fill in DATABASE_URL.'
  }
  # Extract exactly one key. The rest of the file is never read into a variable
  # that gets printed, so an adjacent secret cannot reach the terminal.
  foreach ($line in (Get-Content -LiteralPath $envPath)) {
    if ($line -match '^\s*DATABASE_URL\s*=\s*(.*?)\s*$') {
      $raw = $Matches[1]
      if ($raw.Length -ge 2 -and (($raw[0] -eq '"' -and $raw[-1] -eq '"') -or ($raw[0] -eq "'" -and $raw[-1] -eq "'"))) {
        $raw = $raw.Substring(1, $raw.Length - 2)
      }
      if ($raw -ne '') { $databaseUrl = $raw }
    }
  }
}

if ([string]::IsNullOrWhiteSpace($databaseUrl)) {
  Fail 'DATABASE_URL is empty.' `
       'Set it in .env. For the docker-compose database that is postgres://USER:PASSWORD@localhost:5432/DBNAME?sslmode=disable'
}
if ($databaseUrl -like '*REPLACE_*') {
  Fail 'DATABASE_URL still contains a placeholder from .env.example.' `
       'Edit .env and put your real local values in. Nothing there is a secret worth guarding - it is a throwaway container.'
}

# Reported without the credentials, so a shared screen or a pasted log cannot
# leak the password.
$safeUrl = [regex]::Replace($databaseUrl, '(//)[^/@]*@', '$1<redacted>@')
Write-Step "Target: $safeUrl"

# ---------------------------------------------------------------------------
# Build. Building rather than `go run` is deliberate: `go run` compiles to a
# temp binary and execs it as a CHILD process, so stopping the `go` process can
# leave the server alive and holding the port. Building gives us the exact PID.
# ---------------------------------------------------------------------------
Write-Step 'Building the server binary'
New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
$serverExe = Join-Path $BinDir 'server.exe'

Push-Location $BackendDir
try {
  & go build -o $serverExe ./cmd/server
  if ($LASTEXITCODE -ne 0) {
    Fail 'go build failed.' 'Fix the compile error above. If it is a missing module, run `cd backend; go mod tidy` and commit go.sum.'
  }
} finally {
  Pop-Location
}
Write-Ok 'Built backend\bin\server.exe'

# ---------------------------------------------------------------------------
# Run once with migrations enabled, then stop.
# ---------------------------------------------------------------------------
$logFile = Join-Path ([System.IO.Path]::GetTempPath()) ("jobpaglu-migrate-{0}.log" -f ([guid]::NewGuid().ToString('N')))
$proc = $null

# ADMIN_SESSION_SECRET: the server validates it at startup, before it would
# reach the migration step, so a value must be present even for a
# migrate-and-exit run. Generated fresh from the OS CSPRNG, used for a few
# seconds by a process that serves no request, then discarded. It never touches
# .env, is never printed, and no session is created with it.
#
# Rendered as HEX, not base64, and that is not arbitrary:
# internal/config/config.go rejects a secret containing a template marker such
# as "todo" or "sample", and it lowercases before comparing. A random base64
# string can contain "todo" by chance roughly once in twenty thousand runs,
# which would surface as a baffling "still looks like the .env.example
# placeholder" failure. The hex alphabet [0-9a-f] cannot spell any of those
# markers, so the flake is impossible rather than merely unlikely.
# 32 bytes -> 64 characters, above the 32-character minimum.
#
# RNGCryptoServiceProvider rather than the static
# RandomNumberGenerator::GetBytes(int): the latter is .NET 6+ and therefore
# PowerShell 7.2+ only, and this script must also run on Windows PowerShell 5.1.
$rngBytes = [byte[]]::new(32)
$rng = [System.Security.Cryptography.RNGCryptoServiceProvider]::new()
try   { $rng.GetBytes($rngBytes) }
finally { $rng.Dispose() }
$ephemeralSecret = ([BitConverter]::ToString($rngBytes)).Replace('-', '').ToLowerInvariant()

# Env vars are set on THIS process so the child inherits them, then restored, so
# the caller's session is not left mutated. Values are assigned via
# [Environment]::SetEnvironmentVariable rather than `Set-Item env:` because the
# latter is a cmdlet and would echo its value under -WhatIf.
$previous = @{}
function Set-ChildEnv {
  param([string]$Name, [string]$Value)
  $script:previous[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process')
  [Environment]::SetEnvironmentVariable($Name, $Value, 'Process')
}
function Restore-ChildEnv {
  foreach ($k in $script:previous.Keys) {
    [Environment]::SetEnvironmentVariable($k, $script:previous[$k], 'Process')
  }
}

try {
  Set-ChildEnv 'DATABASE_URL'            $databaseUrl
  Set-ChildEnv 'RUN_MIGRATIONS_ON_START' 'true'
  Set-ChildEnv 'PORT'                    "$Port"
  if ([string]::IsNullOrWhiteSpace($env:APP_ENV))   { Set-ChildEnv 'APP_ENV'   'development' }
  if ([string]::IsNullOrWhiteSpace($env:LOG_LEVEL)) { Set-ChildEnv 'LOG_LEVEL' 'info' }
  if ([string]::IsNullOrWhiteSpace($env:FRONTEND_URL)) { Set-ChildEnv 'FRONTEND_URL' 'http://localhost:4321' }
  if ([string]::IsNullOrWhiteSpace($env:ADMIN_SESSION_SECRET)) { Set-ChildEnv 'ADMIN_SESSION_SECRET' $ephemeralSecret }

  Write-Step "Starting the server once with RUN_MIGRATIONS_ON_START=true (port $Port)"
  $proc = Start-Process -FilePath $serverExe `
                        -WorkingDirectory $BackendDir `
                        -RedirectStandardOutput $logFile `
                        -RedirectStandardError "$logFile.err" `
                        -PassThru -NoNewWindow

  Write-Step 'Waiting for /health (a reply proves startup, and therefore migration, completed)'
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  $healthy  = $false

  while ((Get-Date) -lt $deadline) {
    if ($proc.HasExited) {
      Write-Host ''
      Write-Host '--- server output ---' -ForegroundColor Red
      foreach ($f in @($logFile, "$logFile.err")) {
        if (Test-Path $f) { Get-Content -LiteralPath $f | ForEach-Object { Write-Host $_ } }
      }
      Write-Host '---------------------' -ForegroundColor Red
      Fail 'The server exited before becoming healthy. Migrations did NOT complete.' `
           'Read the output above. A connection refused means Postgres is not up - run .\scripts\dev.ps1. A syntax error means a migration file is broken.'
    }

    try {
      $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" -UseBasicParsing -TimeoutSec 3
      if ($r.StatusCode -eq 200) { $healthy = $true; break }
    } catch {
      # Not up yet. Expected for the first second or two; keep polling.
    }
    Start-Sleep -Milliseconds 500
  }

  if (-not $healthy) {
    Write-Host ''
    Write-Host '--- server output ---' -ForegroundColor Red
    foreach ($f in @($logFile, "$logFile.err")) {
      if (Test-Path $f) { Get-Content -LiteralPath $f | ForEach-Object { Write-Host $_ } }
    }
    Write-Host '---------------------' -ForegroundColor Red
    Fail "The server did not answer /health within $TimeoutSeconds s." `
         "If port $Port is in use, re-run with -Port <free port>. Otherwise read the output above."
  }

  # The migration runner's startup lines are worth seeing: they say which
  # versions were applied, or that there was nothing to do.
  foreach ($f in @($logFile, "$logFile.err")) {
    if ((Test-Path $f) -and ((Get-Item $f).Length -gt 0)) {
      Write-Host ''
      Write-Host '--- server startup log ---'
      Get-Content -LiteralPath $f | ForEach-Object { Write-Host $_ }
      Write-Host '--------------------------'
      Write-Host ''
    }
  }

  Write-Ok 'Migrations applied. Schema is current.'
}
finally {
  Write-Step 'Stopping the temporary server'
  if ($null -ne $proc -and -not $proc.HasExited) {
    # No Ctrl-C to send to a background process on Windows, so this is a hard
    # stop. Acceptable here: the process has done its DDL inside a transaction
    # and is otherwise idle with no in-flight request to interrupt.
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    $proc.WaitForExit(5000) | Out-Null
  }
  Restore-ChildEnv
  foreach ($f in @($logFile, "$logFile.err")) {
    if (Test-Path $f) { Remove-Item -LiteralPath $f -Force -ErrorAction SilentlyContinue }
  }
}

exit 0
