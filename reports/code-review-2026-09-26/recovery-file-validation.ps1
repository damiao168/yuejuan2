$ErrorActionPreference = 'Stop'
$repo = 'D:\project\yuejuan'
$fixture = Join-Path $env:TEMP ('edugrade-recovery-review-' + [guid]::NewGuid().ToString('N'))
$backup = New-Item -ItemType Directory -Path (Join-Path $fixture 'backup')
$minio = New-Item -ItemType Directory -Path (Join-Path $backup.FullName 'minio-good')
$otherMinio = New-Item -ItemType Directory -Path (Join-Path $fixture 'minio-from-another-backup')
$compose = Join-Path $fixture 'compose.yml'
$envFile = Join-Path $fixture 'fixture.env'
Set-Content -LiteralPath $compose -Value 'services: {}'
Set-Content -LiteralPath $envFile -Value @('EDUGRADE_ENV=review-fixture','EDUGRADE_POSTGRES_DB=source','EDUGRADE_POSTGRES_USER=fixture','EDUGRADE_FILE_BUCKET=source-bucket')
$goodDump = Join-Path $backup.FullName 'postgres-good.dump'
$otherDump = Join-Path $backup.FullName 'postgres-from-another-backup.dump'
Set-Content -LiteralPath $goodDump -Value 'synthetic manifest-covered dump; not a real database dump'
Set-Content -LiteralPath $otherDump -Value 'synthetic unlisted alternative dump; not a real database dump'
$goodObject = Join-Path $minio.FullName 'good-object.txt'
Set-Content -LiteralPath $goodObject -Value 'original content'
Set-Content -LiteralPath (Join-Path $otherMinio.FullName 'unrelated-object.txt') -Value 'unrelated content'
$entries = @($goodDump, $goodObject) | ForEach-Object {
  @{path=$_.Substring($backup.FullName.Length + 1);bytes=(Get-Item -LiteralPath $_).Length;sha256=(Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant()}
}
@{format_version=2;created_at='2026-09-26T00:00:00Z';postgres_file='postgres-good.dump';minio_directory='minio-good';files=@($entries)} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $backup.FullName 'manifest-fixture.json')

# This function intercepts EVERY Docker call; no subprocess, socket or deployment is used.
$global:recoveryCalls = [System.Collections.Generic.List[string]]::new()
function global:docker {
  $call = $args -join ' '
  $global:recoveryCalls.Add($call)
  $global:LASTEXITCODE = 0
  if ($call -match ' ps -q postgres$') { return 'fixture-container' }
  if ($call -match ' -Atc ') { return '1' }
  if ($call -match '^cp ' -or $call -match 'pg_restore' -or $call -match 'dropdb' -or $call -match ' rm -f ' -or $call -match 'mc mirror') { return }
  throw "Unexpected MOCK Docker call: $call"
}
$verify = & (Join-Path $repo 'infra/docker-compose/scripts/verify-backup.ps1') -BackupDirectory $backup.FullName | ConvertFrom-Json
& (Join-Path $repo 'infra/docker-compose/scripts/restore.ps1') -PostgresDump $otherDump -TargetDatabase 'isolated_fixture' -ComposeFile $compose -EnvFile $envFile -MinioBackupDirectory $otherMinio.FullName -TargetBucket 'fixture-target' -CreateTargetDatabase -ConfirmRestore
$alternateCalls = @($global:recoveryCalls)
$global:recoveryCalls.Clear()
$failure = ''
try {
  & (Join-Path $repo 'infra/docker-compose/scripts/restore.ps1') -PostgresDump $goodDump -TargetDatabase 'isolated_fixture' -ComposeFile $compose -EnvFile $envFile -MinioBackupDirectory (Join-Path $fixture 'directory-does-not-exist') -TargetBucket 'fixture-target' -CreateTargetDatabase -ConfirmRestore
} catch { $failure = $_.Exception.Message }
@{
  test_type='synthetic local file validation with all Docker calls mocked; no actual dump/restore tested'
  fixture=$fixture
  manifest_valid=$verify.valid
  unlisted_dump_selected=(@($alternateCalls | Where-Object { $_ -match '^cp .*postgres-from-another-backup.dump' }).Count -eq 1)
  unrelated_minio_selected=(@($alternateCalls | Where-Object { $_ -match 'mc mirror' -and $_ -match 'minio-from-another-backup' }).Count -eq 1)
  target_replacement_invoked_before_missing_minio_error=(@($global:recoveryCalls | Where-Object { $_ -match 'dropdb' }).Count -eq 1)
  missing_minio_error=$failure
  alternate_calls=$alternateCalls
} | ConvertTo-Json -Depth 5
