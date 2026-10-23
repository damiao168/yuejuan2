import { expect, test, type Page } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("grader can open fast confirmation from the personal workbench", async ({ page }) => {
  const state = createGradingWorkbenchMockState(1);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await expect(page.locator('.grading-inspector').getByRole("button", { name: "AI 建议分", exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('.fast-confirm-entry')).toHaveCount(0);
  await page.getByRole("button", { name: "AI 建议分", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "批量确认 AI 建议分" });
  await expect(drawer).toContainText("勾选认可的分数后一起提交");
  await expect(drawer.getByRole("button", { name: /提交已核对/ })).toHaveCount(0);
});

test("fast confirmation requires viewing the answer before the teacher submits", async ({ page }) => {
  const state = createGradingWorkbenchMockState(1);
  state.fastConfirmTaskIds.add(state.tasks[0].id);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "AI 建议分", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "批量确认 AI 建议分" });
  await expect(drawer.getByText("符合条件 1 份")).toBeVisible();
  const confirm = drawer.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" });
  await expect(confirm).toHaveCount(0);
  await drawer.getByRole("button", { name: "核对原图与证据" }).click();
  await expect(drawer.getByRole("img", { name: "待核对的学生答题原图" })).toBeVisible();
  await expect(drawer.locator(".fast-confirm-item").first()).toContainText("本份建议分");
  await expect(confirm).toBeEnabled();
  await expect(drawer.getByText("作答要点与标准答案一致")).toBeVisible();
  await confirm.check();
  await drawer.getByRole("button", { name: "提交已核对的 1 份" }).click();
  await expect.poll(() => state.submissions).toEqual([state.tasks[0].id]);
  await expect(drawer.getByText("已由教师确认 1 份", { exact: false })).toBeVisible();
});

test("fast confirmation keeps the evidence and confirmation usable on a narrow screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const state = createGradingWorkbenchMockState(1);
  state.fastConfirmTaskIds.add(state.tasks[0].id);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "AI 建议分", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "批量确认 AI 建议分" });
  await drawer.getByRole("button", { name: "核对原图与证据" }).click();
  const item = drawer.locator(".fast-confirm-item").first();
  await expect(item.getByRole("img", { name: "待核对的学生答题原图" })).toBeVisible();
  const bounds = await item.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await expect(item.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" })).toBeEnabled();
});

async function installConfirmedAIPlan(page: Page) {
  await page.route("**/api/v1/exams/exam-1/grading-ai-plan", route => route.fulfill({ json: {
    plan: { version: 1, revision: "plan-1", effective_mode: "single", fallback_model_config_id: "model-ai",
      roles: {}, resolved_roles: {}, independent_model_count: 1, reason_codes: [],
      single_model: { model_config_id: "model-ai", display_name: "学校评分模型", model_name: "fixture-model" },
      decision_config: { primary_allowed_diff_points: null, teacher_allowed_diff_points: null, aggregation_version: "dual-conditional-arbiter-v1", rounding: "ceil_final" },
      confirmed_by: "user-school_admin", confirmed_at: "2026-10-01T00:00:00Z" },
    available_models: [], can_configure: true
  } }));
}

