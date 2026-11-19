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

function Get-RecoveryContainerEnvironment([string]$ContainerId) {
  $envJSON = (& docker inspect --format '{{json .Config.Env}}' $ContainerId) -join "`n"
  if ($LASTEXITCODE -ne 0 -or -not $envJSON) { throw 'Could not verify the deployed container environment.' }
  $values = @{}
  foreach ($line in (ConvertFrom-Json -InputObject $envJSON)) {
    $pair = $line.Split('=', 2)
    if ($pair.Count -eq 2) { $values[$pair[0]] = $pair[1] }
  }
  return $values
}

function Test-RecoveryContainerFile([string]$ContainerId, [string]$FilePath, [scriptblock]$Check, [switch]$OptionalFromWorkingDirectory) {
  if (-not $ContainerId -or (-not $FilePath.StartsWith('/') -and -not $OptionalFromWorkingDirectory)) { return $false }
  $directory = Join-Path ([IO.Path]::GetTempPath()) ('edugrade-restore-identity-' + [guid]::NewGuid().ToString('N'))
  [IO.Directory]::CreateDirectory($directory) | Out-Null
  $copy = Join-Path $directory 'credential'
  try {
    # 凭据只进入受当前账号保护的临时目录，不写入日志。
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
      $acl = [Security.AccessControl.DirectorySecurity]::new()
      $acl.SetAccessRuleProtection($true, $false)
      $inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
      $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
      $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', $inherit, 'None', 'Allow'))
      Set-Acl -LiteralPath $directory -AclObject $acl
    } else {
      & chmod 700 $directory
      if ($LASTEXITCODE -ne 0) { return $false }
    }
    # MinIO 镜像允许工作目录中不存在的凭据文件，此时才使用非文件凭据。
    if ($OptionalFromWorkingDirectory) {
      & docker exec $ContainerId /bin/sh -ec 'if [ -e "$1" ] || [ -L "$1" ]; then exit 10; fi' -- $FilePath 2>$null | Out-Null
      if ($LASTEXITCODE -eq 0) { return $true }
      if ($LASTEXITCODE -ne 10) { return $false }
      if (-not $FilePath.StartsWith('/')) {
        $containerWorkDir = & docker inspect --format '{{.Config.WorkingDir}}' $ContainerId
        if ($LASTEXITCODE -ne 0 -or -not $containerWorkDir) { return $false }
        $FilePath = $containerWorkDir.TrimEnd('/') + '/' + $FilePath
      }
    }
    # 停止容器无法 exec，此时仍可复核其实际挂载内容，且脚本不会启动原先停止的容器。
    $running = & docker inspect --format '{{.State.Running}}' $ContainerId
    if ($LASTEXITCODE -ne 0 -or $running -cnotmatch '^(true|false)$') { return $false }
    if ($running -ceq 'true') {
      & docker exec $ContainerId /bin/sh -ec 'test -f "$1" && test -r "$1"' -- $FilePath 2>$null | Out-Null
      if ($LASTEXITCODE -ne 0) { return $false }
    }
    & docker cp -L "${ContainerId}:$FilePath" $copy 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0 -or -not [IO.File]::Exists($copy)) { return $false }
    $value = [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($copy)).TrimEnd("`r", "`n")
    return (& $Check $value)
  } finally {
    if ([IO.File]::Exists($copy)) { Remove-Item -LiteralPath $copy -Force }
    Remove-Item -LiteralPath $directory -Force
  }
}

function Test-RecoveryMinioApplicationIdentity($Gateway, $Tools, $ActiveGateway, [string]$ContainerId) {
  if (-not $ActiveGateway) { $ActiveGateway = $Gateway }
  # 运维账号能读回对象并不能证明应用权限。凭据相同时要在写入前拒绝。
  if ($Tools.EDUGRADE_MINIO_APP_ACCESS_KEY -ceq $Tools.EDUGRADE_MINIO_ROOT_USER) { return $false }
  foreach ($key in @('EDUGRADE_MINIO_APP_ACCESS_KEY', 'EDUGRADE_MINIO_APP_SECRET_KEY')) {
    $expected = [string]$Tools.$key
    if (-not $expected) { return $false }
    $fileKey = $key + '_FILE'
    $configuredPath = ([string]$Gateway.$fileKey).Trim()
    $deployedPath = ([string]$ActiveGateway.$fileKey).Trim()
    if ($configuredPath -cne $deployedPath) { return $false }
    if ($configuredPath) {
      # 与网关的配置规则一致，文件选项不能同时指定明文，文件值仅忽略结尾换行。
      if (-not [string]::IsNullOrWhiteSpace($Gateway.$key) -or -not [string]::IsNullOrWhiteSpace($ActiveGateway.$key) -or
          -not (Test-RecoveryContainerFile $ContainerId $deployedPath { param($value) $value -ceq $expected })) { return $false }
    } elseif ($Gateway.$key -cne $expected -or $ActiveGateway.$key -cne $expected) {
      return $false
    }
  }
  return $true
}

