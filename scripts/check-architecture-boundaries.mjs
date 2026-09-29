import { readFileSync, readdirSync } from "node:fs";
import { basename, dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function walk(directory, extension) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return walk(path, extension);
    return entry.isFile() && entry.name.endsWith(extension) ? [path] : [];
  });
}

// 这些门禁依赖仓库约定的源码写法与正则，不是完整的 Go 语法或依赖图分析。
export function checkModuleBoundaries(source) {
  const failures = [];
  const moduleTypes = [...source.matchAll(/^type\s+(\w+Module)\s+struct\s*\{/gm)].map((match) => match[1]);
  for (const match of source.matchAll(/^func\s+(New\w+Module)\(([^)]*)\)/gm)) {
    for (const moduleType of moduleTypes) {
      if (match[2].includes(`*${moduleType}`)) {
        failures.push(`${match[1]} accepts *${moduleType}; pass a narrow capability instead`);
      }
    }
  }
  for (const match of source.matchAll(/^type\s+(\w*Dependencies)\s+struct\s*\{([\s\S]*?)^\}/gm)) {
    if (/\bhttp\.HandlerFunc\b/.test(match[2])) {
      failures.push(`${match[1]} contains http.HandlerFunc; use an application port`);
    }
  }
  return failures;
}

export function findLargeInterfaces(source, filename, threshold = 20) {
  const warnings = [];
  for (const match of source.matchAll(/^type\s+(\w+)\s+interface\s*\{([\s\S]*?)^\}/gm)) {
    const methods = match[2].split("\n").filter((line) => /^\s*\w+\s*\(/.test(line)).length;
    if (methods > threshold) warnings.push(`${filename}: ${match[1]} has ${methods} methods`);
  }
  return warnings;
}

export function pageWarning(source, filename, threshold = 700) {
  const lines = source.trimEnd().split("\n").length;
  return lines > threshold ? `${filename}: ${lines} lines` : null;
}

// 行数按物理文本计算，注释和空行也计入；整理注释不能顺手放宽冻结的债务预算。
export function checkFrontendDebt(sources, debt) {
  const failures = [];
  const seen = new Set();
  for (const { path, source } of sources) {
    seen.add(path);
    const lines = source.trimEnd().split("\n").length;
    const isCss = path.endsWith(".css");
    const budget = isCss ? debt.legacy_css_max_lines[path] : debt.legacy_page_max_lines[path];
    if (budget === undefined) {
      if (isCss && Number.isSafeInteger(debt.css_max_lines) && lines > debt.css_max_lines) {
        failures.push(`${path}: new stylesheet has ${lines} lines; maximum is ${debt.css_max_lines}`);
      } else if (!isCss && lines > debt.page_max_lines) {
        failures.push(`${path}: new page has ${lines} lines; maximum is ${debt.page_max_lines}`);
      }
    } else if (lines > budget) {
      failures.push(`${path}: grew to ${lines} lines; debt budget is ${budget}`);
    } else if (!isCss && lines <= debt.page_max_lines) {
      failures.push(`${path}: now has ${lines} lines; remove its obsolete frontend debt entry`);
    }
  }
  for (const path of [...Object.keys(debt.legacy_page_max_lines), ...Object.keys(debt.legacy_css_max_lines)]) {
    if (!seen.has(path)) failures.push(`${path}: frontend debt entry has no matching file`);
  }
  return failures;
}

export function hookWarnings(source, filename, lineThreshold = 350, stateThreshold = 15) {
  const warnings = [];
  const lines = source.trimEnd().split("\n").length;
  const states = [...source.matchAll(/\buseState\s*(?:<[^()\n]*>)?\s*\(/g)].length;
  if (lines > lineThreshold) warnings.push(`${filename}: ${lines} hook lines`);
  if (states > stateThreshold) warnings.push(`${filename}: ${states} useState calls`);
  return warnings;
}

export const MAX_BROAD_AUTH_STORE_CONSUMERS = 11;

export function countBroadAuthStoreConsumers(sources) {
  return sources.reduce((count, source) => count
    + [...source.matchAll(/^\s*(?:audit|Audit)\s+auth\.Store\b/gm)].length, 0);
}

function main() {
  const serverDirectory = join(root, "services", "api-gateway", "internal", "server");
  const serverSources = walk(serverDirectory, ".go").filter((path) => !path.endsWith("_test.go"));
  const moduleSource = serverSources.map((path) => readFileSync(path, "utf8")).join("\n");
  const failures = checkModuleBoundaries(moduleSource);
  for (const path of serverSources) {
    if (/\bConnectOperations\s*\(/.test(readFileSync(path, "utf8"))) {
      failures.push(`${relative(root, path)} reintroduces ConnectOperations`);
    }
  }

  const warnings = [];
  const webSource = join(root, "apps", "web-admin", "src");
  const frontendPaths = walk(webSource, ".tsx")
    .filter((path) => path.endsWith("Page.tsx") || basename(path) === "GradingWorkbench.tsx");
  const desktopApp = join(root, "apps", "desktop-client", "src", "App.tsx");
  frontendPaths.push(desktopApp, join(webSource, "styles.css"), ...walk(join(webSource, "styles"), ".css"));
  const frontendSources = frontendPaths.map((path) => ({
    path: relative(root, path).split(sep).join("/"),
    source: readFileSync(path, "utf8"),
  }));
  const frontendDebt = JSON.parse(readFileSync(join(root, "contracts", "architecture", "frontend-debt.json"), "utf8"));
  failures.push(...checkFrontendDebt(frontendSources, frontendDebt));
  for (const app of ["web-admin", "desktop-client"]) {
    const features = join(root, "apps", app, "src", "features");
    for (const extension of [".ts", ".tsx"]) {
      for (const path of walk(features, extension)) {
        if (!/^use[A-Z].*\.tsx?$/.test(path.split(/[\\/]/).at(-1))) continue;
        warnings.push(...hookWarnings(readFileSync(path, "utf8"), relative(root, path)));
      }
    }
  }
  const goDirectory = join(root, "services", "api-gateway", "internal");
  const goSources = [];
  for (const path of walk(goDirectory, ".go")) {
    if (!path.endsWith("_test.go")) {
      const source = readFileSync(path, "utf8");
      goSources.push(source);
      warnings.push(...findLargeInterfaces(source, relative(root, path)));
    }
  }
  const broadAuthConsumers = countBroadAuthStoreConsumers(goSources);
  if (broadAuthConsumers > MAX_BROAD_AUTH_STORE_CONSUMERS) {
    failures.push(`broad audit auth.Store consumers grew to ${broadAuthConsumers}; budget is ${MAX_BROAD_AUTH_STORE_CONSUMERS}`);
  }

  for (const warning of warnings) console.warn(`architecture warning: ${warning}`);
  if (failures.length > 0) {
    for (const failure of failures) console.error(`architecture violation: ${failure}`);
    process.exitCode = 1;
    return;
  }
  console.log("Architecture boundaries passed");
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main();
