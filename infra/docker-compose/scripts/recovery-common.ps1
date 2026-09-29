$ErrorActionPreference = 'Stop'

function Get-RecoveryPathComparison {
  if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) { return [StringComparison]::OrdinalIgnoreCase }
  return [StringComparison]::Ordinal
}

function Get-RecoveryPathComparer {
  if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) { return [StringComparer]::OrdinalIgnoreCase }
  return [StringComparer]::Ordinal
}

function Test-RecoveryPathEqual([string]$First, [string]$Second) {
  return [string]::Equals($First.TrimEnd('/', '\'), $Second.TrimEnd('/', '\'), (Get-RecoveryPathComparison))
}

function Read-RecoveryEnvironment([string]$Path) {
  $values = @{}
  foreach ($line in (Get-Content -LiteralPath $Path -Encoding utf8)) {
    $value = $line.Trim()
    if ($value -and -not $value.StartsWith('#') -and $value.Contains('=')) {
      $parts = $value.Split('=', 2)
      $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
    }
  }
  return $values
}

function Invoke-RecoveryCompose($Context, [string[]]$Arguments) {
  $output = @(& docker compose --env-file $Context.EnvPath -f $Context.ComposePath @Arguments)
  if ($LASTEXITCODE -ne 0) { throw "Compose operation failed: $($Arguments[0]) (exit $LASTEXITCODE)." }
  return $output
}

function Get-RecoveryWriters($Context) {
  $writers = @()
  foreach ($id in @(Invoke-RecoveryCompose $Context @('ps', '--status', 'running', '-q'))) {
    if ([string]::IsNullOrWhiteSpace($id)) { continue }
    $service = (& docker inspect --format '{{index .Config.Labels "com.docker.compose.service"}}' $id).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $service) { throw 'Cannot identify a running Compose container; refusing an inconsistent backup.' }
    # Unknown/new services are writers by default. These infrastructure services hold data but do not initiate application writes.
    if ($service -notin @('postgres', 'redis', 'minio', 'qdrant')) { $writers += $id.Trim() }
  }
  return $writers
}

function Enter-RecoveryMaintenance($Context, $State) {
  # 在停止前保存容器列表；即使停止中途失败，调用方 finally 仍知道应恢复哪些容器。
  $State.Containers = @(Get-RecoveryWriters $Context)
  if ($State.Containers.Count -gt 0) {
    $containers = @($State.Containers)
    & docker stop --time 60 @containers | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Unable to stop all application writers.' }
  }
  Assert-RecoveryMaintenance $Context
}

function Assert-RecoveryMaintenance($Context) {
  if (@(Get-RecoveryWriters $Context).Count -ne 0) { throw 'Application writers are running during recovery maintenance.' }
}

function Exit-RecoveryMaintenance($State) {
  if ($State.Containers.Count -gt 0) {
    $containers = @($State.Containers)
    & docker start @containers | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Unable to restart the original application containers; restore them manually before ending maintenance.' }
  }
}

function Invoke-RecoverySQL($Context, [string]$User, [string]$Database, [string]$Query) {
  return ((Invoke-RecoveryCompose $Context @('exec', '-T', 'postgres', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', $User, '-d', $Database, '-Atc', $Query)) -join "`n").Trim()
}

function Get-RecoveryReferences($Context, [string]$User, [string]$Database) {
  # Only these states promise that object bytes exist. In-flight/deleted/quarantined rows remain in the dump for reconciliation.
  $query = "SELECT COALESCE(json_agg(x ORDER BY x.id), '[]'::json)::text FROM (SELECT id::text, storage_bucket, storage_key, size_bytes, hash_sha256 FROM file_asset WHERE deleted_at IS NULL AND lifecycle_status IN ('active','orphan_recovered')) x"
  $json = Invoke-RecoverySQL $Context $User $Database $query
  return @($json | ConvertFrom-Json)
}

function Resolve-RecoveryChild([string]$Root, [string]$Relative, [string]$Type = 'Leaf') {
  if ([string]::IsNullOrWhiteSpace($Relative) -or [IO.Path]::IsPathRooted($Relative) -or $Relative -match ':' -or $Relative.Split([char[]]@('/', '\')) -contains '..') { throw 'Backup contains an unsafe path.' }
  $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd('/', '\')
  $candidate = [IO.Path]::GetFullPath((Join-Path $rootPath $Relative))
  if (-not $candidate.StartsWith($rootPath + [IO.Path]::DirectorySeparatorChar, (Get-RecoveryPathComparison))) { throw 'Backup path escapes its root.' }
  if (-not (Test-Path -LiteralPath $candidate -PathType $Type)) { throw "Backup path is missing: $Relative" }
  # 规范化路径前缀不足以防止符号链接越界，因此逐级拒绝重解析点。
  $cursor = Get-Item -LiteralPath $candidate -Force
  while ($cursor.FullName.Length -ge $rootPath.Length) {
    if ($cursor.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Backup paths must not contain symbolic links or reparse points.' }
    $cursor = if ($cursor -is [IO.FileInfo]) { $cursor.Directory } else { $cursor.Parent }
    if (-not $cursor) { break }
  }
  return $candidate
}

function Assert-RecoveryObjects([object[]]$References, [string]$Directory, [string]$Bucket) {
  foreach ($reference in $References) {
    if ($reference.storage_bucket -cne $Bucket) { throw 'A database file reference points outside the backed-up bucket.' }
    $path = Resolve-RecoveryChild $Directory ([string]$reference.storage_key)
    if ((Get-Item -LiteralPath $path).Length -ne [long]$reference.size_bytes) { throw "Referenced object size mismatch: $($reference.id)" }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne ([string]$reference.hash_sha256).ToLowerInvariant()) { throw "Referenced object hash mismatch: $($reference.id)" }
  }
}

function Get-RecoveryBucketRemapSQL([string]$SourceBucket, [string]$TargetBucket) {
  foreach ($bucket in @($SourceBucket, $TargetBucket)) {
    if ($bucket -notmatch '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$') { throw 'Invalid bucket for database remapping.' }
  }
  # DDL and UPDATE share one transaction. Only the immutable bank-asset guard is suspended;
  # every other constraint remains active, and rollback restores both metadata and trigger mode.
  return @"
BEGIN;
LOCK TABLE file_asset IN ACCESS EXCLUSIVE MODE;
DO `$remap`$
DECLARE guard_mode "char";
BEGIN
  SELECT tgenabled INTO guard_mode FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='question_bank_file_guard' AND NOT tgisinternal;
  IF guard_mode IS NOT NULL THEN ALTER TABLE file_asset DISABLE TRIGGER question_bank_file_guard; END IF;
  UPDATE file_asset SET storage_bucket='$TargetBucket' WHERE storage_bucket='$SourceBucket';
  IF guard_mode='O' THEN ALTER TABLE file_asset ENABLE TRIGGER question_bank_file_guard;
  ELSIF guard_mode='A' THEN ALTER TABLE file_asset ENABLE ALWAYS TRIGGER question_bank_file_guard;
  ELSIF guard_mode='R' THEN ALTER TABLE file_asset ENABLE REPLICA TRIGGER question_bank_file_guard;
  END IF;
END;
`$remap`$;
COMMIT;
"@
}

function Assert-RecoveryReferenceSet([object[]]$Expected, [object[]]$Actual, [string]$TargetBucket) {
  if ($Expected.Count -ne $Actual.Count) { throw 'Restored database file references differ from the backup.' }
  $byId = @{}
  foreach ($item in $Actual) {
    if ($byId.ContainsKey([string]$item.id)) { throw 'Duplicate restored file reference.' }
    $byId[[string]$item.id] = $item
  }
  foreach ($item in $Expected) {
    $restored = $byId[[string]$item.id]
    if (-not $restored -or $restored.storage_bucket -cne $TargetBucket -or $restored.storage_key -cne $item.storage_key -or [long]$restored.size_bytes -ne [long]$item.size_bytes -or $restored.hash_sha256 -cne $item.hash_sha256) { throw 'Restored database file references differ from the backup.' }
  }
}

function Assert-RestoredRecoveryObjects($Context, [string]$User, [string]$Database, [string]$Bucket, [object[]]$Expected) {
  $actual = @(Get-RecoveryReferences $Context $User $Database)
  Assert-RecoveryReferenceSet $Expected $actual $Bucket
  $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('/', '\')
  $temp = Join-Path $tempRoot ('edugrade-object-verification-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $temp | Out-Null
  try {
    $script = 'scheme=http; if [ "$EDUGRADE_MINIO_USE_SSL" = "true" ]; then scheme=https; fi; mc alias set edugrade "$scheme://minio:9000" "$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD" >/dev/null; mc mirror "edugrade/{0}" /verification' -f $Bucket
    Invoke-RecoveryCompose $Context @('--profile', 'tools', 'run', '--rm', '-v', "${temp}:/verification", '--entrypoint', '/bin/sh', 'minio-init', '-ec', $script) | Out-Null
    Assert-RecoveryObjects $actual $temp $Bucket
  } finally {
    $resolved = [IO.Path]::GetFullPath($temp)
    if (-not (Test-RecoveryPathEqual (Split-Path -Parent $resolved) $tempRoot) -or (Split-Path -Leaf $resolved) -notmatch '^edugrade-object-verification-[a-f0-9]{32}$') { throw 'Unsafe verification cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}
