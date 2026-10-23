import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { findBreakingChanges } from "../check-openapi-breaking.mjs";
import { findRouteDebtFailures } from "../check-openapi-route-debt.mjs";
import { buildRouteCoverage } from "../update-api-route-coverage.mjs";
import { schemaToType } from "../lib/openapi-sdk.mjs";
import { changedGenerated } from "./check-generated-contracts.mjs";

test("exam grading AI plan routes contract their scoped reads, admin writes and probe failures", () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
  const spec = JSON.parse(fs.readFileSync(path.join(root, "services/api-gateway/openapi/edugrade-api.openapi.json"), "utf8"));
  const base = "/api/v1/exams/{examId}/grading-ai-plan";
  const expected = [
    [base, "get", "getExamGradingAIPlan", "GradingAIPlanDetail", undefined, ["200", "401", "403", "404", "500", "503"]],
    [base, "put", "saveExamGradingAIPlan", "GradingAIPlanDetail", "GradingAIPlanInput", ["200", "400", "401", "403", "404", "409", "500", "503"]],
    [base + "/probe", "post", "probeExamGradingAIModel", "GradingVisionProbe", "ProbeGradingAIModelRequest", ["200", "400", "401", "403", "404", "500", "502", "503"]],
  ];
  assert.ok(spec.security.some((security) => Object.hasOwn(security, "sessionCookie")));
  assert.ok(spec.security.some((security) => Object.hasOwn(security, "bearerAuth")));
  for (const [route, method, id, response, request, statuses] of expected) {
    const operation = spec.paths[route]?.[method];
    assert.ok(operation, `${method.toUpperCase()} ${route}`);
    assert.equal(operation.operationId, id);
    assert.equal(operation.security, undefined, `${id} must retain document authentication`);
    assert.match(operation.description, /grading:manage/);
    assert.deepEqual(operation.parameters, [{ $ref: "#/components/parameters/ExamId" }]);
    assert.equal(spec.components.parameters.ExamId.schema.format, "uuid");
    assert.equal(operation.responses["200"].content["application/json"].schema.$ref, `#/components/schemas/${response}`);
    for (const status of statuses) assert.ok(operation.responses[status], `${id} response ${status}`);
    if (request) {
      assert.equal(operation.requestBody.required, true);
      assert.equal(operation.requestBody.content["application/json"].schema.$ref, `#/components/schemas/${request}`);
      for (const role of ["school_admin", "tenant_admin", "platform_admin"]) assert.ok(operation.description.includes(role), `${id} role ${role}`);
      assert.match(operation.responses["403"].description, /grading_ai_plan_admin_required/);
    } else {
      assert.equal(operation.requestBody, undefined);
    }
  }
  assert.match(spec.paths[base].put.responses["409"].description, /grading_ai_plan_changed/);
  assert.match(spec.paths[base + "/probe"].post.description, /尚未保存/);
});

