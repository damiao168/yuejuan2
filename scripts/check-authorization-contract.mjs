import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const contractPath = path.join(root, "contracts", "authorization", "role-matrix.json");
const requiredRoles = [
  "platform_admin", "tenant_admin", "school_admin", "teacher", "grader",
  "arbitrator", "auditor", "student", "page_processing_worker", "subjective_grading_worker",
];
const scopes = new Set(["platform", "tenant", "school", "class", "exam_task", "self", "service"]);

// 这里只检查角色矩阵结构和角色边界；数据库实际授予的权限由迁移集成测试另行核对。
export function checkAuthorizationContract(matrix) {
  assert.ok(matrix && typeof matrix === "object" && !Array.isArray(matrix), "role matrix must be an object");
  assert.deepEqual(Object.keys(matrix).sort(), [...requiredRoles].sort(), "role matrix must list every canonical role exactly once");
  for (const [code, role] of Object.entries(matrix)) {
    assert.ok(role && typeof role === "object" && !Array.isArray(role), `${code} must be an object`);
    assert.deepEqual(Object.keys(role).sort(), ["scope", "human", "managed_user_assignable", "workspace", "permissions"].sort(), `${code} fields`);
    assert.ok(scopes.has(role.scope), `${code} has invalid scope`);
    assert.equal(typeof role.human, "boolean", `${code} human must be boolean`);
    assert.equal(typeof role.managed_user_assignable, "boolean", `${code} managed_user_assignable must be boolean`);
    assert.ok(role.workspace === null || typeof role.workspace === "string", `${code} workspace must be string or null`);
    assert.ok(Array.isArray(role.permissions), `${code} permissions must be an array`);
    assert.ok(role.permissions.length > 0, `${code} must have permissions`);
    assert.equal(new Set(role.permissions).size, role.permissions.length, `${code} has duplicate permissions`);
    for (const permission of role.permissions) {
      assert.match(permission, /^[a-z][a-z0-9_]*:[a-z][a-z0-9_:]*$/, `${code} has invalid permission`);
    }
    if (role.scope === "service") {
      assert.equal(role.human, false, `${code} service role cannot be human`);
      assert.equal(role.managed_user_assignable, false, `${code} service role cannot be managed-user assignable`);
      assert.equal(role.workspace, null, `${code} service role cannot have a workspace`);
    } else {
      assert.equal(role.human, true, `${code} human role cannot have service scope`);
    }
  }
  return matrix;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    checkAuthorizationContract(JSON.parse(fs.readFileSync(contractPath, "utf8")));
    console.log("Authorization contract structure is valid; PostgreSQL migration test checks exact grants.");
  } catch (error) {
    console.error(`Authorization contract invalid: ${error.message}`);
    process.exitCode = 1;
  }
}
