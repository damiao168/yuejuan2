import { expect, test } from "@playwright/test";
import { contextFor, createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("整区录分须明确交分，部分失败保留成功回执，重试完成后跳过本区全部题目", async ({ page }) => {
  test.setTimeout(60_000);
  const state = createGradingWorkbenchMockState(4);
  state.tasks.forEach(task => { task.subject = state.tasks[0].subject; });
  const members = state.tasks.slice(0, 3);
  members.forEach(task => { task.submission_id = "submission-shared-region"; });
  await installGradingWorkbenchMocks(page, state);
  await page.route("**/api/v1/review-tasks/*/context", async route => {
    const id = new URL(route.request().url()).pathname.split("/").at(-2)!;
    const task = state.tasks.find(item => item.id === id)!;
    const context = contextFor(task, state.drafts.get(id));
    const artifact = members.some(item => item.id === id) ? { ...context.answer_artifact,
      answer_scope: "grading_group", grading_group_id: "shared-fill-region", grading_group_image_url: "/api/v1/review-assets/shared-region",
      grading_group_question_ids: members.map(item => item.question_id),
      grading_group_tasks: members.map(item => ({ id: item.id, question_id: item.question_id, question_no: item.question_no, status: item.status, revision: item.revision }))
    } : context.answer_artifact;
    await route.fulfill({ json: { context: { ...context, answer_artifact: artifact, ...(state.grades.has(id) ? { human_grade: state.grades.get(id), draft: null } : {}) } } });
  });
  let failure = true;
  await page.route(`**/api/v1/review-tasks/${members[2].id}/submit`, async route => {
    if (failure) return route.fulfill({ status: 400, json: { error: { code: "invalid_score", message: "本题暂未交分" } } });
    return route.fallback();
  });
  await page.goto("/#/teacher/grading");
  const scores = [2, 0, 4];
  for (const [index, task] of members.entries()) {
    await page.getByRole("region", { name: `第 ${task.question_no} 题评分`, exact: true }).getByRole("spinbutton", { name: "最终得分" }).fill(String(scores[index]));
  }
  await expect.poll(() => members.map(task => state.drafts.get(task.id)?.score)).toEqual(scores);
  expect(state.submissions).toEqual([]);
  page.once("dialog", dialog => dialog.accept());
  await page.reload();
  for (const [index, task] of members.entries()) {
    await expect(page.getByRole("region", { name: `第 ${task.question_no} 题评分`, exact: true })
      .getByRole("spinbutton", { name: "最终得分" })).toHaveValue(new RegExp(`^${scores[index]}(?:\\.0)?$`));
  }
  expect(state.submissions).toEqual([]);
  const next = page.getByRole("button", { name: "提交本区并下一份", exact: true });
  await next.click();
  await expect.poll(() => state.submissions).toEqual(members.slice(0, 2).map(task => task.id));
  const failedRow = page.getByRole("region", { name: `第 ${members[2].question_no} 题评分`, exact: true });
  await expect(failedRow.getByRole("alert")).toBeVisible();
  await expect(failedRow.getByRole("spinbutton", { name: "最终得分" })).toHaveValue(/^4(?:\.0)?$/);
  await expect(page.locator(".answer-panel")).toContainText(members[0].anonymous_code);
  failure = false;
  await next.click();
  await expect.poll(() => state.submissions).toEqual(members.map(task => task.id));
  await expect(page.locator(".answer-panel")).toContainText(state.tasks[3].anonymous_code);
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toHaveCount(1);
  expect(state.claims).toEqual([]);
});