test("exam grading AI plan response fields and nullability stay aligned with the Go DTOs", () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
  const spec = JSON.parse(fs.readFileSync(path.join(root, "services/api-gateway/openapi/edugrade-api.openapi.json"), "utf8"));
  const dtoSource = fs.readFileSync(path.join(root, "services/api-gateway/internal/grading/exam_ai_plan.go"), "utf8");
  const schemas = spec.components.schemas;
  // 以响应 DTO 的 json 标签核对公开字段和 omitempty，防止契约漏字段或强制客户端补齐可省略值。
  for (const name of ["GradingAIRoleSelection", "GradingAIDecisionConfig", "GradingAIModel", "GradingAIPlan", "GradingAIPlanDetail", "GradingVisionProbe"]) {
    const body = dtoSource.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))?.[1];
    assert.ok(body, `${name} Go DTO`);
    const fields = [...body.matchAll(/`json:"([^",]+)(,omitempty)?"`/g)];
    assert.deepEqual(Object.keys(schemas[name].properties).sort(), fields.map((field) => field[1]).sort(), `${name} public fields`);
    assert.deepEqual([...schemas[name].required].sort(), fields.filter((field) => !field[2]).map((field) => field[1]).sort(), `${name} required fields`);
  }
  for (const name of ["GradingAIDecisionConfigInput", "GradingAIDecisionConfig"]) {
    for (const field of ["primary_allowed_diff_points", "teacher_allowed_diff_points"]) {
      const threshold = schemas[name].properties[field];
      assert.deepEqual(threshold.anyOf, [{ type: "number" }, { type: "null" }], `${name}.${field} OpenAPI 3.1 null`);
      assert.equal(schemaToType(threshold), "number | null", `${name}.${field} generated type`);
      assert.equal(threshold.minimum, 0);
      assert.equal(threshold.maximum, 1000);
      assert.match(threshold.description, /单位：分/);
    }
  }
  assert.ok(schemas.GradingAIPlanDetail.required.includes("can_configure"));
  assert.equal(schemas.GradingAIModel.additionalProperties, false);
  for (const hidden of ["base_url", "api_key", "api_key_encrypted", "credential", "credentials"]) assert.equal(schemas.GradingAIModel.properties[hidden], undefined);
  assert.deepEqual(schemas.GradingAIPlanInput.required, ["expected_revision", "decision_config"]);
  assert.deepEqual(schemas.GradingAIDecisionConfigInput.required, ["aggregation_version", "rounding"]);
  assert.deepEqual(Object.keys(schemas.GradingAIPlan.properties.roles.properties).sort(), ["arbiter", "primary_a", "primary_b"]);
  assert.equal(schemas.GradingAIPlan.properties.roles.additionalProperties, false);
  const requestID = schemas.ProbeGradingAIModelRequest.properties.request_id;
  assert.deepEqual([requestID.minLength, requestID.maxLength], [8, 128]);
});

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

test("breaking gate preserves parameter and nested payload diagnostic order", () => {
  const operation = {
    operationId: "readResult",
    parameters: [
      { in: "query", name: "removed", schema: { type: "string" }, required: false },
      { in: "query", name: "changed", schema: { type: "string" }, required: false },
      { in: "query", name: "required", schema: { type: "string" }, required: false },
    ],
    requestBody: { required: false, schema: { type: "array", items: { type: "object", required: ["id"], properties: { id: { type: "string" } } } } },
    response: { type: "object", properties: { status: { type: "string", enum: ["ok", "failed"] } } },
  };
  const baseline = { operations: { "GET /result": operation }, schemas: {} };
  assert.deepEqual(findBreakingChanges(baseline, structuredClone(baseline)), []);
  const candidate = structuredClone(operation);
  candidate.operationId = "different";
  candidate.parameters.shift();
  candidate.parameters[0].schema = { type: "number" };
  candidate.parameters[1].required = true;
  candidate.parameters.push({ in: "header", name: "new", schema: { type: "string" }, required: true });
  candidate.requestBody.required = true;
  candidate.requestBody.schema.items.required = [];
  delete candidate.requestBody.schema.items.properties.id;
  candidate.response.properties.status.enum = ["ok"];
  assert.deepEqual(findBreakingChanges(baseline, { operations: { "GET /result": candidate }, schemas: {} }), [
    "changed operationId for GET /result",
    "removed parameter query:removed from GET /result",
    "changed parameter type query:changed in GET /result",
    "made parameter required query:required in GET /result",
    "added required parameter header:new to GET /result",
    "made request body required in GET /result",
    "made required field optional GET /result request[].id",
    "removed property GET /result request[].id",
    'removed enum value "failed" from GET /result response.status',
  ]);
});

