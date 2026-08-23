<#
.SYNOPSIS
    Post a job onto Job Paglu with no admin login -- one command.

.DESCRIPTION
    Calls the live webhook endpoint (POST /api/webhook/jobs) with your bearer
    key, which is read from .env.deploy at the workspace root. The key is NEVER
    printed, passed on the command line, or written anywhere by this script.

    Two ways to call it:

    1. Individual fields:
        .\post-job.ps1 -Title "Senior Cloud Engineer" `
            -Description "Own the platform that serves millions of requests a day..." `
            -Salary '$140k - $175k' -Qualification "B.Sc. or equivalent" `
            -JobType full-time -Location "Berlin, Germany" `
            -ApplyUrl "https://employer.com/careers/123" `
            -Company "Acme GmbH" -Publish

    2. A JSON file (one job object, or {"jobs":[...]} for a batch):
        .\post-job.ps1 -JsonFile .\jobs.json

    ExternalId makes re-posting the same job UPDATE it instead of creating a
    duplicate -- always set it to your source system's id if you have one.

.NOTES
    Required fields: Title, Description (>=20 chars), Salary, Qualification,
    JobType (full-time|part-time|contract|internship|freelance|temporary),
    Location, ApplyUrl (https:// only). Everything else is optional.
    Jobs are created as DRAFT unless you pass -Publish.
#>
[CmdletBinding(DefaultParameterSetName = 'Fields')]
param(
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$Title,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$Description,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$Salary,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$Qualification,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)]
    [ValidateSet('full-time','part-time','contract','internship','freelance','temporary')]
    [string]$JobType,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$Location,
    [Parameter(ParameterSetName = 'Fields', Mandatory = $true)][string]$ApplyUrl,

    [Parameter(ParameterSetName = 'Fields')][string]$Company,
    [Parameter(ParameterSetName = 'Fields')][string]$Country,
    [Parameter(ParameterSetName = 'Fields')][ValidateSet('remote','hybrid','onsite')][string]$RemoteType,
    [Parameter(ParameterSetName = 'Fields')][string]$ExperienceLevel,
    [Parameter(ParameterSetName = 'Fields')][string]$Category,
    [Parameter(ParameterSetName = 'Fields')][string]$ExternalId,
    [Parameter(ParameterSetName = 'Fields')][switch]$Featured,
    [Parameter(ParameterSetName = 'Fields')][switch]$Publish,

    [Parameter(ParameterSetName = 'File', Mandatory = $true)][string]$JsonFile,

    # Override for testing against another deployment.
    [string]$Endpoint = 'https://home.jobpaglu.eu.cc/api/webhook/jobs'
)

$ErrorActionPreference = 'Stop'

# --- find .env.deploy by walking up from this script -------------------------
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
$envFile = $null
while ($dir -and -not $envFile) {
    $c = Join-Path $dir '.env.deploy'
    if (Test-Path $c) { $envFile = $c; break }
    $parent = Split-Path -Parent $dir
    if ($parent -eq $dir) { break }
    $dir = $parent
}
if (-not $envFile) { throw "Could not find .env.deploy above $($MyInvocation.MyCommand.Path). It holds WEBHOOK_API_KEY." }

# --- read the key WITHOUT printing it ----------------------------------------
$key = $null
foreach ($line in Get-Content -LiteralPath $envFile) {
    if ($line -match '^\s*WEBHOOK_API_KEY\s*=\s*(.+?)\s*$') { $key = $Matches[1].Trim(); break }
}
if ([string]::IsNullOrWhiteSpace($key) -or $key -like 'REPLACE*') {
    throw "WEBHOOK_API_KEY is not set in $envFile."
}

# --- build the payload -------------------------------------------------------
if ($PSCmdlet.ParameterSetName -eq 'File') {
    $payload = Get-Content -LiteralPath $JsonFile -Raw
    # validate it is JSON before sending
    try { $null = $payload | ConvertFrom-Json } catch { throw "$JsonFile is not valid JSON: $_" }
} else {
    $job = [ordered]@{
        title         = $Title
        description   = $Description
        salary        = $Salary
        qualification = $Qualification
        job_type      = $JobType
        location      = $Location
        apply_url     = $ApplyUrl
        status        = if ($Publish) { 'published' } else { 'draft' }
    }
    if ($Company)         { $job.company = $Company }
    if ($Country)         { $job.country = $Country }
    if ($RemoteType)      { $job.remote_type = $RemoteType }
    if ($ExperienceLevel) { $job.experience_level = $ExperienceLevel }
    if ($Category)        { $job.category = $Category }
    if ($ExternalId)      { $job.external_id = $ExternalId }
    if ($Featured)        { $job.is_featured = $true }
    $payload = $job | ConvertTo-Json -Depth 5
}

Write-Host ">> POST $Endpoint" -ForegroundColor Cyan
if (-not $Publish -and $PSCmdlet.ParameterSetName -eq 'Fields') {
    Write-Host "   status: draft (pass -Publish to make it live)" -ForegroundColor DarkGray
}

# --- send. The key goes in the header only, never echoed ---------------------
try {
    $resp = Invoke-RestMethod -Uri $Endpoint -Method Post `
        -Headers @{ Authorization = "Bearer $key" } `
        -ContentType 'application/json' -Body $payload -TimeoutSec 30
} catch {
    $code = $_.Exception.Response.StatusCode.value__
    if ($code -eq 401) { throw "401 Unauthorized -- the key in $envFile is wrong or the endpoint rotated it." }
    if ($code -eq 503) { throw "503 -- the endpoint has no key configured on the server (WEBHOOK_API_KEY secret unset)." }
    # surface the structured error body if present
    try {
        $reader = New-Object IO.StreamReader($_.Exception.Response.GetResponseStream())
        throw "HTTP $code`n$($reader.ReadToEnd())"
    } catch { throw }
}

# --- report ------------------------------------------------------------------
$d = $resp.data
Write-Host ""
Write-Host ("created={0}  updated={1}  failed={2}" -f $d.created, $d.updated, $d.failed) -ForegroundColor Green
foreach ($r in $d.results) {
    if ($r.action -eq 'failed') {
        Write-Host ("  [FAIL] #{0} {1}: {2}" -f $r.index, $r.external_id, $r.error.message) -ForegroundColor Red
        if ($r.error.details) { $r.error.details.PSObject.Properties | ForEach-Object { Write-Host ("         {0}: {1}" -f $_.Name, $_.Value) -ForegroundColor DarkRed } }
    } else {
        $url = "https://home.jobpaglu.eu.cc/jobs/$($r.slug)"
        Write-Host ("  [{0}] {1}  ->  {2}" -f $r.action.ToUpper(), $r.status, $url) -ForegroundColor Green
    }
}
Write-Host ""
if ($d.created -gt 0 -or $d.updated -gt 0) {
    Write-Host "Published jobs are live at https://home.jobpaglu.eu.cc/jobs" -ForegroundColor Cyan
}
