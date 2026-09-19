import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function walk(directory, extension) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return walk(path, extension);
    return entry.isFile() && entry.name.endsWith(extension) ? [path] : [];
  });
}

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
  const pageDirectories = [join(root, "apps", "web-admin", "src", "pages")];
  for (const directory of pageDirectories) {
    for (const path of walk(directory, ".tsx")) {
      const warning = pageWarning(readFileSync(path, "utf8"), relative(root, path));
      if (warning) warnings.push(warning);
    }
  }
  const desktopApp = join(root, "apps", "desktop-client", "src", "App.tsx");
  const desktopWarning = pageWarning(readFileSync(desktopApp, "utf8"), relative(root, desktopApp));
  if (desktopWarning) warnings.push(desktopWarning);
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
