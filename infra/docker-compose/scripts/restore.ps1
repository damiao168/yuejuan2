param(
  [Parameter(Mandatory=$true)][string]$PostgresDump,
  [Parameter(Mandatory=$true)][string]$TargetDatabase,
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [string]$MinioBackupDirectory,
  [string]$TargetBucket,
  [switch]$CreateTargetDatabase,
  [switch]$AllowPrimaryDatabase,
  [switch]$AllowPrimaryBucket,
  [switch]$AllowProductionRestore,
  [switch]$ConfirmRestore
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'recovery-common.ps1')
$composeCandidate = if ([IO.Path]::IsPathRooted($ComposeFile)) { $ComposeFile } else { Join-Path $scriptRoot $ComposeFile }
$envCandidate = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$composePath = (Resolve-Path -LiteralPath $composeCandidate).Path
$envPath = (Resolve-Path -LiteralPath $envCandidate).Path
$dumpPath = (Resolve-Path -LiteralPath $PostgresDump).Path
if (-not (Test-Path -LiteralPath $dumpPath -PathType Leaf)) { throw "PostgresDump must reference a dump file." }
$composeDir = Split-Path -Parent $composePath


# 先验证完整备份包及所选路径，再进入停服和写入阶段，避免混用不同备份的数据库与对象。
$backupDirectory = Split-Path -Parent $dumpPath
$verification = & (Join-Path $scriptRoot 'verify-backup.ps1') -BackupDirectory $backupDirectory | ConvertFrom-Json
$manifest = Get-Content -LiteralPath $verification.manifest -Raw -Encoding utf8 | ConvertFrom-Json
if (-not (Test-RecoveryPathEqual $dumpPath $verification.postgres_path)) { throw 'PostgresDump is not the dump selected by this backup manifest.' }
$minioPath = if ($MinioBackupDirectory) { (Resolve-Path -LiteralPath $MinioBackupDirectory).Path } else { $verification.minio_path }
if (-not (Test-RecoveryPathEqual $minioPath $verification.minio_path)) { throw 'MinioBackupDirectory is not the directory selected by this backup manifest.' }
$referenceDocument = Get-Content -LiteralPath $verification.references_path -Raw -Encoding utf8 | ConvertFrom-Json
$references = @($referenceDocument.objects)

if (-not $ConfirmRestore) { throw "Restore requires -ConfirmRestore." }
if ($TargetDatabase -notmatch '^[A-Za-z0-9_]+$') { throw "TargetDatabase contains unsupported characters." }
$context = @{ ComposePath = $composePath; EnvPath = $envPath }
$deployment = Get-RecoveryDeploymentSettings $context
$postgresUser = $deployment.User
if ($deployment.Environment.Trim() -in @("production", "prod") -and -not $AllowProductionRestore) {
  throw "Refusing to restore into a production environment without -AllowProductionRestore."
}
if (-not $TargetBucket) { $TargetBucket = $deployment.Bucket }
if ($TargetBucket -notmatch '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$') { throw "TargetBucket is not a valid S3 bucket name." }
$primaryDatabaseSelected = $TargetDatabase -ceq $deployment.Database
$primaryBucketSelected = $TargetBucket -ceq $deployment.Bucket
if ($primaryDatabaseSelected -and -not $AllowPrimaryDatabase) {
  throw "Refusing to overwrite the primary database without -AllowPrimaryDatabase. Prefer an isolated verification database."
}
if ($primaryBucketSelected -and -not $AllowPrimaryBucket) {
  throw "Refusing to overwrite the primary MinIO bucket without -AllowPrimaryBucket. Prefer an isolated verification bucket."
}
# 主库与主桶成对切换。混用隔离目标会让恢复后的主应用读不到原件或污染仍在使用的对象。
if ($primaryDatabaseSelected -ne $primaryBucketSelected) { throw 'Primary database and primary MinIO bucket must be restored together. Use two isolated targets for a restore drill.' }
$restoreTouchesPrimary = $primaryDatabaseSelected
# 演练使用隔离资源；主环境数据变更与已有生产标志都要遵循网关的生产配置边界。
if ($restoreTouchesPrimary -and $deployment.Environment.Trim() -notin @('development', 'dev', 'test', 'local') -and -not $AllowProductionRestore) {
  throw "Refusing to restore into a production-like environment without -AllowProductionRestore."
}
if ($restoreTouchesPrimary -and -not $deployment.ApplicationEndpointMatches) { throw 'Primary restore requires the gateway and recovery tools to use the same MinIO endpoint and TLS mode.' }
if ($restoreTouchesPrimary -and -not $deployment.ApplicationIdentityMatches) { throw 'Primary restore requires matching MinIO application credentials in the gateway and recovery tools.' }
if ($restoreTouchesPrimary) { Assert-RecoveryMinioApplicationScope $context $deployment.ApplicationAccessKey $deployment.UseMinioTLS }
$maintenance = @{ Containers = @() }
$restoreMutated = $false
$restoreVerified = $false

