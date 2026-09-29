param(
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [string]$OutputDir = "..\backups"
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'recovery-common.ps1')
$composeCandidate = if ([IO.Path]::IsPathRooted($ComposeFile)) { $ComposeFile } else { Join-Path $scriptRoot $ComposeFile }
$envCandidate = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$composePath = (Resolve-Path -LiteralPath $composeCandidate).Path
$envPath = (Resolve-Path -LiteralPath $envCandidate).Path
$stamp = (Get-Date -Format "yyyyMMdd-HHmmss") + '-' + [guid]::NewGuid().ToString('N')
$backupRoot = if ([IO.Path]::IsPathRooted($OutputDir)) { $OutputDir } else { Join-Path $scriptRoot $OutputDir }
$backupDir = Join-Path $backupRoot "edugrade-$stamp"
$backupDir = (New-Item -ItemType Directory -Path $backupDir).FullName
$composeDir = Split-Path -Parent $composePath
$envValues = @{}
Get-Content -LiteralPath $envPath -Encoding utf8 | ForEach-Object {
  $line = $_.Trim()
  if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
    $parts = $line.Split("=", 2)
    $envValues[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
  }
}
$migrationDir = (Resolve-Path -LiteralPath (Join-Path $composeDir "../../services/api-gateway/migrations")).Path
$latestMigration = Get-ChildItem -LiteralPath $migrationDir -File -Filter "*.sql" | Sort-Object Name | Select-Object -Last 1
if (-not $latestMigration) { throw "No database migrations were found." }
$schemaVersion = $latestMigration.BaseName.Split('_')[0]
$context = @{ ComposePath = $composePath; EnvPath = $envPath }
$maintenance = @{ Containers = @() }
$sourceBucket = [string]$envValues['EDUGRADE_FILE_BUCKET']
if ($sourceBucket -notmatch '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$') { throw 'Invalid source bucket.' }

