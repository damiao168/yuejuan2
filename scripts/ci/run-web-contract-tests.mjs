import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");

// 静态字串只能检查接口与文案；权限和状态门禁必须执行真实组件行为，组件拆分不能改变验收含义。
export function runWebContractTests(files) {
  const result = spawnSync(process.execPath, [join(root, "node_modules/vitest/vitest.mjs"), "run", ...files, "--maxWorkers=2"], {
    cwd: join(root, "apps/web-admin"), stdio: "inherit", env: process.env
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, "web product contract behavior tests failed");
}
