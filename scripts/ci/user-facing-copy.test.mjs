import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { checkUserFacingCopy } from "../check-user-facing-copy.mjs";

const mockSource = 'getUserErrorMessage: (error) => error instanceof Error ? error.message : "失败"';

test("test and spec TypeScript fixtures are excluded by suffix", () => {
  for (const suffix of ["test.ts", "test.tsx", "spec.ts", "spec.tsx"]) {
    assert.deepEqual(checkUserFacingCopy(mockSource, `apps/web-admin/src/Example.batch9.${suffix}`), []);
    assert.deepEqual(checkUserFacingCopy(mockSource, `apps\\desktop-client\\src\\Example.${suffix}`), []);
  }
});

test("test-like names and directories do not exempt production copy", () => {
  for (const path of [
    "apps/web-admin/src/Example.test-helper.ts",
    "apps/web-admin/src/Example.spec-helper.tsx",
    "apps/web-admin/src/tests/ProductionView.tsx",
    "apps/desktop-client/src/ProductionView.tsx"
  ]) {
    assert.equal(checkUserFacingCopy(mockSource, path).length, 1);
  }
});

test("all existing production copy rules still block unsafe output", () => {
  const unsafeLines = [
    "const text = response.statusText;",
    "message.error(error.message);",
    "<Alert description={error.message} />",
    "setLoadError(error.message);",
    "return error.message;",
    "const text = error instanceof Error ? error.message : fallback;",
    "const text = statusLabels[item.status] ?? item.status;"
  ];
  for (const line of unsafeLines) {
    const failures = checkUserFacingCopy(`\n${line}`, "apps/web-admin/src/ProductionView.tsx");
    assert.equal(failures.length, 1, line);
    assert.match(failures[0], /^apps\/web-admin\/src\/ProductionView\.tsx:2 /u);
  }
});

test("safe error conversion, logging and existing converter allowlist stay permitted", () => {
  const source = 'message.error(getUserErrorMessage(error, "操作失败"));\nconsole.error(error.message);\nlogEvent(error.message);';
  assert.deepEqual(checkUserFacingCopy(source, "apps/web-admin/src/ProductionView.tsx"), []);
  assert.deepEqual(checkUserFacingCopy(mockSource, "apps/web-admin/src/api/userError.ts"), []);
  assert.deepEqual(checkUserFacingCopy(mockSource, "apps/desktop-client/src/api/userError.ts"), []);
});

test("CLI ignores test fixtures and exits unsuccessfully for production Error.message", () => {
  const fixtureParent = resolve(tmpdir());
  const fixtureRoot = mkdtempSync(join(fixtureParent, "user-facing-copy-"));
  const script = fileURLToPath(new URL("../check-user-facing-copy.mjs", import.meta.url));
  try {
    for (const app of ["web-admin", "desktop-client"]) {
      mkdirSync(join(fixtureRoot, "apps", app, "src"), { recursive: true });
      writeFileSync(join(fixtureRoot, "apps", app, "src", "Example.test.tsx"), mockSource);
    }
    const run = () => spawnSync(process.execPath, [script], { cwd: fixtureRoot, encoding: "utf8" });
    assert.equal(run().status, 0);

    // 同一目录再加入生产文件，退出码与错误位置仍须阻止门禁放行。
    writeFileSync(join(fixtureRoot, "apps", "web-admin", "src", "ProductionView.tsx"), "message.error(error.message);");
    const result = run();
    assert.equal(result.status, 1);
    assert.match(result.stderr, /apps\/web-admin\/src\/ProductionView\.tsx:1/u);
  } finally {
    assert.equal(dirname(resolve(fixtureRoot)), fixtureParent);
    rmSync(fixtureRoot, { recursive: true, force: true });
  }
});

test("status enum fallback keeps the original textual bracket and identifier boundaries", () => {
  const cases = [
    ["statusLabels[item.status]??status", true],
    ["statusLabels[item.status] ?? item.status.detail", true],
    ["mystatusLabels[item.status] ?? status", true],
    ["statusLabels[item.status]\u00a0??\titem.status", true],
    ["statusLabels[item.status]\u2028??\u2029status", true],
    ["statusLabels[statusLabels[] ?? status", true],
    ["statusLabels[] ?? status; statusLabels[item.status] ?? status", true],
    ["statusLabels[first] ?? fallback; statusLabels[second] ?? item.status", true],
    ["statusLabels[cache[key]] ?? status", false],
    ["statusLabels[] ?? status", false],
    ["statusLabels[item.status] ?? task.value.status", false],
    ["statusLabels[item.status] ?? statusCode", false],
    ["statusLabels[item.status] ? status : fallback", false],
    ["statusLabels[item.status ?? status", false]
  ];
  for (const [source, unsafe] of cases) {
    const failures = checkUserFacingCopy(source, "apps/web-admin/src/ProductionView.tsx");
    assert.deepEqual(failures, unsafe
      ? ["apps/web-admin/src/ProductionView.tsx:1 状态标签回退到机器枚举"]
      : [], source);
  }
});

test("status enum fallback handles repeated markers and long missing brackets", () => {
  const nestedMarkers = "statusLabels[".repeat(20_000);
  const earlierSafeLookups = "statusLabels[item] ?? fallback; ".repeat(2_000);
  assert.deepEqual(checkUserFacingCopy(nestedMarkers, "apps/web-admin/src/ProductionView.tsx"), []);
  assert.equal(checkUserFacingCopy(`${nestedMarkers}] ?? status`, "apps/web-admin/src/ProductionView.tsx").length, 1);
  assert.deepEqual(checkUserFacingCopy(`${earlierSafeLookups}statusLabels[] ?? status`, "apps/web-admin/src/ProductionView.tsx"), []);
  assert.equal(checkUserFacingCopy(`${earlierSafeLookups}statusLabels[item] ?? item.status`, "apps/web-admin/src/ProductionView.tsx").length, 1);
});

test("返回原始错误的文本检查保留分号、空白和标识符边界", () => {
  const previous = /return\s+[^\n;]*\b\w*[Ee]rror\.message/;
  const returns = ["return ", "prereturn\t", "return\u2028", "return", "return; ", "return .message; return "];
  const values = ["error.message", "Error.message", "loadError.message", "terror.message", "$error.message", "中文error.message", "error.messageDetail", "eRror.message", "error .message", "error?.message", "\"error\".message", "value; error.message", "value.message; return error.message"];
  for (const prefix of returns) {
    for (const value of values) {
      const source = `${prefix}${value}`;
      const failure = checkUserFacingCopy(source, "apps/web-admin/src/ProductionView.tsx").some((entry) => entry.endsWith(" 返回原生 Error.message"));
      assert.equal(failure, previous.test(source), source);
    }
  }
});

test("长空白与缺少原始错误属性的返回表达式不能绕过检查或重复尝试后缀", () => {
  const safe = `return ${" ".repeat(100_000)}value.message;`;
  assert.deepEqual(checkUserFacingCopy(safe, "apps/web-admin/src/ProductionView.tsx"), []);
  assert.equal(checkUserFacingCopy(`return ${"x.message ".repeat(10_000)}loadError.message`, "apps/web-admin/src/ProductionView.tsx").length, 1);
  assert.deepEqual(checkUserFacingCopy(`${"error.message; ".repeat(10_000)}return value`, "apps/web-admin/src/ProductionView.tsx"), []);
});
