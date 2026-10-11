import assert from "node:assert/strict";
import { constants } from "node:fs";
import { test } from "node:test";

import { resolveTrustedExecutable } from "./trusted-executable.mjs";

function installation({ platform = "linux", redirects = new Map(), missing = new Set(), regular = true, executable = true } = {}) {
  const accesses = [];
  return {
    accesses,
    options: {
      platform,
      workspaceRoot: platform === "win32" ? "D:\\project\\yuejuan" : "/workspace/yuejuan",
      fileSystem: {
        realpathSync(value) {
          if (missing.has(value)) throw new Error("ENOENT");
          return redirects.get(value) ?? value;
        },
        statSync() { return { isFile: () => regular }; },
        accessSync(value, mode) {
          accesses.push([value, mode]);
          if (!executable) throw new Error("EACCES");
        },
      },
    },
  };
}

test("trusted tools use fixed installation paths despite a prepended fake PATH", () => {
  const fixture = installation();
  const env = { PATH: "/workspace/yuejuan/node_modules/.bin:/fake-tools" };
  assert.equal(resolveTrustedExecutable("go", { ...fixture.options, env }), "/usr/local/go/bin/go");
  assert.equal(resolveTrustedExecutable("docker", { ...fixture.options, env }), "/usr/bin/docker");
  assert.deepEqual(fixture.accesses, [["/usr/local/go/bin/go", constants.X_OK], ["/usr/bin/docker", constants.X_OK]]);
});

test("explicit executable paths cannot be missing, relative, local dependencies or temporary files", () => {
  const fixture = installation({ missing: new Set(["/opt/missing/go"]) });
  for (const candidate of ["", "go", "./go", "/opt/missing/go", "/workspace/yuejuan/bin/go", "/opt/node_modules/bin/go", "/tmp/go", "/var/tmp/go"]) {
    assert.throws(() => resolveTrustedExecutable("go", {
      ...fixture.options,
      env: { EDUGRADE_GO_EXECUTABLE: candidate, PATH: "/fake-tools" },
    }), /absolute executable path|ENOENT|outside the repository/);
  }
  assert.deepEqual(fixture.accesses, []);
  assert.throws(() => resolveTrustedExecutable("go", {
    ...fixture.options,
    env: { EDUGRADE_GO_EXECUTABLE: "/runner-temp/go", RUNNER_TEMP: "/runner-temp" },
  }), /temporary directories/);
  const redirectedTemp = installation({ redirects: new Map([["/runner-temp", "/real-runner-temp"]]) });
  assert.throws(() => resolveTrustedExecutable("go", {
    ...redirectedTemp.options,
    env: { EDUGRADE_GO_EXECUTABLE: "/real-runner-temp/go", RUNNER_TEMP: "/runner-temp" },
  }), /temporary directories/);
});

test("real paths cannot redirect an executable outside its configured installation", () => {
  // 同时模拟文件链接和上级目录链接；即使目标在另一处系统目录，也要求显式配置真实路径。
  for (const target of ["/workspace/yuejuan/go", "/opt/node_modules/go", "/tmp/go", "/other-install/go"]) {
    const fixture = installation({ redirects: new Map([["/opt/install/go", target]]) });
    assert.throws(() => resolveTrustedExecutable("go", {
      ...fixture.options,
      env: { EDUGRADE_GO_EXECUTABLE: "/opt/install/go" },
    }), /outside the repository|symbolic-link redirection/);
    assert.deepEqual(fixture.accesses, []);
  }
});

test("a linked workspace is rejected by both the configured root and its real root", () => {
  const fixture = installation({ redirects: new Map([["/workspace/yuejuan", "/real-workspace"]]) });
  for (const candidate of ["/workspace/yuejuan/go", "/real-workspace/go"]) {
    assert.throws(() => resolveTrustedExecutable("go", {
      ...fixture.options,
      env: { EDUGRADE_GO_EXECUTABLE: candidate },
    }), /outside the repository/);
  }
});

test("tools must be regular executable files and unsupported platforms do not search PATH", () => {
  for (const overrides of [{ regular: false }, { executable: false }]) {
    const fixture = installation(overrides);
    assert.throws(() => resolveTrustedExecutable("go", { ...fixture.options, env: {} }), /regular file|EACCES/);
  }
  assert.throws(() => resolveTrustedExecutable("go", {
    ...installation({ platform: "freebsd" }).options, env: { PATH: "/fake-tools" },
  }), /absolute executable path/);
});

