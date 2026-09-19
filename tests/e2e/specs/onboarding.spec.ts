import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test.setTimeout(90_000);
const patientExpect = expect.configure({ timeout: 15_000 });

function platformReadiness(firstSchoolReady: boolean) {
  return {
    scope: "platform",
    ready_for_use: firstSchoolReady,
    completed_count: firstSchoolReady ? 3 : 2,
    total_required: 3,
    checks: [
      { key: "system_core", state: "ready", severity: "blocking", title: "系统运行正常", action_path: "/system/status" },
      { key: "platform_admin", state: "ready", severity: "blocking", title: "平台管理员有效" },
      { key: "first_school", state: firstSchoolReady ? "ready" : "action_required", severity: "blocking", title: firstSchoolReady ? "已创建第一所学校" : "创建第一所学校", action_code: "create_school", action_path: "/platform/getting-started" },
      { key: "ai_mode", state: "ready", severity: "recommended", title: "当前使用本地模型", action_path: "/platform/model-config", metadata: { mode: "local", configured: true, available: true, model: "Qwen3-4B" } },
      { key: "data_policy", state: "ready", severity: "recommended", title: "答题数据保持在本地", action_path: "/system/models", metadata: { external_enabled: false, text_export_enabled: false, image_export_enabled: false, fallback_mode: "manual_only" } }
    ],
    ...(!firstSchoolReady ? { next_action: { code: "create_school", path: "/platform/getting-started" } } : {})
  };
}

function schoolReadiness(ready: boolean) {
  const checks = [
    { key: "school", state: "ready", severity: "blocking", title: "学校资料", action_path: "/organization/setup" },
    { key: "teaching_structure", state: ready ? "ready" : "action_required", severity: "blocking", title: "年级与班级", action_code: "continue_school_setup", action_path: "/organization/setup" },
    { key: "students", state: ready ? "ready" : "action_required", severity: "blocking", title: "学生", action_code: "continue_school_setup", action_path: "/organization/setup" },
    { key: "staff", state: "ready", severity: "blocking", title: "教师与管理员", action_path: "/organization/setup" },
    { key: "first_exam", state: "action_required", severity: "recommended", title: "创建第一场考试", action_code: "create_exam", action_path: "/exams/new" }
  ];
  return {
    scope: "school",
    ready_for_use: ready,
    completed_count: ready ? 4 : 2,
    total_required: 4,
    checks,
    next_action: ready ? { code: "create_exam", path: "/exams/new" } : { code: "continue_school_setup", path: "/organization/setup" }
  };
}

test("全新平台管理员从工作台自动进入首次启用", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/onboarding/readiness", (route) => route.fulfill({ json: platformReadiness(false) }));
  await page.goto("/#/admin/dashboard");
  await patientExpect(page).toHaveURL(/platform\/getting-started/);
  await patientExpect(page.getByRole("heading", { name: "把系统交付给第一所学校" })).toBeVisible();
});

test("创建第一所学校后 readiness 刷新且刷新浏览器仍保持完成", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  let created = false;
  await page.route("**/api/v1/onboarding/readiness", (route) => route.fulfill({ json: platformReadiness(created) }));
  await page.route("**/api/v1/tenants", async (route) => {
    if (route.request().method() === "POST") {
      created = true;
      return route.fulfill({ status: 201, json: { tenant: { id: "tenant-new", name: "新学校", code: "new-school", status: "active" } } });
    }
    return route.fallback();
  });
  await page.goto("/#/admin/platform/getting-started");
  await page.getByLabel("学校名称").fill("新学校");
  await page.getByLabel("学校代码").fill("new-school");
  await page.getByLabel("管理员姓名").fill("陈老师");
  await page.getByLabel("管理员账号").fill("school-admin");
  await page.getByLabel("初始密码").fill("Strong-First-Password-2026!");
  await page.getByRole("button", { name: "创建学校并继续" }).click();
  await patientExpect(page.getByText("3 / 3")).toBeVisible();
  await page.reload();
  await patientExpect(page.getByText("3 / 3")).toBeVisible();
});

test("readiness 接口失败时工作台保持可访问且不循环跳转", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/onboarding/readiness", (route) => route.fulfill({ status: 500, json: { error: { code: "onboarding_readiness_failed", message: "failed" } } }));
  await page.goto("/#/admin/dashboard");
  await patientExpect(page).toHaveURL(/admin\/dashboard/);
  await patientExpect(page.getByText("考试工作台", { exact: true })).toBeVisible();
  await patientExpect(page.getByText("暂时无法读取启用状态", { exact: true })).toBeVisible();
});

test("学校管理员基础数据不完整时从工作台进入学校初始化", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/onboarding/readiness", (route) => route.fulfill({ json: schoolReadiness(false) }));
  await page.goto("/#/admin/dashboard");
  await patientExpect(page).toHaveURL(/organization\/setup/);
  await patientExpect(page.getByRole("heading", { name: "学校初始化" })).toBeVisible();
});

test("学校基础数据完整但无考试时可留在工作台并看到推荐", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/onboarding/readiness", (route) => route.fulfill({ json: schoolReadiness(true) }));
  await page.goto("/#/admin/dashboard");
  await patientExpect(page).toHaveURL(/admin\/dashboard/);
  await patientExpect(page.getByText("考试工作台", { exact: true })).toBeVisible();
  await patientExpect(page.getByText("基础启用已完成", { exact: true })).toBeVisible();
  await patientExpect(page.getByText(/建议完成：创建第一场考试/)).toBeVisible();
});
