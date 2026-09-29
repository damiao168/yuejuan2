param(
  [Parameter(Mandatory=$true)][string]$BackupDirectory,
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [switch]$KeepDrillData
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$composeCandidate = if ([IO.Path]::IsPathRooted($ComposeFile)) { $ComposeFile } else { Join-Path $scriptRoot $ComposeFile }
$envCandidate = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$composePath = (Resolve-Path -LiteralPath $composeCandidate).Path
$envPath = (Resolve-Path -LiteralPath $envCandidate).Path
$backupRoot = (Resolve-Path -LiteralPath $BackupDirectory).Path
$verification = & (Join-Path $scriptRoot "verify-backup.ps1") -BackupDirectory $backupRoot | ConvertFrom-Json
$manifest = Get-Content -LiteralPath $verification.manifest -Raw -Encoding utf8 | ConvertFrom-Json

# 每次演练使用独立数据库和桶；finally 仅清理本次固定前缀与随机后缀的目标。
$suffix = [guid]::NewGuid().ToString('N').Substring(0, 12)
$targetDatabase = "edugrade_restore_drill_$suffix"
$targetBucket = "edugrade-restore-drill-$suffix"
$dumpPath = Join-Path $backupRoot $manifest.postgres_file
$minioPath = Join-Path $backupRoot $manifest.minio_directory
$composeDir = Split-Path -Parent $composePath
$envValues = @{}
Get-Content -LiteralPath $envPath -Encoding utf8 | ForEach-Object {
  $line = $_.Trim()
  if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
    $parts = $line.Split("=", 2)
    $envValues[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
  }
}
$postgresUser = $envValues["EDUGRADE_POSTGRES_USER"]

Push-Location $composeDir
try {
  & (Join-Path $scriptRoot "restore.ps1") -PostgresDump $dumpPath -TargetDatabase $targetDatabase -MinioBackupDirectory $minioPath -TargetBucket $targetBucket -ComposeFile $composePath -EnvFile $envPath -CreateTargetDatabase -ConfirmRestore | Out-Null

  $checks = @(
    "SELECT CASE WHEN count(*) > 0 THEN 1 ELSE 0 END FROM tenant",
    "SELECT CASE WHEN count(*) > 0 THEN 1 ELSE 0 END FROM schema_migration",
    "SELECT CASE WHEN count(*) = 0 THEN 1 ELSE 0 END FROM exam e JOIN school s ON s.id=e.school_id WHERE e.tenant_id<>s.tenant_id",
    "SELECT CASE WHEN count(*) = 0 THEN 1 ELSE 0 END FROM submission s JOIN exam e ON e.id=s.exam_id WHERE s.tenant_id<>e.tenant_id",
    "SELECT CASE WHEN count(*) = 0 THEN 1 ELSE 0 END FROM file_asset f LEFT JOIN tenant t ON t.id=f.tenant_id WHERE t.id IS NULL"
  )
  foreach ($query in $checks) {
    $result = (& docker compose --env-file $envPath -f $composePath exec -T postgres psql -U $postgresUser -d $targetDatabase -Atc $query).Trim()
    if ($LASTEXITCODE -ne 0 -or $result -ne "1") { throw "Restore drill database invariant failed." }
  }

  # restore.ps1 已读取恢复后的数据库引用，并从目标桶下载实际字节校验 SHA-256 和大小。
  $backupObjectCount = @(Get-ChildItem -LiteralPath $minioPath -File -Recurse).Count

  [ordered]@{
    succeeded = $true
    completed_at = (Get-Date).ToUniversalTime().ToString("o")
    source_environment = $manifest.source_environment
    schema_version = $manifest.schema_version
    database = $targetDatabase
    bucket = $targetBucket
    object_count = $backupObjectCount
    verified_file_references = [int]$verification.reference_count
    reference_bytes_and_hashes_verified = $true
  } | ConvertTo-Json -Depth 3
} finally {
  if (-not $KeepDrillData) {
    if ($targetDatabase -match '^edugrade_restore_drill_[a-f0-9]{12}$') {
      & docker compose --env-file $envPath -f $composePath exec -T postgres dropdb -U $postgresUser --if-exists $targetDatabase | Out-Null
    }
    if ($targetBucket -match '^edugrade-restore-drill-[a-f0-9]{12}$') {
      $cleanupScript = 'scheme=http; if [ "$EDUGRADE_MINIO_USE_SSL" = "true" ]; then scheme=https; fi; mc alias set edugrade "$scheme://minio:9000" "$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD" >/dev/null; mc rb --force "edugrade/{0}"' -f $targetBucket
      & docker compose --env-file $envPath -f $composePath --profile tools run --rm --entrypoint /bin/sh minio-init -ec $cleanupScript | Out-Null
    }
  }
  Pop-Location
}
