import { expect, test } from "@playwright/test";
import {
  contextFor,
  createGradingWorkbenchMockState,
  installGradingWorkbenchMocks,
  subjectFixtures
} from "../fixtures/gradingWorkbenchMocks";

test("阅卷教师核对异常来源，录分停留不交分，点击或快捷键确认后切换 20 份跨学科任务", async ({ page }) => {
  test.setTimeout(240_000);
  await page.emulateMedia({ reducedMotion: "reduce" });
  const state = createGradingWorkbenchMockState(20);
  // 队列按考试/学科组织连续任务；每学科内维持服务端顺序，不再交替更换评分依据。
  const taskOrder = subjectFixtures.flatMap((subject) => state.tasks.filter((task) => task.subject.code === subject.code));
  state.tasks[0].source = "ai_low_confidence";
  await installGradingWorkbenchMocks(page, state);
  await page.goto("/#/teacher/grading", { waitUntil: "domcontentloaded" });

  const score = page.getByRole("spinbutton", { name: "最终得分" });
  const next = page.getByRole("button", { name: "提交并下一份", exact: true });
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

  // 录分、失焦和普通 Enter 都只保存草稿；必须明确执行下一份。
  await page.getByRole("button", { name: "标记试卷", exact: true }).click();
  await expect(page.getByRole("button", { name: "已标记", exact: true })).toBeEnabled();
  await score.focus();
  await page.keyboard.press("1");
  await page.keyboard.press("Enter");
  await score.evaluate((element) => (element as HTMLInputElement).blur());
  await expect.poll(() => state.drafts.get("review-task-01")?.score, { timeout: 8_000 }).toBe(1);
  expect(state.submissions).toHaveLength(0);

  // 已保存草稿仍未正式交分；刷新须明确确认离开，不能绕过评分未提交的保护。
  await page.getByRole("button", { name: "刷新" }).click();
  const refreshConfirmation = page.getByRole("dialog", { name: "评分尚未提交，继续离开？", exact: true });
  await expect(refreshConfirmation).toBeVisible();
  await refreshConfirmation.getByRole("button", { name: "继续离开", exact: true }).click();
  await expect(refreshConfirmation).toHaveCount(0);
  await expect(score).toHaveAttribute("aria-valuenow", "1");

  for (let index = 0; index < taskOrder.length; index += 1) {
    const task = taskOrder[index];
    await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);

    if (index === 0) await page.getByRole("button", { name: "已标记", exact: true }).click();
    else await page.keyboard.press("1");
    // 首份已等待草稿回执，其余交替使用点击和 Ctrl+Enter；二者都需正式提交成功才翻页。
    const submitted = page.waitForResponse((response) => response.request().method() === "POST"
      && new URL(response.url()).pathname === `/api/v1/review-tasks/${task.id}/submit`, { timeout: 10_000 });
    if (index % 2 === 0) await next.click();
    else await page.keyboard.press("Control+Enter");
    const response = await submitted;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toMatchObject({ score: 1, score_only: true });
    expect(response.request().postDataJSON()).not.toHaveProperty("auto_submit");
    await expect.poll(() => state.submissions).toHaveLength(index + 1);
    if (index + 1 < taskOrder.length) {
      await expect(page.locator(".answer-panel")).toContainText(taskOrder[index + 1].anonymous_code);
      if (index === 0) {
        await page.getByRole("button", { name: "上一份", exact: true }).click();
        await expect(page.locator(".answer-panel")).toContainText(task.anonymous_code);
        await expect(page.getByRole("region", { name: "本题评分记录" })).toContainText("已提交得分：1 / 9");
        await page.getByRole("button", { name: "下一份", exact: true }).click();
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
  // 两个窗口保留草稿，比较修订号；输入本身不会触发正式提交。
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

test("教师核对完整冻结资料后直接采纳建议分，批注工具保持常驻", async ({ page }) => {
  test.setTimeout(90_000);
  const state = createGradingWorkbenchMockState(1);
  const task = state.tasks[0];
  state.currentAiSuggestionTaskIds.add(task.id);
  // 核对资料与采纳建议只更新草稿，教师确认下一份后才提交。
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
  const tools = page.getByRole("toolbar", { name: "批注工具" });
  await expect(tools).toBeVisible();
  for (const name of ["选择批注", "打勾", "打叉", "框选批注", "删除选中批注"]) await expect(tools.getByRole("button", { name, exact: true })).toBeVisible();
  await page.getByRole("button", { name: "采纳 AI 建议分" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("spinbutton", { name: "最终得分" })).toHaveAttribute("aria-valuenow", "7");
  await expect.poll(() => state.drafts.get(task.id)?.score, { timeout: 8_000 }).toBe(7);
  expect(state.drafts.get(task.id)).toMatchObject({ comments: "", private_note: "", student_feedback: "" });
  expect(state.submissions).toHaveLength(0);
});
