import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
const checks = [
  {
    name: 'compose deployment hardening',
    file: 'infra/docker-compose/docker-compose.yml',
    includes: ['schema_migration', 'checksum mismatch for applied migration', "grep -qi ':18BD' /proc/net/tcp", 'EDUGRADE_MIGRATION_BASELINE_VERSION', 'pg_advisory_lock', 'EDUGRADE_POSTGRES_ADMIN_DSN', 'EDUGRADE_POSTGRES_APP_USER', 'GRANT edugrade_tenant_runtime'],
  },
  {
    name: 'local environment and image mirrors',
    file: 'infra/docker-compose/.env.example',
    includes: ['EDUGRADE_ENV=local', 'EDUGRADE_GO_BUILD_IMAGE=', 'EDUGRADE_NODE_BUILD_IMAGE=', 'EDUGRADE_MIGRATION_BASELINE_VERSION=', 'EDUGRADE_POSTGRES_TENANT_RLS=true', 'EDUGRADE_API_GATEWAY_IMAGE=', 'EDUGRADE_POSTGRES_ADMIN_DSN='],
  },
  {
    name: 'deployment preflight',
    file: 'infra/docker-compose/scripts/preflight.ps1',
    includes: ['Production preflight rejected unsafe configuration', 'RequireApplicationBaseImages', 'docker compose', 'PostgreSQL tenant RLS must be enabled', 'must be pinned to an immutable image digest'],
  },
  {
    name: 'idempotent initialization',
    file: 'infra/docker-compose/scripts/init.ps1',
    includes: ['MigrationBaselineVersion', 'Wait-ComposeService', 'smoke-test.ps1', 'BootstrapAdmin'],
  },
  {
    name: 'authenticated smoke test',
    file: 'infra/docker-compose/scripts/smoke-test.ps1',
    includes: ['/api/v1/auth/login', '/api/v1/auth/me', 'HttpOnly', '/backend-health'],
  },
  {
    name: 'binary-safe backup',
    file: 'infra/docker-compose/scripts/backup.ps1',
    includes: ['docker cp', 'pg_restore --list', 'Get-FileHash', 'applied_migration_count', 'images = $images'],
  },
  {
    name: 'guarded restore',
    file: 'infra/docker-compose/scripts/restore.ps1',
    includes: ['ConfirmRestore', 'AllowPrimaryDatabase', 'TargetDatabase', 'pg_restore --list'],
  },
  {
    name: 'preproduction runbook',
    file: 'docs/deployment/preproduction-runbook.md',
    includes: ['## 4. 部署预检', '## 10. 备份', '## 11. 隔离恢复验证', '## 13. TLS 与正式环境', 'EDUGRADE_POSTGRES_APP_USER', 'repository@sha256:<digest>'],
  },
  {
    name: 'story evidence',
    file: 'docs/stories/STORY-052-production-deployment-preproduction-acceptance.md',
    includes: ['## Plan Review', '## Spec Fixes', '## Implementation', '## Approval'],
  },
];

const failures = [];
for (const check of checks) {
  const absolute = path.join(root, check.file);
  if (!fs.existsSync(absolute)) {
    failures.push(`${check.name}: missing ${check.file}`);
    continue;
  }
  const source = fs.readFileSync(absolute, 'utf8');
  for (const expected of check.includes) {
    if (!source.includes(expected)) failures.push(`${check.name}: ${check.file} missing ${expected}`);
  }
}

const migrationDir = path.join(root, 'services/api-gateway/migrations');
const latestMigration = fs.readdirSync(migrationDir)
  .map((name) => /^(\d{6})_.*\.sql$/.exec(name)?.[1])
  .filter(Boolean)
  .sort()
  .at(-1);
if (!latestMigration) {
  failures.push('schema release metadata: no numbered migration found');
} else {
  const environmentExample = fs.readFileSync(path.join(root, 'infra/docker-compose/.env.example'), 'utf8');
  const compose = fs.readFileSync(path.join(root, 'infra/docker-compose/docker-compose.yml'), 'utf8');
  if (!environmentExample.includes(`EDUGRADE_SCHEMA_VERSION=${latestMigration}`)) {
    failures.push(`schema release metadata: .env.example must declare latest migration ${latestMigration}`);
  }
  if (!compose.includes(`EDUGRADE_SCHEMA_VERSION:-${latestMigration}`)) {
    failures.push(`schema release metadata: Compose default must match latest migration ${latestMigration}`);
  }
}

if (failures.length) {
  console.error('STORY-052 production deployment check failed:');
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log('STORY-052 production deployment check passed.');
