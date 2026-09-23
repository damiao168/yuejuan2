import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { checkAuthorizationContract } from "../check-authorization-contract.mjs";

const contractPath = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../contracts/authorization/role-matrix.json");
const current = JSON.parse(fs.readFileSync(contractPath, "utf8"));

test("current authorization contract has complete role metadata", () => {
  checkAuthorizationContract(current);
  assert.deepEqual(current.subjective_grading_worker.permissions, ["orchestrator:manage"]);
  for (const code of ["platform_admin", "tenant_admin"]) {
    assert.ok(current[code].permissions.includes("model:policy:manage"));
    assert.ok(current[code].permissions.includes("model:evaluation:manage"));
  }
  assert.ok(current.platform_admin.permissions.includes("model:managed_api:manage"));
  assert.ok(current.platform_admin.permissions.includes("model:panel:manage"));
  assert.ok(!current.tenant_admin.permissions.includes("model:managed_api:manage"));
});

test("authorization gate rejects missing roles, duplicate grants and service identity drift", () => {
  const missing = structuredClone(current);
  delete missing.subjective_grading_worker;
  assert.throws(() => checkAuthorizationContract(missing), /canonical role/);

  const duplicate = structuredClone(current);
  duplicate.tenant_admin.permissions.push(duplicate.tenant_admin.permissions[0]);
  assert.throws(() => checkAuthorizationContract(duplicate), /duplicate permissions/);

  const humanWorker = structuredClone(current);
  humanWorker.subjective_grading_worker.human = true;
  assert.throws(() => checkAuthorizationContract(humanWorker), /service role cannot be human/);
});
