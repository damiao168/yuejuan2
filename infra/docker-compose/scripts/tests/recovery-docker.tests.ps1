param(
  [string]$PostgresImage = 'postgres:16-alpine',
  [string]$MinioImage = 'minio/minio:RELEASE.2025-04-22T22-12-26Z',
  [string]$MinioClientImage = 'minio/mc:RELEASE.2025-04-16T18-13-26Z'
)

# Opt-in real integration test. It uses synthetic data, no host ports, and a unique
# Compose project with its own volumes. Existing deployment containers are untouched.
$ErrorActionPreference = 'Stop'
$scriptDirectory = Split-Path -Parent $PSScriptRoot
. (Join-Path $scriptDirectory 'recovery-common.ps1')
$composeDirectory = (Resolve-Path -LiteralPath (Join-Path $scriptDirectory '..')).Path
$suffix = [guid]::NewGuid().ToString('N')
$project = 'edugrade-recovery-test-' + $suffix
$composePath = Join-Path $composeDirectory ('.recovery-test-' + $suffix + '.yml')
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('/', '\')
$work = Join-Path $tempRoot $project
$envPath = Join-Path $work 'test.env'
$context = @{ ComposePath = $composePath; EnvPath = $envPath }
$started = $false
$passed = 0
function Assert([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Pass([string]$Message) { $script:passed++; Write-Host "PASS Docker $Message" }
function Assert-WriterState {
  $running = @(Invoke-RecoveryCompose $context @('ps', '--status', 'running', '--services'))
  Assert ($running -contains 'api-gateway' -and $running -contains 'future-writer') 'The original writers must be running again.'
  Assert ($running -notcontains 'stopped-worker') 'The initially stopped worker must remain stopped.'
}
function Invoke-Minio([string]$Script) {
  $prefix = 'mc alias set edugrade http://minio:9000 "$EDUGRADE_MINIO_ROOT_USER" "$EDUGRADE_MINIO_ROOT_PASSWORD" >/dev/null; '
  Invoke-RecoveryCompose $context @('--profile', 'tools', 'run', '--rm', '-v', "${work}:/fixture:ro", '--entrypoint', '/bin/sh', 'minio-init', '-ec', ($prefix + $Script)) | Out-Null
}

New-Item -ItemType Directory -Path $work | Out-Null
try {
  @('EDUGRADE_ENV=test', 'EDUGRADE_POSTGRES_USER=fixture', 'EDUGRADE_POSTGRES_DB=source', 'EDUGRADE_FILE_BUCKET=source-bucket') | Set-Content -LiteralPath $envPath
  @"
name: $project
services:
  postgres:
    image: $PostgresImage
    environment:
      POSTGRES_USER: fixture
      POSTGRES_PASSWORD: synthetic-recovery-fixture
      POSTGRES_DB: source
    volumes: ["pgdata:/var/lib/postgresql/data"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U fixture -d source"]
      interval: 1s
      timeout: 3s
      retries: 60
  minio:
    image: $MinioImage
    command: ["server", "/data"]
    environment:
      MINIO_ROOT_USER: syntheticfixture
      MINIO_ROOT_PASSWORD: synthetic-recovery-fixture
    volumes: ["objects:/data"]
  minio-init:
    image: $MinioClientImage
    profiles: ["tools"]
    environment:
      EDUGRADE_MINIO_ROOT_USER: syntheticfixture
      EDUGRADE_MINIO_ROOT_PASSWORD: synthetic-recovery-fixture
      EDUGRADE_MINIO_USE_SSL: "false"
      EDUGRADE_FILE_BUCKET: source-bucket
  api-gateway: &writer
    image: $PostgresImage
    entrypoint: ["/bin/sh", "-ec", "trap 'exit 0' TERM INT; while :; do sleep 1 & wait; done"]
  future-writer: *writer
  stopped-worker: *writer
volumes:
  pgdata:
  objects:
"@ | Set-Content -LiteralPath $composePath
  $started = $true
  Invoke-RecoveryCompose $context @('up', '-d', '--wait', '--wait-timeout', '90', 'postgres', 'minio', 'api-gateway', 'future-writer', 'stopped-worker') | Out-Null
  Invoke-RecoveryCompose $context @('stop', 'stopped-worker') | Out-Null
  $object = Join-Path $work 'file.bin'
  [IO.File]::WriteAllBytes($object, [Text.Encoding]::UTF8.GetBytes('synthetic recovery reference bytes'))
  $hash = (Get-FileHash -LiteralPath $object -Algorithm SHA256).Hash.ToLowerInvariant()
  $size = (Get-Item -LiteralPath $object).Length
  Invoke-Minio 'mc ready edugrade; mc mb edugrade/source-bucket; mc cp /fixture/file.bin edugrade/source-bucket/tenant/active.bin; mc cp /fixture/file.bin edugrade/source-bucket/tenant/recovered.bin'
  $schema = @"
CREATE TABLE tenant (id uuid PRIMARY KEY);
CREATE TABLE school (id uuid PRIMARY KEY,tenant_id uuid);
CREATE TABLE exam (id uuid PRIMARY KEY,tenant_id uuid,school_id uuid);
CREATE TABLE submission (id uuid PRIMARY KEY,tenant_id uuid,exam_id uuid);
CREATE TABLE schema_migration (version text);
INSERT INTO schema_migration VALUES ('fixture');
INSERT INTO tenant VALUES ('00000000-0000-4000-8000-000000000010');
CREATE TABLE file_asset (id uuid PRIMARY KEY,tenant_id uuid,storage_bucket text,storage_key text,size_bytes bigint,hash_sha256 text,original_name text,content_type text,deleted_at timestamptz,lifecycle_status text);
CREATE TABLE question_bank_item_asset (tenant_id uuid,file_asset_id uuid REFERENCES file_asset(id));
INSERT INTO file_asset VALUES
 ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000010','source-bucket','tenant/active.bin',$size,'$hash','fixture','application/octet-stream',NULL,'active'),
 ('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000010','source-bucket','tenant/recovered.bin',$size,'$hash','fixture','application/octet-stream',NULL,'orphan_recovered'),
 ('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000010','source-bucket','tenant/pending.bin',$size,'$hash','fixture','application/octet-stream',NULL,'pending_upload');
INSERT INTO question_bank_item_asset SELECT tenant_id,id FROM file_asset WHERE lifecycle_status='active';
"@
  $migration = Get-Content -LiteralPath (Join-Path $PSScriptRoot '../../../../services/api-gateway/migrations/000141_question_bank_publication.sql') -Raw
  $guard = [regex]::Match($migration, '(?s)CREATE FUNCTION question_bank_file_guard\(\).*?CREATE TRIGGER question_bank_file_guard[^;]*;').Value
  Assert ([bool]$guard) 'Cannot locate the production immutable asset guard.'
  Invoke-RecoverySQL $context 'fixture' 'source' ($schema + "`n" + $guard) | Out-Null
  $output = Join-Path $work 'backups'
  & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composePath -EnvFile $envPath -OutputDir $output
  Assert-WriterState
  $backup = (Get-ChildItem -LiteralPath $output -Directory | Select-Object -First 1).FullName
  $verified = & (Join-Path $scriptDirectory 'verify-backup.ps1') -BackupDirectory $backup | ConvertFrom-Json
  $manifest = Get-Content -LiteralPath $verified.manifest -Raw | ConvertFrom-Json
  Assert ($verified.reference_count -eq 2 -and $manifest.stopped_containers.Count -eq 2) 'Backup must include both persisted objects and both original running writers.'
  Assert (@($manifest.images | Where-Object { $_.container -match 'api-gateway|future-writer' }).Count -eq 2) 'Manifest must retain stopped writer image evidence.'
  Pass 'backup freezes writers, produces verified paired data, and restores exact running set'

  & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'restore_fixture' -TargetBucket 'restored-bucket' -CreateTargetDatabase -ConfirmRestore
  Assert-WriterState
  Assert ((Invoke-RecoverySQL $context 'fixture' 'restore_fixture' "SELECT count(*) FROM file_asset WHERE storage_bucket='restored-bucket'") -eq '3') 'All references, including pending assets, must be remapped.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'restore_fixture' "SELECT tgenabled FROM pg_trigger WHERE tgname='question_bank_file_guard'") -eq 'O') 'Immutable guard must be re-enabled after remapping.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'source' "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'") -eq '3') 'Original database must be unchanged.'
  Pass 'restore remaps immutable bank assets and verifies downloaded target bytes and hashes'

  $drillOutput = @(& (Join-Path $scriptDirectory 'restore-drill.ps1') -ComposeFile $composePath -EnvFile $envPath -BackupDirectory $backup)
  $drill = ($drillOutput -join "`n" | ConvertFrom-Json)
  Assert ($drill.succeeded -and $drill.reference_bytes_and_hashes_verified -and $drill.verified_file_references -eq 2) 'Restore drill must verify actual referenced bytes.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'source' "SELECT count(*) FROM pg_database WHERE datname='$($drill.database)'") -eq '0') 'Drill database must be cleaned.'
  Assert-WriterState
  Pass 'restore drill verifies reference integrity and cleans isolated database and bucket'

  Invoke-Minio 'mc rm edugrade/source-bucket/tenant/active.bin'
  $failure = $null
  try { & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composePath -EnvFile $envPath -OutputDir (Join-Path $work 'incomplete-backup') } catch { $failure = $_.Exception.Message }
  Assert ($failure -and $failure -match 'missing') 'Backup must fail if a database reference lacks object bytes.'
  Assert-WriterState
  Pass 'missing source object fails backup and still resumes only original writers'
} finally {
  if ($started) {
    # The unique Compose name and explicit file prevent cleanup from selecting the deployment.
    & docker compose --env-file $envPath -f $composePath down --volumes --remove-orphans | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Warning "Cleanup failed for isolated test project $project." }
  }
  if (-not (Test-RecoveryPathEqual (Split-Path -Parent ([IO.Path]::GetFullPath($work))) $tempRoot) -or (Split-Path -Leaf $work) -cnotmatch '^edugrade-recovery-test-[a-f0-9]{32}$') { throw 'Unsafe integration test cleanup path.' }
  Remove-Item -LiteralPath $work -Recurse -Force
  if (-not (Test-RecoveryPathEqual (Split-Path -Parent $composePath) $composeDirectory) -or (Split-Path -Leaf $composePath) -cnotmatch '^\.recovery-test-[a-f0-9]{32}\.yml$') { throw 'Unsafe test Compose cleanup path.' }
  Remove-Item -LiteralPath $composePath -Force -ErrorAction SilentlyContinue
}
Write-Host "$passed Docker recovery tests passed."
