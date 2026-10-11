import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { resolveTrustedExecutable } from "./trusted-executable.mjs";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

export function main({ env = process.env, cwd = repositoryRoot, spawn = spawnSync, log = console } = {}) {
  let executable;
  try {
    executable = resolveTrustedExecutable("docker", { env });
  } catch (error) {
    log.error(`Unable to start actionlint: ${error.message}`);
    return 1;
  }
  // Docker CLI 是可执行文件；不经 Windows shell 解释，仓库路径始终保留为一个挂载参数。
  const result = spawn(
    executable,
    ["run", "--rm", "-v", `${cwd}:/repo`, "-w", "/repo", "rhysd/actionlint:1.7.7"],
    { stdio: "inherit", shell: false },
  );
  if (result.error) {
    log.error(`Unable to start actionlint: ${result.error.message}`);
    return 1;
  }
  return result.status ?? 1;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) process.exitCode = main();
