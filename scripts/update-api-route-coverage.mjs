import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// 按仓库固定的字面量注册写法扫描，遇到动态路由或其他注册方式时需扩展检测。
const routePattern = /\b(?:mux|[A-Za-z_]\w*Mux)\.Handle\("(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) ([^" ]+)"/g;

function walk(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) return walk(absolute);
    return entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go") ? [absolute] : [];
  });
}

export function buildRouteCoverage({ root, gatewayRoot, openapiPath, exceptionsPath }) {
  const openapi = JSON.parse(fs.readFileSync(openapiPath, "utf8"));
  const ledger = JSON.parse(fs.readFileSync(exceptionsPath, "utf8"));
  const approved = new Map();
  const policyIds = new Set();
  for (const policy of ledger.policies ?? []) {
    for (const field of ["id", "interface_category", "contract_carrier", "exception_scope", "owner", "review_after", "reason"]) {
      if (typeof policy[field] !== "string" || !policy[field].trim()) {
        throw new Error(`registered-gap policy ${policy.id ?? "<missing>"} requires ${field}`);
      }
    }
    if (policyIds.has(policy.id)) throw new Error(`duplicate registered-gap policy ${policy.id}`);
    policyIds.add(policy.id);
    if (!/^\d{4}-\d{2}-\d{2}$/.test(policy.review_after) ||
        !Number.isFinite(Date.parse(policy.review_after)) ||
        new Date(policy.review_after).toISOString().slice(0, 10) !== policy.review_after) {
      throw new Error(`registered-gap policy ${policy.id} has invalid review_after`);
    }
    if (!Array.isArray(policy.routes) || !policy.routes.length) {
      throw new Error(`registered-gap policy ${policy.id} requires exact routes`);
    }
    for (const key of policy.routes ?? []) {
      if (typeof key !== "string" || !/^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) \/\S+$/.test(key) || key.includes("*")) {
        throw new Error(`registered-gap policy ${policy.id} requires an exact route: ${key}`);
      }
      if (approved.has(key)) throw new Error(`duplicate registered-gap approval ${key}`);
      approved.set(key, policy);
    }
  }

  const routes = [];
  const usedApprovals = new Set();
  for (const file of walk(gatewayRoot)) {
    const source = fs.readFileSync(file, "utf8");
    for (const match of source.matchAll(routePattern)) {
      const method = match[1].toLowerCase();
      const routePath = match[2];
      const key = `${match[1]} ${routePath}`;
      const relativeSource = path.relative(root, file).replaceAll("\\", "/");
      const operation = openapi.paths?.[routePath]?.[method];
      if (operation) {
        routes.push({ method: match[1], path: routePath, source: relativeSource, coverage: "openapi", ...(operation.operationId ? { operation_id: operation.operationId } : {}) });
        continue;
      }
      const policy = approved.get(key);
      if (!policy) throw new Error(`unapproved registered route without OpenAPI contract: ${key}`);
      usedApprovals.add(key);
      routes.push({
        method: match[1], path: routePath, source: relativeSource, coverage: "registered-gap",
        interface_category: policy.interface_category, contract_carrier: policy.contract_carrier,
        exception_scope: policy.exception_scope, exception_policy: policy.id, owner: policy.owner,
        review_after: policy.review_after, reason: policy.reason,
      });
    }
  }
  routes.sort((left, right) => left.path.localeCompare(right.path) || left.method.localeCompare(right.method));
  const duplicates = routes.filter((route, index) => index > 0 && route.method === routes[index - 1].method && route.path === routes[index - 1].path);
  if (duplicates.length) throw new Error(`duplicate registered routes: ${duplicates.map((route) => `${route.method} ${route.path}`).join(", ")}`);
  // 路由移除或纳入契约后必须删掉旧例外，避免它将来被相同路径重新利用。
  const stale = [...approved.keys()].filter((key) => !usedApprovals.has(key)).sort();
  if (stale.length) throw new Error(`stale registered-gap approvals (route removed or now contracted): ${stale.join(", ")}`);

  return {
    schema_version: 2,
    generated_from: "services/api-gateway/internal/**/*.go",
    exception_ledger: "contracts/openapi/registered-route-exceptions.json",
    policy: "Every registered route is covered by OpenAPI or an exact, reviewed exception-ledger entry; generation never invents approvals.",
    counts: {
      registered: routes.length,
      openapi: routes.filter((route) => route.coverage === "openapi").length,
      registered_gaps: routes.filter((route) => route.coverage === "registered-gap").length,
    },
    routes,
  };
}

export function writeRouteCoverage(root = process.cwd()) {
  const document = buildRouteCoverage({
    root,
    gatewayRoot: path.join(root, "services", "api-gateway", "internal"),
    openapiPath: path.join(root, "services", "api-gateway", "openapi", "edugrade-api.openapi.json"),
    exceptionsPath: path.join(root, "contracts", "openapi", "registered-route-exceptions.json"),
  });
  const outputPath = path.join(root, "services", "api-gateway", "openapi", "route-coverage.json");
  fs.writeFileSync(outputPath, `${JSON.stringify(document, null, 2)}\n`);
  return document;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const document = writeRouteCoverage();
  console.log(`Registered ${document.counts.registered} routes: ${document.counts.openapi} OpenAPI, ${document.counts.registered_gaps} reviewed gaps.`);
}
