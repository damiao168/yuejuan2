import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { contextFor, createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("当前 AI 失败隐藏历史建议，教师草稿和人工提交仍可使用（本地夹具）", async ({ page }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const state = createGradingWorkbenchMockState(1);
  const task = state.tasks[0];
  state.fastConfirmTaskIds.add(task.id);
  let currentFailure = false;
  await installGradingWorkbenchMocks(page, state);
  await page.route(`**/api/v1/review-tasks/${task.id}/context`, async route => {
    state.contextsRead.push(task.id);
    // 故意同时保留旧成功建议和当前失败，验证页面不会把历史结果当作本次有效输出。
    const context = contextFor(task, state.drafts.get(task.id), true);
    await route.fulfill({ json: { context: { ...context, ...(currentFailure ? { ai_failure: { failure_kind: "output_truncated" } } : {}) } } });
  });
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "采纳 AI 建议分" })).toBeEnabled();
  await score.fill("4");
  await page.getByText("评语与备注（可选）", { exact: true }).click();
  await page.locator("#grading-student-feedback-input").fill("教师保留的学生反馈");
  await page.locator("#grading-private-note-input").fill("教师保留的内部备注");
  await expect(page.locator(".draft-save-status")).toHaveText("已保存到服务端", { timeout: 8_000 });
  await expect.poll(() => state.drafts.get(task.id)?.private_note).toBe("教师保留的内部备注");

  currentFailure = true;
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(page.getByText("AI 建议未完成，请人工评分。", { exact: true })).toBeVisible();
  await expect(score).toHaveValue("4.0");
  await expect(page.locator("#grading-student-feedback-input")).toHaveValue("教师保留的学生反馈");
  await expect(page.locator("#grading-private-note-input")).toHaveValue("教师保留的内部备注");
  await expect(page.getByRole("button", { name: "采纳 AI 建议分" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "审读 AI 反馈" })).toHaveCount(0);
  await expect(page.getByText("AI 学生反馈草稿", { exact: true })).toHaveCount(0);
  await page.screenshot({ path: "output/playwright/ai-output-incomplete-teacher.png", fullPage: true });

  await page.getByRole("button", { name: "查看待确认的建议分" }).click();
  const drawer = page.getByRole("dialog", { name: "批量确认 AI 建议分" });
  await expect(drawer.getByText("本次检查的任务中，暂无可批量确认的 AI 建议分")).toBeVisible();
  await expect(drawer.getByRole("button", { name: /提交已核对/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "提交并下一份" }).click();
  await expect.poll(() => state.submissions).toEqual([task.id]);
});

test("主观题批次显示固定失败标签和未知用量，查看诊断不创建新批次（本地夹具）", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.addInitScript(() => localStorage.setItem("subjective-batch-command:demo-school:user-school_admin:batch", "failed-batch"));
  let created = 0;
  await page.route("**/api/v1/subjective-grading-batches", route => {
    created++;
    return route.fulfill({ status: 500, json: { error: { code: "unexpected_create" } } });
  });
  await page.route("**/api/v1/subjective-grading-batches/failed-batch", route => route.fulfill({ json: { batch: {
    id: "failed-batch", tenant_id: "tenant-school", idempotency_key: "fixture", status: "failed",
    segment_ids: ["segment-1", "segment-2"], total_count: 2, queued_count: 0, processing_count: 0,
    succeeded_count: 0, failed_count: 2, created_by: "user-school_admin", created_at: "2026-10-18T00:00:00Z", updated_at: "2026-10-18T00:00:00Z",
    failure_summary: [
      { failure_kind: "output_truncated", count: 1, attempts: 1, provider_request_count: 1, usage_status: "partial" },
      { failure_kind: "response_envelope_invalid", count: 1, usage_status: "unavailable" }
    ]
  } } }));
  await page.goto("/#/grading/subjective-batches", { waitUntil: "domcontentloaded" });
  const diagnostics = page.getByLabel("AI 失败诊断");
  await expect(diagnostics).toBeVisible({ timeout: 30_000 });
  await expect(diagnostics).toContainText("输出达到上限 · 1 项");
  await expect(diagnostics).toContainText("响应结构异常 · 1 项");
  await expect(diagnostics).toContainText("尝试次数未知 · 上游调用次数未知 · 用量：未报告（未知）");
  await expect(diagnostics).toContainText("尝试 1 次 · 上游调用 1 次 · 用量：部分报告，仍有未知用量");
  expect(created).toBe(0);
  await page.screenshot({ path: "output/playwright/ai-output-incomplete-batch.png", fullPage: true });
});
