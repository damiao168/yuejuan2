import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("grader can open fast confirmation from the personal workbench", async ({ page }) => {
  const state = createGradingWorkbenchMockState(1);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await expect(page.getByText("AI 快捷确认", { exact: true })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "查看候选任务" }).click();
  const drawer = page.getByRole("dialog", { name: "AI 快捷确认 · 教师逐份核对" });
  await expect(drawer).toContainText("逐份勾选后提交");
  await expect(drawer.getByRole("button", { name: /提交已核对/ })).toHaveCount(0);
});

test("fast confirmation requires viewing the answer before the teacher submits", async ({ page }) => {
  const state = createGradingWorkbenchMockState(1);
  state.fastConfirmTaskIds.add(state.tasks[0].id);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "查看候选任务" }).click();
  const drawer = page.getByRole("dialog", { name: "AI 快捷确认 · 教师逐份核对" });
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
  await page.getByRole("button", { name: "查看候选任务" }).click();
  const drawer = page.getByRole("dialog", { name: "AI 快捷确认 · 教师逐份核对" });
  await drawer.getByRole("button", { name: "核对原图与证据" }).click();
  const item = drawer.locator(".fast-confirm-item").first();
  await expect(item.getByRole("img", { name: "待核对的学生答题原图" })).toBeVisible();
  const bounds = await item.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await expect(item.getByRole("checkbox", { name: "已核对原图、证据并确认此分数" })).toBeEnabled();
});

