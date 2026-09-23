import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const floors = JSON.parse(readFileSync(join(root, "contracts", "testing", "coverage-floor.json"), "utf8")).packages;

export function parseCoverage(output) {
  const coverage = new Map();
  for (const match of output.matchAll(/^ok\s+(\S+)\s+.*\bcoverage:\s+(\d+(?:\.\d+)?)% of statements\s*$/gm)) {
    coverage.set(match[1].split("/").at(-1), Number(match[2]));
  }
  return coverage;
}

export function coverageFailures(coverage, budgets) {
  const failures = [];
  for (const [name, floor] of Object.entries(budgets)) {
    const actual = coverage.get(name);
    if (actual === undefined) {
      failures.push(`${name}: no coverage result`);
    } else if (actual < floor) {
      failures.push(`${name}: ${actual.toFixed(1)}% is below the ${floor.toFixed(1)}% floor`);
    }
  }
  return failures;
}

function main() {
  const packages = Object.keys(floors).map((name) => `./internal/${name}`);
  const result = spawnSync("go", ["test", "-cover", "-count=1", ...packages], {
    cwd: join(root, "services", "api-gateway"),
    encoding: "utf8",
    maxBuffer: 20 * 1024 * 1024,
  });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  if (result.error) {
    console.error(`Unable to run Go coverage tests: ${result.error.message}`);
    process.exitCode = 1;
    return;
  }
  if (result.status !== 0) {
    process.exitCode = result.status ?? 1;
    return;
  }
  const failures = coverageFailures(parseCoverage(result.stdout), floors);
  for (const failure of failures) console.error(`coverage violation: ${failure}`);
  if (failures.length > 0) process.exitCode = 1;
  else console.log("Go coverage floors passed");
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main();