test("breaking gate retains missing operation, shape, ref and response checks", () => {
  const baseline = {
    operations: {
      "GET /removed": { operationId: "removed", parameters: [] },
      "POST /result": { operationId: "result", parameters: [], requestBody: { required: true }, response: { type: "object" } },
    },
    schemas: { Absent: { type: "string" }, Scalar: 1, Array: [], Shape: { type: "object" }, Reference: { $ref: "#/before" } },
  };
  const current = {
    operations: { "POST /result": { operationId: "result", parameters: [] } },
    schemas: { Scalar: 2, Array: {}, Shape: "text", Reference: { $ref: "#/after" } },
  };
  assert.deepEqual(findBreakingChanges(baseline, current), [
    "removed operation GET /removed",
    "removed request body from POST /result",
    "removed success response from POST /result",
    "removed schema Absent",
    "changed schema Scalar",
    "changed schema Array",
    "removed schema shape Shape",
    "changed ref for Reference",
  ]);
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
  const gaps = current.routes.filter((route) => route.coverage === "registered-gap");
  const readGapCount = gaps.filter((route) => route.interface_category === "legacy-public-read").length;
  // 补齐 OpenAPI 后可能有剩余预算；测试数据需同时越过总量和读接口预算，不能假定新增一条就超限。
  const addedGapCount = Math.max(0, budget.max_registered_gaps - gaps.length, budget.max_legacy_public_read - readGapCount) + 1;
  for (let index = 0; index < addedGapCount; index++) {
    increased.routes.push({ method: "GET", path: `/api/v1/new-read-${index}`, coverage: "registered-gap", interface_category: "legacy-public-read" });
  }
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

test("route coverage preserves exact approvals, duplicate checks and stale approval rejection", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "edugrade-route-validation-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const gatewayRoot = path.join(root, "gateway");
  const openapiPath = path.join(root, "openapi.json");
  const exceptionsPath = path.join(root, "exceptions.json");
  fs.mkdirSync(gatewayRoot);
  const policy = {
    id: "reviewed", interface_category: "test", contract_carrier: "test schema",
    exception_scope: "exact fixture", owner: "test", review_after: "2026-12-31",
    reason: "reviewed fixture", routes: ["POST /gap"],
  };
  fs.writeFileSync(path.join(gatewayRoot, "routes.go"), 'package gateway\nfunc register() { mux.Handle("GET /contracted", handler); mux.Handle("POST /gap", handler) }\n');
  fs.writeFileSync(openapiPath, JSON.stringify({ paths: { "/contracted": { get: { operationId: "readContract" } } } }));
  fs.writeFileSync(exceptionsPath, JSON.stringify({ policies: [policy] }));
  const options = { root, gatewayRoot, openapiPath, exceptionsPath };
  const coverage = buildRouteCoverage(options);
  assert.deepEqual(coverage.counts, { registered: 2, openapi: 1, registered_gaps: 1 });
  assert.deepEqual(coverage.routes[0], { method: "GET", path: "/contracted", source: "gateway/routes.go", coverage: "openapi", operation_id: "readContract" });
  const invalidLedgers = [
    { policies: [policy, policy], error: /duplicate registered-gap policy reviewed/ },
    { policies: [policy, { ...policy, id: "other" }], error: /duplicate registered-gap approval POST \/gap/ },
    { policies: [{ ...policy, routes: [] }], error: /requires exact routes/ },
    { policies: [{ ...policy, routes: ["POST /gap/*"] }], error: /requires an exact route/ },
    { policies: [{ ...policy, routes: ["POST /missing"] }], error: /unapproved registered route without OpenAPI contract: POST \/gap/ },
    { policies: [{ ...policy, routes: ["POST /gap", "GET /contracted"] }], error: /stale registered-gap approvals.*GET \/contracted/ },
  ];
  for (const { policies, error } of invalidLedgers) {
    fs.writeFileSync(exceptionsPath, JSON.stringify({ policies }));
    assert.throws(() => buildRouteCoverage(options), error);
  }
  fs.writeFileSync(exceptionsPath, JSON.stringify({ policies: [policy] }));
  fs.writeFileSync(path.join(gatewayRoot, "duplicate.go"), 'package gateway\nfunc other() { mux.Handle("GET /contracted", handler) }\n');
  assert.throws(() => buildRouteCoverage(options), /duplicate registered routes: GET \/contracted/);
});
