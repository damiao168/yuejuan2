import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (...parts) => readFileSync(join(root, ...parts), "utf8");

const api = read("apps", "web-admin", "src", "api", "modelGovernance.ts");
const workspace = read(
  "apps",
  "web-admin",
  "src",
  "components",
  "model-governance",
  "ModelEvaluationWorkspace.tsx"
);
const appShell = read("apps", "web-admin", "src", "AppShell.tsx");
const governanceRoutes = read("services", "api-gateway", "internal", "server", "routes_governance.go");
const story = read("docs", "stories", "STORY-061-multi-provider-native-model-governance.md");

for (const endpoint of [
  "/api/v1/model-evaluation-runs",
  "/candidates",
  "/complete",
  "/invalidate"
]) {
  assert.ok(api.includes(endpoint), `web evaluation API missing ${endpoint}`);
  assert.ok(governanceRoutes.includes(endpoint), `server evaluation route missing ${endpoint}`);
}

for (const invariant of [
  "教师接受率",
  "严重错误率",
  "证据有效率",
  "稳定性",
  "P95 时延",
  "平均成本",
  "协议样例证据",
  "授权冻结集证据",
  "不等于模型已批准",
  "不会批准模型或改变评分路由"
]) {
  assert.ok(workspace.includes(invariant), `evaluation workspace missing product invariant: ${invariant}`);
}

assert.ok(appShell.includes('["model:evaluation:manage"]'), "evaluation mutations must be permission gated");
assert.ok(workspace.includes("canManage && selectedRun.status"), "lifecycle controls must be permission and state gated");
assert.ok(workspace.includes("至少需要两个候选，并且必须包含本地基线"), "completion readiness must explain the local baseline gate");
assert.equal(workspace.includes("/promote"), false, "STORY-061C2 must not expose promotion");
assert.equal(workspace.includes("promoteModel"), false, "STORY-061C2 must not implement promotion");
assert.ok(story.includes("061C2（已完成）"), "story status must record completed visible evaluation center");
assert.ok(story.includes("061C3（下一步"), "story status must keep promotion as the next independent review");

console.log("STORY-061 model evaluation UI invariants passed");
