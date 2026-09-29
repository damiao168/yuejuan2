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
CREATE TABLE recovery_update_evidence (id uuid NOT NULL);
CREATE FUNCTION recovery_observer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  INSERT INTO recovery_update_evidence VALUES (NEW.id); RETURN NEW;
END $$;
CREATE TRIGGER recovery_observer AFTER UPDATE ON file_asset FOR EACH ROW EXECUTE FUNCTION recovery_observer();
INSERT INTO file_asset (id,tenant_id,storage_bucket,storage_key,size_bytes,hash_sha256,lifecycle_status) VALUES
 ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000010','source-bucket','a',1,repeat('a',64),'active'),
 ('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000010','source-bucket','b',1,repeat('b',64),'orphan_recovered'),
 ('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000010','source-bucket','c',1,repeat('c',64),'pending_upload'),
 ('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000010','other-bucket','d',1,repeat('d',64),'active');
INSERT INTO question_bank_item_asset SELECT tenant_id,id FROM file_asset WHERE storage_key='a';
'@
  Invoke-TestSQL $target.Uri.AbsoluteUri $schema | Out-Null
  # Exercise the actual migration's immutable asset guard, without running unrelated migrations.
  $migration = Get-Content -LiteralPath (Join-Path $PSScriptRoot '../../../../services/api-gateway/migrations/000141_question_bank_publication.sql') -Raw
  $guard = [regex]::Match($migration, '(?s)CREATE FUNCTION question_bank_file_guard\(\).*?CREATE TRIGGER question_bank_file_guard[^;]*;').Value
  if (-not $guard) { throw 'Cannot locate the production question-bank asset guard in its migration.' }
  Invoke-TestSQL $target.Uri.AbsoluteUri $guard | Out-Null
  Invoke-TestSQL $target.Uri.AbsoluteUri "UPDATE file_asset SET storage_bucket='target-bucket' WHERE storage_key='a'" -ExpectFailure | Out-Null
  $passed++; Write-Host 'PASS PostgreSQL production immutable asset guard rejects ordinary bucket updates'

  foreach ($mode in @(@('ENABLE', 'O'), @('ENABLE ALWAYS', 'A'), @('ENABLE REPLICA', 'R'), @('DISABLE', 'D'))) {
    Invoke-TestSQL $target.Uri.AbsoluteUri "ALTER TABLE file_asset $($mode[0]) TRIGGER question_bank_file_guard; TRUNCATE recovery_update_evidence" | Out-Null
    Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL 'source-bucket' 'target-bucket') | Out-Null
    Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='target-bucket'" '3'
    Assert-SQL "SELECT storage_bucket FROM file_asset WHERE storage_key='d'" 'other-bucket'
    Assert-SQL "SELECT tgenabled FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='question_bank_file_guard'" $mode[1]
    Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '3'
    Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL 'target-bucket' 'source-bucket') | Out-Null
    $passed++; Write-Host "PASS PostgreSQL bucket remap preserves guard mode $($mode[1]) and other triggers"
  }

  Invoke-TestSQL $target.Uri.AbsoluteUri 'ALTER TABLE file_asset ENABLE ALWAYS TRIGGER question_bank_file_guard; TRUNCATE recovery_update_evidence' | Out-Null
  Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL 'source-bucket' 'blocked-bucket') -ExpectFailure | Out-Null
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='source-bucket'" '3'
  Assert-SQL "SELECT tgenabled FROM pg_trigger WHERE tgrelid='file_asset'::regclass AND tgname='question_bank_file_guard'" 'A'
  Assert-SQL 'SELECT count(*) FROM recovery_update_evidence' '0'
  $passed++; Write-Host 'PASS PostgreSQL failed remap rolls back data and restores immutable guard atomically'

  Invoke-TestSQL $target.Uri.AbsoluteUri 'DROP TRIGGER question_bank_file_guard ON file_asset' | Out-Null
  Invoke-TestSQL $target.Uri.AbsoluteUri (Get-RecoveryBucketRemapSQL 'source-bucket' 'target-bucket') | Out-Null
  Assert-SQL "SELECT count(*) FROM file_asset WHERE storage_bucket='target-bucket'" '3'
  $passed++; Write-Host 'PASS PostgreSQL remap also supports snapshots preceding the bank guard'
} finally {
  if ($created) {
    if ($database -cnotmatch '^fix_recovery_[a-f0-9]{32}$') { throw 'Unsafe SQL test database cleanup name.' }
    Invoke-TestSQL $PostgresTestURL "DROP DATABASE $database WITH (FORCE)" | Out-Null
  }
}
Write-Host "$passed PostgreSQL recovery tests passed."
