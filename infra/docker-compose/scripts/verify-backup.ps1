param(
  [Parameter(Mandatory=$true)][string]$BackupDirectory,
  [string]$ExpectedEnvironment,
  [string]$ExpectedComposeProject
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'recovery-common.ps1')
$backupRoot = (Resolve-Path -LiteralPath $BackupDirectory).Path
$manifests = @(Get-ChildItem -LiteralPath $backupRoot -File -Filter 'manifest-*.json')
if ($manifests.Count -ne 1) { throw 'Backup must contain exactly one manifest-*.json file.' }
$manifest = Get-Content -LiteralPath $manifests[0].FullName -Encoding utf8 -Raw | ConvertFrom-Json
if ([int]$manifest.format_version -ne 3) { throw 'Backup manifest version is unsupported: create a version 3 backup with maintenance and file-reference evidence.' }
if (-not $manifest.created_at -or -not $manifest.postgres_file -or -not $manifest.minio_directory -or -not $manifest.references_file -or $manifest.maintenance_boundary -ne 'compose-writers-stopped') { throw 'Backup manifest is incomplete.' }
if ($manifest.minio_directory -cnotmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { throw 'Backup object directory must be a safe single directory name.' }
if ($manifest.source_bucket -notmatch '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$') { throw 'Backup source bucket is invalid.' }
if ($ExpectedEnvironment -and $manifest.source_environment -ne $ExpectedEnvironment) { throw 'Backup source environment does not match ExpectedEnvironment.' }
if ($ExpectedComposeProject -and $manifest.compose_project -ne $ExpectedComposeProject) { throw 'Backup Compose project does not match ExpectedComposeProject.' }

$dump = Resolve-RecoveryChild $backupRoot $manifest.postgres_file
$minio = Resolve-RecoveryChild $backupRoot $manifest.minio_directory 'Container'
$referencesPath = Resolve-RecoveryChild $backupRoot $manifest.references_file
$listed = [Collections.Generic.HashSet[string]]::new((Get-RecoveryPathComparer))
foreach ($entry in @($manifest.files)) {
  $candidate = Resolve-RecoveryChild $backupRoot ([string]$entry.path)
  if ($listed.Contains($candidate)) { throw 'Backup manifest contains duplicate files.' }
  if ((Get-Item -LiteralPath $candidate).Length -ne [long]$entry.bytes) { throw "Backup file size mismatch: $($entry.path)" }
  if (([string]$entry.sha256) -notmatch '^[a-fA-F0-9]{64}$' -or (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash.ToLowerInvariant() -cne ([string]$entry.sha256).ToLowerInvariant()) { throw "Backup file hash mismatch: $($entry.path)" }
  [void]$listed.Add($candidate)
}
if (-not $listed.Contains($dump) -or -not $listed.Contains($referencesPath)) { throw 'PostgreSQL dump and references must be covered by the manifest.' }

# 既验证清单内文件，也拒绝清单之外的文件，防止目录混入未被校验的内容。
foreach ($file in @(Get-ChildItem -LiteralPath $backupRoot -File -Recurse -Force)) {
  if (-not (Test-RecoveryPathEqual $file.FullName $manifests[0].FullName) -and -not $listed.Contains($file.FullName)) { throw 'Backup contains files not covered by the manifest.' }
}
$referenceDocument = Get-Content -LiteralPath $referencesPath -Raw -Encoding utf8 | ConvertFrom-Json
if ($referenceDocument.scope -ne 'active-and-orphan-recovered') { throw 'Backup object reference scope is invalid.' }
$references = @($referenceDocument.objects)
Assert-RecoveryObjects $references $minio $manifest.source_bucket
[ordered]@{
  valid = $true
  manifest = $manifests[0].FullName
  postgres_path = $dump
  minio_path = $minio
  references_path = $referencesPath
  format_version = [int]$manifest.format_version
  source_environment = [string]$manifest.source_environment
  schema_version = [string]$manifest.schema_version
  file_count = $listed.Count
  reference_count = $references.Count
} | ConvertTo-Json -Depth 3