test("AI 评分路线提交一次启动命令，刷新后沿用服务端进度", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  await installConfirmedAIPlan(page);
  let started = false;
  let browserBatchCreates = 0;
  const commands: Array<{ key: string; body: { idempotency_key: string } }> = [];
  const run = { id: "run-ai", exam_id: "exam-1", status: "needs_review", total_count: 1, queued_count: 0, auto_confirmed_count: 0, human_confirmed_count: 0, review_count: 1, failed_count: 0 };
  const batch = { id: "batch-ai", idempotency_key: "service-plan-run-ai", status: "processing", segment_ids: ["segment-ai"], total_count: 1, queued_count: 1, processing_count: 0, succeeded_count: 0, failed_count: 0 };
  const item = { answer_segment_id: "segment-ai", question_id: "question-ai", question_no: "1", question_type: "essay", state: "review", decision_status: "suggestion_ready", anonymous_code: "A001", normalized_bbox: { x: 0, y: 0, width: 1, height: 1 } };
  await page.route("**/api/v1/exams/exam-1/scoring-summary", route => route.fulfill({ json: { scoring_summary: { run: started ? run : undefined, questions: [{ question_id: "question-ai", question_no: "1", question_type: "essay", total: 1, queued: 0, confirmed: 0, review: started ? 1 : 0, failed: 0 }] } } }));
  await page.route("**/api/v1/exams/exam-1/questions/question-ai/assessment-snapshot", route => route.fulfill({ json: { assessment_snapshot: { scoring_policy_snapshot: { mode: "AI_ASSIST" }, risk_tier: "R2" } } }));
  await page.route("**/api/v1/exams/exam-1/scoring-runs", route => {
    commands.push({ key: route.request().headers()["idempotency-key"], body: route.request().postDataJSON() });
    started = true;
    return route.fulfill({ status: 201, json: { scoring_run: run } });
  });
  await page.route("**/api/v1/scoring-runs/run-ai/ai-batches", route => route.fulfill({ json: { batches: started ? [batch] : [] } }));
  await page.route("**/api/v1/exams/exam-1/automation-results", route => route.fulfill({ json: { items: [item] } }));
  // 服务端持久计划负责安排批次；浏览器只能提交启动命令和读取进度，不能另拼一套批次。
  await page.route("**/api/v1/subjective-grading-batches", route => {
    browserBatchCreates++;
    return route.fulfill({ status: 500, json: { error: { code: "unexpected_browser_batch" } } });
  });

  await page.goto("/#/admin/exams/exam-1/grading");
  const start = page.getByRole("button", { name: "开始评分", exact: true });
  await expect(start).toBeEnabled({ timeout: 15_000 });
  await start.click();
  const plan = page.getByRole("dialog", { name: "确认本次评分路线" });
  await expect(plan.locator(".grading-plan-counts")).toContainText("AI 建议");
  await expect(plan.getByText("1 · 作文题")).toBeVisible();
  await expect(plan.getByText("自动识别学生作答并生成建议分，由教师确认最终分数", { exact: true })).toBeVisible();
  await expect(plan.getByText("单模型评分 · 非三智能体", { exact: true })).toBeVisible();
  await plan.getByRole("button", { name: "按此路线启动" }).click();
  await expect.poll(() => commands.length).toBe(1);
  expect(commands[0].key).toMatch(/^web-[a-f0-9-]{36}$/);
  expect(commands[0].body.idempotency_key).toBe(commands[0].key);
  await page.reload();
  await expect(page.getByRole("button", { name: "确认建议分", exact: true })).toBeVisible({ timeout: 15_000 });
  expect(commands).toHaveLength(1);
  expect(browserBatchCreates).toBe(0);
});

// 已接受运行暂时没有 AI 批次时，页面只恢复服务端事实；失败项由明确操作交回原运行重试。
test("已接受运行恢复服务端事实，失败项通过原运行重试且不重复创建", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  await installConfirmedAIPlan(page);
  let failed = false;
  let starts = 0;
  let browserBatchCreates = 0;
  let retried = 0;
  const run = () => ({ id: "run-ai", exam_id: "exam-1", status: "needs_review", total_count: 1, queued_count: 0, auto_confirmed_count: 0, human_confirmed_count: 0, review_count: failed ? 0 : 1, failed_count: failed ? 1 : 0 });
  await page.route("**/api/v1/exams/exam-1/scoring-summary", route => route.fulfill({ json: { scoring_summary: { run: run(), questions: [{ question_id: "question-ai", question_no: "1", question_type: "essay", total: 1, queued: 0, confirmed: 0, review: failed ? 0 : 1, failed: failed ? 1 : 0 }] } } }));
  await page.route("**/api/v1/scoring-runs/run-ai/ai-batches", route => route.fulfill({ json: { batches: [] } }));
  await page.route("**/api/v1/exams/exam-1/automation-results", route => route.fulfill({ json: { items: [{ question_id: "question-ai", question_no: "1", question_type: "essay", answer_segment_id: "segment-ai", state: failed ? "failed" : "review", decision_status: failed ? undefined : "suggestion_ready" }] } }));
  await page.route("**/api/v1/exams/exam-1/scoring-runs", route => {
    starts++;
    return route.fulfill({ status: 500, json: { error: { code: "unexpected_new_run" } } });
  });
  await page.route("**/api/v1/subjective-grading-batches", route => {
    browserBatchCreates++;
    return route.fulfill({ status: 500, json: { error: { code: "unexpected_browser_batch" } } });
  });
  await page.route("**/api/v1/scoring-runs/run-ai/retry-failed", route => {
    expect(route.request().method()).toBe("POST");
    retried++;
    failed = false;
    return route.fulfill({ json: { scoring_run: run(), requeued: 1 } });
  });

  await page.goto("/#/admin/exams/exam-1/grading");
  await expect(page.getByRole("button", { name: "确认建议分", exact: true })).toBeVisible({ timeout: 15_000 });
  await page.reload();
  await expect(page.getByRole("button", { name: "确认建议分", exact: true })).toBeVisible({ timeout: 15_000 });
  expect(starts).toBe(0);
  expect(browserBatchCreates).toBe(0);
  failed = true;
  await page.locator(".grading-overview").getByRole("button", { name: "刷新", exact: true }).click();
  await page.getByRole("button", { name: "重新处理失败项", exact: true }).click();
  await expect.poll(() => retried).toBe(1);
  await expect(page.getByRole("button", { name: "确认建议分", exact: true })).toBeVisible();
  expect(starts).toBe(0);
  expect(browserBatchCreates).toBe(0);
});