Push-Location $composeDir
try {
  # 停止本 Compose 的应用写入后依次导出数据库和对象；外部写入者需另行隔离。
  Enter-RecoveryMaintenance $context $maintenance
  $postgresContainerIds = @(
    @(& docker compose --env-file $envPath -f $composePath ps -q postgres) |
      Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
  )
  if ($LASTEXITCODE -ne 0 -or $postgresContainerIds.Count -ne 1) {
    throw "Expected exactly one running PostgreSQL container for this Compose deployment."
  }
  $postgresContainerId = $postgresContainerIds[0].Trim()

  $postgresFile = "postgres-$stamp.dump"
  $postgresPath = Join-Path $backupDir $postgresFile
  $remoteDump = "/tmp/$postgresFile"
  $remoteDumpCreated = $false
  try {
    docker compose --env-file $envPath -f $composePath exec -T postgres sh -ec "pg_dump -U `"`$POSTGRES_USER`" -d `"`$POSTGRES_DB`" --format=custom --file=$remoteDump"
    $remoteDumpCreated = $true
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL backup failed." }
    docker compose --env-file $envPath -f $composePath exec -T postgres pg_restore --list $remoteDump | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL backup verification failed." }
    docker cp "${postgresContainerId}:$remoteDump" $postgresPath
    if ($LASTEXITCODE -ne 0) { throw "Copying PostgreSQL backup failed." }
  } finally {
    if ($remoteDumpCreated) {
      docker compose --env-file $envPath -f $composePath exec -T postgres rm -f $remoteDump
      if ($LASTEXITCODE -ne 0) { Write-Warning "Unable to remove temporary PostgreSQL dump $remoteDump from the container." }
    }
  }
  if (-not (Test-Path $postgresPath) -or (Get-Item $postgresPath).Length -eq 0) { throw "PostgreSQL backup is empty." }

  $postgresUser = (& docker compose --env-file $envPath -f $composePath exec -T postgres printenv POSTGRES_USER).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Reading PostgreSQL user failed.' }
  $postgresDatabase = (& docker compose --env-file $envPath -f $composePath exec -T postgres printenv POSTGRES_DB).Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Reading PostgreSQL database failed.' }
  $references = @(Get-RecoveryReferences $context $postgresUser $postgresDatabase)
  $referencesFile = "file-references-$stamp.json"
  @{ scope = 'active-and-orphan-recovered'; objects = @($references) } | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 -LiteralPath (Join-Path $backupDir $referencesFile)

  New-Item -ItemType Directory -Force -Path (Join-Path $backupDir "minio-$stamp") | Out-Null
  $backupMount = (Resolve-Path $backupDir).Path
  $minioScript = 'scheme=http; if [ "$EDUGRADE_MINIO_USE_SSL" = "true" ]; then scheme=https; fi; mc alias set edugrade "$scheme://minio:9000" "$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD"; mc mirror --overwrite "edugrade/$EDUGRADE_FILE_BUCKET" "/backup/minio-{0}"' -f $stamp
  & docker compose --env-file $envPath -f $composePath --profile tools run --rm -v "${backupMount}:/backup" --entrypoint /bin/sh minio-init -ec $minioScript
  if ($LASTEXITCODE -ne 0) { throw "MinIO backup failed." }
  # 对象镜像成功仍不足以证明备份一致，需按数据库引用核对文件大小和哈希。
  Assert-RecoveryObjects $references (Join-Path $backupDir "minio-$stamp") $sourceBucket
  Assert-RecoveryMaintenance $context

  $files = Get-ChildItem -Path $backupDir -File -Recurse | Where-Object { $_.Name -ne "manifest-$stamp.json" } | ForEach-Object {
    [ordered]@{
      path = $_.FullName.Substring($backupDir.Length).TrimStart('\', '/')
      bytes = $_.Length
      sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
    }
  }
  $images = @()
  $runningContainerIds = @(Invoke-RecoveryCompose $context @('ps', '-q'))
  # 已停止的应用容器不会出现在 compose ps -q；它们的镜像 ID 仍属于本次备份版本，必须保留在证据中。
  $containerIds = @(@($runningContainerIds) + @($maintenance.Containers) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Sort-Object -Unique)
  foreach ($containerId in $containerIds) {
    $imageEvidence = docker inspect --format '{{.Name}}|{{.Config.Image}}|{{.Image}}' $containerId
    if ($LASTEXITCODE -ne 0) { throw "Reading image evidence failed." }
    $parts = $imageEvidence.Trim().Split('|', 3)
    $images += [ordered]@{ container = $parts[0].TrimStart('/'); image = $parts[1]; id = $parts[2] }
  }
  $migrationCount = & docker compose --env-file $envPath -f $composePath exec -T postgres psql -U $postgresUser -d $postgresDatabase -Atc "SELECT count(*) FROM schema_migration"
  if ($LASTEXITCODE -ne 0) { throw "Reading migration evidence failed." }
  $labelsJson = docker inspect --format '{{json .Config.Labels}}' $postgresContainerId
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($labelsJson)) { throw "Reading Compose project labels failed." }
  $labels = $labelsJson | ConvertFrom-Json
  $composeProjectProperty = $labels.PSObject.Properties["com.docker.compose.project"]
  $composeProject = if ($composeProjectProperty) { [string]$composeProjectProperty.Value } else { "" }
  if ([string]::IsNullOrWhiteSpace($composeProject)) { throw "Reading Compose project evidence failed." }
  $manifest = [ordered]@{
    format_version = 3
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    source_environment = [string]$envValues["EDUGRADE_ENV"]
    compose_project = $composeProject
    applied_migration_count = [int]$migrationCount
    schema_version = $schemaVersion
    postgres_file = $postgresFile
    minio_directory = "minio-$stamp"
    references_file = $referencesFile
    source_bucket = $sourceBucket
    maintenance_boundary = 'compose-writers-stopped'
    stopped_containers = @($maintenance.Containers)
    images = $images
    files = @($files)
  }
  $manifest | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 -LiteralPath (Join-Path $backupDir "manifest-$stamp.json")
  & (Join-Path $scriptRoot 'verify-backup.ps1') -BackupDirectory $backupDir | Out-Null
  Write-Host "Backup written to $backupDir."
} finally {
  try { Exit-RecoveryMaintenance $maintenance } finally { Pop-Location }
}
