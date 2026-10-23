import { expect, test } from "@playwright/test";
import {
  createGradingWorkbenchMockState,
  installGradingWorkbenchMocks,
  subjectFixtures
} from "../fixtures/gradingWorkbenchMocks";

test("阅卷教师从任务列表核对异常来源，启用快捷键后连续完成 20 份跨学科任务", async ({ page }) => {
  test.setTimeout(120_000);
  const state = createGradingWorkbenchMockState(20);
  // 队列按考试/学科组织连续任务；每学科内维持服务端顺序，不再交替更换评分依据。
  const taskOrder = subjectFixtures.flatMap((subject) => state.tasks.filter((task) => task.subject.code === subject.code));
  state.tasks[0].source = "ai_low_confidence";
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  const submit = page.getByRole("button", { name: "提交并下一份" });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".answer-panel")).toContainText("fixture-01");
  await page.getByRole("button", { name: "任务", exact: true }).click();
  const taskDrawer = page.getByRole("dialog", { name: "阅卷任务", exact: true });
  await expect(taskDrawer.locator(".grading-task-item").filter({ hasText: "fixture-01" })).toContainText("需要人工确认：系统评分把握不足");
  await taskDrawer.locator(".ant-drawer-close").click();

  await page.getByRole("button", { name: "开始处理" }).click();
  await expect.poll(() => state.claims).toEqual(["review-task-01"]);
  // 单字符快捷键默认关闭；先显式启用，再验证输入框仍保留自身键盘语义。
  await page.getByText(/^键盘帮助 ·/).click();
  await page.getByRole("checkbox", { name: /^启用单字符快捷键/ }).check();
  await page.getByText(/^键盘帮助 ·/).click();

  // 聚焦的输入框必须保留自身键盘语义：数字键和 Enter
  // 都不能触发工作台的全局评分或提交快捷键。
  await score.focus();
  await page.keyboard.press("1");
  await page.keyboard.press("Enter");
  await expect.poll(() => state.submissions).toHaveLength(0);
  await score.evaluate((element) => (element as HTMLInputElement).blur());
  await expect(page.locator(".draft-save-status")).toHaveText("已保存到服务端", { timeout: 8_000 });
  await expect.poll(() => state.draftWrites).toEqual(["review-task-01"]);

  await page.getByRole("button", { name: "刷新" }).click();
  await expect(score).toHaveValue("1.0");

  for (let index = 0; index < taskOrder.length; index += 1) {
    const task = taskOrder[index];
    const expectedTool = task.subject.tool;
    await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
    await page.getByText("学科工具与任务信息", { exact: true }).click();
    await expect(page.locator(".subject-tool-panel h2")).toHaveText(expectedTool);
    await page.getByText("学科工具与任务信息", { exact: true }).click();

    // 刷新后焦点位于表单输入框之外，此时评分和提交
    // 完全通过已说明的键盘快捷键驱动。
    await page.keyboard.press("1");
    await expect(submit).toBeEnabled();
    await page.keyboard.press("Control+Enter");
    await expect.poll(() => state.submissions).toHaveLength(index + 1);

    if (index + 1 < taskOrder.length) {
      await expect(page.locator(".answer-panel")).toContainText(taskOrder[index + 1].anonymous_code);
    }
  }

  expect(state.contextsRead).toEqual(expect.arrayContaining(state.tasks.map((task) => task.id)));
  expect(new Set(state.tasks.map((task) => task.subject.code))).toEqual(new Set(["chinese", "mathematics", "physics", "history"]));
});

test("两个浏览器窗口保存同一草稿时，后写窗口收到 revision conflict", async ({ browser }) => {
  test.setTimeout(60_000);
  const state = createGradingWorkbenchMockState(1);
  const firstContext = await browser.newContext();
  const secondContext = await browser.newContext();
  const first = await firstContext.newPage();
  const second = await secondContext.newPage();
  await installGradingWorkbenchMocks(first, state);
  await installGradingWorkbenchMocks(second, state);

  try {
    await Promise.all([
      first.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" }),
      second.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" })
    ]);
    const firstScore = first.getByRole("spinbutton", { name: "最终得分" });
    const secondScore = second.getByRole("spinbutton", { name: "最终得分" });
    await Promise.all([
      expect(firstScore).toBeVisible({ timeout: 30_000 }),
      expect(secondScore).toBeVisible({ timeout: 30_000 })
    ]);

    await firstScore.fill("5");
    await expect(first.locator(".draft-save-status")).toHaveText("已保存到服务端", { timeout: 8_000 });
    await expect.poll(() => state.drafts.get("review-task-01")?.revision).toBe(1);

    await secondScore.fill("6");
    await expect(second.locator(".draft-save-status")).toHaveText("草稿冲突", { timeout: 8_000 });
    await expect(second.getByText("草稿已被其他会话更新", { exact: true })).toBeVisible();
    expect(state.drafts.get("review-task-01")?.score).toBe(5);
  } finally {
    await Promise.all([firstContext.close(), secondContext.close()]);
  }
});

test("教师核对完整冻结资料后分别审读采纳建议分和学生反馈", async ({ page }) => {
  test.setTimeout(90_000);
  const state = createGradingWorkbenchMockState(1);
  state.fastConfirmTaskIds.add(state.tasks[0].id);
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toBeVisible({ timeout: 30_000 });
  await page.getByText("题目、参考答案与评分细则", { exact: true }).click();
  const reference = page.getByRole("region", { name: "本任务题目与完整评分依据" });
  await expect(reference).toContainText("连续阅卷测试考试");
  await expect(reference).toContainText("冻结参考答案");
  await expect(reference).toContainText("冻结解答全文");
  await expect(reference).toContainText("冻结解答步骤");
  await reference.getByRole("tab", { name: "完整评分依据" }).click();
  await expect(reference).toContainText("单位遗漏");
  await expect(reference).toContainText("冻结评分样例");
  await page.getByRole("button", { name: "采纳 AI 建议分" }).click();
  await expect(page.getByRole("dialog")).toContainText("学生反馈和内部备注保持原文");
  await page.getByRole("button", { name: "填入评分草稿" }).click();
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toHaveValue("7.0");
  const feedback = page.locator("#grading-student-feedback-input");
  await expect(feedback).toHaveValue("");
  await page.getByRole("button", { name: "审读 AI 反馈" }).click();
  await expect(page.getByRole("dialog")).toContainText("AI 学生反馈草稿");
  await expect(page.getByRole("dialog")).toContainText("AI 内部备注草稿");
  await page.getByRole("button", { name: "采用并展开编辑" }).click();
  await expect(feedback).toBeVisible();
  await expect(feedback).toHaveValue("AI 学生反馈草稿");
  await expect(page.locator("#grading-private-note-input")).toHaveValue("AI 内部备注草稿");
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toHaveValue("7.0");
  expect(state.submissions).toHaveLength(0);
});
