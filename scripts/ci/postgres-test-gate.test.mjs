import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { spawnSync } from "node:child_process";
import { createPostgresTestGate, discoverPostgresTests, normalizePostgresTestDsn, postgresFixtureDsn, postgresTestScope } from "./postgres-test-gate.mjs";

test("CI defaults to all packages and local reruns cannot masquerade as a full gate", () => {
  const expected = new Set([
    "edugrade-enterprise/services/api-gateway/internal/auth.TestDatabase",
    "edugrade-enterprise/services/api-gateway/internal/score.TestDatabase",
  ]);
  assert.equal(postgresTestScope([], expected).expected, expected);
  assert.deepEqual(postgresTestScope([], expected).packages, ["./..."]);
  const local = postgresTestScope(["./internal/auth"], expected);
  assert.match(local.label, /^selected packages:/);
  assert.equal(local.expected.size, 1);
  const filtered = postgresTestScope(["./internal/auth", "--tests=TestDatabase"], expected);
  assert.deepEqual(filtered.filterArgs, ["-run", "^(TestDatabase)$"]);
  assert.match(filtered.label, /selected tests: TestDatabase/);
  assert.throws(() => postgresTestScope(["./internal/auth", "--tests=Test.*"], expected), /literal/);
  assert.throws(() => postgresTestScope(["./internal/auth", "--tests=TestMissing"], expected), /Unknown top-level/);
  assert.throws(() => postgresTestScope(["./internal/auth", "--tests=TestDatabase,TestDatabsae"], expected), /TestDatabsae/);
  const declared = new Set([...expected, "edugrade-enterprise/services/api-gateway/internal/auth.TestMemory"]);
  const mixed = postgresTestScope(["./internal/auth", "--tests=TestDatabase,TestMemory"], expected, declared);
  assert.equal(mixed.expected.size, 1);
  assert.deepEqual(mixed.filterArgs, ["-run", "^(TestDatabase|TestMemory)$"]);
  assert.throws(() => postgresTestScope(["-run=Nothing"], expected), /explicit/);
  assert.throws(() => postgresTestScope(["./internal/missing"], expected), /No required/);
});