function Test-RecoveryMinioEndpoint($Gateway, $Tools) {
  # 恢复工具固定访问此 Compose 的 minio:9000。必须先确认网关也读同一服务和协议。
  $endpoint = [string]$Gateway.EDUGRADE_MINIO_ENDPOINT
  $gatewayMode = ([string]$Gateway.EDUGRADE_MINIO_USE_SSL).Trim()
  if ($gatewayMode -cnotmatch '^(|0|1|t|T|f|F|true|TRUE|True|false|FALSE|False)$') { return $false }
  $gatewayTLS = $gatewayMode -cmatch '^(1|t|T|true|TRUE|True)$'
  return ($endpoint -ieq 'minio:9000' -and $gatewayTLS -eq ($Tools.EDUGRADE_MINIO_USE_SSL -ceq 'true'))
}

function Test-RecoveryPostgresTarget([string]$DSN, [string]$Database) {
  $uri = $null
  if (-not [Uri]::TryCreate($DSN, [UriKind]::Absolute, [ref]$uri)) { return $false }
  # 使用项目 Compose 的 URL 配置。库名和服务名要从网关实际连接核对，不能只看初始化环境。
  if ($uri.Scheme -notin @('postgres', 'postgresql') -or $uri.Host -ine 'postgres' -or $uri.Port -notin @(-1, 5432) -or
      -not $uri.AbsolutePath.StartsWith('/') -or [Uri]::UnescapeDataString($uri.AbsolutePath.Substring(1)) -cne $Database) { return $false }
  foreach ($option in $uri.Query.TrimStart('?').Split('&')) {
    if ([Uri]::UnescapeDataString($option.Split('=',2)[0]) -in @('host', 'hostaddr', 'port', 'dbname', 'database')) { return $false }
  }
  return $true
}

