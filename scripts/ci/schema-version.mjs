import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
const migrationsDir = path.join(root, 'services/api-gateway/migrations');
const envFile = path.join(root, 'infra/docker-compose/.env.example');
const composeFile = path.join(root, 'infra/docker-compose/docker-compose.yml');

export function readEnvSchemaVersion(source) {
  return source.match(/^EDUGRADE_SCHEMA_VERSION=(\d{6})$/m)?.[1];
}

export function readComposeSchemaVersion(source) {
  return source.match(/SCHEMA_VERSION:\s*\$\{EDUGRADE_SCHEMA_VERSION:-(\d{6})\}/)?.[1];
}

export function getLatestMigrationVersion() {
  const migrations = fs.readdirSync(migrationsDir).filter((name) => /^\d{6}_.*\.sql$/.test(name)).sort();
  if (!migrations.length) throw new Error('No versioned migrations found');
  return migrations.at(-1).slice(0, 6);
}

export function check() {
  const latest = getLatestMigrationVersion();
  const envVersion = readEnvSchemaVersion(fs.readFileSync(envFile, 'utf8'));
  const composeVersion = readComposeSchemaVersion(fs.readFileSync(composeFile, 'utf8'));
  const mismatches = [];
  if (envVersion !== latest) mismatches.push(`.env.example=${envVersion ?? 'missing'}`);
  if (composeVersion !== latest) mismatches.push(`docker-compose.yml=${composeVersion ?? 'missing'}`);
  if (mismatches.length) throw new Error(`Expected schema ${latest}; found ${mismatches.join(', ')}`);
  console.log(`Schema version ${latest} is synchronized.`);
}

// 只同步示例部署元数据，不执行迁移，也不证明运行中的数据库已达到该版本。
export function sync() {
  const latest = getLatestMigrationVersion();
  const envSource = fs.readFileSync(envFile, 'utf8');
  const composeSource = fs.readFileSync(composeFile, 'utf8');
  if (!readEnvSchemaVersion(envSource) || !readComposeSchemaVersion(composeSource)) {
    throw new Error('Schema metadata field is missing; refusing an unsafe sync');
  }
  fs.writeFileSync(envFile, envSource.replace(/^(EDUGRADE_SCHEMA_VERSION=)\d{6}$/m, `$1${latest}`));
  fs.writeFileSync(composeFile, composeSource.replace(/(SCHEMA_VERSION:\s*\$\{EDUGRADE_SCHEMA_VERSION:-)\d{6}(\})/, `$1${latest}$2`));
  console.log(`Synchronized schema metadata to ${latest}.`);
}
