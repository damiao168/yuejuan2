import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

const desktopViewports = [
  { name: "1366x768", width: 1366, height: 768 },
  { name: "1440x900", width: 1440, height: 900 },
  { name: "1920x1080", width: 1920, height: 1080 }
] as const;

test.use({
  colorScheme: "light",
  locale: "zh-CN",
  timezoneId: "Asia/Shanghai"
});

for (const viewport of desktopViewports) {
  test(`exam workspace showcase is stable at ${viewport.name}`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
    // 展示已保存的评分方式；缺少新增目录接口会产生 404，不能把错误画面录入展示基线。
    const profileID = "senior.mathematics.v1";
    await page.route("**/api/v1/exams/exam-1/questions", route => route.fulfill({ json: { questions: [
      { id: "question-visual", exam_id: "exam-1", question_no: "1", question_type: "calculation", stem: "计算并说明步骤。", score: 10, page_no: 1, status: "active", revision: 1, knowledge_points: [] }
    ] } }));
    await page.route("**/api/v1/assessment/subject-profiles", route => route.fulfill({ json: { subject_profiles: [
      { id: profileID, code: "senior.mathematics.standard", education_stage: "senior", subject_code: "mathematics", version: 1, status: "active", parser_policy: {}, evidence_policy: { allowed_types: ["math_step"] }, scoring_default: { mode: "AI_ASSIST", require_evidence: true } }
    ] } }));
    await page.route("**/api/v1/assessment/question-archetypes", route => route.fulfill({ json: { question_archetypes: [
      { code: "structured_steps", response_schema: {}, evidence_types: ["math_step"], default_scoring_mode: "AI_ASSIST" }
    ] } }));
    await page.route("**/api/v1/exams/exam-1/questions/question-visual/assessment-profile", route => route.fulfill({ json: { assessment_profile: {
      id: "assessment-visual", exam_id: "exam-1", question_id: "question-visual", subject_profile_id: profileID,
      education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps", allowed_evidence_types: ["math_step"], risk_tier: "R2", scoring_policy: { mode: "AI_ASSIST", require_evidence: true }, revision: 1
    } } }));
    await page.goto("/#/admin/exams/exam-1/settings", { waitUntil: "domcontentloaded" });

    const workspace = page.locator(".eg-workspace-layout");
    await expect(workspace).toBeVisible({ timeout: 30_000 });
    const stages = workspace.getByRole("combobox", { name: "切换考试环节", exact: true });
    await expect(stages).toBeVisible();
    await expect(workspace.locator(".exam-context-status")).toContainText("答卷导入");
    await stages.press("ArrowDown");
    await expect(page.locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option-content")).toHaveText(["考试准备", "答卷导入", "阅卷", "成绩"]);
    await stages.press("Escape");
    await expect(page.getByRole("navigation", { name: "考试准备" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "准备检查与确认" })).toBeVisible();
    await expect(page.getByText("已核实 1 / 1 题。先核对谁来判分，再冻结本场考试。", { exact: true })).toBeVisible();
    await expect(page.getByRole("row", { name: "第 1 题 系统建议分数，教师逐题确认 已保存", exact: true })).toBeVisible();
    await expect(page.getByText("请求的内容不存在或已被删除。", { exact: true })).toHaveCount(0);
    await page.evaluate(async () => document.fonts.ready);
    // 异步评分表格变高可能触发浏览器滚动锚定；展示基线固定首屏，并验证考试页头完整可见。
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect(workspace.locator(".exam-context-header")).toBeInViewport({ ratio: 1 });

    const dimensions = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth
    }));
    expect(dimensions.viewportWidth).toBe(viewport.width);
    expect(dimensions.documentWidth).toBeLessThanOrEqual(viewport.width);

    const image = await page.screenshot({ fullPage: false, animations: "disabled", scale: "css" });
    await testInfo.attach(`exam-workspace-${viewport.name}`, {
      body: image,
      contentType: "image/png"
    });
    // 仓库中的 Windows 展示主机与 Linux CI 在字体和栅格化方面
    // 存在像素渲染差异。CI 仍会执行上面的所有布局断言，
    // 并发布实际截图附件。
    if (process.platform === "win32") {
      await expect(page).toHaveScreenshot(`exam-workspace-${viewport.name}.png`, {
        animations: "disabled",
        caret: "hide",
        scale: "css"
      });
    }
  });
}

test("new exam opens a focused paper-upload first screen", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/exams/exam-created-math/papers", (route) => route.fulfill({ json: { papers: [] } }));
  await page.route("**/api/v1/exams/exam-created-math/questions", (route) => route.fulfill({ json: { questions: [] } }));
  await page.route("**/api/v1/exams/exam-created-math/paper-imports", (route) => route.fulfill({ json: { imports: [] } }));
  await page.goto("/#/admin/exams/exam-created-math/paper", { waitUntil: "domcontentloaded" });

  await expect(page.getByRole("heading", { name: "添加试卷资料" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("navigation", { name: "考试准备" })).toBeVisible();
  await expect(page.getByPlaceholder("选择考试")).toHaveCount(0);
  await page.evaluate(async () => document.fonts.ready);

  const image = await page.screenshot({ fullPage: false, animations: "disabled", scale: "css" });
  await testInfo.attach("exam-paper-empty-1440x900", { body: image, contentType: "image/png" });
  if (process.platform === "win32") {
    await expect(page).toHaveScreenshot("exam-paper-empty-1440x900.png", { animations: "disabled", caret: "hide", scale: "css" });
  }
});