function Get-RecoveryDeploymentSettings($Context) {
  # 读取 Compose 解析后的目标，避免 .env 的值与宿主机覆盖或插值后的目标不一致。
  $dockerOutput = (Invoke-RecoveryCompose $Context @('--profile', 'tools', 'config', '--format', 'json')) -join "`n"
  $services = (ConvertFrom-Json -InputObject $dockerOutput).services
  $api = $services.'api-gateway'.environment
  $tools = $services.'minio-init'.environment
  $settings = @{ Database = $services.postgres.environment.POSTGRES_DB; User = $services.postgres.environment.POSTGRES_USER; Environment = $api.EDUGRADE_ENV; Bucket = $api.EDUGRADE_FILE_BUCKET }
  if (-not $settings.Database -or -not $settings.User -or -not $settings.Environment -or -not $settings.Bucket -or $settings.Bucket -cne $tools.EDUGRADE_FILE_BUCKET) { throw 'Could not determine matching PostgreSQL and MinIO targets from the Compose configuration.' }
  $postgresIds = @(Invoke-RecoveryCompose $Context @('ps', '-q', 'postgres') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  if ($postgresIds.Count -ne 1) { throw 'Expected exactly one running PostgreSQL container for this Compose deployment.' }
  $settings.PostgresContainer = $postgresIds[0].Trim()
  $postgres = Get-RecoveryContainerEnvironment $settings.PostgresContainer
  if ($postgres.POSTGRES_DB -cne $settings.Database -or $postgres.POSTGRES_USER -cne $settings.User) {
    throw 'Restore configuration does not match the deployed PostgreSQL database or administrator.'
  }
  # docker start 沿用现有容器配置。校验当前配置和旧容器，避免覆盖实际主桶或核对了尚未生效的账号。
  $gatewayIds = @(Invoke-RecoveryCompose $Context @('ps', '--all', '-q', 'api-gateway') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  $dsnPath = ([string]$api.EDUGRADE_POSTGRES_DSN_FILE).Trim()
  $validConfiguredDSN = if ($dsnPath) {
    $gatewayIds.Count -gt 0 -and [string]::IsNullOrWhiteSpace($api.EDUGRADE_POSTGRES_DSN)
  } else { Test-RecoveryPostgresTarget $api.EDUGRADE_POSTGRES_DSN $settings.Database }
  if (-not $validConfiguredDSN) { throw 'Restore configuration does not match the configured gateway PostgreSQL target.' }

  $settings.ApplicationIdentityMatches = $true
  $settings.ApplicationAccessKey = [string]$tools.EDUGRADE_MINIO_APP_ACCESS_KEY
  $settings.UseMinioTLS = $tools.EDUGRADE_MINIO_USE_SSL -ceq 'true'
  $settings.ApplicationEndpointMatches = Test-RecoveryMinioEndpoint $api $tools
  if ($gatewayIds.Count -eq 0) {
    $settings.ApplicationIdentityMatches = Test-RecoveryMinioApplicationIdentity $api $tools
  }
  foreach ($id in $gatewayIds) {
    $active = Get-RecoveryContainerEnvironment $id.Trim()
    if ($active.EDUGRADE_FILE_BUCKET -cne $settings.Bucket -or $active.EDUGRADE_ENV -cne $settings.Environment) { throw 'Restore configuration does not match the deployed gateway bucket or environment.' }
    $activeDsnPath = ([string]$active.EDUGRADE_POSTGRES_DSN_FILE).Trim()
    $validActiveDSN = if ($dsnPath) {
      [string]::IsNullOrWhiteSpace($active.EDUGRADE_POSTGRES_DSN) -and
        (Test-RecoveryContainerFile $id.Trim() $activeDsnPath { param($value) Test-RecoveryPostgresTarget $value $settings.Database })
    } else { Test-RecoveryPostgresTarget $active.EDUGRADE_POSTGRES_DSN $settings.Database }
    if ($activeDsnPath -cne $dsnPath -or -not $validActiveDSN) {
      throw 'Restore configuration does not match the deployed gateway PostgreSQL target.'
    }
    if (-not (Test-RecoveryMinioEndpoint $active $tools)) { $settings.ApplicationEndpointMatches = $false }
    if (-not (Test-RecoveryMinioApplicationIdentity $api $tools $active $id.Trim())) { $settings.ApplicationIdentityMatches = $false }
  }
  return $settings
}

function Assert-RecoveryMinioApplicationScope($Context, [string]$AccessKey, [bool]$UseTLS) {
  $minioIds = @(Invoke-RecoveryCompose $Context @('ps', '-q', 'minio') | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
  if ($minioIds.Count -ne 1) { throw 'Expected exactly one running MinIO container for this Compose deployment.' }
  $minioId = $minioIds[0].Trim()
  $active = Get-RecoveryContainerEnvironment $minioId
  # 镜像默认声明了文件名，文件存在时还要核对文件账号，避免把运维凭据误当应用凭据。
  $rootFile = [string]$active.MINIO_ROOT_USER_FILE
  if (-not $active.MINIO_ROOT_USER -or $active.MINIO_ROOT_USER -ceq $AccessKey -or
      ($rootFile -and -not (Test-RecoveryContainerFile $minioId $rootFile { param($value) $value -and $value -cne $AccessKey } -OptionalFromWorkingDirectory))) {
    throw 'Primary restore cannot verify MinIO using a root or unknown deployed application identity.'
  }
  if (($active.EDUGRADE_MINIO_USE_SSL -ceq 'true') -ne $UseTLS) { throw 'Primary restore TLS mode does not match the deployed MinIO configuration.' }
}

function Get-RecoveryWriters($Context) {
  $writers = @()
  foreach ($id in @(Invoke-RecoveryCompose $Context @('ps', '--status', 'running', '-q'))) {
    if ([string]::IsNullOrWhiteSpace($id)) { continue }
    $service = (& docker inspect --format '{{index .Config.Labels "com.docker.compose.service"}}' $id).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $service) { throw 'Cannot identify a running Compose container; refusing an inconsistent backup.' }
    # 未知或新增服务默认视为写入方；下列基础设施服务存储数据，但不主动发起应用写入。
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
  # 只有这些状态表示对象字节已存在；进行中、已删除和隔离的记录仍保留在导出文件中，供后续核对。
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

function Get-RecoveryBucketRemapSQL([string]$SourceBucket, [string]$TargetBucket, [object[]]$VerifiedReferences) {
  foreach ($bucket in @($SourceBucket, $TargetBucket)) {
    if ($bucket -notmatch '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$') { throw 'Invalid bucket for database remapping.' }
  }
  if ($SourceBucket -ceq $TargetBucket) { return 'SELECT 1;' }
  $referencesJson = ConvertTo-Json -InputObject @($VerifiedReferences) -Depth 4 -Compress
  $referencesBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($referencesJson))
  # 在锁内核对已验证的文件引用，再只为这些对象临时暂停三种位置保护。对象键、内容与其他字段保持原值。
  # 其余未删除记录在恢复保护后再改桶。上传和删除重试都从记录读取桶名，恢复后只能对目标桶重试。
  return @"
BEGIN;
LOCK TABLE public.file_asset IN ACCESS EXCLUSIVE MODE;
DO `$remap`$
DECLARE
  guards JSONB;
  guard RECORD;
  references_json JSONB := convert_from(decode('$referencesBase64','base64'),'UTF8')::jsonb;
BEGIN
  CREATE TEMP TABLE recovery_verified_files ON COMMIT DROP AS
  SELECT id,storage_bucket,storage_key,size_bytes,hash_sha256
  FROM jsonb_to_recordset(references_json) AS f(id UUID,storage_bucket TEXT,storage_key TEXT,size_bytes BIGINT,hash_sha256 TEXT);
  ALTER TABLE pg_temp.recovery_verified_files ADD PRIMARY KEY (id);
  IF EXISTS (
    SELECT 1 FROM pg_temp.recovery_verified_files v
    FULL JOIN (SELECT id,storage_bucket,storage_key,size_bytes,hash_sha256 FROM public.file_asset
      WHERE deleted_at IS NULL AND lifecycle_status IN ('active','orphan_recovered')) f USING (id)
    WHERE v.id IS NULL OR f.id IS NULL OR v.storage_bucket IS DISTINCT FROM '$SourceBucket'
      OR (f.storage_bucket,f.storage_key,f.size_bytes,f.hash_sha256) IS DISTINCT FROM
         (v.storage_bucket,v.storage_key,v.size_bytes,v.hash_sha256)
  ) THEN RAISE EXCEPTION 'restored file references differ from verified backup'; END IF;

  SELECT COALESCE(jsonb_object_agg(tgname,tgenabled),'{}'::jsonb) INTO guards FROM pg_trigger
  WHERE tgrelid='public.file_asset'::regclass AND NOT tgisinternal
    AND tgname IN ('question_bank_file_guard','answer_sheet_library_reference_pinned','trg_reference_material_object_pin');
  FOR guard IN SELECT key,value FROM jsonb_each_text(guards) WHERE value<>'D' LOOP
    EXECUTE format('ALTER TABLE public.file_asset DISABLE TRIGGER %I',guard.key);
  END LOOP;
  UPDATE public.file_asset SET storage_bucket='$TargetBucket' WHERE id IN (SELECT id FROM pg_temp.recovery_verified_files);
  FOR guard IN SELECT key,value FROM jsonb_each_text(guards) LOOP
    CASE guard.value
      WHEN 'O' THEN EXECUTE format('ALTER TABLE public.file_asset ENABLE TRIGGER %I',guard.key);
      WHEN 'A' THEN EXECUTE format('ALTER TABLE public.file_asset ENABLE ALWAYS TRIGGER %I',guard.key);
      WHEN 'R' THEN EXECUTE format('ALTER TABLE public.file_asset ENABLE REPLICA TRIGGER %I',guard.key);
      ELSE NULL;
    END CASE;
  END LOOP;
  UPDATE public.file_asset SET storage_bucket='$TargetBucket'
  WHERE storage_bucket='$SourceBucket' AND deleted_at IS NULL AND lifecycle_status <> 'deleted';
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

function Assert-RestoredRecoveryObjects($Context, [string]$Bucket, [object[]]$References, [switch]$UseApplicationIdentity) {
  $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('/', '\')
  $temp = Join-Path $tempRoot ('edugrade-object-verification-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $temp | Out-Null
  try {
    $credentials = '"$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD"'
    if ($UseApplicationIdentity) {
      # 主桶必须能用部署所用的应用账号读取，避免管理账号的权限掩盖恢复后的访问错误。
      $credentials = '"${EDUGRADE_MINIO_APP_ACCESS_KEY:?Missing MinIO application access key}" "${EDUGRADE_MINIO_APP_SECRET_KEY:?Missing MinIO application secret key}"'
    }
    $script = 'scheme=http; if [ "$EDUGRADE_MINIO_USE_SSL" = "true" ]; then scheme=https; fi; mc alias set edugrade "$scheme://minio:9000" {0} >/dev/null; mc ls "edugrade/{1}" >/dev/null; mc mirror "edugrade/{1}" /verification' -f $credentials,$Bucket
    try {
      Invoke-RecoveryCompose $Context @('--profile', 'tools', 'run', '--rm', '-v', "${temp}:/verification", '--entrypoint', '/bin/sh', 'minio-init', '-ec', $script) | Out-Null
    } catch {
      if ($UseApplicationIdentity) { throw 'The MinIO application identity could not verify the restored bucket. Check the existing application credentials and bucket policy before restarting.' }
      throw
    }
    Assert-RecoveryObjects $References $temp $Bucket
  } finally {
    $resolved = [IO.Path]::GetFullPath($temp)
    if (-not (Test-RecoveryPathEqual (Split-Path -Parent $resolved) $tempRoot) -or (Split-Path -Leaf $resolved) -notmatch '^edugrade-object-verification-[a-f0-9]{32}$') { throw 'Unsafe verification cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse -Force
  }
}