Push-Location $composeDir
try {
  Enter-RecoveryMaintenance $context $maintenance
  $postgresContainerId = $deployment.PostgresContainer
  $remoteDump = "/tmp/restore-$([guid]::NewGuid().ToString('N')).dump"
  $remoteDumpCopied = $false
  try {
    docker cp $dumpPath "${postgresContainerId}:$remoteDump"
    $remoteDumpCopied = $true
    if ($LASTEXITCODE -ne 0) { throw "Copying restore dump failed." }
    docker compose --env-file $envPath -f $composePath exec -T postgres pg_restore --list $remoteDump | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL dump validation failed." }
    $restoreMutated = $true
    if ($CreateTargetDatabase) {
      docker compose --env-file $envPath -f $composePath exec -T postgres sh -ec "dropdb -U `"`$POSTGRES_USER`" --if-exists '$TargetDatabase'; createdb -U `"`$POSTGRES_USER`" '$TargetDatabase'"
      if ($LASTEXITCODE -ne 0) { throw "Creating target database failed." }
    }
    docker compose --env-file $envPath -f $composePath exec -T postgres sh -ec "pg_restore -U `"`$POSTGRES_USER`" -d '$TargetDatabase' --exit-on-error --single-transaction --clean --if-exists '$remoteDump'"
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL restore failed." }
  } finally {
    if ($remoteDumpCopied) {
      docker compose --env-file $envPath -f $composePath exec -T postgres rm -f $remoteDump
      if ($LASTEXITCODE -ne 0) { Write-Warning "Unable to remove temporary PostgreSQL dump $remoteDump from the container." }
    }
  }

  # 先核对 dump 的引用与迁移账本，再覆盖目标桶，避免数据库与备份不匹配时继续修改对象。
  $actual = @(Get-RecoveryReferences $context $postgresUser $TargetDatabase)
  Assert-RecoveryReferenceSet $references $actual $manifest.source_bucket
  $schemaCount = Invoke-RecoverySQL $context $postgresUser $TargetDatabase 'SELECT count(*) FROM schema_migration'
  if ([int]$schemaCount -le 0) { throw "Restored database schema history validation failed." }
  Assert-RecoveryMaintenance $context

  $backupMount = Split-Path -Parent $minioPath
  $backupName = Split-Path -Leaf $minioPath
  $minioScript = 'scheme=http; if [ "$EDUGRADE_MINIO_USE_SSL" = "true" ]; then scheme=https; fi; mc alias set edugrade "$scheme://minio:9000" "$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD"; mc mb --ignore-existing "edugrade/{1}"; mc mirror --overwrite "/backup/{0}" "edugrade/{1}"' -f $backupName, $TargetBucket
  & docker compose --env-file $envPath -f $composePath --profile tools run --rm -v "${backupMount}:/backup:ro" --entrypoint /bin/sh minio-init -ec $minioScript | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "MinIO restore failed." }
  # 用目标桶的实际字节校验后再切换引用，主恢复还验证原应用账号读取权限。
  $targetReferences = @($actual | Select-Object -Property id,storage_key,size_bytes,hash_sha256,@{Name='storage_bucket'; Expression={$TargetBucket}})
  Assert-RestoredRecoveryObjects $context $TargetBucket $targetReferences -UseApplicationIdentity:$restoreTouchesPrimary
  if ($manifest.source_bucket -cne $TargetBucket) {
    Assert-RecoveryMaintenance $context
    $remap = Get-RecoveryBucketRemapSQL $manifest.source_bucket $TargetBucket $actual
    # 引用 JSON 可能超过主机命令行长度，直接从标准输入交给 PostgreSQL。
    $remap | & docker compose --env-file $envPath -f $composePath exec -T postgres psql -X -v ON_ERROR_STOP=1 -U $postgresUser -d $TargetDatabase -f - | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Restored database bucket remapping failed.' }
  }
  $remappedReferences = @(Get-RecoveryReferences $context $postgresUser $TargetDatabase)
  Assert-RecoveryReferenceSet $references $remappedReferences $TargetBucket
  Assert-RecoveryMaintenance $context
  $restoreVerified = $true
  Write-Host "Restore completed into database '$TargetDatabase' and bucket '$TargetBucket'. Database file references and object bytes/hashes verified."
} finally {
  try {
    # 主数据已被修改但验证未完成时继续停服，避免应用使用半恢复状态；由维护者完成恢复。
    if (-not $restoreTouchesPrimary -or -not $restoreMutated -or $restoreVerified) { Exit-RecoveryMaintenance $maintenance }
    elseif ($maintenance.Containers.Count -gt 0) {
      Write-Warning "Restore changed primary data but did not complete verification. Application containers remain stopped. After completing and verifying recovery, restart the original containers with: docker start $($maintenance.Containers -join ' ')"
    }
  } finally { Pop-Location }
}
