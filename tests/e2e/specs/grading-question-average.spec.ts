import { expect, test } from "@playwright/test";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("按每道题独立显示平均分，零分计入且切题不沿用跨题总分", async ({ page }) => {
  const state = createGradingWorkbenchMockState(3);
  state.tasks[1].question_id = state.tasks[0].question_id;
  state.tasks[1].question_no = state.tasks[0].question_no;
  state.tasks[1].subject = state.tasks[0].subject;
  for (const [index, task] of state.tasks.entries()) {
    task.status = "submitted";
    state.grades.set(task.id, { id: `grade-${task.id}`, review_task_id: task.id, reviewer_id: task.assigned_to,
      score: [0, 6, 9][index], max_score: index === 2 ? 9 : 6, rubric_selections: [], comments: "已确认",
      created_at: "2026-10-29T08:00:00Z" });
  }
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/teacher/grading?status=reviewed&task=review-task-01");
  const panel = page.getByRole("region", { name: "本人逐题平均分", exact: true });
  const summary = panel.locator(".review-statistics-totals");
  await expect(summary).toContainText("第 Q01 题平均分3 分");
  await expect(summary).toContainText("已评分2 份");
  await expect(panel).not.toContainText("正确率");
  await panel.getByText("查看全部题目平均分", { exact: true }).click();
  await expect(panel.getByRole("row").filter({ hasText: "第 Q03 题" })).toContainText("9 分");
  await page.getByRole("button", { name: "下一份", exact: true }).click();
  await page.getByRole("button", { name: "下一份", exact: true }).click();
  await expect(summary).toContainText("第 Q03 题平均分9 分");
  await expect(summary).toContainText("已评分1 份");
  expect(state.submissions).toEqual([]);
});
