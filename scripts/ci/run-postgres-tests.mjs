import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { assertRequiredPostgresTests, createPostgresTestGate, discoverPostgresTests, normalizePostgresTestDsn, postgresFixtureDsn, postgresTestScope } from "./postgres-test-gate.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const cwd = path.join(root, "services/api-gateway");
// 在创建数据库前限制为 URL，且移除 pgx 会用来覆盖 pathname 的数据库别名。
const sourceDsn = normalizePostgresTestDsn(process.env.EDUGRADE_E2E_DATABASE_URL);
const env = { ...process.env, EDUGRADE_REQUIRE_PG_TESTS: "1", GOMAXPROCS: "2" };
env.EDUGRADE_E2E_DATABASE_URL = sourceDsn;
const { aliases, expected, declared } = discoverPostgresTests(cwd);
if (!expected.size) throw new Error("No PostgreSQL tests discovered; refusing an empty gate");
// CI 使用全树入口时先核对关键用例存在；本地定向复验仍只执行明确选定的子集。
if (!process.argv.slice(2).length) assertRequiredPostgresTests(expected);
const scope = postgresTestScope(process.argv.slice(2), expected, declared);
console.log(`PostgreSQL verification scope: ${scope.label}`);
const setup = spawnSync("go", ["run", "-p=1", "./cmd/prepare-postgres-tests"], { cwd, env, encoding: "utf8", maxBuffer: 1024 * 1024 });
if (setup.status !== 0) throw new Error(`Prepare PostgreSQL test database failed: ${setup.stderr}`);
const gate = createPostgresTestGate(scope.expected);
let pending = "";
let testFailure;
try {
  const { database } = JSON.parse(setup.stdout);
  const fixtureDsn = postgresFixtureDsn(sourceDsn, database);
  for (const alias of aliases) env[alias] = fixtureDsn;
  env.EDUGRADE_E2E_DATABASE_URL = fixtureDsn;
  // 配置测试显式使用合成密钥，不依赖产品默认值或继承部署凭据。
  env.EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY = "ci-synthetic-model-key-32-characters-only";
  env.EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY = "ci-synthetic-review-key-32-characters-only";
  const child = spawn("go", ["test", "-p=1", ...scope.packages, ...scope.filterArgs, "-count=1", "-timeout=45m", "-json"], { cwd, env, stdio: ["ignore", "pipe", "inherit"] });
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", chunk => {
    pending += chunk.toString();
    let index;
    while ((index = pending.indexOf("\n")) >= 0) {
      const line = pending.slice(0, index);
      pending = pending.slice(index + 1);
      if (!line.trim()) continue;
      let event;
      try { event = JSON.parse(line); } catch { process.stdout.write(`${line}\n`); continue; }
      if (event.Output) process.stdout.write(event.Output);
      gate.observe(event);
    }
  });
  const status = await new Promise((resolve, reject) => { child.on("error", reject); child.on("close", resolve); });
  console.log(`PostgreSQL gate ran ${gate.finish(status)} required tests without skips.`);
} catch (error) {
  testFailure = error;
} finally {
  // 只清理本入口建立的随机库；原始 DSN 指定的数据库不被迁移或删除。
  const cleanup = spawnSync("go", ["run", "-p=1", "./cmd/prepare-postgres-tests", "cleanup"], { cwd, env, encoding: "utf8" });
  if (cleanup.status !== 0) {
    const cleanupFailure = new Error(`Test database cleanup failed: ${cleanup.stderr}`);
    if (testFailure) throw new AggregateError([testFailure, cleanupFailure], "PostgreSQL tests and fixture cleanup both failed");
    throw cleanupFailure;
  }
}
if (testFailure) throw testFailure;
