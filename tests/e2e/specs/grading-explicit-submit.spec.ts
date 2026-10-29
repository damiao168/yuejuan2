import { expect, test } from "@playwright/test";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("输入零分、失焦、普通 Enter 和输入法确认都只保存草稿，教师确认后才交分切换", async ({ page }) => {
  const state = createGradingWorkbenchMockState(2);
  await installGradingWorkbenchMocks(page, state);
  await page.clock.install();
  await page.goto("/#/teacher/grading");
  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await expect(score).toBeVisible();
  await score.fill("0");
  await score.press("Enter");
  await score.evaluate(element => element.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, isComposing: true, bubbles: true })));
  await score.blur();
  await page.clock.fastForward(3_000);
  await expect.poll(() => state.drafts.get("review-task-01")?.score).toBe(0);
  expect(state.submissions).toEqual([]);
  await expect(page.locator(".answer-panel")).toContainText("fixture-01");

  await page.getByRole("button", { name: "提交并下一份", exact: true }).click();
  await expect.poll(() => state.submissions).toEqual(["review-task-01"]);
  expect(state.grades.get("review-task-01")?.score).toBe(0);
  const statistics = page.getByRole("region", { name: "本人已评正确率", exact: true });
  await expect(statistics).toContainText("0.0%");
  await expect(statistics).toContainText("1 题次");
  await expect(page.locator(".answer-panel")).toContainText("fixture-02");
  // 空分数不能被误当零分，也不能通过下一份跳过当前任务。
  await page.getByRole("button", { name: "提交并下一份", exact: true }).click();
  await expect(page.locator(".answer-panel")).toContainText("fixture-02");
  expect(state.submissions).toHaveLength(1);
  await score.fill("3");
  await score.press("Control+Enter");
  await expect.poll(() => state.submissions).toEqual(["review-task-01", "review-task-02"]);
});

test("交分失败保留当前分数和答卷，重复确认只发送一次请求", async ({ page }) => {
  const state = createGradingWorkbenchMockState(2);
  await installGradingWorkbenchMocks(page, state);
  let requests = 0;
  await page.route("**/api/v1/review-tasks/*/submit", async route => {
    requests++;
    if (requests === 1) {
      await new Promise(resolve => setTimeout(resolve, 300));
      return route.fulfill({ status: 400, json: { error: { code: "invalid_score", message: "本次评分未能提交" } } });
    }
    return route.fallback();
  });
  await page.goto("/#/teacher/grading");
  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await score.fill("4");
  const next = page.getByRole("button", { name: "提交并下一份", exact: true });
  const failed = page.waitForResponse(response => response.request().method() === "POST" && response.url().endsWith("/submit"));
  await next.evaluate(element => { (element as HTMLButtonElement).click(); (element as HTMLButtonElement).click(); });
  expect((await failed).status()).toBe(400);
  await expect(next).toBeEnabled();
  expect(requests).toBe(1);
  expect(state.submissions).toEqual([]);
  await expect(page.locator(".answer-panel")).toContainText("fixture-01");
  await expect(score).toHaveValue(/^4(?:\.0)?$/);
  await next.click();
  await expect(page.locator(".answer-panel")).toContainText("fixture-02");
  expect(state.submissions).toEqual(["review-task-01"]);
  expect(requests).toBe(2);
});
