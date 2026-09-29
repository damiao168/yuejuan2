param([string]$PostgresTestURL = $env:EDUGRADE_RECOVERY_TEST_DATABASE_URL)
$ErrorActionPreference = 'Stop'
$scriptDirectory = Split-Path -Parent $PSScriptRoot
. (Join-Path $scriptDirectory 'recovery-common.ps1')
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('edugrade-recovery-tests-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$composeFile = (Resolve-Path -LiteralPath (Join-Path $scriptDirectory '../docker-compose.yml')).Path
$envFile = Join-Path $testRoot 'test.env'
@('EDUGRADE_ENV=test', 'EDUGRADE_POSTGRES_USER=fixture', 'EDUGRADE_POSTGRES_DB=source', 'EDUGRADE_FILE_BUCKET=source-bucket') | Set-Content -LiteralPath $envFile
$script:passed = 0
function Assert([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Expect-Failure([scriptblock]$Action, [string]$Pattern) {
  $failure = $null
  try { & $Action | Out-Null } catch { $failure = $_.Exception.Message }
  Assert ($failure -and $failure -match $Pattern) "Expected failure matching '$Pattern', got '$failure'."
}
function Test-Case([string]$Name, [scriptblock]$Action) {
  Reset-Mock
  & $Action
  $script:passed++
  Write-Host "PASS $Name"
}
function Reset-Mock {
  $global:recoveryMock = @{
    Calls = [Collections.Generic.List[object]]::new()
    Running = @('pg-id', 'minio-id', 'api-id', 'worker-id', 'future-id')
    InitialWriters = @('api-id', 'worker-id', 'future-id')
    Failure = ''; Remapped = $false; TargetBucket = ''
    Bytes = [Text.Encoding]::UTF8.GetBytes('fixture-object-bytes')
    SourceReference = $null
  }
  $hash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($global:recoveryMock.Bytes)).ToLowerInvariant()
  $global:recoveryMock.SourceReference = @{ id = 'fixture-asset'; storage_bucket = 'source-bucket'; storage_key = 'tenant/file.dat'; size_bytes = $global:recoveryMock.Bytes.Length; hash_sha256 = $hash }
}
function New-BackupFixture {
  $root = Join-Path $testRoot ([guid]::NewGuid().ToString('N'))
  $objects = Join-Path $root 'objects'
  New-Item -ItemType Directory -Path (Join-Path $objects 'tenant') | Out-Null
  [IO.File]::WriteAllBytes((Join-Path $objects 'tenant/file.dat'), $global:recoveryMock.Bytes)
  Set-Content -LiteralPath (Join-Path $root 'database.dump') -Value 'synthetic dump'
  @{ scope = 'active-and-orphan-recovered'; objects = @($global:recoveryMock.SourceReference) } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $root 'references.json')
  $files = @(Get-ChildItem -LiteralPath $root -File -Recurse | ForEach-Object { @{ path = $_.FullName.Substring($root.Length + 1); bytes = $_.Length; sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant() } })
  @{ format_version = 3; created_at = '2026-09-26T00:00:00Z'; postgres_file = 'database.dump'; minio_directory = 'objects'; references_file = 'references.json'; source_bucket = 'source-bucket'; maintenance_boundary = 'compose-writers-stopped'; files = $files } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $root 'manifest-fixture.json')
  return $root
}
function Invoke-TestRestore([string]$Root, [hashtable]$Overrides = @{}) {
  $parameters = @{ PostgresDump = (Join-Path $Root 'database.dump'); TargetDatabase = 'isolated_fixture'; TargetBucket = 'target-bucket'; ComposeFile = $composeFile; EnvFile = $envFile; ConfirmRestore = $true; CreateTargetDatabase = $true }
  foreach ($key in $Overrides.Keys) { $parameters[$key] = $Overrides[$key] }
  & (Join-Path $scriptDirectory 'restore.ps1') @parameters
}
function Assert-WritersRestored {
  $starts = @($global:recoveryMock.Calls | Where-Object { $_[0] -eq 'start' })
  Assert ($starts.Count -eq 1) 'Expected one restart of the original writers.'
  Assert (($starts[0][1..($starts[0].Count - 1)] -join ',') -eq ($global:recoveryMock.InitialWriters -join ',')) 'Restart must include exactly the original running writer IDs.'
}

