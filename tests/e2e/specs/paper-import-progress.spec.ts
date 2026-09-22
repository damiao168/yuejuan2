import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("识别轮询在短暂失败后恢复，停止及重试保留资料和滚动位置", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  const job = {
    id: "import-1", generation: 1, run_id: "run-1", source_revision: "source-revision-1",
    exam_id: "exam-1", subject: "math", status: "processing",
    issues: [] as string[], structured_issues: [] as object[], questions: [],
    sources: [{ id: "source-1", file_asset_id: "file-1", document_index: 0,
      role_hint: "auto", detected_role: "unknown", original_name: "会议回放.png", processing_status: "processing" }]
  };
  let polls = 0;
  let failNext = false;
  await page.route("**/api/v1/exams/exam-1/papers", route => route.fulfill({ json: { papers: [] } }));
  await page.route("**/api/v1/exams/exam-1/questions", route => route.fulfill({ json: { questions: [] } }));
  await page.route("**/api/v1/exams/exam-1/paper-imports", route => route.fulfill({ json: { imports: [job] } }));
  await page.route("**/api/v1/paper-imports/import-1", async route => {
    polls += 1;
    if (failNext) {
      failNext = false;
      return route.fulfill({ status: 503, json: { error: { code: "unavailable", message: "temporary" } } });
    }
    return route.fulfill({ json: { import: job } });
  });
  await page.route("**/api/v1/paper-imports/import-1/cancel?expected_generation=1", route => {
    job.status = "cancelled";
    return route.fulfill({ json: { import: job } });
  });
  await page.route("**/api/v1/paper-imports/import-1/sources", route => {
    job.status = "processing";
    return route.fulfill({ json: { import: job } });
  });
  await page.goto("/#/admin/exams/exam-1/paper");
  await expect(page.getByRole("button", { name: "停止识别", exact: true })).toBeVisible();
  await page.getByRole("heading", { name: "自动识别结果" }).scrollIntoViewIfNeeded();
  const scrollPosition = () => page.evaluate(() => ({
    window: window.scrollY,
    containers: Array.from(document.querySelectorAll("main, .app-content")).map(el => el.scrollTop)
  }));
  const before = await scrollPosition();
  const startPolls = polls;
  failNext = true;
  await expect.poll(() => polls, { timeout: 15_000 }).toBeGreaterThanOrEqual(startPolls + 2);
  await expect(page.getByRole("progressbar")).toBeVisible();
  expect(await scrollPosition()).toEqual(before);

  await page.getByRole("button", { name: "停止识别", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "停止识别", exact: true }).click();
    await expect(page.getByText("已停止识别", { exact: true })).toBeVisible();
    await expect(page.getByText("1. 会议回放.png")).toBeVisible();
    await page.getByRole("button", { name: "重新识别全部", exact: true }).click();
  await expect(page.getByRole("button", { name: "停止识别", exact: true })).toBeVisible();
  const retryScroll = await scrollPosition();
  job.status = "review_required";
  job.sources[0].processing_status = "processed";
  job.structured_issues = [{ code: "NO_EXAM_CONTENT_DETECTED", severity: "error", certainty: "confirmed",
    message: "未识别到与考试有关的题目、答案、解析或评分标准，请检查是否上传了无关图片或错误文件" }];
  await expect(page.getByText("未识别到考试内容", { exact: true })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("AI 解析服务暂不可用", { exact: false })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "确认导入", exact: true })).toHaveCount(0);
    expect(await scrollPosition()).toEqual(retryScroll);
    await page.getByRole("button", { name: "重新识别", exact: true }).click();
  await expect(page.getByRole("button", { name: "停止识别", exact: true })).toBeVisible();
  await expect(page.getByText("未识别到考试内容", { exact: true })).toHaveCount(0);
});
