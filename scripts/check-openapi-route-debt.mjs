import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { buildRouteCoverage } from "./update-api-route-coverage.mjs";

const commandMethods = new Set(["POST", "PUT", "PATCH", "DELETE"]);
const nonPublicCategories = new Set(["internal-worker", "operational"]);

function publicCommandGaps(routes) {
  return routes
    .filter((route) => route.coverage === "registered-gap" &&
      !nonPublicCategories.has(route.interface_category) && commandMethods.has(route.method))
    .map((route) => `${route.method} ${route.path}`)
    .sort();
}

export function findRouteDebtFailures(budget, coverage) {
  const failures = [];
  if (budget.schema_version !== 1) failures.push("route debt budget requires schema_version 1");
  for (const field of ["registered", "max_registered_gaps", "max_legacy_public_read", "max_legacy_public_command"]) {
    if (!Number.isSafeInteger(budget[field]) || budget[field] < 0) failures.push(`route debt budget requires nonnegative integer ${field}`);
  }
  if (!Array.isArray(budget.allowed_public_command_gaps) ||
      budget.allowed_public_command_gaps.some((key) => typeof key !== "string" || !/^(POST|PUT|PATCH|DELETE) \/\S+$/.test(key)) ||
      new Set(budget.allowed_public_command_gaps).size !== budget.allowed_public_command_gaps.length) {
    failures.push("route debt budget requires unique, exact allowed_public_command_gaps");
    return failures;
  }

  const gaps = coverage.routes.filter((route) => route.coverage === "registered-gap");
  const categoryCount = (category) => gaps.filter((route) => route.interface_category === category).length;
  for (const [name, actual, maximum] of [
    ["registered gaps", gaps.length, budget.max_registered_gaps],
    ["legacy public read gaps", categoryCount("legacy-public-read"), budget.max_legacy_public_read],
    ["legacy public command gaps", categoryCount("legacy-public-command"), budget.max_legacy_public_command],
  ]) {
    if (Number.isSafeInteger(maximum) && actual > maximum) failures.push(`${name} increased to ${actual} (budget ${maximum})`);
  }

  // 总缺口未增长也可能出现新写接口缺口，因此按方法和路径再核对逐项批准的例外。
  const approvedCommands = new Set(budget.allowed_public_command_gaps);
  for (const key of publicCommandGaps(coverage.routes)) {
    if (!approvedCommands.has(key)) failures.push(`new public command exception requires OpenAPI coverage: ${key}`);
  }
  return failures;
}

export function checkOpenApiRouteDebt(root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")) {
  const budget = JSON.parse(fs.readFileSync(path.join(root, "contracts/openapi/route-debt-budget.json"), "utf8"));
  const coverage = buildRouteCoverage({
    root,
    gatewayRoot: path.join(root, "services/api-gateway/internal"),
    openapiPath: path.join(root, "services/api-gateway/openapi/edugrade-api.openapi.json"),
    exceptionsPath: path.join(root, "contracts/openapi/registered-route-exceptions.json"),
  });
  return { coverage, failures: findRouteDebtFailures(budget, coverage) };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { coverage, failures } = checkOpenApiRouteDebt();
  if (failures.length) {
    console.error("OpenAPI route debt gate failed:\n" + failures.map((failure) => `- ${failure}`).join("\n"));
    process.exitCode = 1;
  } else {
    console.log(`OpenAPI route debt gate passed: ${coverage.counts.registered_gaps} registered gaps.`);
  }
}