# Every Docker call is intercepted. This test never starts or contacts a Docker daemon.
function global:docker {
  $arguments = @($args)
  $global:recoveryMock.Calls.Add($arguments)
  $global:LASTEXITCODE = 0
  $command = $arguments -join ' '
  if ($arguments[0] -eq 'stop') {
    $global:recoveryMock.Running = @('pg-id', 'minio-id')
    if ($global:recoveryMock.Failure -eq 'stop') { $global:LASTEXITCODE = 1 }
    return
  }
  if ($arguments[0] -eq 'start') { $global:recoveryMock.Running = @('pg-id', 'minio-id') + $global:recoveryMock.InitialWriters; return }
  if ($arguments[0] -eq 'inspect') {
    if ($command -match 'com.docker.compose.service') {
      return @{ 'pg-id' = 'postgres'; 'minio-id' = 'minio'; 'api-id' = 'api-gateway'; 'worker-id' = 'page-processing-worker'; 'future-id' = 'future-writer' }[$arguments[-1]]
    }
    if ($command -match 'json .Config.Labels') { return '{"com.docker.compose.project":"fixture"}' }
    return '/fixture|fixture-image|sha256:fixture'
  }
  if ($command -match ' ps --status running -q$') { return $global:recoveryMock.Running }
  if ($command -match ' ps -q postgres$') { return 'pg-id' }
  if ($command -match ' ps -q$') { return $global:recoveryMock.Running }
  if ($command -match 'printenv POSTGRES_USER$') { return 'fixture' }
  if ($command -match 'printenv POSTGRES_DB$') { return 'source' }
  if ($command -match 'pg_dump') { if ($global:recoveryMock.Failure -eq 'dump') { $global:LASTEXITCODE = 1 }; return }
  if ($arguments[0] -eq 'cp') { if ($arguments[1] -like 'pg-id:*') { Set-Content -LiteralPath $arguments[2] -Value 'synthetic dump' }; return }
  if ($command -match 'pg_restore --list') {
    if ($global:recoveryMock.Failure -eq 'dump-list') { $global:LASTEXITCODE = 1 }
    return
  }
  if ($command -match 'pg_restore|dropdb| rm -f ') { return }
  if ($command -match 'psql') {
    $query = $arguments[-1]
    if ($query -match "UPDATE file_asset SET storage_bucket='([^']+)'") {
      $global:recoveryMock.Remapped = $true
      $global:recoveryMock.TargetBucket = $Matches[1]
      return 'COMMIT'
    }
    if ($query -match 'json_agg') {
      $reference = $global:recoveryMock.SourceReference.Clone()
      if ($global:recoveryMock.Remapped) { $reference.storage_bucket = $global:recoveryMock.TargetBucket }
      if ($global:recoveryMock.Failure -eq 'reference') { $reference.storage_key = 'tenant/other.dat' }
      return ConvertTo-Json -InputObject @($reference) -Compress
    }
    return '1'
  }
  if ($command -match 'mc mirror') {
    if ($global:recoveryMock.Failure -eq 'mirror') { $global:LASTEXITCODE = 1; return }
    $volume = $arguments[[Array]::IndexOf($arguments, '-v') + 1]
    $destination = $null
    if ($volume -match '^(.*):/verification$') { $destination = $Matches[1] }
    elseif ($volume -match '^(.*):/backup$') {
      $mount = $Matches[1]
      if ($arguments[-1] -match '"/backup/(minio-[^"]+)"') { $destination = Join-Path $mount $Matches[1] }
    }
    if ($destination) {
      New-Item -ItemType Directory -Force -Path (Join-Path $destination 'tenant') | Out-Null
      $bytes = $global:recoveryMock.Bytes
      if ($global:recoveryMock.Failure -eq 'object') { $bytes = [Text.Encoding]::UTF8.GetBytes('fixture-object-bytex') }
      [IO.File]::WriteAllBytes((Join-Path $destination 'tenant/file.dat'), $bytes)
    }
    return
  }
  throw "Unexpected mocked Docker operation: $command"
}

