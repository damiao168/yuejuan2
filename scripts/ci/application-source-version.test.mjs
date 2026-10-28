import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { execFileSync } from "node:child_process";

test("运行源码指纹纳入未提交新增文件，文档和测试不触发运行版本变化", () => {
  const root = mkdtempSync(join(tmpdir(), "edugrade-source-version-"));
  const helper = resolve("scripts/application-source-version.ps1");
  try {
    execFileSync("git", ["init", "-q", root]);
    mkdirSync(join(root, "apps/web-admin/src"), { recursive: true });
    const source = join(root, "apps/web-admin/src/app.ts");
    writeFileSync(source, "export const version = 1;\n");
    execFileSync("git", ["-C", root, "add", "."]);
    execFileSync("git", ["-C", root, "-c", "user.name=Synthetic test", "-c", "user.email=test@invalid.example", "commit", "-qm", "fixture"]);
    const runner = join(root, "inspect.ps1");
    writeFileSync(runner, 'param([string]$Helper,[string]$Root)\n. $Helper\nGet-ApplicationSourceVersion $Root | ConvertTo-Json -Compress\n');
    const read = () => JSON.parse(execFileSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner, "-Helper", helper, "-Root", root], { encoding: "utf8" }));
    const before = read();
    writeFileSync(join(root, "apps/web-admin/src/app.test.ts"), "synthetic test\n");
    writeFileSync(join(root, "apps/web-admin/README.md"), "synthetic documentation\n");
    assert.deepEqual(read(), before);
    writeFileSync(source, "export const version = 2;\n");
    const changed = read();
    assert.equal(changed.commit, before.commit); assert.notEqual(changed.fingerprint, before.fingerprint);
    writeFileSync(join(root, "apps/web-admin/src/new.ts"), "export const added = true;\n");
    assert.notEqual(read().fingerprint, changed.fingerprint);
    mkdirSync(join(root, "ai-services/prompts"), { recursive: true });
    const prompt = join(root, "ai-services/prompts/synthetic.md");
    writeFileSync(prompt, "synthetic runtime prompt v1\n");
    const promptVersion = read();
    writeFileSync(prompt, "synthetic runtime prompt v2\n");
    assert.notEqual(read().fingerprint, promptVersion.fingerprint);
  } finally {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()));
    assert.ok(root.includes("edugrade-source-version-"));
    rmSync(root, { recursive: true });
  }
});
