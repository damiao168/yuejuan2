import assert from "node:assert/strict";
import { readFileSync, realpathSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { coverageFailures, main, parseCoverage } from "./check-go-coverage.mjs";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const floors = JSON.parse(readFileSync(join(repositoryRoot, "contracts", "testing", "coverage-floor.json"), "utf8")).packages;
const executable = realpathSync(process.execPath);

function coverageRun(result, env = {}) {
  const calls = [];
  const output = [];
  const errors = [];
  const logs = [];
  const status = main({
    env: { EDUGRADE_GO_EXECUTABLE: executable, ...env },
    spawn: (...args) => { calls.push(args); return result; },
    stdout: { write: (value) => output.push(["stdout", value]) },
    stderr: { write: (value) => output.push(["stderr", value]) },
    log: { error: (value) => errors.push(value), log: (value) => logs.push(value) },
  });
  return { status, calls, output, errors, logs };
}

test("Go coverage fixes the executable, arguments and local toolchain despite fake PATH entries", () => {
  // 用 spawn 替身观察边界；PATH 包含同名工具和版本工具链时也不能改变启动文件或启用自动切换。
  const env = { PATH: "/fake-go:/fake-go1.26.6:/workspace/node_modules/.bin", GOTOOLCHAIN: "path", FIXTURE_VALUE: "kept" };
  const report = Object.keys(floors).map((name) => `ok example/${name} coverage: 100% of statements`).join("\n");
  const result = coverageRun({ status: 0, stdout: report, stderr: "fixture warning" }, env);
  assert.equal(result.status, 0);
  assert.deepEqual(result.calls, [[executable,
    ["test", "-cover", "-count=1", ...Object.keys(floors).map((name) => `./internal/${name}`)],
    {
      cwd: join(repositoryRoot, "services", "api-gateway"),
      encoding: "utf8",
      maxBuffer: 20 * 1024 * 1024,
      shell: false,
      env: { EDUGRADE_GO_EXECUTABLE: executable, ...env, GOTOOLCHAIN: "local" },
    },
  ]]);
  assert.deepEqual(result.output, [["stdout", report], ["stderr", "fixture warning"]]);
  assert.deepEqual(result.logs, ["Go coverage floors passed"]);
  assert.deepEqual(result.errors, []);
  assert.equal(env.GOTOOLCHAIN, "path");
});

test("Go coverage keeps process failure statuses, startup errors and floor enforcement", () => {
  for (const status of [7, null]) {
    const result = coverageRun({ status, stdout: "fixture output", stderr: "fixture stderr" });
    assert.equal(result.status, status ?? 1);
    assert.deepEqual(result.output, [["stdout", "fixture output"], ["stderr", "fixture stderr"]]);
    assert.deepEqual(result.errors, []);
  }
  const failed = coverageRun({ status: null, error: new Error("fixture-start-error") });
  assert.equal(failed.status, 1);
  assert.deepEqual(failed.errors, ["Unable to run Go coverage tests: fixture-start-error"]);
  const missing = coverageRun({ status: 0, stdout: "", stderr: "" });
  assert.equal(missing.status, 1);
  assert.deepEqual(missing.errors, Object.keys(floors).map((name) => `coverage violation: ${name}: no coverage result`));
});

test("Go coverage rejects an invalid configured executable before starting a process", () => {
  const result = coverageRun({ status: 0 }, { EDUGRADE_GO_EXECUTABLE: "go" });
  assert.equal(result.status, 1);
  assert.deepEqual(result.calls, []);
  assert.match(result.errors[0], /Unable to run Go coverage tests:.*PATH lookup is disabled/);
});

test("coverage ratchet checks every required package and rejects regressions", () => {
  const report = `ok  edugrade-enterprise/services/api-gateway/internal/auth  1.2s  coverage: 57.1% of statements
ok  edugrade-enterprise/services/api-gateway/internal/capture  1.2s  coverage: 11.3% of statements
`;
  const failures = coverageFailures(parseCoverage(report), { auth: 57.1, capture: 11.4, subjective: 63.4 });
  assert.deepEqual(failures, [
    "capture: 11.3% is below the 11.4% floor",
    "subjective: no coverage result",
  ]);
});

test("coverage parser preserves numeric, marker and line boundaries", () => {
  const cases = [
    ["ok example/auth (cached) coverage: 0% of statements", [["auth", 0]]],
    ["ok\texample/auth\t1.2s\tcoverage:\u00a057.1% of statements\t\r\n", [["auth", 57.1]]],
    ["ok example/auth arbitrary details coverage: 101.5% of statements", [["auth", 101.5]]],
    ["ok example/auth coverage: invalid then coverage: 42% of statements", [["auth", 42]]],
    ["ok example/auth details-coverage: 42% of statements", [["auth", 42]]],
    ["ok example/auth coverage: 42% of statements then coverage: invalid", []],
    ["ok example/auth details_coverage: 42% of statements", []],
    ["ok example/auth uncoverage: 42% of statements", []],
    ["ok example/auth coverage: .5% of statements", []],
    ["ok example/auth coverage: 5.% of statements", []],
    ["ok example/auth coverage: -5% of statements", []],
    ["ok example/auth coverage: 1e2% of statements", []],
    ["ok example/auth coverage: 42%  of statements", []],
    ["ok example/auth coverage: 42% of statements trailing", []],
    [" ok example/auth coverage: 42% of statements", []],
    ["okay example/auth coverage: 42% of statements", []],
    ["FAIL example/auth coverage: 42% of statements", []]
  ];
  for (const [output, expected] of cases) {
    assert.deepEqual([...parseCoverage(output)], expected, output);
  }
});

test("coverage parser retains cross-line whitespace and global match consumption", () => {
  // 旧规则的空白匹配允许跨行，包后的说明文本则受当前行限制；不要无意收紧输入。
  const cases = [
    ["ok\nexample/auth\n(cached) coverage: 42% of statements", [["auth", 42]]],
    ["ok example/auth coverage:\n42% of statements", [["auth", 42]]],
    ["ok example/auth details\ncoverage: 42% of statements", []],
    ["ok example/auth\nok example/capture coverage: 42% of statements", [["auth", 42]]],
    ["ok example/auth\nok example/capture no result\nok example/subjective coverage: 63.4% of statements", [["subjective", 63.4]]],
    ["ok example/auth coverage: 42% of statements \n\n\tok example/capture coverage: 11% of statements", [["auth", 42]]],
    ["ok example/auth coverage: 42% of statements\r\nok example/capture coverage: 11% of statements", [["auth", 42], ["capture", 11]]],
    ["ok\u2028example/auth\u2029coverage: 42% of statements", [["auth", 42]]],
    ["ok example/auth details\u2028coverage: 42% of statements", []],
    ["ok example/auth coverage: 42% of statements\nnoise on a later line", [["auth", 42]]]
  ];
  for (const [output, expected] of cases) {
    assert.deepEqual([...parseCoverage(output)], expected, output);
  }
});

test("coverage parser keeps the last result for duplicate package basenames", () => {
  const output = [
    "ok first/auth coverage: 57.1% of statements",
    "? example/ignored [no test files]",
    "ok example/capture coverage: 11.3% of statements",
    "ok second/auth coverage: 58% of statements"
  ].join("\n");
  assert.deepEqual([...parseCoverage(output)], [["auth", 58], ["capture", 11.3]]);
});

test("coverage parser handles long missing suffixes and many earlier markers", () => {
  const details = "coverage: not-a-number ".repeat(20_000);
  assert.deepEqual([...parseCoverage(`ok example/auth ${details}`)], []);
  assert.deepEqual([...parseCoverage(`ok example/auth ${details}coverage: 57.1% of statements`)], [["auth", 57.1]]);
});
