param(
  [string]$PostgresImage = 'postgres:16-alpine',
  [string]$MinioImage = 'minio/minio:RELEASE.2025-04-22T22-12-26Z',
  [string]$MinioClientImage = 'minio/mc:RELEASE.2025-04-16T18-13-26Z'
)

# 需显式启用的真实集成测试，使用合成数据、不暴露主机端口，
# 并使用独立数据卷的唯一 Compose 项目，不影响现有部署容器。
$ErrorActionPreference = 'Stop'
$scriptDirectory = Split-Path -Parent $PSScriptRoot
. (Join-Path $scriptDirectory 'recovery-common.ps1')
$suffix = [guid]::NewGuid().ToString('N')
$project = 'edugrade-recovery-test-' + $suffix
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('/', '\')
$work = Join-Path $tempRoot $project
$composeDirectory = Join-Path $work 'infra/docker-compose'
$composePath = Join-Path $composeDirectory 'compose.yml'
$workForCompose = $work.Replace('\', '/')
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

New-Item -ItemType Directory -Path $composeDirectory | Out-Null
try {
  # 备份从 Compose 家目录解析迁移版本。镜像与数据卷都隔离，迁移文件复制到相同相对位置。
  $migrationFixture = Join-Path $work 'services/api-gateway/migrations'
  New-Item -ItemType Directory -Path $migrationFixture | Out-Null
  $latestMigration = Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot '../../../../services/api-gateway/migrations') -File -Filter '*.sql' | Sort-Object Name | Select-Object -Last 1
  Copy-Item -LiteralPath $latestMigration.FullName -Destination $migrationFixture
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
      EDUGRADE_MINIO_APP_ACCESS_KEY: fixture-app
      EDUGRADE_MINIO_APP_SECRET_KEY: synthetic-recovery-app-fixture
      EDUGRADE_MINIO_USE_SSL: "false"
      EDUGRADE_FILE_BUCKET: source-bucket
  api-gateway: &writer
    image: $PostgresImage
    environment:
      EDUGRADE_ENV: test
      EDUGRADE_FILE_BUCKET: source-bucket
      EDUGRADE_POSTGRES_DSN: "`${YJ_RECOVERY_FIXTURE_GATEWAY_DSN-postgres://fixture:synthetic-recovery-fixture@postgres:5432/source?sslmode=disable}"
      EDUGRADE_POSTGRES_DSN_FILE: "`${YJ_RECOVERY_FIXTURE_GATEWAY_DSN_FILE-}"
      EDUGRADE_MINIO_ENDPOINT: minio:9000
      EDUGRADE_MINIO_USE_SSL: "false"
      EDUGRADE_MINIO_APP_ACCESS_KEY: fixture-app
      EDUGRADE_MINIO_APP_SECRET_KEY: "`${YJ_RECOVERY_FIXTURE_GATEWAY_SECRET-synthetic-recovery-app-fixture}"
      EDUGRADE_MINIO_APP_SECRET_KEY_FILE: "`${YJ_RECOVERY_FIXTURE_GATEWAY_SECRET_FILE-}"
    volumes:
      - "${workForCompose}:/restore-fixture:ro"
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
  Invoke-Minio 'mc ready edugrade; mc mb edugrade/source-bucket; mc cp /fixture/file.bin edugrade/source-bucket/tenant/active.bin; mc cp /fixture/file.bin edugrade/source-bucket/tenant/recovered.bin; mc cp /fixture/file.bin edugrade/source-bucket/tenant/reference.bin; mc admin user add edugrade "$EDUGRADE_MINIO_APP_ACCESS_KEY" "$EDUGRADE_MINIO_APP_SECRET_KEY"'
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
CREATE TABLE answer_sheet_template_library (tenant_id uuid,reference_file_asset_id uuid REFERENCES file_asset(id));
CREATE TABLE question_reference_manifest_item (tenant_id uuid,source_file_asset_id uuid REFERENCES file_asset(id),page_file_asset_id uuid REFERENCES file_asset(id),derived_file_asset_id uuid REFERENCES file_asset(id));
INSERT INTO file_asset VALUES
 ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000010','source-bucket','tenant/active.bin',$size,'$hash','fixture','application/octet-stream',NULL,'active'),
 ('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000010','source-bucket','tenant/recovered.bin',$size,'$hash','fixture','application/octet-stream',NULL,'orphan_recovered'),
 ('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000010','source-bucket','tenant/pending.bin',$size,'$hash','fixture','application/octet-stream',NULL,'pending_upload'),
 ('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000010','source-bucket','tenant/reference.bin',$size,'$hash','fixture','application/octet-stream',NULL,'active');
INSERT INTO question_bank_item_asset SELECT tenant_id,id FROM file_asset WHERE storage_key='tenant/active.bin';
INSERT INTO answer_sheet_template_library SELECT tenant_id,id FROM file_asset WHERE storage_key='tenant/recovered.bin';
INSERT INTO question_reference_manifest_item SELECT tenant_id,id,id,id FROM file_asset WHERE storage_key='tenant/reference.bin';
"@
  $migrations = Join-Path $PSScriptRoot '../../../../services/api-gateway/migrations'
  $definitions = @(
    @('000141_question_bank_publication.sql','question_bank_file_guard','question_bank_file_guard'),
    @('000190_answer_sheet_template_library.sql','protect_answer_sheet_library_reference','answer_sheet_library_reference_pinned'),
    @('000198_question_reference_materials.sql','protect_reference_material_object','trg_reference_material_object_pin')
  )
  $guardSQL = foreach ($definition in $definitions) {
    $migration = Get-Content -LiteralPath (Join-Path $migrations $definition[0]) -Raw
    $guard = [regex]::Match($migration, "(?s)CREATE FUNCTION $($definition[1])\(\).*?CREATE TRIGGER $($definition[2])\b[^;]*;").Value
    Assert ([bool]$guard) "Cannot locate the production asset guard $($definition[2])."
    $guard
  }
  Invoke-RecoverySQL $context 'fixture' 'source' ($schema + "`n" + ($guardSQL -join "`n")) | Out-Null
  $output = Join-Path $work 'backups'
  & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composePath -EnvFile $envPath -OutputDir $output
  Assert-WriterState
  $backup = (Get-ChildItem -LiteralPath $output -Directory | Select-Object -First 1).FullName
  $verified = & (Join-Path $scriptDirectory 'verify-backup.ps1') -BackupDirectory $backup | ConvertFrom-Json
  $manifest = Get-Content -LiteralPath $verified.manifest -Raw | ConvertFrom-Json
  Assert ($verified.reference_count -eq 3 -and $manifest.stopped_containers.Count -eq 2) 'Backup must include all three persisted objects and both original writers.'
  Assert (@($manifest.images | Where-Object { $_.container -match 'api-gateway|future-writer' }).Count -eq 2) 'Manifest must retain stopped writer image evidence.'
  Pass 'backup freezes writers, produces verified paired data, and restores exact running set'

  & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'restore_fixture' -TargetBucket 'restored-bucket' -CreateTargetDatabase -ConfirmRestore
  Assert-WriterState
  Assert ((Invoke-RecoverySQL $context 'fixture' 'restore_fixture' "SELECT count(*) FROM file_asset WHERE storage_bucket='restored-bucket'") -eq '4') 'All references, including pending assets, must be remapped.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'restore_fixture' "SELECT count(*) FROM pg_trigger WHERE tgname IN ('question_bank_file_guard','answer_sheet_library_reference_pinned','trg_reference_material_object_pin') AND tgenabled='O'") -eq '3') 'All asset guards must be re-enabled after remapping.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'source' "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'") -eq '4') 'Original database must be unchanged.'
  Pass 'restore remaps protected bank, template, and reference assets after verifying actual target bytes'

  $drillOutput = @(& (Join-Path $scriptDirectory 'restore-drill.ps1') -ComposeFile $composePath -EnvFile $envPath -BackupDirectory $backup)
  $drill = ($drillOutput -join "`n" | ConvertFrom-Json)
  Assert ($drill.succeeded -and $drill.reference_bytes_and_hashes_verified -and $drill.verified_file_references -eq 3) 'Restore drill must verify actual referenced bytes.'
  Assert ((Invoke-RecoverySQL $context 'fixture' 'source' "SELECT count(*) FROM pg_database WHERE datname='$($drill.database)'") -eq '0') 'Drill database must be cleaned.'
  Assert-WriterState
  Pass 'restore drill verifies reference integrity and cleans isolated database and bucket'

  # 改动 Compose 文件但不重建容器，验证主库识别使用已部署目标。
  $deployedCompose = Get-Content -LiteralPath $composePath -Raw
  $driftedCompose = $deployedCompose.Replace('POSTGRES_DB: source', 'POSTGRES_DB: new-config-db')
  Assert ($driftedCompose -cne $deployedCompose) 'Expected to change the configured PostgreSQL database.'
  $failure = $null
  try {
    Set-Content -LiteralPath $composePath -Value $driftedCompose
    & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'source' -TargetBucket 'drift-bucket' -ConfirmRestore
  } catch { $failure = $_.Exception.Message }
  finally { Set-Content -LiteralPath $composePath -Value $deployedCompose }
  Assert ($failure -match 'deployed PostgreSQL database') 'Restore must reject database drift that misclassifies the active database as isolated.'
  Assert-WriterState
  Pass 'restore rejects drift between the configured database and the deployed container before stopping writers'

  $failure = $null
  try {
    & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'source' -TargetBucket 'source-bucket' -AllowPrimaryDatabase -AllowPrimaryBucket -ConfirmRestore
  } catch { $failure = $_.Exception.Message }
  Assert ($failure -match 'application identity could not verify') 'MinIO root access alone must not permit restarting the gateway.'
  $running = @(Invoke-RecoveryCompose $context @('ps', '--status', 'running', '--services'))
  Assert ($running -notcontains 'api-gateway' -and $running -notcontains 'future-writer') 'Primary verification failure must keep writers stopped.'
  Pass 'root can restore objects but a denied application account prevents restart'

  $appPolicy = '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:ListBucket","s3:GetBucketLocation"],"Resource":["arn:aws:s3:::source-bucket"]},{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::source-bucket/*"]}]}'
  [IO.File]::WriteAllText((Join-Path $work 'minio-app-policy.json'), $appPolicy)
  Invoke-Minio 'mc admin policy create edugrade fixture-read /fixture/minio-app-policy.json; mc admin policy attach edugrade fixture-read --user "$EDUGRADE_MINIO_APP_ACCESS_KEY"'
  Invoke-RecoveryCompose $context @('start', 'api-gateway', 'future-writer') | Out-Null
  & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'source' -TargetBucket 'source-bucket' -AllowPrimaryDatabase -AllowPrimaryBucket -ConfirmRestore
  Assert-WriterState
  Pass 'primary restore verifies referenced bytes with the existing restricted application identity'

  # 工具继续使用部署账号，网关从文件读取数据库定位及 MinIO 凭据。主恢复需核对文件所指的目标。
  [IO.File]::WriteAllText((Join-Path $work 'app-secret'), "synthetic-recovery-app-fixture`r`n")
  [IO.File]::WriteAllText((Join-Path $work 'app-dsn'), "postgres://fixture:synthetic-recovery-fixture@postgres:5432/source?sslmode=disable`r`n")
  Add-Content -LiteralPath $envPath -Value @('YJ_RECOVERY_FIXTURE_GATEWAY_SECRET=', 'YJ_RECOVERY_FIXTURE_GATEWAY_SECRET_FILE=/restore-fixture/app-secret',
    'YJ_RECOVERY_FIXTURE_GATEWAY_DSN=', 'YJ_RECOVERY_FIXTURE_GATEWAY_DSN_FILE=/restore-fixture/app-dsn')
  Invoke-RecoveryCompose $context @('up', '-d', '--no-deps', 'api-gateway') | Out-Null
  $deployedGateway = Invoke-RecoveryCompose $context @('ps', '-q', 'api-gateway')
  [IO.File]::WriteAllText((Join-Path $work 'app-secret'), 'old-synthetic-recovery-app-fixture')
  $failure = $null
  try {
    & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'source' -TargetBucket 'source-bucket' -AllowPrimaryDatabase -AllowPrimaryBucket -ConfirmRestore
  } catch { $failure = $_.Exception.Message }
  Assert ($failure -match 'matching MinIO application credentials') 'Changed gateway credential files must fail before stopping writers.'
  Assert-WriterState
  [IO.File]::WriteAllText((Join-Path $work 'app-secret'), "synthetic-recovery-app-fixture`r`n")
  Pass 'primary restore rejects rotated gateway file credentials before stopping writers'
  & (Join-Path $scriptDirectory 'restore.ps1') -ComposeFile $composePath -EnvFile $envPath -PostgresDump $verified.postgres_path -TargetDatabase 'source' -TargetBucket 'source-bucket' -AllowPrimaryDatabase -AllowPrimaryBucket -ConfirmRestore
  Assert ((Invoke-RecoveryCompose $context @('ps', '-q', 'api-gateway')) -ceq $deployedGateway) 'Primary restore must restart the original gateway without recreating it.'
  Assert-WriterState
  Pass 'primary restore verifies the existing gateway account when its credential comes from a file'

  Invoke-Minio 'mc rm edugrade/source-bucket/tenant/active.bin'
  $failure = $null
  try { & (Join-Path $scriptDirectory 'backup.ps1') -ComposeFile $composePath -EnvFile $envPath -OutputDir (Join-Path $work 'incomplete-backup') } catch { $failure = $_.Exception.Message }
  Assert ($failure -and $failure -match 'missing') 'Backup must fail if a database reference lacks object bytes.'
  Assert-WriterState
  Pass 'missing source object fails backup and still resumes only original writers'
} finally {
  if ($started) {
    # 唯一的 Compose 名称和显式文件参数可防止清理时选中部署资源。
    & docker compose --env-file $envPath -f $composePath down --volumes --remove-orphans | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Warning "Cleanup failed for isolated test project $project." }
  }
  if (-not (Test-RecoveryPathEqual (Split-Path -Parent ([IO.Path]::GetFullPath($work))) $tempRoot) -or (Split-Path -Leaf $work) -cnotmatch '^edugrade-recovery-test-[a-f0-9]{32}$') { throw 'Unsafe integration test cleanup path.' }
  Remove-Item -LiteralPath $work -Recurse -Force
}
Write-Host "$passed Docker recovery tests passed."
