import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { execFileSync } from "node:child_process";
import { test } from "node:test";
import assert from "node:assert/strict";

const script = resolve("infra/docker-compose/scripts/sync-local-grading-model-key.ps1");
test("本地密钥只能同步至显式选择的受管本地连接", () => {
  const dir = mkdtempSync(join(tmpdir(), "edugrade-key-owner-"));
  const config = join(dir, ".env");
  const key = join(dir, "synthetic.key");
  const original = "EDUGRADE_GRADING_MODEL_API_KEY=synthetic-external-key\n";
  writeFileSync(key, "synthetic-local-key");
  // 测试只写临时合成配置；不能让当前终端的部署覆盖变量改变待验证的连接。
  const env = { ...process.env };
  delete env.EDUGRADE_GRADING_MODEL_BASE_URL;
  delete env.EDUGRADE_AI_ADAPTER_TYPE;
  try {
    for (const [url, adapter, managed, changed] of [
      ["https://external.invalid/v1", "openai", true, false],
      ["http://host.docker.internal:8087/v1", "openai", true, false],
      ["http://host.docker.internal:8087/v1", "local_llama_cpp", false, false],
      ["http://host.docker.internal:8087/v1", "local_llama_cpp", true, true]
    ]) {
      const input = `${original}EDUGRADE_GRADING_MODEL_BASE_URL=${url}\nEDUGRADE_AI_ADAPTER_TYPE=${adapter}\n`;
      writeFileSync(config, input);
      const output = execFileSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-EnvFile", config, "-KeyFile", key, ...(managed ? ["-ManagedLocal"] : [])], { encoding: "utf8", env });
      assert.equal(readFileSync(config, "utf8").includes("API_KEY=synthetic-local-key"), changed);
      if (!changed) assert.equal(readFileSync(config, "utf8"), input);
      assert.ok(!output.includes("synthetic-local-key") && !output.includes("synthetic-external-key"));
    }
  } finally {
    assert.equal(dirname(resolve(dir)), resolve(tmpdir()));
    assert.ok(dir.includes("edugrade-key-owner-"));
    rmSync(dir, { recursive: true });
  }
});
