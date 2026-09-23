import { expect, test, type Page, type Route } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

const tenantId = "10000000-0000-0000-0000-000000000001";

function json(route: Route, body: unknown) {
  return route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
}

const school = {
  tenant_id: tenantId,
  school_id: "20000000-0000-0000-0000-000000000001",
  name: "北京第一中学",
  code: "beijing-no1",
  status: "active",
  created_at: "2026-08-12T02:21:00Z",
  last_activity_at: "2026-09-22T12:31:00Z",
  administrator: { id: "admin-1", display_name: "张老师", username: "admin01", admin_count: 2, last_login_at: "2026-09-22T12:31:00Z" },
  members: { accounts: 18, active_accounts: 17, administrators: 2, teachers: 11, graders: 5, students: 1426, classes: 32 },
  usage: { input_tokens: 12_800_000, output_tokens: 3_300_000, cached_input_tokens: 0, reasoning_tokens: 0, total_tokens: 16_100_000, requests: 9284, arbitration_requests: 823, estimated_cost_microusd: 0, window_days: 30 },
  model_health: { status: "healthy", config_status: "active", config_id: "model-1", display_name: "DashScope", provider_key: "dashscope", model_name: "Qwen3-VL-Flash", credential_hint: "••••7X2A", connection_status: "success", capability_status: "success", capability_version: "structured-json-v3", latency_ms: 842, last_tested_at: "2026-09-22T10:00:00Z" },
  exam_count: 18,
  attention_reasons: []
};

async function installSchoolMocks(page: Page) {
  await page.route(/\/api\/v1\/platform\/schools(?:\?.*)?$/, (route) => json(route, {
    schools: [school], summary: { total: 1, active: 1, disabled: 0 }, next_cursor: "", has_more: false
  }));
  await page.route(`**/api/v1/platform/schools/${tenantId}/members*`, (route) => json(route, {
    members: [{ id: "admin-1", username: "admin01", display_name: "张老师", phone_masked: "138****5678", employee_no: "T001", status: "active", roles: ["school_admin"], school_id: school.school_id, created_at: "2026-08-12T02:30:00Z", activated_at: "2026-08-12T02:31:00Z", last_login_at: "2026-09-22T12:31:00Z" }],
    summary: { total: 18, active: 17, disabled: 1, admins: 2, teachers: 11, graders: 5 }
  }));
  await page.route(`**/api/v1/platform/schools/${tenantId}/usage*`, (route) => json(route, {
    summary: school.usage,
    trend: [{ date: "2026-09-21", total_tokens: 420000, input_tokens: 330000, output_tokens: 90000, requests: 221 }],
    by_feature: [{ key: "subjective_grading", label: "主观题阅卷", total_tokens: 10_900_000, requests: 7811, share: 0.677, estimated_cost_microusd: 0 }],
    by_model: [{ key: "Qwen3-VL-Flash", label: "Qwen3-VL-Flash", total_tokens: 9_100_000, requests: 5201, share: 0.565, estimated_cost_microusd: 0 }],
    start_date: "2026-08-24", end_date: "2026-09-22"
  }));
  await page.route(`**/api/v1/platform/schools/${tenantId}/model-health`, (route) => json(route, {
    default_model: school.model_health,
    roles: [
      { agent_role: "primary_a", model_name: "Qwen-Primary-A", provider_key: "dashscope", status: "healthy" },
      { agent_role: "primary_b", model_name: "DeepSeek-Primary-B", provider_key: "deepseek", status: "healthy" },
      { agent_role: "arbiter", model_name: "Qwen-Arbiter", provider_key: "dashscope", status: "healthy" }
    ]
  }));
  await page.route(`**/api/v1/platform/schools/${tenantId}/activity*`, (route) => json(route, {
    activities: [{ id: "event-1", event_type: "score.released", severity: "success", title: "发布期中考试成绩", summary: "成绩已完成发布", actor_name: "张老师", happened_at: "2026-09-22T12:20:00Z" }],
    security: { active_admins: 2, mfa_enabled: 1, active_sessions: 5, last_login_at: "2026-09-22T12:31:00Z" }
  }));
}

test("平台学校总表提供运营摘要、搜索与五个详情入口", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await installSchoolMocks(page);
  await page.goto("/#/platform/schools");

  await expect(page.getByRole("heading", { name: "学校管理" })).toBeVisible();
  await expect(page.getByText("共 1 所学校 · 1 使用中 · 0 已停用")).toBeVisible();
  await expect(page.getByRole("columnheader")).toHaveText(["学校", "管理员", "账号 / 学生", "AI 用量", "模型", "最近活跃", "创建时间", "状态", "操作"]);
  await expect(page.getByRole("button", { name: "北京第一中学", exact: true })).toBeVisible();
  await expect(page.getByText("16.1M", { exact: true })).toBeVisible();

  const listRequest = page.waitForRequest((request) => request.url().includes("/api/v1/platform/schools?") && new URL(request.url()).searchParams.get("q") === "zhangsan");
  await page.getByPlaceholder("搜索学校名称、代码或管理员").fill("zhangsan");
  await listRequest;
  await page.getByPlaceholder("搜索学校名称、代码或管理员").fill("");

  await page.getByRole("button", { name: "北京第一中学", exact: true }).click();
  const drawer = page.locator(".platform-school-drawer");
  await expect(drawer.locator(".platform-school-drawer-heading .mono")).toHaveText("beijing-no1");
  await expect(drawer.getByRole("tab")).toHaveText(["概览", "成员与账号", "AI 用量", "模型服务", "活动与安全"]);

  await drawer.getByRole("tab", { name: "成员与账号" }).click();
  await expect(drawer.getByText("admin01", { exact: true })).toBeVisible();
  await expect(drawer.getByText("138****5678", { exact: true })).toBeVisible();

  await drawer.getByRole("tab", { name: "AI 用量" }).click();
  await expect(drawer.getByText("主观题阅卷", { exact: true })).toBeVisible();
  await expect(drawer.getByText("Qwen3-VL-Flash", { exact: true })).toBeVisible();
  await expect(drawer.getByText("预估成本不等于供应商最终账单", { exact: false })).toBeVisible();

  await drawer.getByRole("tab", { name: "模型服务" }).click();
  await expect(drawer.getByText("Qwen-Primary-A", { exact: true })).toBeVisible();
  await expect(drawer.getByText("Qwen-Arbiter", { exact: true })).toBeVisible();

  await drawer.getByRole("tab", { name: "活动与安全" }).click();
  await expect(drawer.getByText("发布期中考试成绩", { exact: true })).toBeVisible();
  await expect(drawer.getByText("1 / 2", { exact: true })).toBeVisible();
});
