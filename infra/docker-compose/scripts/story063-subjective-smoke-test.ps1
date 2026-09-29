[CmdletBinding()]
param(
  [string]$ApiUrl = "http://127.0.0.1:8080",
  [Parameter(Mandatory = $true)][string[]]$SegmentIds,
  [string]$TenantCode = $env:EDUGRADE_SMOKE_TENANT_CODE,
  [string]$Username = $env:EDUGRADE_SMOKE_USERNAME,
  [string]$Password = $env:EDUGRADE_SMOKE_PASSWORD,
  [string]$AccessToken = $env:EDUGRADE_SMOKE_ACCESS_TOKEN,
  [int]$TimeoutSeconds = 300,
  [int]$PollSeconds = 2
)

$ErrorActionPreference = "Stop"
$apiOrigin = $ApiUrl.TrimEnd("/")

if ($apiOrigin -notmatch '^https?://[^/]+$') {
  throw "ApiUrl must be a single HTTP(S) origin without a path."
}
if ($SegmentIds.Count -lt 1 -or $SegmentIds.Count -gt 1000) {
  throw "SegmentIds must contain between 1 and 1000 answer segment IDs."
}
$seenSegments = @{}
foreach ($segmentId in $SegmentIds) {
  $parsed = [Guid]::Empty
  if (-not [Guid]::TryParse($segmentId, [ref]$parsed)) {
    throw "Every SegmentIds value must be a UUID."
  }
  if ($seenSegments.ContainsKey($segmentId)) {
    throw "SegmentIds must not contain duplicates."
  }
  $seenSegments[$segmentId] = $true
}
if ($TimeoutSeconds -lt 10 -or $PollSeconds -lt 1) {
  throw "TimeoutSeconds must be at least 10 and PollSeconds must be at least 1."
}

function Invoke-SubjectiveApi {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("GET", "POST")][string]$Method,
    [Parameter(Mandatory = $true)][string]$Path,
    [object]$Body
  )

  $headers = @{ Accept = "application/json" }
  if (-not [string]::IsNullOrWhiteSpace($script:AccessToken)) {
    $headers.Authorization = "Bearer $script:AccessToken"
  }
  $parameters = @{
    Uri = "$apiOrigin$Path"
    Method = $Method
    Headers = $headers
    ContentType = "application/json"
    TimeoutSec = 60
  }
  if ($null -ne $Body) {
    $parameters.Body = $Body | ConvertTo-Json -Depth 8 -Compress
  }
  Invoke-RestMethod @parameters
}

if ([string]::IsNullOrWhiteSpace($AccessToken)) {
  if ([string]::IsNullOrWhiteSpace($TenantCode) -or [string]::IsNullOrWhiteSpace($Username) -or [string]::IsNullOrWhiteSpace($Password)) {
    throw "Provide EDUGRADE_SMOKE_ACCESS_TOKEN, or all of EDUGRADE_SMOKE_TENANT_CODE, EDUGRADE_SMOKE_USERNAME, and EDUGRADE_SMOKE_PASSWORD."
  }
  $login = Invoke-SubjectiveApi -Method POST -Path "/api/v1/auth/token" -Body @{
    tenant_code = $TenantCode
    username = $Username
    password = $Password
    client_type = "desktop"
    device_name = "STORY-063 smoke test"
  }
  $AccessToken = [string]$login.access_token
  if ([string]::IsNullOrWhiteSpace($AccessToken)) {
    throw "Login succeeded without an access token."
  }
}


# 本脚本会创建并入队真实批次；每次运行生成新键，只观察终态，不发布最终分数。
$idempotencyKey = "story063-smoke-$((Get-Date).ToUniversalTime().ToString('yyyyMMddHHmmss'))-$([Guid]::NewGuid().ToString('N').Substring(0, 8))"
$created = Invoke-SubjectiveApi -Method POST -Path "/api/v1/subjective-grading-batches" -Body @{
  idempotency_key = $idempotencyKey
  segment_ids = $SegmentIds
}
$batchId = [string]$created.batch.id
if ([string]::IsNullOrWhiteSpace($batchId) -or [int]$created.batch.total_count -ne $SegmentIds.Count) {
  throw "Batch creation did not return the expected durable batch."
}

$enqueued = Invoke-SubjectiveApi -Method POST -Path "/api/v1/subjective-grading-batches/$batchId/enqueue"
if ([string]$enqueued.batch.status -notin @("processing", "completed", "failed")) {
  throw "Batch enqueue returned an unexpected status."
}
Write-Host "STORY-063 batch $batchId enqueued with $($SegmentIds.Count) answer segment(s)."

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
do {
  $current = (Invoke-SubjectiveApi -Method GET -Path "/api/v1/subjective-grading-batches/$batchId").batch
  $accounted = [int]$current.queued_count + [int]$current.processing_count + [int]$current.succeeded_count + [int]$current.failed_count
  if ($accounted -ne [int]$current.total_count) {
    throw "Batch progress counters do not add up to total_count."
  }
  Write-Host "Batch progress: queued=$($current.queued_count), processing=$($current.processing_count), succeeded=$($current.succeeded_count), failed=$($current.failed_count)."
  if ([string]$current.status -eq "completed") {
    if ([int]$current.succeeded_count -ne [int]$current.total_count -or [int]$current.failed_count -ne 0) {
      throw "Completed batch contains inconsistent terminal counters."
    }
    Write-Host "STORY-063 real subjective grading worker smoke test passed for batch $batchId."
    exit 0
  }
  if ([string]$current.status -eq "failed") {
    throw "STORY-063 batch $batchId reached failed state with $($current.failed_count) failed task(s)."
  }
  Start-Sleep -Seconds $PollSeconds
} while ((Get-Date) -lt $deadline)

throw "Timed out waiting for STORY-063 batch $batchId after $TimeoutSeconds seconds. Verify the subjective-grading Compose profile and worker credentials."
