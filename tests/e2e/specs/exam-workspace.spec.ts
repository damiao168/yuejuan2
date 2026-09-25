import { expect, test } from "@playwright/test";
import { captureFailedRequests, installApiMocks } from "../fixtures/apiMocks";

test("mock API 支持动态考试 ID，并在测试层拦截未配置接口", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  const failedRequests = captureFailedRequests(page);
  await page.goto("/#/admin/dashboard", { waitUntil: "domcontentloaded" });

  const result = await page.evaluate(async () => {
    const workspaceResponse = await fetch("/api/v1/exams/exam-created-math/workspace");
    const workspace = await workspaceResponse.json();
    const missingResponse = await fetch("/api/v1/e2e-unconfigured");
    const missing = await missingResponse.json();
    return {
      workspaceStatus: workspaceResponse.status,
      workspaceExamID: workspace.workspace.exam_id,
      missingStatus: missingResponse.status,
      missingCode: missing.error.code
    };
  });

  expect(result).toEqual({
    workspaceStatus: 200,
    workspaceExamID: "exam-created-math",
    missingStatus: 404,
    missingCode: "e2e_mock_missing"
  });
  expect(failedRequests).toEqual([]);
});

test("考试工作区按四阶段导航，并让阻断项跳到可处理环节", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.goto("/#/admin/exams/exam-1/overview", { waitUntil: "domcontentloaded" });

  await expect(page.getByRole("heading", { name: "2026 春季数学期中考试" })).toBeVisible({ timeout: 30_000 });
  const stageRail = page.getByRole("navigation", { name: "考试流程" });
  await expect(stageRail.getByRole("button")).toHaveCount(4);
  await expect(stageRail).toContainText("考试准备");
  await expect(stageRail).toContainText("答卷导入");
  await expect(stageRail).toContainText("阅卷");
  await expect(stageRail).toContainText("成绩");
  await expect(page.locator("#primary-navigation")).toBeVisible();

  await page.getByRole("button", { name: "查看并重试", exact: true }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-1\/capture/);
  await expect(page.locator("#primary-navigation")).toBeVisible();

  await page.getByRole("button", { name: /阅卷/ }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-1\/grading/);
  await expect(page.locator("#primary-navigation")).toBeVisible();
  await page.getByRole("button", { name: /成绩/ }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-1\/scores/);
  await expect(page.locator("#primary-navigation")).toBeVisible();
});

test("左侧显示固定准备任务，右侧仅显示当前任务", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.goto("/#/admin/exams/exam-created-math/settings", { waitUntil: "domcontentloaded" });

  await expect(page.getByRole("heading", { name: "准备检查与确认" })).toBeVisible({ timeout: 60_000 });
  await expect(page.locator(".exam-preparation-nav button > span:last-child")).toHaveText([
    "学生范围", "考试资料", "小题与分值", "答题卡设置", "确认准备完成"
  ]);
  await expect(page.locator(".exam-preparation-nav .exam-preparation-step-number")).toHaveText(["1", "2", "3", "4", "5"]);
  await expect(page.locator(".exam-setup-questions")).toHaveCount(0);
  await expect(page.locator(".exam-readiness-issues")).toContainText("请上传并确认考试资料");

  await page.locator(".exam-readiness-issues li").filter({ hasText: "上传试卷" }).getByRole("button", { name: "去处理" }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-created-math\/paper/);

  await page.getByRole("navigation", { name: "考试准备" }).getByRole("button", { name: "学生范围" }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-created-math\/students/);
  await expect(page.getByRole("heading", { name: "学生范围" })).toBeVisible();
});

test("没有考试资料时仍可手动配置小题", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/exams/exam-1/papers", (route) => route.fulfill({ json: { papers: [] } }));
  await page.route("**/api/v1/exams/exam-1/questions", (route) => route.fulfill({ json: { questions: [] } }));
  await page.route("**/api/v1/exams/exam-1/paper-imports", (route) => route.fulfill({ json: { imports: [] } }));

  await page.goto("/#/admin/exams/exam-1/questions", { waitUntil: "domcontentloaded" });

  await expect(page.getByText("尚未上传考试资料", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("也可以继续手动添加题目。", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "新建", exact: true })).toBeEnabled();
});

test("阅卷员无法通过考试工作区深链访问学校管理员环节", async ({ page }) => {
  await installApiMocks(page, { role: "grader", initiallyAuthenticated: true });
  await page.goto("/#/admin/exams/exam-1/overview", { waitUntil: "domcontentloaded" });

  await expect(page.getByText("无权限", { exact: true })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "考试流程" })).toHaveCount(0);
});

test("嵌入式考试资料固定当前考试，并直接显示空资料上传入口", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  let examListRequests = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/exams") examListRequests += 1;
  });
  await page.route("**/api/v1/exams/exam-1/papers", (route) => route.fulfill({ json: { papers: [] } }));
  await page.route("**/api/v1/exams/exam-1/questions", (route) => route.fulfill({ json: { questions: [] } }));
  await page.route("**/api/v1/exams/exam-1/paper-imports", (route) => route.fulfill({ json: { imports: [] } }));

  await page.goto("/#/admin/exams/exam-1/paper", { waitUntil: "domcontentloaded" });

  await expect(page.getByRole("heading", { name: "添加试卷资料" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("选择文件或拖到这里", { exact: true })).toBeVisible();
  await expect(page.getByPlaceholder("选择考试")).toHaveCount(0);
  expect(examListRequests).toBe(0);
});

test("同场次学科切换直接使用工作台投影，不拉取全校考试列表", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true, sessionSubjects: true });
  let examListRequests = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/exams") examListRequests += 1;
  });

  await page.goto("/#/admin/exams/exam-1/settings", { waitUntil: "networkidle" });
  await expect(page.getByRole("heading", { name: "2026 春季期中考试" })).toBeVisible();
  await page.locator(".exam-workspace-subject .ant-select-selector").click();
  await page.getByTitle("物理 · 100 分").click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-physics\/settings/);
  expect(examListRequests).toBe(0);
});
