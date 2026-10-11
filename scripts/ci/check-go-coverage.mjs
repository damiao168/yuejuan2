import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { resolveTrustedExecutable } from "./trusted-executable.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const floors = JSON.parse(readFileSync(join(root, "contracts", "testing", "coverage-floor.json"), "utf8")).packages;

export function parseCoverage(output) {
  const coverage = new Map();
  const packagePrefix = /^ok\s+(\S+)\s+/gm;
  const lineBreak = /[\r\n\u2028\u2029]/g;
  const coverageSuffix = /\bcoverage:\s+(\d+(?:\.\d+)?)% of statements\s*$/my;
  let match;
  while ((match = packagePrefix.exec(output)) !== null) {
    // 原规则允许包名两侧与覆盖率后的空白跨行，任意说明文本只能位于当前行。
    const contentsStart = packagePrefix.lastIndex;
    lineBreak.lastIndex = contentsStart;
    const contentsEnd = lineBreak.exec(output)?.index ?? output.length;
    // 合法数值后缀不能再包含 coverage:，只需尝试当前行最后一个标记。
    const marker = output.slice(contentsStart, contentsEnd).lastIndexOf("coverage:");
    coverageSuffix.lastIndex = contentsStart + marker;
    const result = marker === -1 ? null : coverageSuffix.exec(output);
    if (result) {
      coverage.set(match[1].split("/").at(-1), Number(result[1]));
      packagePrefix.lastIndex = coverageSuffix.lastIndex;
    } else {
      packagePrefix.lastIndex = match.index + 1;
    }
  }
  return coverage;
}

export function coverageFailures(coverage, budgets) {
  const failures = [];
  for (const [name, floor] of Object.entries(budgets)) {
    const actual = coverage.get(name);
    // 被预算约束的包必须有本次结果，不能因未执行或输出格式变化而跳过覆盖率门禁。
    if (actual === undefined) {
      failures.push(`${name}: no coverage result`);
    } else if (actual < floor) {
      failures.push(`${name}: ${actual.toFixed(1)}% is below the ${floor.toFixed(1)}% floor`);
    }
  }
  return failures;
}

export function main({
  env = process.env,
  spawn = spawnSync,
  stdout = process.stdout,
  stderr = process.stderr,
  log = console,
} = {}) {
  const packages = Object.keys(floors).map((name) => `./internal/${name}`);
  let executable;
  try {
    executable = resolveTrustedExecutable("go", { env });
  } catch (error) {
    log.error(`Unable to run Go coverage tests: ${error.message}`);
    return 1;
  }
  const result = spawn(executable, ["test", "-cover", "-count=1", ...packages], {
    cwd: join(root, "services", "api-gateway"),
    encoding: "utf8",
    maxBuffer: 20 * 1024 * 1024,
    shell: false,
    // 固定首个 go 路径后仍需禁止自动工具链经 PATH 转移；安装版本不足时由 go 明确失败。
    env: { ...env, GOTOOLCHAIN: "local" },
  });
  if (result.stdout) stdout.write(result.stdout);
  if (result.stderr) stderr.write(result.stderr);
  if (result.error) {
    log.error(`Unable to run Go coverage tests: ${result.error.message}`);
    return 1;
  }
  if (result.status !== 0) {
    return result.status ?? 1;
  }
  const failures = coverageFailures(parseCoverage(result.stdout), floors);
  for (const failure of failures) log.error(`coverage violation: ${failure}`);
  if (failures.length > 0) return 1;
  log.log("Go coverage floors passed");
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) process.exitCode = main();
