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
    // 准备页使用同次检查返回的有效评分方案，展示夹具也须提供该摘要。
    const profileID = "senior.mathematics.v1";
    await page.route("**/api/v1/exams/exam-1/readiness", route => route.fulfill({ json: { readiness: {
      ready: false, confirmed: false, configuration_hash: "visual-readiness", import_snapshot_available: false,
      checks: [{ code: "paper_file", label: "上传试卷", passed: false, severity: "blocker", message: "请上传并确认考试资料", section: "paper" }],
      advisories: [], scoring_preview: [{ question_id: "question-visual", question_no: "1", source: "saved",
        profile: { id: profileID, code: "senior.mathematics.standard", education_stage: "senior", subject_code: "mathematics", version: 1 },
        archetype_code: "structured_steps", allowed_evidence_types: ["math_step"], risk_tier: "R2",
        scoring_policy: { mode: "AI_ASSIST", require_evidence: true }, reason: "沿用本题已保存的评分方案" }]
    } } }));
    await page.goto("/#/admin/exams/exam-1/settings", { waitUntil: "domcontentloaded" });

    const workspace = page.locator(".eg-workspace-layout");
    await expect(workspace).toBeVisible({ timeout: 30_000 });
    const preparation = page.getByRole("navigation", { name: "考试准备与答卷导入", exact: true });
    await expect(preparation.locator("button > span:last-child")).toHaveText([
      "学生范围", "考试资料", "小题与分值", "答题卡设置", "确认准备完成", "学生答卷"
    ]);
    await expect(page.getByRole("heading", { name: "确认准备完成" })).toBeVisible();
    const scoring = page.locator(".scoring-preparation");
    await expect(scoring.getByText("已匹配 1 题 · 沿用单题设置 1 题", { exact: true })).toBeVisible();
    await scoring.getByRole("button", { name: "查看评分明细", exact: true }).click();
    await expect(scoring.getByRole("cell", { name: "系统建议分数，教师逐题确认", exact: true })).toBeVisible();
    await expect(scoring.getByRole("cell", { name: "单题设置", exact: true })).toBeVisible();
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
  await expect(page.getByRole("navigation", { name: "考试准备与答卷导入", exact: true })).toBeVisible();
  await expect(page.getByPlaceholder("选择考试")).toHaveCount(0);
  await page.evaluate(async () => document.fonts.ready);

  const image = await page.screenshot({ fullPage: false, animations: "disabled", scale: "css" });
  await testInfo.attach("exam-paper-empty-1440x900", { body: image, contentType: "image/png" });
  if (process.platform === "win32") {
    await expect(page).toHaveScreenshot("exam-paper-empty-1440x900.png", { animations: "disabled", caret: "hide", scale: "css" });
  }
});
