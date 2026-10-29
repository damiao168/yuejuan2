import { expect, test } from "@playwright/test";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks, subjectFixtures } from "../fixtures/gradingWorkbenchMocks";

test("120 份任务跨三页确认提交并翻页到最后，进度与下一份无重复遗漏", async ({ page }) => {
  test.setTimeout(420_000);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.clock.install();
  const state = createGradingWorkbenchMockState(120);
  const taskOrder = subjectFixtures.flatMap((subject) => state.tasks.filter((task) => task.subject.code === subject.code));
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  const next = page.getByRole("button", { name: "提交并下一份", exact: true });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".grading-work-title")).toContainText("已完成 0/120");
  await expect.poll(() => [...new Set(state.listCursors)].filter(Boolean)).toEqual(["review-task-50", "review-task-100"]);
  // 连续阅卷沿用默认收起的任务抽屉；分页范围由游标和最终逐份提交账本共同核对。
  await expect(page.locator(".answer-panel")).toContainText(taskOrder[0].anonymous_code);

  for (let index = 0; index < 120; index += 1) {
    const task = taskOrder[index];
    const saved = page.waitForResponse((response) => response.request().method() === "PUT"
      && new URL(response.url()).pathname === `/api/v1/review-tasks/${task.id}/draft`, { timeout: 10_000 });
    await score.fill("1");
    // 去抖只保存草稿；收到草稿回执后仍必须点击确认才交分。
    await page.clock.fastForward(1_250);
    expect((await saved).status()).toBe(200);
    await expect(page.locator(".draft-save-status")).toHaveCount(0);
    expect(state.submissions).toHaveLength(index);
    // 正式提交由点击发起；从该动作前订阅回执，不把草稿保存耗时计入交分等待。
    const submitted = page.waitForResponse((response) => response.request().method() === "POST"
      && new URL(response.url()).pathname === `/api/v1/review-tasks/${task.id}/submit`, { timeout: 10_000 });
    await next.click();
    const response = await submitted;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toMatchObject({ score: 1, score_only: true });
    expect(state.submissions).toHaveLength(index + 1);
    expect(state.submissions[index]).toBe(task.id);
    if (index + 1 < 120) {
      // 每份填分前已核实身份：首份在循环前，其余由上一轮切换后的检查覆盖，避免重复同一断言。
      const nextCode = taskOrder[index + 1].anonymous_code;
      // 每个动画帧核对下一份身份，保留原 5 秒上限；返回当时的正文供立即断言，不再叠加轮询等待。
      const nextAnswer = await page.waitForFunction((code) => {
        const panels = document.querySelectorAll(".answer-panel");
        if (panels.length !== 1) return false;
        const panel = panels[0];
        const bounds = panel.getBoundingClientRect();
        const text = panel.textContent ?? "";
        return bounds.width > 0 && bounds.height > 0 && getComputedStyle(panel).visibility !== "hidden" && text.includes(code)
          ? text : false;
      }, nextCode, { timeout: 5_000, polling: "raf" });
      try {
        expect(await nextAnswer.jsonValue()).toContain(nextCode);
      } finally {
        await nextAnswer.dispose();
      }
    }
    if (index === 59 || index === 119) {
      await expect(page.locator(".grading-work-title")).toContainText(`已完成 ${index + 1}/120`);
    }
  }

  expect(state.submissions).toEqual(taskOrder.map((task) => task.id));
  expect(new Set(state.submissions).size).toBe(120);
  await page.getByRole("button", { name: "任务", exact: true }).click();
  const taskDrawer = page.getByRole("dialog", { name: "阅卷任务", exact: true });
  await expect(taskDrawer.locator(".grading-task-item")).toHaveCount(0);
  await expect(page.locator(".grading-work-title")).toContainText("已完成 120/120");
});

test("任务转派后重新分页，筛选与服务端进度同步", async ({ page }) => {
  test.setTimeout(90_000);
  await page.emulateMedia({ reducedMotion: "reduce" });
  const state = createGradingWorkbenchMockState(120);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "任务", exact: true }).click();
  const taskDrawer = page.getByRole("dialog", { name: "阅卷任务", exact: true });
  await expect(taskDrawer.locator(".grading-task-item")).toHaveCount(120);
  await taskDrawer.locator(".ant-drawer-close").click();

  // 管理人员把一项任务转移到此阅卷人的分配范围之外。
  state.tasks[50].assigned_to = "other-reviewer";
  await page.getByRole("button", { name: "刷新" }).click();
  await expect(page.locator(".grading-work-title")).toContainText("已完成 0/119");
  await page.getByRole("button", { name: "任务", exact: true }).click();
  await expect(taskDrawer.locator(".grading-task-item")).toHaveCount(119);
  await expect(taskDrawer.locator(".grading-task-rail")).not.toContainText(state.tasks[50].anonymous_code);
  await taskDrawer.locator(".ant-drawer-close").click();

  await score.fill("1");
  await page.getByRole("button", { name: "提交并下一份", exact: true }).click();
  await expect(page.locator(".grading-work-title")).toContainText("已完成 1/119", { timeout: 10_000 });

  await page.getByRole("button", { name: "任务", exact: true }).click();
  const statusFilter = taskDrawer.getByRole("combobox", { name: "任务状态", exact: true });
  await statusFilter.press("ArrowDown");
  await page.getByText("已提交", { exact: true }).last().click({ timeout: 5_000 });
  await expect(taskDrawer.locator(".grading-task-item")).toHaveCount(1);
  await expect(taskDrawer.locator(".grading-task-item")).toContainText("fixture-01");

  await statusFilter.press("ArrowDown");
  await page.getByText("可处理", { exact: true }).last().click({ timeout: 5_000 });
  await expect(taskDrawer.locator(".grading-task-item")).toHaveCount(118);
  await expect(page.locator(".grading-work-title")).toContainText("已完成 1/119");
});
