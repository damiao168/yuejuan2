import { expect, test } from "@playwright/test";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("教师可回看本人已提交和已完成评分，零分不丢失，历史下一份不领取或交分", async ({ page }) => {
  const state = createGradingWorkbenchMockState(4);
  for (const [index, status, score] of [[0, "submitted", 0], [1, "completed", 7], [3, "completed", 9]] as const) {
    const task = state.tasks[index];
    task.status = status;
    if (index === 3) task.assigned_to = "other-reviewer";
    state.grades.set(task.id, { id: `grade-${task.id}`, review_task_id: task.id, reviewer_id: task.assigned_to,
      score, max_score: 9, rubric_selections: [], comments: `第${index + 1}份正式评语`, private_note: "", student_feedback: "",
      created_at: "2026-10-28T08:10:00Z" });
  }
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading");
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toBeVisible();
  await page.getByRole("button", { name: "历史评阅", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "阅卷任务", exact: true });
  await expect(drawer.locator(".grading-task-item")).toHaveCount(2);
  await expect(drawer).not.toContainText("fixture-04");
  await drawer.locator(".grading-task-item").filter({ hasText: "fixture-01" }).getByRole("button").click();
  const record = page.getByRole("region", { name: "本题评分记录", exact: true });
  await expect(record).toContainText("已提交得分：0 / 9");
  await expect(record).toContainText("第1份正式评语");
  await expect(record).toContainText("评阅时间");
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toHaveCount(0);
  await page.getByRole("button", { name: "下一份", exact: true }).click();
  await expect(page.locator(".answer-panel")).toContainText("fixture-02");
  await expect(record).toContainText("已提交得分：7 / 9");
  await page.getByRole("button", { name: "下一份", exact: true }).click();
  await expect(page.locator(".answer-panel")).toContainText("fixture-02");
  expect(state.claims).toEqual([]);
  expect(state.submissions).toEqual([]);
  expect(state.draftWrites).toEqual([]);
});
