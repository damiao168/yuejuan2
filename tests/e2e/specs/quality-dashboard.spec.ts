import { expect, test } from "@playwright/test";
import { installQualityDashboardMocks } from "../fixtures/qualityDashboardMocks";
import { installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

for (const initialStatus of ["active", "paused"] as const) {
  test(`盲测 ${initialStatus} 保存暂停状态`, async ({ page }) => {
    const state = await installQualityDashboardMocks(page, initialStatus);
    await page.goto("/#/admin/exams/exam-1/quality");
    await page.getByRole("button", { name: "盲测策略", exact: true }).click();
    const toggle = page.getByRole("switch");
    if (initialStatus === "active") { await expect(toggle).toBeChecked(); await toggle.click(); }
    await expect(toggle).not.toBeChecked();
    await page.getByRole("button", { name: /^保\s*存$/ }).click();
    await expect.poll(() => state.policyWrites).toHaveLength(1);
    expect(state.policyWrites[0].status).toBe("paused");
    await expect(toggle).not.toBeChecked();
  });
}

test("所有范围字段修改后必须重新预览，第二页阅卷员可分配", async ({ page }) => {
  const state = await installQualityDashboardMocks(page);
  await page.goto("/#/admin/exams/exam-1/quality");
  await page.getByRole("button", { name: "创建回标", exact: true }).click();
  const create = page.getByRole("button", { name: "创建并分配回标", exact: true });
  const preview = page.getByRole("button", { name: "预览影响范围", exact: true });
  await expect(create).toBeDisabled();
  await expect.poll(() => state.userCursors).toEqual(["", "users-page-2"]);
  for (const [label, value] of [["原分下限", "80"], ["原分上限", "100"], ["仅原阅卷员 ID（可选）", "grader-0"]]) {
    await preview.click();
    await expect(create).toBeEnabled();
    await page.getByLabel(label, { exact: true }).fill(value);
    await expect(create).toBeDisabled();
    await expect(page.getByText("受影响任务", { exact: true })).toHaveCount(0);
  }
  await preview.click();
  await expect(create).toBeEnabled();
  await page.locator(".ant-select").filter({ has: page.getByLabel("题目", { exact: true }) }).click();
  await page.getByText("Q2 · structured_steps", { exact: true }).click();
  await expect(create).toBeDisabled();
  await preview.click();
  await expect(create).toBeEnabled();
  const dates = page.locator(".ant-picker-range input");
  await dates.nth(0).fill("2026-09-01");
  await dates.nth(0).press("Enter");
  await dates.nth(1).fill("2026-09-15");
  await dates.nth(1).press("Enter");
  await expect(create).toBeDisabled();
  await page.getByLabel("质量事件编号", { exact: true }).fill("incident-1");
  await page.getByLabel("指定回标阅卷员", { exact: true }).fill("第二页");
  await page.getByText("第二页阅卷员", { exact: true }).click();
  await preview.click();
  await expect(create).toBeEnabled();
  await create.click();
  await expect.poll(() => state.creates).toHaveLength(1);
  expect(state.creates[0]).toMatchObject({ reassigned_to: "grader-last", selector_hash: "a".repeat(64), selector: { score_band: { min: 80, max: 100 }, grader_id: "grader-0" } });
});

test("延迟返回的旧预览不能恢复创建入口", async ({ page }) => {
  const state = await installQualityDashboardMocks(page);
  let release: () => void = () => {};
  state.previewWait = new Promise<void>((resolve) => { release = resolve; });
  await page.goto("/#/admin/exams/exam-1/quality");
  await page.getByRole("button", { name: "创建回标", exact: true }).click();
  await page.getByRole("button", { name: "预览影响范围", exact: true }).click();
  await expect.poll(() => state.previews).toBe(1);
  await page.getByLabel("原分下限", { exact: true }).fill("80");
  const finished = page.waitForResponse((r) => r.url().endsWith("/backmark-preview"));
  release();
  await finished;
  await expect(page.getByRole("button", { name: "创建并分配回标", exact: true })).toBeDisabled();
  await expect(page.getByText("受影响任务", { exact: true })).toHaveCount(0);
});

test("后端拒绝过期预览后清除范围确认并提示重新预览", async ({ page }) => {
  const state = await installQualityDashboardMocks(page);
  state.rejectCreateAsStale = true;
  await page.goto("/#/admin/exams/exam-1/quality");
  await page.getByRole("button", { name: "创建回标", exact: true }).click();
  await page.getByLabel("质量事件编号", { exact: true }).fill("incident-1");
  await page.getByLabel("指定回标阅卷员", { exact: true }).fill("第二页");
  await page.getByText("第二页阅卷员", { exact: true }).click();
  await page.getByRole("button", { name: "预览影响范围", exact: true }).click();
  const create = page.getByRole("button", { name: "创建并分配回标", exact: true });
  await expect(create).toBeEnabled();
  await create.click();
  await expect(create).toBeDisabled();
  await expect(page.getByText("回标范围或原评分已变化，请重新预览影响范围后再创建。", { exact: true })).toBeVisible();
});

test("严重项总数不受当前页影响，仍显示正式复评入口", async ({ page }) => {
  const state = await installQualityDashboardMocks(page);
  await page.goto("/#/admin/exams/exam-1/quality");
  await page.getByRole("button", { name: "创建回标", exact: true }).click();
  await page.getByRole("button", { name: "查看差异", exact: true }).click();
  await expect(page.getByRole("heading", { name: "转入题目复评", exact: true })).toBeVisible();
  await expect(page.getByText("8 份", { exact: true })).toBeVisible();
  expect(state.summaryFilters).toEqual(["regrade_required"]);
});

test("评分工作台回标 mock 与生成 SDK 的路由和响应保持一致", async ({ page }) => {
  await installGradingWorkbenchMocks(page);
  await page.goto("/#/teacher/grading");
  const response = await page.evaluate(async () => {
    const r = await fetch("/api/v1/backmark-batches?exam_id=exam-1&limit=50");
    return { status: r.status, body: await r.json() };
  });
  expect(response).toEqual({ status: 200, body: { backmark_batches: [], next_cursor: "", has_more: false } });
});