test("AI assisted question follows the visible plan into an idempotent suggestion batch", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  let started = false;
  const batches: Array<{ key: string; segments: string[]; runId?: string }> = [];
  const run = { id: "run-ai", exam_id: "exam-1", status: "needs_review", total_count: 1, queued_count: 0, auto_confirmed_count: 0, human_confirmed_count: 0, review_count: 1, failed_count: 0 };
  const batch = { id: "batch-ai", idempotency_key: "scoring-ai-run-ai-original", status: "processing", segment_ids: ["segment-ai"], total_count: 1, queued_count: 1, processing_count: 0, succeeded_count: 0, failed_count: 0 };
  const item = { answer_segment_id: "segment-ai", question_id: "question-ai", question_no: "1", question_type: "essay", state: "review", anonymous_code: "A001", normalized_bbox: { x: 0, y: 0, width: 1, height: 1 } };
  await page.route("**/api/v1/exams/exam-1/scoring-summary", route => route.fulfill({ json: { scoring_summary: { run: started ? run : undefined, questions: [{ question_id: "question-ai", question_no: "1", question_type: "essay", total: 1, queued: 0, confirmed: 0, review: started ? 1 : 0, failed: 0 }] } } }));
  await page.route("**/api/v1/exams/exam-1/questions/question-ai/assessment-snapshot", route => route.fulfill({ json: { assessment_snapshot: { scoring_policy_snapshot: { mode: "AI_ASSIST" }, risk_tier: "R2" } } }));
  await page.route("**/api/v1/exams/exam-1/scoring-runs", route => { started = true; return route.fulfill({ status: 201, json: { scoring_run: run } }); });
  await page.route("**/api/v1/scoring-runs/run-ai", route => route.fulfill({ json: { scoring_run: run, items: [item] } }));
  await page.route("**/api/v1/scoring-runs/run-ai/ai-batches", route => route.fulfill({ json: { batches: batches.length ? [batch] : [] } }));
  await page.route("**/api/v1/exams/exam-1/automation-results", route => route.fulfill({ json: { items: [item] } }));
  await page.route("**/api/v1/subjective-grading-batches", async route => {
    const body = route.request().postDataJSON() as { idempotency_key: string; segment_ids: string[]; scoring_run_id?: string };
    batches.push({ key: body.idempotency_key, segments: body.segment_ids, runId: body.scoring_run_id });
    await route.fulfill({ status: 201, json: { batch } });
  });
  await page.route("**/api/v1/subjective-grading-batches/batch-ai/enqueue", route => route.fulfill({ json: { batch, tasks: [], enqueue_result: { requested_count: 1, accepted_count: 1, task_count: 1, failed_count: 0, partial_success: false, failures: [] } } }));
  await page.route("**/api/v1/subjective-grading-batches/batch-ai", route => route.fulfill({ json: { batch } }));

  await page.goto("/#/exams/exam-1/grading");
  const start = page.getByRole("button", { name: "开始评分", exact: true });
  await expect(start).toBeEnabled({ timeout: 15_000 });
  await start.click();
  const plan = page.getByRole("dialog", { name: "确认本次评分路线" });
  await expect(plan.locator(".grading-plan-counts")).toContainText("AI 建议");
  await expect(plan.getByText("1 · 作文题")).toBeVisible();
  await expect(plan.getByText("AI 仅生成建议，须由教师确认")).toBeVisible();
  await plan.getByRole("button", { name: "按此路线启动" }).click();
  await expect.poll(() => batches.length).toBe(1);
  expect(batches[0]).toEqual({ key: expect.stringMatching(/^scoring-ai-run-ai-[a-f0-9]{32}$/), segments: ["segment-ai"], runId: "run-ai" });
  await expect(page.getByText("AI 辅助建议", { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText("AI 辅助建议", { exact: true })).toBeVisible({ timeout: 15_000 });
});

test("an accepted scoring run without AI batches resumes after opening the workbench", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  const run = { id: "run-ai", exam_id: "exam-1", status: "needs_review", total_count: 1, queued_count: 0, auto_confirmed_count: 0, human_confirmed_count: 0, review_count: 1, failed_count: 0 };
  let batch = { id: "batch-ai", idempotency_key: "scoring-ai-run-ai-original", status: "processing", segment_ids: ["segment-ai"], total_count: 1, queued_count: 1, processing_count: 0, succeeded_count: 0, failed_count: 0 };
  const retryBatch = { ...batch, id: "batch-retry", idempotency_key: "scoring-ai-retry-batch-ai" };
  let created = 0;
  let retried = 0;
  await page.route("**/api/v1/exams/exam-1/scoring-summary", route => route.fulfill({ json: { scoring_summary: { run, questions: [{ question_id: "question-ai", question_no: "1", question_type: "essay", total: 1, queued: 0, confirmed: 0, review: 1, failed: 0 }] } } }));
  await page.route("**/api/v1/exams/exam-1/questions/question-ai/assessment-snapshot", route => route.fulfill({ json: { assessment_snapshot: { scoring_policy_snapshot: { mode: "AI_ASSIST" }, risk_tier: "R2" } } }));
  await page.route("**/api/v1/scoring-runs/run-ai", route => route.fulfill({ json: { scoring_run: run, items: [{ question_id: "question-ai", answer_segment_id: "segment-ai", state: "review" }] } }));
  await page.route("**/api/v1/scoring-runs/run-ai/ai-batches", route => route.fulfill({ json: { batches: created ? [batch, ...(retried ? [retryBatch] : [])] : [] } }));
  await page.route("**/api/v1/subjective-grading-batches", route => {
    const input = route.request().postDataJSON() as { idempotency_key: string; segment_ids: string[] };
    if (input.idempotency_key === "scoring-ai-retry-batch-ai") { retried++; expect(input.segment_ids).toEqual(["segment-ai"]); return route.fulfill({ status: 201, json: { batch: retryBatch } }); }
    created++; return route.fulfill({ status: 201, json: { batch } });
  });
  await page.route("**/api/v1/subjective-grading-batches/batch-ai/enqueue", route => route.fulfill({ json: { batch, tasks: [], enqueue_result: { requested_count: 1, accepted_count: 1, task_count: 1, failed_count: 0, partial_success: false, failures: [] } } }));
  await page.route("**/api/v1/subjective-grading-batches/batch-ai", route => route.fulfill({ json: { batch } }));
  await page.route("**/api/v1/subjective-grading-batches/batch-ai/failed-segments", route => route.fulfill({ json: { segment_ids: ["segment-ai"] } }));
  await page.route("**/api/v1/subjective-grading-batches/batch-retry/enqueue", route => route.fulfill({ json: { batch: retryBatch, tasks: [], enqueue_result: { requested_count: 1, accepted_count: 1, task_count: 1, failed_count: 0, partial_success: false, failures: [] } } }));
  await page.route("**/api/v1/subjective-grading-batches/batch-retry", route => route.fulfill({ json: { batch: retryBatch } }));
  await page.goto("/#/exams/exam-1/grading");
  await expect.poll(() => created, { timeout: 15_000 }).toBe(1);
  await page.reload();
  await expect(page.getByText("AI 辅助建议", { exact: true })).toBeVisible({ timeout: 15_000 });
  expect(created).toBe(1);
  batch = { ...batch, status: "failed", queued_count: 0, failed_count: 1 };
  await page.locator(".grading-overview").getByRole("button", { name: "刷新" }).click();
  await expect(page.getByRole("button", { name: "重试失败的 AI 建议" })).toBeVisible();
  await page.getByRole("button", { name: "重试失败的 AI 建议" }).click();
  await expect.poll(() => retried).toBe(1);
});