try {
  Test-Case 'path equality follows host filesystem case conventions' {
    $ignoreCase = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    Assert ((Test-RecoveryPathEqual (Join-Path $testRoot 'Object') (Join-Path $testRoot 'object')) -eq $ignoreCase) 'Path comparison must be case-sensitive outside Windows.'
    Assert (Test-RecoveryPathEqual (Join-Path $testRoot 'objects/') (Join-Path $testRoot 'objects')) 'Trailing directory separators must not change path identity.'
  }
  Test-Case 'valid paired backup verifies references' {
    $fixture = New-BackupFixture
    $result = & (Join-Path $scriptDirectory 'verify-backup.ps1') -BackupDirectory $fixture | ConvertFrom-Json
    Assert ($result.valid -and $result.reference_count -eq 1) 'Expected valid paired evidence.'
  }
  Test-Case 'unlisted dump rejected before any Docker action' {
    $fixture = New-BackupFixture
    $alternate = Join-Path $fixture 'other.dump'
    Set-Content -LiteralPath $alternate -Value 'other'
    Expect-Failure { Invoke-TestRestore $fixture @{ PostgresDump = $alternate } } 'not covered|not the dump'
    Assert ($global:recoveryMock.Calls.Count -eq 0) 'No Docker call is allowed for invalid inputs.'
  }
  Test-Case 'even a listed alternate dump is not selectable' {
    $fixture = New-BackupFixture
    $alternate = Join-Path $fixture 'other.dump'
    Set-Content -LiteralPath $alternate -Value 'other'
    $manifestPath = Join-Path $fixture 'manifest-fixture.json'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $manifest.files += @{ path = 'other.dump'; bytes = (Get-Item $alternate).Length; sha256 = (Get-FileHash $alternate).Hash }
    $manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath
    Expect-Failure { Invoke-TestRestore $fixture @{ PostgresDump = $alternate } } 'not the dump'
    Assert ($global:recoveryMock.Calls.Count -eq 0) 'No Docker call is allowed for mismatched inputs.'
  }
  if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    Test-Case 'case-distinct listed dump cannot replace manifest dump on Linux' {
      $fixture = New-BackupFixture
      $alternate = Join-Path $fixture 'DATABASE.dump'
      Set-Content -LiteralPath $alternate -Value 'alternate dump'
      $manifestPath = Join-Path $fixture 'manifest-fixture.json'
      $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
      $manifest.files += @{ path = 'DATABASE.dump'; bytes = (Get-Item $alternate).Length; sha256 = (Get-FileHash $alternate).Hash }
      $manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath
      Expect-Failure { Invoke-TestRestore $fixture @{ PostgresDump = $alternate } } 'not the dump'
      Assert ($global:recoveryMock.Calls.Count -eq 0) 'Case-distinct dump must fail before Docker.'
    }
  }
  Test-Case 'missing or unrelated MinIO path rejected before mutation' {
    $fixture = New-BackupFixture
    Expect-Failure { Invoke-TestRestore $fixture @{ MinioBackupDirectory = (Join-Path $fixture 'missing') } } 'does not exist|Cannot find'
    Expect-Failure { Invoke-TestRestore $fixture @{ MinioBackupDirectory = $testRoot } } 'not the directory'
    Assert ($global:recoveryMock.Calls.Count -eq 0) 'Missing/mismatched objects must be detected before stopping writers.'
  }
  Test-Case 'manifest-covered object corruptions fail before Docker' {
    $fixture = New-BackupFixture
    [IO.File]::WriteAllBytes((Join-Path $fixture 'objects/tenant/file.dat'), [Text.Encoding]::UTF8.GetBytes('fixture-object-bytex'))
    Expect-Failure { Invoke-TestRestore $fixture } 'hash mismatch'
    Assert ($global:recoveryMock.Calls.Count -eq 0) 'Corrupt input must not reach Docker.'
  }
  Test-Case 'backup stops all writers including future services before dump and mirror' {
    $output = Join-Path $testRoot 'successful-backup'
    & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composeFile -EnvFile $envFile -OutputDir $output
    $calls = @($global:recoveryMock.Calls | ForEach-Object { $_ -join ' ' })
    $stop = [Array]::FindIndex($calls, [Predicate[object]]{ param($x) $x -match '^stop ' })
    $dump = [Array]::FindIndex($calls, [Predicate[object]]{ param($x) $x -match 'pg_dump' })
    $mirror = [Array]::FindIndex($calls, [Predicate[object]]{ param($x) $x -match 'mc mirror' })
    Assert ($stop -ge 0 -and $stop -lt $dump -and $dump -lt $mirror) 'Stop must precede both snapshot operations.'
    Assert-WritersRestored
    $directory = (Get-ChildItem -LiteralPath $output -Directory)[0].FullName
    $result = & (Join-Path $scriptDirectory 'verify-backup.ps1') -BackupDirectory $directory | ConvertFrom-Json
    Assert $result.valid 'Generated backup must be verifiable.'
    $manifest = Get-Content -LiteralPath $result.manifest -Raw | ConvertFrom-Json
    Assert ($manifest.images.Count -eq 5) 'Image evidence must include infrastructure and stopped application writers.'
    foreach ($id in $global:recoveryMock.InitialWriters) {
      Assert (@($calls | Where-Object { $_ -like '*{{.Config.Image}}*' -and $_.EndsWith(' ' + $id) }).Count -eq 1) "Missing image evidence for stopped writer $id."
    }
  }
  Test-Case 'backups in the same clock tick remain isolated' {
    function Get-Date([string]$Format) {
      $fixed = [DateTime]::Parse('2026-09-26T12:00:00Z').ToUniversalTime()
      if ($Format) { return $fixed.ToString($Format) }
      return $fixed
    }
    $output = Join-Path $testRoot 'same-time-backups'
    & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composeFile -EnvFile $envFile -OutputDir $output
    & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composeFile -EnvFile $envFile -OutputDir $output
    $directories = @(Get-ChildItem -LiteralPath $output -Directory)
    Assert ($directories.Count -eq 2) 'Two backups must never share an output directory.'
    foreach ($directory in $directories) {
      $verified = & (Join-Path $scriptDirectory 'verify-backup.ps1') -BackupDirectory $directory.FullName | ConvertFrom-Json
      Assert $verified.valid 'Every concurrent-timestamp backup must verify independently.'
    }
  }
  foreach ($failure in @('stop', 'dump', 'mirror', 'object')) {
    Test-Case "backup restarts original writers after $failure failure" {
      $global:recoveryMock.Failure = $failure
      Expect-Failure { & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composeFile -EnvFile $envFile -OutputDir (Join-Path $testRoot "failed-$failure") } 'failed|mismatch|Unable'
      Assert-WritersRestored
    }
  }
  Test-Case 'restore remaps DB bucket and validates downloaded bytes while frozen' {
    $fixture = New-BackupFixture
    Invoke-TestRestore $fixture
    Assert $global:recoveryMock.Remapped 'Expected the database bucket remap.'
    $calls = @($global:recoveryMock.Calls | ForEach-Object { $_ -join ' ' })
    Assert (@($calls | Where-Object { $_ -match ':/verification' }).Count -eq 1) 'Actual target object bytes must be downloaded.'
    Assert (($calls[-1]) -match '^start ') 'Writers must only resume after reference verification.'
    Assert-WritersRestored
  }
  foreach ($failure in @('reference', 'object')) {
    Test-Case "restore fails on actual target $failure mismatch" {
      $fixture = New-BackupFixture
      $global:recoveryMock.Failure = $failure
      Expect-Failure { Invoke-TestRestore $fixture } 'differ|mismatch'
      Assert-WritersRestored
    }
  }
  Test-Case 'restore stop failure resumes original writers before data mutation' {
    $fixture = New-BackupFixture
    $global:recoveryMock.Failure = 'stop'
    Expect-Failure { Invoke-TestRestore $fixture } 'Unable'
    Assert-WritersRestored
    Assert (@($global:recoveryMock.Calls | Where-Object { ($_ -join ' ') -match 'pg_restore|dropdb' }).Count -eq 0) 'No data operation may run after a failed maintenance boundary.'
  }
  foreach ($primary in @('database', 'bucket')) {
    Test-Case "successful primary $primary restore resumes original writers" {
      $fixture = New-BackupFixture
      $overrides = if ($primary -eq 'database') { @{ TargetDatabase = 'source'; AllowPrimaryDatabase = $true } } else { @{ TargetBucket = 'source-bucket'; AllowPrimaryBucket = $true } }
      Invoke-TestRestore $fixture $overrides
      Assert-WritersRestored
    }
    Test-Case "primary $primary failure before data mutation resumes original writers" {
      $fixture = New-BackupFixture
      $global:recoveryMock.Failure = 'dump-list'
      $overrides = if ($primary -eq 'database') { @{ TargetDatabase = 'source'; AllowPrimaryDatabase = $true } } else { @{ TargetBucket = 'source-bucket'; AllowPrimaryBucket = $true } }
      Expect-Failure { Invoke-TestRestore $fixture $overrides } 'dump validation failed'
      Assert-WritersRestored
      Assert (@($global:recoveryMock.Calls | Where-Object { ($_ -join ' ') -match 'dropdb|pg_restore -U|mc mirror|UPDATE file_asset' }).Count -eq 0) 'No target data change may occur after dump validation failure.'
    }
    Test-Case "failed partial primary $primary restore keeps writers stopped" {
      $fixture = New-BackupFixture
      $global:recoveryMock.Failure = 'object'
      $overrides = if ($primary -eq 'database') { @{ TargetDatabase = 'source'; AllowPrimaryDatabase = $true } } else { @{ TargetBucket = 'source-bucket'; AllowPrimaryBucket = $true } }
      Expect-Failure { Invoke-TestRestore $fixture $overrides } 'differ|mismatch'
      Assert (@($global:recoveryMock.Calls | Where-Object { $_[0] -eq 'start' }).Count -eq 0) 'A failed restore after primary data mutation must not resume writers.'
      Assert (@($global:recoveryMock.Running | Where-Object { $_ -in $global:recoveryMock.InitialWriters }).Count -eq 0) 'All original writers must remain stopped after primary restoration failure.'
    }
  }
} finally {
  Remove-Item Function:\docker -ErrorAction SilentlyContinue
  $resolved = [IO.Path]::GetFullPath($testRoot)
  if (-not (Test-RecoveryPathEqual (Split-Path -Parent $resolved) ([IO.Path]::GetFullPath([IO.Path]::GetTempPath()))) -or (Split-Path -Leaf $resolved) -notmatch '^edugrade-recovery-tests-[a-f0-9]{32}$') { throw 'Unsafe test cleanup path.' }
  Remove-Item -LiteralPath $resolved -Recurse -Force
}

if ($PostgresTestURL) {
  & (Join-Path $PSScriptRoot 'recovery-postgres.tests.ps1') -PostgresTestURL $PostgresTestURL
}
Write-Host "$script:passed mocked recovery tests passed."
