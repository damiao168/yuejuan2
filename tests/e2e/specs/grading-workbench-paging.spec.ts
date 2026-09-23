import { expect, test } from "@playwright/test";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("120 份任务跨三页连续提交到最后，进度与下一份无重复遗漏", async ({ page }) => {
  test.setTimeout(300_000);
  const state = createGradingWorkbenchMockState(120);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  const submit = page.getByRole("button", { name: "提交并下一份" });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".grading-work-title")).toContainText("已完成 0 / 共 120");
  await expect.poll(() => [...new Set(state.listCursors)].filter(Boolean)).toEqual(["review-task-50", "review-task-100"]);
  await expect(page.locator(".grading-task-item")).toHaveCount(120);

  for (let index = 0; index < 120; index += 1) {
    const task = state.tasks[index];
    await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
    await score.fill("1");
    await submit.click();
    await expect.poll(() => state.submissions.length).toBe(index + 1);
    if (index + 1 < 120) {
      await expect(page.locator(".answer-panel")).toContainText(state.tasks[index + 1].anonymous_code);
    }
    if (index === 59 || index === 119) {
      await expect(page.locator(".grading-work-title")).toContainText(`已完成 ${index + 1} / 共 120`);
    }
  }

  expect(state.submissions).toEqual(state.tasks.map((task) => task.id));
  expect(new Set(state.submissions).size).toBe(120);
  await expect(page.locator(".grading-task-item")).toHaveCount(0);
  await expect(page.locator(".grading-work-title")).toContainText("已完成 120 / 共 120");
});

test("任务转派后重新分页，筛选与服务端进度同步", async ({ page }) => {
  test.setTimeout(90_000);
  const state = createGradingWorkbenchMockState(120);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".grading-task-item")).toHaveCount(120);

  // A manager transfers one task outside this reviewer's assignment scope.
  state.tasks[50].assigned_to = "other-reviewer";
  await page.getByRole("button", { name: "刷新" }).click();
  await expect(page.locator(".grading-work-title")).toContainText("已完成 0 / 共 119");
  await expect(page.locator(".grading-task-item")).toHaveCount(119);
  await expect(page.locator(".grading-task-rail")).not.toContainText(state.tasks[50].anonymous_code);

  await score.fill("1");
  await page.getByRole("button", { name: "提交并下一份" }).click();
  await expect(page.locator(".grading-work-title")).toContainText("已完成 1 / 共 119");

  const statusFilter = page.locator(".grading-taskbar .ant-select");
  await statusFilter.click();
  await page.getByText("已提交", { exact: true }).last().click({ timeout: 5_000 });
  await expect(page.locator(".grading-task-item")).toHaveCount(1);
  await expect(page.locator(".grading-task-item")).toContainText("fixture-01");

  await statusFilter.click();
  await page.getByText("可处理", { exact: true }).last().click({ timeout: 5_000 });
  await expect(page.locator(".grading-task-item")).toHaveCount(118);
  await expect(page.locator(".grading-work-title")).toContainText("已完成 1 / 共 119");
});
