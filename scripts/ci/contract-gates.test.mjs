import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { findBreakingChanges } from "../check-openapi-breaking.mjs";
import { findRouteDebtFailures } from "../check-openapi-route-debt.mjs";
import { buildRouteCoverage } from "../update-api-route-coverage.mjs";
import { changedGenerated } from "./check-generated-contracts.mjs";

test("generated artifact gate rejects modified, missing and extra output independently of git status", () => {
  const before = new Map([["kept",Buffer.from("same")],["changed",Buffer.from("old")],["removed",Buffer.from("gone")]]);
  const after = new Map([["kept",Buffer.from("same")],["changed",Buffer.from("new")],["added",Buffer.from("extra")]]);
  assert.deepEqual(changedGenerated(before,after),["added","changed","removed"]);
  assert.deepEqual(changedGenerated(before,new Map(before)),[]);
});

test("breaking gate rejects removal of a required field and status enum value", () => {
  const baseline = {
    operations: {},
    schemas: { Result: { type: "object", required: ["id", "status"], properties: { id: { type: "string" }, status: { type: "string", enum: ["processing", "succeeded", "failed"] } } } },
  };
  const current = {
    operations: {},
    schemas: { Result: { type: "object", required: ["id"], properties: { id: { type: "string" }, status: { type: "string", enum: ["processing", "succeeded"] } } } },
  };
  const failures = findBreakingChanges(baseline, current);
  assert.ok(failures.some((failure) => failure.includes("made required field optional Result.status")), failures.join("\n"));
  assert.ok(failures.some((failure) => failure.includes('removed enum value "failed" from Result.status')), failures.join("\n"));
});

test("route gate rejects a newly registered route until its exact exception is reviewed", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "edugrade-contract-gate-"));
  const gatewayRoot = path.join(root, "services/api-gateway/internal/server");
  const openapiPath = path.join(root, "openapi.json");
  const exceptionsPath = path.join(root, "exceptions.json");
  fs.mkdirSync(gatewayRoot, { recursive: true });
  fs.writeFileSync(path.join(gatewayRoot, "server.go"), 'package server\nfunc register() { mux.Handle("POST /api/v1/new-critical-command", handler) }\n');
  fs.writeFileSync(openapiPath, JSON.stringify({ paths: {} }));
  fs.writeFileSync(exceptionsPath, JSON.stringify({ policies: [] }));
  assert.throws(() => buildRouteCoverage({ root, gatewayRoot, openapiPath, exceptionsPath }), /unapproved registered route.*POST \/api\/v1\/new-critical-command/);

  fs.writeFileSync(exceptionsPath, JSON.stringify({ policies: [{
    id: "reviewed-test", interface_category: "test", contract_carrier: "test schema",
    exception_scope: "this exact fixture", owner: "test", review_after: "2026-12-31",
    reason: "explicit test approval", routes: ["POST /api/v1/new-critical-command"],
  }] }));
  const coverage = buildRouteCoverage({ root, gatewayRoot, openapiPath, exceptionsPath });
  assert.equal(coverage.routes[0].reason, "explicit test approval");
  assert.equal(coverage.routes[0].exception_policy, "reviewed-test");
  const validLedger = JSON.parse(fs.readFileSync(exceptionsPath, "utf8"));
  for (const field of ["id", "interface_category", "contract_carrier", "exception_scope", "owner", "review_after", "reason"]) {
    const invalid = structuredClone(validLedger);
    delete invalid.policies[0][field];
    fs.writeFileSync(exceptionsPath, JSON.stringify(invalid));
    assert.throws(() => buildRouteCoverage({ root, gatewayRoot, openapiPath, exceptionsPath }), new RegExp(`requires ${field}`));
  }
  const invalidDate = structuredClone(validLedger);
  invalidDate.policies[0].review_after = "2026-02-30";
  fs.writeFileSync(exceptionsPath, JSON.stringify(invalidDate));
  assert.throws(() => buildRouteCoverage({ root, gatewayRoot, openapiPath, exceptionsPath }), /invalid review_after/);
  fs.rmSync(root, { recursive: true, force: true });
});

test("OpenAPI debt ratchet rejects growth and new public commands even when totals stay flat", () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
  const budget = JSON.parse(fs.readFileSync(path.join(root, "contracts/openapi/route-debt-budget.json"), "utf8"));
  const current = JSON.parse(fs.readFileSync(path.join(root, "services/api-gateway/openapi/route-coverage.json"), "utf8"));
  assert.deepEqual(findRouteDebtFailures(budget, current), []);

  const increased = structuredClone(current);
  increased.routes.push({ method: "GET", path: "/api/v1/new-read", coverage: "registered-gap", interface_category: "legacy-public-read" });
  assert.ok(findRouteDebtFailures(budget, increased).some((failure) => failure.includes("registered gaps increased")));
  assert.ok(findRouteDebtFailures(budget, increased).some((failure) => failure.includes("legacy public read gaps increased")));

  const swapped = structuredClone(current);
  const oldCommand = swapped.routes.findIndex((route) => route.coverage === "registered-gap" && route.interface_category === "legacy-public-command");
  swapped.routes.splice(oldCommand, 1);
  swapped.routes.push({ method: "POST", path: "/api/v1/new-command", coverage: "registered-gap", interface_category: "legacy-public-command" });
  assert.deepEqual(findRouteDebtFailures(budget, swapped), ["new public command exception requires OpenAPI coverage: POST /api/v1/new-command"]);

  swapped.routes.at(-1).interface_category = "public-feature";
  assert.ok(findRouteDebtFailures(budget, swapped).some((failure) => failure.includes("new public command exception")));
});
