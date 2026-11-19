param([Parameter(Mandatory=$true)][string]$PostgresTestURL)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../recovery-common.ps1')
$database = 'fix_recovery_' + [guid]::NewGuid().ToString('N')
$target = [UriBuilder]::new($PostgresTestURL)
$target.Path = '/' + $database
$created = $false
$passed = 0

function Invoke-TestSQL([string]$URL, [string]$SQL, [switch]$ExpectFailure) {
  $output = @(& psql $URL -X -v ON_ERROR_STOP=1 -Atc $SQL 2>&1)
  if ($ExpectFailure) {
    if ($LASTEXITCODE -eq 0) { throw 'Expected SQL constraint failure, but the update succeeded.' }
  } elseif ($LASTEXITCODE -ne 0) { throw "Recovery SQL test failed: $($output -join '`n')" }
  return ($output -join "`n").Trim()
}
function Assert-SQL([string]$SQL, [string]$Expected) {
  $actual = Invoke-TestSQL $target.Uri.AbsoluteUri $SQL
  if ($actual -cne $Expected) { throw "Recovery SQL assertion expected '$Expected', got '$actual'." }
}
function Move-TestBucketReferences([string]$Source, [string]$TargetBucket, [switch]$ExpectFailure) {
  $query = "SELECT COALESCE(json_agg(f),'[]'::json) FROM (SELECT id,storage_bucket,storage_key,size_bytes,hash_sha256 FROM file_asset WHERE deleted_at IS NULL AND lifecycle_status IN ('active','orphan_recovered')) f"
  $references = @(Invoke-TestSQL $target.Uri.AbsoluteUri $query | ConvertFrom-Json)
  return Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL $Source $TargetBucket $references) -ExpectFailure:$ExpectFailure
}

