import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("stale human-submit command resumes after reload without changing score or revision", async ({ page }) => {
  await installGradingWorkbenchMocks(page, createGradingWorkbenchMockState(2));
  const requests: { key: string; body: string }[] = [];
  await page.route("**/api/v1/review-tasks/*/submit", async route => {
    requests.push({ key: route.request().headers()["idempotency-key"], body: route.request().postData()! });
    await route.fulfill({ status: 503, json: { error: { code: "idempotency_persist_failed" } } });
  });
  await page.route("**/api/v1/review-commands/*", route => route.fulfill({ json: {
    command_id: route.request().url().split("/").at(-1),
    status: "takeover_ready",
    payload: JSON.parse(requests[0]!.body)
  } }));
  await page.goto("/#/teacher/grading");
  await page.getByRole("button", { name: "开始处理" }).click();
  await page.getByRole("spinbutton", { name: "最终得分" }).fill("4");
  const submit = page.getByRole("button", { name: "提交并下一份" });
  await expect(submit).toBeEnabled();
  // 同一事件循环连续触发两次，绕过常规点击等待，验证提交锁能挡住同步双击。
  await submit.evaluate(button => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click(); });
  await expect.poll(() => requests.length).toBe(1);
  await page.reload();
  await page.getByRole("button", { name: "继续确认人工评分" }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]).toEqual(requests[0]);
});

test("refresh exposes recovery for terminal publish, arbitration and expired export links", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.addInitScript(() => {
    for (const operation of ["score.confirm", "score.publish", "review.arbitrate", "report.export"]) {
      localStorage.setItem(`business-command:demo-school:user-school_admin:${operation}:target`, JSON.stringify({ id: operation, payload: { reason: "original", expected_revision: 2 } }));
    }
  });
  let writes = 0;
  await page.route("**/api/v1/*-commands/*", async route => {
    const id = decodeURIComponent(route.request().url().split("/").at(-1)!);
    await route.fulfill({ json: { command_id: id, status: "succeeded", result: id === "report.export" ? { Content: btoa("original,csv\n1,2\n"), ContentType: "text/csv", Filename: "original.csv" } : {} } });
  });
  page.on("request", request => { if (request.method() === "POST" && /\/publish|\/confirm-grades|\/arbitration-tasks|\/reports\/export/.test(request.url())) writes++; });
  await page.goto("/#/dashboard");
  for (const label of ["成绩确认", "成绩发布", "仲裁评分"]) {
    await page.getByRole("button", { name: `继续确认${label}` }).click();
    await expect(page.getByRole("button", { name: `继续确认${label}` })).toHaveCount(0);
  }
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "继续确认报表导出" }).click();
  expect((await download).suggestedFilename()).toBe("original.csv");
  expect(writes).toBe(0);
});
