import { expect, test, type Locator, type Page } from "@playwright/test";
import { installQualityWorkflowMocks } from "../fixtures/qualityWorkflowMocks";
import { installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

for (const initialStatus of ["active", "paused"] as const) {
  test(`盲测 ${initialStatus} 保存暂停状态`, async ({ page }) => {
    const state = await installQualityWorkflowMocks(page, initialStatus);
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

async function openBackmark(page: Page) {
  await page.goto("/#/admin/exams/exam-1/quality");
  await page.getByRole("button", { name: "创建回标", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "创建回标批次", exact: true });
  await expect(drawer.getByText("Q1 · structured_steps", { exact: true })).toBeVisible();
  return drawer;
}

async function selectReviewer(page: Page, drawer: Locator, field: string, search: string, label: string) {
  await drawer.getByLabel(field, { exact: true }).fill(search);
  await page.locator(".ant-select-dropdown:visible").getByText(label, { exact: true }).click();
}

async function selectIncident(page: Page, drawer: Locator, questionNo: string) {
  await drawer.getByLabel("来源质量事件", { exact: true }).click();
  await page.locator(".ant-select-dropdown:visible").getByText(new RegExp(`^${questionNo} · 严重 · 评分持续偏高 · 20份样本`)).click();
}

test("所有范围字段修改后必须重新预览，第二页阅卷员可分配", async ({ page }) => {
  const state = await installQualityWorkflowMocks(page);
  const drawer = await openBackmark(page);
  const create = drawer.getByRole("button", { name: "创建并分配回标", exact: true });
  const preview = drawer.getByRole("button", { name: "预览影响范围", exact: true });
  await expect(create).toBeDisabled();
  await expect.poll(() => state.actorRequests).toEqual(["exam-1"]);
  expect(state.userCursors).toEqual([]);
  for (const [label, value] of [["原分下限", "80"], ["原分上限", "100"]]) {
    await preview.click();
    await expect(create).toBeEnabled();
    await drawer.getByLabel(label, { exact: true }).fill(value);
    await expect(create).toBeDisabled();
    await expect(drawer.getByText("受影响任务", { exact: true })).toHaveCount(0);
  }
  await preview.click();
  await expect(create).toBeEnabled();
  await selectReviewer(page, drawer, "原阅卷教师（可选）", "阅卷员 1", "阅卷员 1（账号：grader-0）");
  await expect(create).toBeDisabled();
  await expect(drawer.getByText("受影响任务", { exact: true })).toHaveCount(0);
  await preview.click();
  await expect(create).toBeEnabled();
  await drawer.getByText("Q1 · structured_steps", { exact: true }).click();
  await page.locator(".ant-select-dropdown:visible").getByText("Q2 · structured_steps", { exact: true }).click();
  await expect(create).toBeDisabled();
  await preview.click();
  await expect(create).toBeEnabled();
  const dates = drawer.locator(".ant-picker-range input");
  await dates.nth(0).fill("2026-09-01");
  await dates.nth(0).press("Enter");
  await dates.nth(1).fill("2026-09-15");
  await dates.nth(1).press("Enter");
  await expect(create).toBeDisabled();
  await expect(drawer.getByText("受影响任务", { exact: true })).toHaveCount(0);
  await selectIncident(page, drawer, "Q2");
  await selectReviewer(page, drawer, "指定回标阅卷员", "第二页", "第二页阅卷员（账号：grader-last）");
  await preview.click();
  await expect(create).toBeEnabled();
  await create.click();
  await expect.poll(() => state.creates).toHaveLength(1);
  const timeRange = await page.evaluate(() => ({ from: new Date(2026, 8, 1).toISOString(), to: new Date(2026, 8, 15, 23, 59, 59, 999).toISOString() }));
  expect(state.creates[0]).toMatchObject({ source_incident_id: "incident-2", reassigned_to: "grader-last", selector_hash: "a".repeat(64), selector: { score_band: { min: 80, max: 100 }, grader_id: "grader-0", time_range: timeRange } });
  expect(state.previewRequests.at(-1)).toMatchObject({ questionID: "question-2", selector: state.creates[0].selector });
});

test("延迟返回的旧预览不能恢复创建入口", async ({ page }) => {
  const state = await installQualityWorkflowMocks(page);
  let release: () => void = () => {};
  // 先挂起预览，再改筛选范围后放行旧响应，稳定复现过期响应覆盖当前表单的时序。
  state.previewWait = new Promise<void>((resolve) => { release = resolve; });
  const drawer = await openBackmark(page);
  await drawer.getByRole("button", { name: "预览影响范围", exact: true }).click();
  await expect.poll(() => state.previews).toBe(1);
  await drawer.getByLabel("原分下限", { exact: true }).fill("80");
  const finished = page.waitForResponse((r) => r.url().endsWith("/backmark-preview"));
  release();
  await finished;
  await expect(drawer.getByRole("button", { name: "创建并分配回标", exact: true })).toBeDisabled();
  await expect(drawer.getByText("受影响任务", { exact: true })).toHaveCount(0);
  expect(state.creates).toHaveLength(0);
});

test("后端拒绝过期预览后清除范围确认并提示重新预览", async ({ page }) => {
  const state = await installQualityWorkflowMocks(page);
  state.rejectCreateAsStale = true;
  const drawer = await openBackmark(page);
  await selectIncident(page, drawer, "Q1");
  await selectReviewer(page, drawer, "指定回标阅卷员", "第二页", "第二页阅卷员（账号：grader-last）");
  await drawer.getByRole("button", { name: "预览影响范围", exact: true }).click();
  const create = drawer.getByRole("button", { name: "创建并分配回标", exact: true });
  await expect(create).toBeEnabled();
  await create.click();
  await expect.poll(() => state.creates).toHaveLength(1);
  await expect(create).toBeDisabled();
  await expect(drawer.getByText("受影响任务", { exact: true })).toHaveCount(0);
  await expect(page.getByText("回标范围或原评分已变化，请重新预览影响范围后再创建。", { exact: true })).toBeVisible();
});

test("严重项总数不受当前页影响，仍显示正式复评入口", async ({ page }) => {
  const state = await installQualityWorkflowMocks(page);
  const drawer = await openBackmark(page);
  await drawer.getByRole("button", { name: "查看差异", exact: true }).click();
  const summary = page.getByRole("dialog", { name: "回标差异与复评交接", exact: true });
  await expect(summary.getByRole("heading", { name: "转入题目复评", exact: true })).toBeVisible();
  await expect(summary.getByText("8 份", { exact: true })).toBeVisible();
  await expect(summary.getByText("当前页暂无需重评项，请加载更多结果", { exact: true })).toBeVisible();
  await expect(summary.getByRole("button", { name: "加载更多回标结果", exact: true })).toBeVisible();
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
