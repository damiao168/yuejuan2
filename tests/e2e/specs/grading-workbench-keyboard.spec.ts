import { expect, test } from "@playwright/test";
import {
  contextFor,
  createGradingWorkbenchMockState,
  installGradingWorkbenchMocks,
  subjectFixtures
} from "../fixtures/gradingWorkbenchMocks";

test("阅卷教师核对异常来源，快捷录分后自动提交并手动切换 20 份跨学科任务", async ({ page }) => {
  test.setTimeout(240_000);
  await page.emulateMedia({ reducedMotion: "reduce" });
  const state = createGradingWorkbenchMockState(20);
  // 队列按考试/学科组织连续任务；每学科内维持服务端顺序，不再交替更换评分依据。
  const taskOrder = subjectFixtures.flatMap((subject) => state.tasks.filter((task) => task.subject.code === subject.code));
  state.tasks[0].source = "ai_low_confidence";
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  const next = page.getByRole("button", { name: "下一份", exact: true });
  await expect(score).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".answer-panel")).toContainText("fixture-01");
  await page.getByRole("button", { name: "任务", exact: true }).click();
  const taskDrawer = page.getByRole("dialog", { name: "阅卷任务", exact: true });
  await expect(taskDrawer.locator(".grading-task-item").filter({ hasText: "fixture-01" })).toContainText("需要人工确认：系统评分把握不足");
  await taskDrawer.locator(".ant-drawer-close").click();

  // 打开已分配任务即可评分，不再要求点击重复领取当前答卷的按钮。
  await expect(page.getByRole("button", { name: "开始处理" })).toHaveCount(0);
  // 单字符快捷键默认关闭；先显式启用，再验证输入框仍保留自身键盘语义。
  await page.getByText("键盘帮助", { exact: true }).click();
  await page.getByRole("checkbox", { name: /^启用单字符快捷键/ }).check();
  await page.getByText("键盘帮助", { exact: true }).click();

  // 标记时只保留草稿；Enter 和 Ctrl+Enter 都不能绕过标记触发正式提交。
  await page.getByRole("button", { name: "标记试卷", exact: true }).click();
  await expect(page.getByRole("button", { name: "已标记", exact: true })).toBeEnabled();
  await score.focus();
  await page.keyboard.press("1");
  await page.keyboard.press("Enter");
  await page.keyboard.press("Control+Enter");
  await score.evaluate((element) => (element as HTMLInputElement).blur());
  await expect.poll(() => state.drafts.get("review-task-01")?.score, { timeout: 8_000 }).toBe(1);
  expect(state.submissions).toHaveLength(0);

  await page.getByRole("button", { name: "刷新" }).click();
  await expect(score).toHaveValue("1.0");

  for (let index = 0; index < taskOrder.length; index += 1) {
    const task = taskOrder[index];
    const expectedTool = task.subject.tool;
    await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
    await page.getByText("学科工具与任务信息", { exact: true }).click();
    await expect(page.locator(".subject-tool-panel h2")).toHaveText(expectedTool);
    await page.getByText("学科工具与任务信息", { exact: true }).click();

    // 首份取消标记后提交已有编辑，其余任务用数字快捷键录分；提交成功不再自动翻页。
    const submitted = page.waitForResponse((response) => response.request().method() === "POST"
      && new URL(response.url()).pathname === `/api/v1/review-tasks/${task.id}/submit`, { timeout: 10_000 });
    if (index === 0) await page.getByRole("button", { name: "已标记", exact: true }).click();
    else await page.keyboard.press("1");
    const response = await submitted;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toMatchObject({ score: 1, auto_submit: true });
    await expect.poll(() => state.submissions).toHaveLength(index + 1);
    await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
    await expect(page.getByRole("region", { name: "本题评分记录" })).toContainText("已提交得分：1 / 9");

    if (index + 1 < taskOrder.length) {
      await next.click();
      await expect(page.locator(".answer-panel")).toContainText(taskOrder[index + 1].anonymous_code);
      if (index === 0) {
        await page.getByRole("button", { name: "上一份", exact: true }).click();
        await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
        await expect(page.getByRole("region", { name: "本题评分记录" })).toContainText("已提交得分：1 / 9");
        await next.click();
        await expect(page.locator(".answer-panel")).toContainText(taskOrder[index + 1].anonymous_code);
        expect(state.submissions).toHaveLength(1);
      }
    }
  }

  expect(state.contextsRead).toEqual(expect.arrayContaining(state.tasks.map((task) => task.id)));
  expect(new Set(state.tasks.map((task) => task.subject.code))).toEqual(new Set(["chinese", "mathematics", "physics", "history"]));
});

test("两个浏览器窗口保存同一草稿时，后写窗口收到 revision conflict", async ({ browser }) => {
  test.setTimeout(60_000);
  const state = createGradingWorkbenchMockState(1);
  // 两个窗口保持标记状态，只比较草稿修订号，避免首个窗口自动提交后变成只读。
  Object.assign(state.bookmarks.get(state.tasks[0].id)!, { id: `bookmark-${state.tasks[0].id}`, marked: true, revision: 1 });
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
    await expect.poll(() => state.drafts.get("review-task-01")?.revision, { timeout: 8_000 }).toBe(1);

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
  const task = state.tasks[0];
  state.fastConfirmTaskIds.add(task.id);
  // 本场景逐项审读建议和反馈，先标记以保留可编辑草稿。
  Object.assign(state.bookmarks.get(task.id)!, { id: `bookmark-${task.id}`, marked: true, revision: 1 });
  await installGradingWorkbenchMocks(page, state);
  await page.route(`**/api/v1/review-tasks/${task.id}/context`, async (route) => {
    state.contextsRead.push(task.id);
    const context = contextFor(task, state.drafts.get(task.id), true);
    context.question.stem += "，求 $x^2 = 4$ 的正根";
    context.question.answer_key.standard_answer += "：$x = 2$";
    context.frozen_rubric.deductions[0].condition += "：$v = 2$";
    await route.fulfill({ json: { context } });
  });
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "题目与答案", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "题目与评分细则", exact: true });
  const reference = drawer.getByRole("region", { name: "本任务题目与完整评分依据" });
  await expect(reference).toContainText("连续阅卷测试考试");
  await expect(reference).toContainText("冻结参考答案");
  await expect(reference).toContainText("冻结解答全文");
  await expect(reference).toContainText("冻结解答步骤");
  await expect(reference.locator(".ant-tabs-tabpane-active .katex")).toHaveCount(2);
  await reference.getByRole("tab", { name: "完整评分依据" }).click();
  await expect(reference).toContainText("单位遗漏");
  await expect(reference).toContainText("冻结评分样例");
  await expect(reference.locator(".ant-tabs-tabpane-active .katex")).toHaveCount(1);
  await drawer.locator(".ant-drawer-close").click();
  await page.getByRole("button", { name: "评分细则", exact: true }).click();
  await expect(drawer.getByRole("tab", { name: "完整评分依据" })).toHaveAttribute("aria-selected", "true");
  await expect(drawer.locator(".ant-tabs-tabpane-active .katex")).toHaveCount(1);
  await drawer.locator(".ant-drawer-close").click();
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