test("Windows paths preserve spaces, require a qualified .exe and reject redirection", () => {
  const fixture = installation({ platform: "win32" });
  assert.equal(resolveTrustedExecutable("go", { ...fixture.options, env: {} }), String.raw`C:\Program Files\Go\bin\go.exe`);
  assert.equal(resolveTrustedExecutable("docker", { ...fixture.options, env: {} }), String.raw`C:\Program Files\Docker\Docker\resources\bin\docker.exe`);
  const candidate = "C:\\Program Files\\Trusted Tools & Co\\docker.exe";
  assert.equal(resolveTrustedExecutable("docker", {
    ...fixture.options, env: { EDUGRADE_DOCKER_EXECUTABLE: candidate },
  }), candidate);
  for (const value of ["C:docker.exe", "\\docker.exe", "C:\\Tools\\docker.cmd", "D:\\project\\yuejuan\\docker.exe", "C:\\Tools\\NODE_MODULES\\docker.exe", String.raw`C:\Windows\Temp\docker.exe`]) {
    assert.throws(() => resolveTrustedExecutable("docker", {
      ...fixture.options, env: { EDUGRADE_DOCKER_EXECUTABLE: value },
    }), /absolute executable path|\.exe file|outside the repository/);
  }
  const redirected = installation({ platform: "win32", redirects: new Map([[candidate, "C:\\Other Tools\\docker.exe"]]) });
  assert.throws(() => resolveTrustedExecutable("docker", {
    ...redirected.options, env: { EDUGRADE_DOCKER_EXECUTABLE: candidate },
  }), /symbolic-link redirection/);
});

test("the default Windows temporary directory cannot provide Go through its path or real path", () => {
  const directory = String.raw`C:\Windows\Temp`;
  const actualDirectory = String.raw`D:\ActualWindowsTemp`;
  const candidate = String.raw`C:\Windows\Temp\go.exe`;
  const actualCandidate = String.raw`D:\ActualWindowsTemp\go.exe`;
  const installedCandidate = String.raw`C:\Tools\go.exe`;
  for (const [value, redirects] of [
    [candidate, new Map()],
    [actualCandidate, new Map([[directory, actualDirectory]])],
    [installedCandidate, new Map([[installedCandidate, candidate]])],
  ]) {
    const fixture = installation({ platform: "win32", redirects });
    assert.throws(() => resolveTrustedExecutable("go", {
      ...fixture.options, env: { EDUGRADE_GO_EXECUTABLE: value },
    }), /temporary directories/);
    assert.deepEqual(fixture.accesses, []);
  }
});

test("temporary environment roots and their real paths are rejected before executable access", () => {
  // 根目录链接和程序链接分别模拟两种重定向；临时路径只能参与拒绝判断，不能作为返回的安装路径。
  const platforms = [
    { platform: "linux", directory: "/tool-temp", actualDirectory: "/actual-tool-temp", candidate: "/tool-temp/go", actualCandidate: "/actual-tool-temp/go", installedCandidate: "/opt/installed/go" },
    { platform: "win32", directory: String.raw`C:\ToolTemp`, actualDirectory: String.raw`D:\ActualToolTemp`, candidate: String.raw`C:\ToolTemp\go.exe`, actualCandidate: String.raw`D:\ActualToolTemp\go.exe`, installedCandidate: String.raw`C:\InstalledTools\go.exe` },
  ];
  for (const { platform, directory, actualDirectory, candidate, actualCandidate, installedCandidate } of platforms) {
    for (const variable of ["RUNNER_TEMP", "TEMP", "TMP", "TMPDIR"]) {
      for (const [value, redirects] of [
        [candidate, new Map()],
        [actualCandidate, new Map([[directory, actualDirectory]])],
        [installedCandidate, new Map([[installedCandidate, actualCandidate], [directory, actualDirectory]])],
      ]) {
        const fixture = installation({ platform, redirects });
        assert.throws(() => resolveTrustedExecutable("go", {
          ...fixture.options, env: { EDUGRADE_GO_EXECUTABLE: value, [variable]: directory },
        }), /temporary directories/);
        assert.deepEqual(fixture.accesses, []);
      }
    }
  }
});

test("setup-go paths use the actual stable version and validated tool cache architecture", () => {
  const fixture = installation();
  const env = { EDUGRADE_GO_TOOL_CACHE: "/opt/hostedtoolcache", EDUGRADE_GO_VERSION: "1.26.6", EDUGRADE_GO_ARCH: "x64" };
  assert.equal(resolveTrustedExecutable("go", { ...fixture.options, env }), "/opt/hostedtoolcache/go/1.26.6/x64/bin/go");
  for (const invalid of [
    { EDUGRADE_GO_TOOL_CACHE: "relative-cache" },
    { EDUGRADE_GO_VERSION: undefined },
    { EDUGRADE_GO_VERSION: "go1.26.6" },
    { EDUGRADE_GO_VERSION: "1.26.6/../../evil" },
    { EDUGRADE_GO_VERSION: "1.26.6\n" },
    { EDUGRADE_GO_ARCH: "../../evil" },
    { EDUGRADE_GO_EXECUTABLE: "/another-install/go" },
  ]) {
    assert.throws(() => resolveTrustedExecutable("go", { ...fixture.options, env: { ...env, ...invalid } }), /setup-go/);
  }
  const candidate = "/opt/hostedtoolcache/go/1.26.6/x64/bin/go";
  const redirected = installation({ redirects: new Map([[candidate, "/other-cache/go"]]) });
  assert.throws(() => resolveTrustedExecutable("go", { ...redirected.options, env }), /symbolic-link redirection/);
});
