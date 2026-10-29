import { expect, test, type Page } from "@playwright/test";
import { contextFor, createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

async function installAnswerImage(page: Page, questionType: string, width: number, height: number) {
  const state = createGradingWorkbenchMockState(1);
  const task = state.tasks[0];
  task.status = "submitted";
  const grade = { id: "grade-reading", review_task_id: task.id, reviewer_id: task.assigned_to, score: 9, max_score: 9,
    rubric_selections: [], comments: "历史评阅", created_at: "2026-10-28T08:10:00Z" };
  state.grades.set(task.id, grade);
  await installGradingWorkbenchMocks(page, state);
  await page.route(`**/api/v1/review-tasks/${task.id}/context`, route => {
    const context = contextFor(task, undefined);
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ context: {
      ...context, question: { ...context.question, question_type: questionType }, human_grade: grade,
      answer_artifact: { ...context.answer_artifact, segment_image_url: "/api/v1/review-assets/reading-image" }
    } }) });
  });
  await page.route("**/api/v1/review-assets/reading-image", route => route.fulfill({ status: 200, contentType: "image/svg+xml",
    body: `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="white"/><text x="8" y="24" font-size="20">A B C D</text></svg>` }));
  // 有图时才挂载批注画布；无批注是合法空列表，不能让旧通用夹具的未知接口404伪装成产品错误。
  const annotationPath = `/api/v1/review-tasks/${task.id}/annotations`;
  await page.route(`**${annotationPath}`, route => route.request().method() === "GET"
    ? route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ annotations: [] }) })
    : route.fallback());
  const annotationResponse = page.waitForResponse(response => new URL(response.url()).pathname === annotationPath && response.request().method() === "GET");
  await page.goto(`/teacher/grading?status=reviewed&task=${task.id}`);
  await expect(page.getByRole("region", { name: "本题评分记录", exact: true })).toBeVisible();
  expect((await annotationResponse).status()).toBe(200);
  await expect(page.locator(".review-annotation-workspace").getByRole("alert")).toHaveCount(0);
  return state;
}

test("历史多选短答案保持完整，自动不拉伸且重置不产生分段", async ({ page }, testInfo) => {
  const state = await installAnswerImage(page, "multiple_choice", 302, 28);
  const answer = page.locator(".answer-image-reading");
  await expect(answer.locator(".image-stage img")).toBeVisible();
  await expect(answer.getByLabel("答题图缩放比例")).toHaveText("100%");
  await expect(answer.locator(".answer-reading-tile")).toHaveCount(0);
  await expect(answer.getByLabel("答题图阅读布局")).toHaveCount(0);
  await answer.getByRole("button", { name: "放大答题图", exact: true }).click();
  await expect(answer.getByLabel("答题图缩放比例")).not.toHaveText("100%");
  await answer.getByRole("button", { name: "重置视图", exact: true }).click();
  await expect(answer.getByLabel("答题图缩放比例")).toHaveText("100%");
  await expect(answer.locator(".answer-reading-tile")).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath("choice-whole-answer.png"), fullPage: true });
  expect(state.claims).toEqual([]);
  expect(state.submissions).toEqual([]);
});

test("纵长主观答案仍支持连续分段，整图和重置保留原图坐标", async ({ page }) => {
  await installAnswerImage(page, "calculation", 600, 6000);
  const answer = page.locator(".answer-image-reading");
  await expect(answer.locator(".answer-reading-tile").first()).toBeVisible();
  await expect(answer.getByText("分段阅读", { exact: true })).toBeVisible();
  const windows = await answer.locator(".answer-reading-tile").evaluateAll(tiles => tiles.map(tile => tile.getAttribute("data-source-rect")));
  expect(windows.length).toBeGreaterThan(1);
  expect(Number((await answer.getByLabel("答题图缩放比例").innerText()).replace("%", ""))).toBeLessThanOrEqual(100);
  await answer.getByText("整图查看", { exact: true }).click();
  await expect(answer.locator(".image-stage img")).toBeVisible();
  await answer.getByRole("button", { name: "重置视图", exact: true }).click();
  await expect(answer.locator(".answer-reading-tile")).toHaveCount(windows.length);
  expect(await answer.locator(".answer-reading-tile").evaluateAll(tiles => tiles.map(tile => tile.getAttribute("data-source-rect")))).toEqual(windows);
});
