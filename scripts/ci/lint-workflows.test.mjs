import assert from "node:assert/strict";
import { realpathSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { main } from "./lint-workflows.mjs";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const executable = realpathSync(process.execPath);

test("actionlint uses an absolute CLI with one volume argument and no Windows shell", () => {
  // spawn 替身只记录调用；Node 的外部真实路径用于触发路径校验，不执行 Docker 或下载镜像。
  const cwd = "D:\\Repo with spaces & metacharacters (1)\\yuejuan";
  const env = { EDUGRADE_DOCKER_EXECUTABLE: executable, PATH: "D:\\fake-docker" };
  const calls = [];
  const status = main({ env, cwd, spawn: (...args) => { calls.push(args); return { status: 0 }; } });
  assert.equal(status, 0);
  assert.deepEqual(calls, [[executable,
    ["run", "--rm", "-v", `${cwd}:/repo`, "-w", "/repo", "rhysd/actionlint:1.7.7"],
    { stdio: "inherit", shell: false },
  ]]);
});

test("direct invocation mounts the script repository even when process cwd differs", () => {
  const original = process.cwd();
  const calls = [];
  try {
    process.chdir(dirname(executable));
    main({ env: { EDUGRADE_DOCKER_EXECUTABLE: executable }, spawn: (...args) => { calls.push(args); return { status: 0 }; } });
  } finally {
    process.chdir(original);
  }
  assert.equal(calls[0][1][3], `${repositoryRoot}:/repo`);
});

test("actionlint preserves process status and startup error reporting", () => {
  const env = { EDUGRADE_DOCKER_EXECUTABLE: executable };
  for (const status of [0, 17, null]) {
    assert.equal(main({ env, spawn: () => ({ status }) }), status ?? 1);
  }
  const errors = [];
  assert.equal(main({ env, spawn: () => ({ status: null, error: new Error("fixture-start-error") }), log: { error: (value) => errors.push(value) } }), 1);
  assert.deepEqual(errors, ["Unable to start actionlint: fixture-start-error"]);
});

test("actionlint rejects an invalid configured CLI before starting a process", () => {
  let called = false;
  const errors = [];
  assert.equal(main({
    env: { EDUGRADE_DOCKER_EXECUTABLE: "docker", PATH: "D:\\fake-docker" },
    spawn: () => { called = true; return { status: 0 }; },
    log: { error: (value) => errors.push(value) },
  }), 1);
  assert.equal(called, false);
  assert.match(errors[0], /Unable to start actionlint:.*PATH lookup is disabled/);
});
