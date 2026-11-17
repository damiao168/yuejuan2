import fs from "node:fs";
import path from "node:path";

const moduleName = "edugrade-enterprise/services/api-gateway";

export function normalizePostgresTestDsn(source) {
  if (!source?.trim()) throw new Error("EDUGRADE_E2E_DATABASE_URL is required; PostgreSQL tests may not be skipped");
  let url;
  try { url = new URL(source.trim()); } catch {
    // 连接字符串可能含密码；无效输入的异常不能原样打印到 CI 日志。
    throw new Error("PostgreSQL test DSN must be a postgres:// or postgresql:// URL");
  }
  if (!["postgres:", "postgresql:"].includes(url.protocol)) throw new Error("PostgreSQL test DSN must be a postgres:// or postgresql:// URL");
  // pgx 的 query 参数会覆盖 pathname，重写测试库之前必须清除全部数据库别名。
  url.searchParams.delete("database");
  url.searchParams.delete("dbname");
  return url.toString();
}

export function postgresFixtureDsn(source, database) {
  if (!/^edugrade_ci_shared_[a-f0-9]{32}_test$/.test(database)) throw new Error("Unexpected database name from test fixture setup");
  const url = new URL(normalizePostgresTestDsn(source));
  url.pathname = `/${database}`;
  return url.toString();
}

// 这些用例需要额外运行环境，或只由父测试启动子进程；新的数据库测试不得加到此表绕过门禁。
const optionalTests = new Set([
  `${moduleName}/internal/server.TestBusinessCommandReservationCrashHelper`,
  `${moduleName}/internal/apicontract.TestReliabilitySharedClientContract`,
  `${moduleName}/internal/subjective.TestHTTPAdapterRealLocalAgent`,
  `${moduleName}/internal/paper.TestQuestionRubricDraftUsesPythonContractWithoutImportSources`,
  `${moduleName}/internal/paper.TestQuestionRubricSegmentsReachRealPythonWithStoredContext`,
  `${moduleName}/internal/paper.TestTemplateLibraryVisualClientAgainstRunningCV`,
]);

// 全树发现不能让关键事务用例随删除、改名或发现规则变化一起消失。
// 这些名称只固定最低必测范围；其他已发现的 PostgreSQL 用例仍必须运行且不能跳过。
export const requiredPostgresTests = Object.freeze([
  `${moduleName}/internal/auth.TestPostgresManagedSessionRevokeEnforcesTargetsAndAtomicRevocation`,
  `${moduleName}/internal/capture.TestPostgresCaptureDecodeCompletionRollsBackBeforeReceipt`,
  `${moduleName}/internal/capture.TestPostgresStudentIdentityPreventsCrossRosterAndConcurrentDoubleBinding`,
  `${moduleName}/internal/capture.TestPostgresCollectionMissingPagesAndLateReceiptFailureRecoverAtomically`,
  `${moduleName}/internal/modelgovernance.TestPostgresInvalidationRevokesDependentApprovalsAtomically`,
  `${moduleName}/internal/releasegate.TestPostgresPublicationCommitsEvidenceAndStatusTogether`,
  `${moduleName}/internal/score.TestPostgresHistoricalPublicationRemainsReadableUntilFormalVersion`,
  `${moduleName}/internal/score.TestPostgresLegacyPublicationSharesSnapshotAndSurvivesNewerRelease`,
  `${moduleName}/internal/scorerelease.TestPostgresAnonymousRegistryRollbackAndCommitFailure`,
  `${moduleName}/internal/scorerelease.TestPostgresConcurrentRegradePublicationPreservesBothCorrections`,
  `${moduleName}/internal/scorerelease.TestPostgresPublicationReceiptRollsBackLateFailureAndNeverRewindsCurrent`,
  `${moduleName}/internal/server.TestLegacyScorePublicationUsesCurrentSnapshotWithPostgresTestDatabase`,
  `${moduleName}/internal/server.TestPaperImportSourceChangesSerializeReviewWithPostgresTestDatabase`,
  `${moduleName}/internal/subjective.TestRemediationPostgresWorkerCompletionAtomicRollbackAndReplay`,
  `${moduleName}/internal/subjective.TestRemediationPostgresLeaseRotationDoesNotAcceptLateCallbacks`,
]);

export function assertRequiredPostgresTests(expected) {
  const missing = requiredPostgresTests.filter(key => !expected.has(key));
  if (missing.length) throw new Error(`Required PostgreSQL transaction tests are not discovered: ${JSON.stringify(missing)}`);
}