test("database gate refuses absent DSN before launching Go", () => {
  const env = { ...process.env };
  delete env.EDUGRADE_E2E_DATABASE_URL;
  const result = spawnSync(process.execPath, ["scripts/ci/run-postgres-tests.mjs"], { env, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /EDUGRADE_E2E_DATABASE_URL is required/);
});

test("explicit tools reruns may have no database tests but cannot bypass exit or skip checks", () => {
  const expected = new Set(["edugrade-enterprise/services/api-gateway/internal/auth.TestDatabase"]);
  const declared = new Set([...expected, "edugrade-enterprise/services/api-gateway/tools/import-fixture/nested.TestParse"]);
  const scope = postgresTestScope(["./tools/import-fixture/nested"], expected, declared);
  assert.deepEqual(scope.packages, ["./tools/import-fixture/nested"]);
  assert.equal(scope.expected.size, 0);
  assert.match(scope.label, /^selected packages:/);
  const filtered = postgresTestScope(["./tools/import-fixture/nested", "--tests=TestParse"], expected, declared);
  assert.deepEqual(filtered.filterArgs, ["-run", "^(TestParse)$"]);
  assert.throws(() => postgresTestScope(["./tools/import-fixture/nested", "--tests=TestParze"], expected, declared), /Unknown top-level/);
  assert.throws(() => postgresTestScope(["./internal/missing", "./tools/import-fixture/nested"], expected, declared), /No required/);
  for (const name of ["./tools", "./tools/../internal/auth", "./tools/a/../../b", "./tools/a//b", "./tools/*", "./tools/.hidden", "./tools/a/..."]) {
    assert.throws(() => postgresTestScope([name], expected, declared), /explicit/);
  }
  const gate = createPostgresTestGate(scope.expected);
  assert.equal(gate.finish(0), 0);
  assert.throws(() => gate.finish(1), /exit=1/);
  gate.observe({ Package: "fixture", Test: "TestParse", Action: "skip" });
  assert.throws(() => gate.finish(0), /TestParse/);
});

test("invalid DSNs fail before launching Go without echoing possible passwords", () => {
  for (const value of ["host=localhost user=test password=do-not-log", "file:///private", "https://example.invalid/postgres"]) {
    const result = spawnSync(process.execPath, ["scripts/ci/run-postgres-tests.mjs"], { env: { ...process.env, EDUGRADE_E2E_DATABASE_URL: value }, encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /DSN must be a postgres:\/\/ or postgresql:\/\/ URL/);
    assert.doesNotMatch(result.stderr, /do-not-log/);
  }
});

test("fixture URLs cannot be redirected by database query aliases", () => {
  const database = `edugrade_ci_shared_${"a".repeat(32)}_test`;
  for (const protocol of ["postgres", "postgresql"]) {
    const source = `${protocol}://synthetic@127.0.0.1:52837/original?sslmode=disable&database=original&dbname=original&dbname=other`;
    const clean = new URL(normalizePostgresTestDsn(source));
    assert.equal(clean.pathname, "/original");
    const target = new URL(postgresFixtureDsn(source, database));
    assert.equal(target.pathname, `/${database}`);
    assert.equal(target.searchParams.get("sslmode"), "disable");
    assert.equal(target.searchParams.has("database"), false);
    assert.equal(target.searchParams.has("dbname"), false);
    assert.equal(target.hostname, "127.0.0.1");
  }
  for (const database of ["original", "edugrade_ci_shared_existing", `edugrade_ci_shared_${"a".repeat(32)}`, `edugrade_ci_shared_${"a".repeat(31)}_test`, `edugrade_ci_shared_${"A".repeat(32)}_test`]) {
    assert.throws(() => postgresFixtureDsn("postgres://synthetic@localhost/original", database), /Unexpected database name/);
  }
});

test("discovery maps legacy variables and distinguishes packages with equal test names", () => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "postgres-gate-"));
  try {
    for (const name of ["one", "two"]) {
      const directory = path.join(cwd, "internal", name);
      fs.mkdirSync(directory, { recursive: true });
      fs.writeFileSync(path.join(directory, "store_test.go"), 'package fixture\nfunc fixture() { os.Getenv("YUEJUAN_REVIEW_PG_DSN"); os.Getenv("EDUGRADE_E2E_DATABASE_URL") }\nfunc TestDatabase(t *testing.T) {}\n');
    }
    fs.writeFileSync(path.join(cwd, "internal", "one", "memory_test.go"), 'package fixture\nfunc TestMemory(t *testing.T) {}\n');
    const toolDirectory = path.join(cwd, "tools", "nested", "fixture");
    fs.mkdirSync(toolDirectory, { recursive: true });
    fs.writeFileSync(path.join(toolDirectory, "parse_test.go"), 'package fixture\nfunc TestParse(t *testing.T) {}\n');
    const found = discoverPostgresTests(cwd);
    assert.deepEqual([...found.aliases], ["YUEJUAN_REVIEW_PG_DSN", "EDUGRADE_E2E_DATABASE_URL"]);
    assert.equal(found.expected.size, 2);
    assert.equal(found.declared.size, 4);
    assert.equal(found.expected.has("edugrade-enterprise/services/api-gateway/internal/one.TestMemory"), false);
    assert.equal(found.declared.has("edugrade-enterprise/services/api-gateway/internal/one.TestMemory"), true);
    assert.equal(found.declared.has("edugrade-enterprise/services/api-gateway/tools/nested/fixture.TestParse"), true);
    const gate = createPostgresTestGate(found.expected);
    gate.observe({ Package: "edugrade-enterprise/services/api-gateway/internal/one", Test: "TestDatabase", Action: "pass" });
    assert.throws(() => gate.finish(0), /internal\/two.TestDatabase/);
  } finally { fs.rmSync(cwd, { recursive: true, force: true }); }
});

test("passing parent cannot hide skipped database subtests or unenumerated fixture callers", () => {
  const gate = createPostgresTestGate(new Set(["pkg.TestDatabase"]));
  gate.observe({ Package: "pkg", Test: "TestDatabase", Action: "pass" });
  gate.observe({ Package: "pkg", Test: "TestDatabase/concurrent", Action: "skip" });
  gate.observe({ Package: "pkg", Test: "TestSharedFixture", Action: "skip" });
  assert.throws(() => gate.finish(0), /TestDatabase\/concurrent.*TestSharedFixture/);
});

test("required passes and documented crash helper satisfy gate", () => {
  const gate = createPostgresTestGate(new Set(["pkg.TestDatabase"]));
  gate.observe({ Package: "pkg", Test: "TestDatabase", Action: "pass" });
  gate.observe({ Package: "edugrade-enterprise/services/api-gateway/internal/server", Test: "TestBusinessCommandReservationCrashHelper", Action: "skip" });
  assert.equal(gate.finish(0), 1);
  assert.throws(() => gate.finish(1), /exit=1/);
});
