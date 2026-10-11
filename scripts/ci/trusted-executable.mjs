import { accessSync, constants, realpathSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const toolSettings = {
  go: {
    variable: "EDUGRADE_GO_EXECUTABLE",
    paths: {
      linux: "/usr/local/go/bin/go",
      darwin: "/usr/local/go/bin/go",
      win32: String.raw`C:\Program Files\Go\bin\go.exe`,
    },
  },
  docker: {
    variable: "EDUGRADE_DOCKER_EXECUTABLE",
    paths: {
      linux: "/usr/bin/docker",
      darwin: "/Applications/Docker.app/Contents/Resources/bin/docker",
      win32: String.raw`C:\Program Files\Docker\Docker\resources\bin\docker.exe`,
    },
  },
};

function withinDirectory(candidate, directory, pathApi) {
  const relative = pathApi.relative(directory, candidate);
  return relative === "" || (
    relative !== ".." && !relative.startsWith(`..${pathApi.sep}`) && !pathApi.isAbsolute(relative)
  );
}

function inNodeModules(candidate, pathApi) {
  return pathApi.normalize(candidate).split(pathApi.sep).some((part) => part.toLowerCase() === "node_modules");
}

function setupGoExecutable(env, platform, pathApi) {
  const cache = env.EDUGRADE_GO_TOOL_CACHE;
  const version = env.EDUGRADE_GO_VERSION;
  const arch = env.EDUGRADE_GO_ARCH;
  if (cache === undefined && version === undefined && arch === undefined) return undefined;
  // 固定 setup-go 只提供版本输出；缓存目录必须由 runner 提供，版本和架构不能作为任意路径片段。
  if (typeof cache !== "string" || !pathApi.isAbsolute(cache)
    || typeof version !== "string" || !/^\d+\.\d+\.\d+$/.test(version) || version.trim() !== version
    || !["x64", "arm64"].includes(arch)) {
    throw new Error("setup-go requires an absolute EDUGRADE_GO_TOOL_CACHE, stable EDUGRADE_GO_VERSION and EDUGRADE_GO_ARCH (x64 or arm64)");
  }
  return pathApi.join(cache, "go", version, arch, "bin", platform === "win32" ? "go.exe" : "go");
}

function temporaryDirectories(env, platform, pathApi) {
  const defaults = platform === "win32"
    ? [String.raw`C:\Windows\Temp`]
    : ["/tmp", "/var/tmp", "/private/tmp", "/private/var/tmp"];
  return [...defaults, tmpdir(), env.RUNNER_TEMP, env.TMPDIR, env.TEMP, env.TMP]
    .filter((value) => typeof value === "string" && pathApi.isAbsolute(value))
    .map((value) => pathApi.resolve(value));
}

// 运维提供安装路径，或采用明确的系统安装位置；缺失时不能再从 PATH 查找同名程序。
// 文件和真实路径检查不证明目录 ACL 安全，安装目录的写权限仍由运行环境负责。
export function resolveTrustedExecutable(tool, {
  env = process.env,
  platform = process.platform,
  workspaceRoot = repositoryRoot,
  fileSystem = { accessSync, realpathSync, statSync },
} = {}) {
  const settings = toolSettings[tool];
  if (!settings) throw new Error(`Unsupported executable: ${tool}`);
  const pathApi = platform === "win32" ? path.win32 : path.posix;
  const cachedGo = tool === "go" ? setupGoExecutable(env, platform, pathApi) : undefined;
  if (cachedGo !== undefined && env[settings.variable] !== undefined
    && pathApi.relative(cachedGo, env[settings.variable]) !== "") {
    throw new Error(`${settings.variable} does not match the configured setup-go installation`);
  }
  const candidate = cachedGo ?? env[settings.variable] ?? settings.paths[platform];
  const fullyQualified = typeof candidate === "string" && pathApi.isAbsolute(candidate)
    && (platform !== "win32" || /^[A-Za-z]:[\\/]/.test(candidate) || candidate.startsWith("\\\\"));
  if (!fullyQualified) {
    throw new Error(`${settings.variable} must identify an absolute executable path; PATH lookup is disabled`);
  }
  try {
    const executable = fileSystem.realpathSync(candidate);
    const workspace = fileSystem.realpathSync(workspaceRoot);
    const temporaryRoots = temporaryDirectories(env, platform, pathApi).flatMap((directory) => {
      try {
        return [directory, fileSystem.realpathSync(directory)];
      } catch {
        return [directory];
      }
    });
    // 原路径和真实路径都不能由仓库、npm 依赖或临时目录提供。
    if ([pathApi.resolve(candidate), executable].some((value) =>
      withinDirectory(value, pathApi.resolve(workspaceRoot), pathApi)
      || withinDirectory(value, workspace, pathApi) || inNodeModules(value, pathApi)
      || temporaryRoots.some((directory) => withinDirectory(value, directory, pathApi))
    )) {
      throw new Error("executable must be outside the repository, node_modules and temporary directories");
    }
    // 链接可能将安装路径转向别处；调用者必须明确配置真实安装路径，不能隐式接受链接目标。
    if (pathApi.relative(pathApi.resolve(candidate), executable) !== "") {
      throw new Error("executable path must be canonical; symbolic-link redirection is not allowed");
    }
    if (!fileSystem.statSync(executable).isFile()) throw new Error("executable must be a regular file");
    if (platform === "win32" && pathApi.extname(executable).toLowerCase() !== ".exe") {
      throw new Error("Windows executable must be an .exe file for shell=false");
    }
    fileSystem.accessSync(executable, constants.X_OK);
    return executable;
  } catch (error) {
    throw new Error(`Unable to use ${tool} at '${candidate}': ${error.message}. Set ${settings.variable} to a trusted absolute installation path; PATH lookup is disabled`);
  }
}