try {
  Invoke-TestSQL $PostgresTestURL "CREATE DATABASE $database" | Out-Null
  $created = $true
  $schema = @'
CREATE TABLE file_asset (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, storage_bucket text NOT NULL,
  storage_key text NOT NULL, size_bytes bigint NOT NULL, hash_sha256 text NOT NULL,
  original_name text NOT NULL DEFAULT 'fixture', content_type text NOT NULL DEFAULT 'application/octet-stream',
  deleted_at timestamptz, lifecycle_status text NOT NULL DEFAULT 'active',
  CHECK (storage_bucket <> 'blocked-bucket')
);
CREATE TABLE question_bank_item_asset (tenant_id uuid NOT NULL, file_asset_id uuid NOT NULL REFERENCES file_asset(id));
CREATE TABLE answer_sheet_template_library (tenant_id uuid NOT NULL, reference_file_asset_id uuid NOT NULL REFERENCES file_asset(id));
CREATE TABLE question_reference_manifest_item (tenant_id uuid NOT NULL, source_file_asset_id uuid NOT NULL REFERENCES file_asset(id), page_file_asset_id uuid NOT NULL REFERENCES file_asset(id), derived_file_asset_id uuid NOT NULL REFERENCES file_asset(id));
CREATE TABLE recovery_update_evidence (id uuid NOT NULL);
CREATE FUNCTION recovery_observer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  INSERT INTO recovery_update_evidence VALUES (NEW.id); RETURN NEW;
END $$;
CREATE TRIGGER recovery_observer AFTER UPDATE ON file_asset FOR EACH ROW EXECUTE FUNCTION recovery_observer();
INSERT INTO file_asset (id,tenant_id,storage_bucket,storage_key,size_bytes,hash_sha256,lifecycle_status) VALUES
 ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000010','source-bucket','a',1,repeat('a',64),'active'),
 ('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000010','source-bucket','b',1,repeat('b',64),'orphan_recovered'),
 ('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000010','source-bucket','c',1,repeat('c',64),'pending_upload'),
 ('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000010','other-bucket','d',1,repeat('d',64),'missing_object'),
 ('00000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000010','source-bucket','e',1,repeat('e',64),'active'),
 ('00000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000010','source-bucket','f',1,repeat('f',64),'deleted'),
 ('00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000010','source-bucket','e',1,repeat('e',64),'deleted'),
 ('00000000-0000-4000-8000-000000000008','00000000-0000-4000-8000-000000000010','source-bucket','g',1,repeat('a',64),'pending_delete'),
 ('00000000-0000-4000-8000-000000000009','00000000-0000-4000-8000-000000000010','source-bucket','h',1,repeat('b',64),'delete_failed'),
 ('00000000-0000-4000-8000-000000000011','00000000-0000-4000-8000-000000000010','source-bucket','i',1,repeat('c',64),'delete_failed');
UPDATE file_asset SET deleted_at='2026-09-26T00:00:00Z' WHERE storage_key='i';
INSERT INTO question_bank_item_asset SELECT tenant_id,id FROM file_asset WHERE storage_key='a';
INSERT INTO answer_sheet_template_library SELECT tenant_id,id FROM file_asset WHERE storage_key='b';
INSERT INTO question_reference_manifest_item SELECT tenant_id,id,id,id FROM file_asset WHERE storage_key='e' AND lifecycle_status='active';
'@
  Invoke-TestSQL $target.Uri.AbsoluteUri $schema | Out-Null
  # 加载正式迁移的三条触发器，验证恢复前后题库、答题卡模板和阅卷参考对象的保护。
  $migrations = Join-Path $PSScriptRoot '../../../../services/api-gateway/migrations'
  $guards = @(
    @{ Migration = '000141_question_bank_publication.sql'; Function = 'question_bank_file_guard'; Trigger = 'question_bank_file_guard'; Key = 'a'; Error = 'referenced bank asset retained' },
    @{ Migration = '000190_answer_sheet_template_library.sql'; Function = 'protect_answer_sheet_library_reference'; Trigger = 'answer_sheet_library_reference_pinned'; Key = 'b'; Error = 'answer sheet library reference object is pinned' },
    @{ Migration = '000198_question_reference_materials.sql'; Function = 'protect_reference_material_object'; Trigger = 'trg_reference_material_object_pin'; Key = 'e'; Error = 'file object is pinned by a reference manifest' }
  )
  foreach ($guard in $guards) {
    $migration = Get-Content -LiteralPath (Join-Path $migrations $guard.Migration) -Raw
    $sql = [regex]::Match($migration, "(?s)CREATE FUNCTION $($guard.Function)\(\).*?CREATE TRIGGER $($guard.Trigger)\b[^;]*;").Value
    if (-not $sql) { throw "Cannot locate production file guard $($guard.Trigger)." }
    Invoke-TestSQL $target.Uri.AbsoluteUri $sql | Out-Null
    $ordinaryUpdate = Invoke-TestSQL $target.Uri.AbsoluteUri "UPDATE file_asset SET storage_bucket='target-bucket' WHERE storage_key='$($guard.Key)' AND lifecycle_status<>'deleted'" -ExpectFailure
    if ($ordinaryUpdate -notmatch $guard.Error) { throw "Unexpected rejection for $($guard.Trigger)." }
  }
  $passed++; Write-Host 'PASS PostgreSQL all production file guards reject ordinary bucket updates'

  foreach ($mode in @(@('ENABLE', 'O'), @('ENABLE ALWAYS', 'A'), @('ENABLE REPLICA', 'R'), @('DISABLE', 'D'))) {
    $alter = $guards | ForEach-Object { "ALTER TABLE file_asset $($mode[0]) TRIGGER $($_.Trigger)" }
    Invoke-TestSQL $target.Uri.AbsoluteUri (($alter + 'TRUNCATE recovery_update_evidence') -join '; ') | Out-Null
    Move-TestBucketReferences 'source-bucket' 'target-bucket' | Out-Null
    Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='target-bucket'" '6'
    Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_key='d' AND storage_bucket='other-bucket'" '1'
    # 对账会读取尚未删除记录的桶名。两种删除重试状态改指目标桶，已删除记录留在源桶。
    Assert-SQL "SELECT count(*) FROM file_asset WHERE deleted_at IS NULL AND lifecycle_status IN ('pending_delete','delete_failed') AND storage_bucket='target-bucket'" '2'
    Assert-SQL "SELECT count(*) FROM file_asset WHERE (deleted_at IS NOT NULL OR lifecycle_status='deleted') AND storage_bucket='source-bucket'" '3'
    Assert-SQL "SELECT string_agg(tgenabled::text,'') FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname IN ('question_bank_file_guard','answer_sheet_library_reference_pinned','trg_reference_material_object_pin')" (([string]$mode[1]) * 3)
    Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '6'
    if ($mode[1] -eq 'O') {
      foreach ($guard in $guards) {
        $ordinaryUpdate = Invoke-TestSQL $target.Uri.AbsoluteUri "UPDATE file_asset SET storage_key='changed' WHERE storage_bucket='target-bucket' AND storage_key='$($guard.Key)'" -ExpectFailure
        if ($ordinaryUpdate -notmatch $guard.Error) { throw "Restored guard did not protect $($guard.Trigger)." }
      }
    }
    Move-TestBucketReferences 'target-bucket' 'source-bucket' | Out-Null
    $passed++; Write-Host "PASS PostgreSQL bucket remap preserves guard mode $($mode[1]) and other triggers"
  }

  Invoke-TestSQL $target.Uri.AbsoluteUri "ALTER TABLE file_asset ENABLE ALWAYS TRIGGER question_bank_file_guard; ALTER TABLE file_asset ENABLE TRIGGER answer_sheet_library_reference_pinned; ALTER TABLE file_asset ENABLE REPLICA TRIGGER trg_reference_material_object_pin; TRUNCATE recovery_update_evidence" | Out-Null
  Move-TestBucketReferences 'source-bucket' 'blocked-bucket' -ExpectFailure | Out-Null
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'" '9'
  Assert-SQL "SELECT tgenabled FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='question_bank_file_guard'" 'A'
  Assert-SQL "SELECT tgenabled FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='answer_sheet_library_reference_pinned'" 'O'
  Assert-SQL "SELECT tgenabled FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='trg_reference_material_object_pin'" 'R'
  Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '0'
  $passed++; Write-Host 'PASS PostgreSQL failed remap rolls back data and preserves every guard mode atomically'

  $incomplete = Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL 'source-bucket' 'target-bucket' @()) -ExpectFailure
  if ($incomplete -notmatch 'restored file references differ from verified backup') { throw 'Expected the verified reference check to reject incomplete evidence.' }
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'" '9'
  Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '0'
  $passed++; Write-Host 'PASS PostgreSQL incomplete verification evidence leaves all references unchanged'

  # 存量异常可能把受保护对象留在待删状态。第二阶段仍需遵守保护，且失败要回滚第一阶段。
  Invoke-TestSQL $target.Uri.AbsoluteUri "INSERT INTO question_bank_item_asset SELECT tenant_id,id FROM file_asset WHERE storage_key='g'" | Out-Null
  $protectedDelete = Move-TestBucketReferences 'source-bucket' 'target-bucket' -ExpectFailure
  if ($protectedDelete -notmatch 'referenced bank asset retained') { throw 'Expected the file guard to reject an unverified protected delete.' }
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'" '9'
  Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '0'
  $passed++; Write-Host 'PASS PostgreSQL guarded pending deletion rolls back the entire restore remap'

  $drop = $guards | ForEach-Object { "DROP TRIGGER $($_.Trigger) ON file_asset" }
  Invoke-TestSQL $target.Uri.AbsoluteUri ($drop -join '; ') | Out-Null
  Move-TestBucketReferences 'source-bucket' 'target-bucket' | Out-Null
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='target-bucket'" '6'
  $passed++; Write-Host 'PASS PostgreSQL remap also supports snapshots preceding the file guards'
} finally {
  if ($created) {
    if ($database -cnotmatch '^fix_recovery_[a-f0-9]{32}$') { throw 'Unsafe SQL test database cleanup name.' }
    Invoke-TestSQL $PostgresTestURL "DROP DATABASE $database WITH (FORCE)" | Out-Null
  }
}
Write-Host "$passed PostgreSQL recovery tests passed."