export function discoverPostgresTests(cwd) {
  function walk(dir) {
    return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(path.join(dir, entry.name)) : [path.join(dir, entry.name)]);
  }
  const aliases = new Set();
  const expected = new Set();
  const declared = new Set();
  const testFiles = ["internal", "tools"].flatMap(name => {
    const directory = path.join(cwd, name);
    return fs.existsSync(directory) ? walk(directory) : [];
  }).filter(file => file.endsWith("_test.go"));
  for (const file of testFiles) {
    const source = fs.readFileSync(file, "utf8");
    const matches = [...source.matchAll(/(?:Getenv|LookupEnv)\(\s*"((?:EDUGRADE|YUEJUAN)_[A-Z0-9_]*(?:DATABASE_URL|PG_DSN))"\s*\)/g)];
    for (const match of matches) aliases.add(match[1]);
    const databaseFixture = matches.length || (/(?:postgres|integration)_test\.go$/.test(file) && /pgx|postgres/i.test(source));
    const packageName = `${moduleName}/${path.relative(cwd, path.dirname(file)).split(path.sep).join("/")}`;
    for (const match of source.matchAll(/^func (Test\w+)\(t \*testing\.T\)/gm)) {
      const key = `${packageName}.${match[1]}`;
      declared.add(key);
      if (databaseFixture && !optionalTests.has(key)) expected.add(key);
    }
  }
  return { aliases, expected, declared };
}

export function createPostgresTestGate(expected) {
  const observed = new Set();
  const skipped = new Set();
  return {
    observe(event) {
      if (!event.Test) return;
      const key = `${event.Package}.${event.Test}`;
      if (expected.has(key) && (event.Action === "pass" || event.Action === "fail")) observed.add(key);
      // 子测试和经共享夹具访问数据库的测试也不能跳过，不能只依赖文件名枚举。
      if (event.Action === "skip" && !optionalTests.has(key)) skipped.add(key);
    },
    finish(status) {
      const missing = [...expected].filter(key => !observed.has(key));
      if (status !== 0 || skipped.size || missing.length) throw new Error(`PostgreSQL gate failed: exit=${status}, skipped=${JSON.stringify([...skipped])}, missing=${JSON.stringify(missing)}`);
      return observed.size;
    },
  };
}

// CI 不传参数，始终运行全树；本地修正失败后可显式复验具体包，输出必须标明局部范围。
export function postgresTestScope(requested, expected, declared = expected) {
  if (!requested.length) return { packages: ["./..."], filterArgs: [], expected, label: "full Go tree" };
  const filters = requested.filter(name => name.startsWith("--tests="));
  const packageNames = requested.filter(name => !name.startsWith("--tests="));
  const validPackage = name => /^\.\/internal\/[a-z0-9_]+$/.test(name)
    || /^\.\/tools\/[a-z0-9][a-z0-9_-]*(?:\/[a-z0-9][a-z0-9_-]*)*$/.test(name);
  if (filters.length > 1 || !packageNames.length || packageNames.some(name => !validPackage(name))) throw new Error("Local PostgreSQL rerun accepts explicit ./internal/<package> or ./tools/<path> names only");
  const tests = filters.length ? [...new Set(filters[0].slice("--tests=".length).split(","))] : [];
  if (tests.some(name => !/^Test[A-Za-z0-9_]+$/.test(name))) throw new Error("Local rerun test names must be literal top-level Go Test names");
  const packages = [...new Set(packageNames)];
  const prefixes = packages.map(name => `${moduleName}/${name.slice(2)}.`);
  // Go 的 -run 会静默忽略拼错的名字；逐个核对声明，避免部分匹配被误当成全部复验。
  const unknown = tests.filter(name => !prefixes.some(prefix => declared.has(`${prefix}${name}`)));
  if (unknown.length) throw new Error(`Unknown top-level tests in selected packages: ${unknown.join(", ")}`);
  const selected = new Set([...expected].filter(key => prefixes.some(prefix => key.startsWith(prefix)) && (!tests.length || tests.includes(key.slice(key.lastIndexOf(".") + 1)))));
  // 纯工具包可能只测解析或回放；仍由真实 Go 退出码和零跳过约束，不能借工具包放空 internal 的数据库范围。
  if (!selected.size && !packages.every(name => name.startsWith("./tools/"))) throw new Error("No required PostgreSQL tests in selected packages");
  return { packages, filterArgs: tests.length ? ["-run", `^(${tests.join("|")})$`] : [], expected: selected, label: `selected packages: ${packages.join(", ")}${tests.length ? `; selected tests: ${tests.join(", ")}` : ""}` };
}
