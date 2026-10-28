import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { createGradingWorkbenchMockState, installGradingWorkbenchMocks } from "../fixtures/gradingWorkbenchMocks";

test("stale automatic human-submit command resumes after reload without changing score or revision", async ({ page }) => {
  // 场景包含首次加载、自动保存和刷新恢复；冷启动耗时也计入整场上限，单步断言仍沿用原超时。
  test.setTimeout(60_000);
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
  await expect(page.getByRole("button", { name: "开始处理" })).toHaveCount(0);
  const score = page.getByRole("spinbutton", { name: "最终得分" });
  await score.fill("4");
  // Enter 快捷提交已移除；连续确认不能增加命令，草稿保存后仅由自动提交创建原请求。
  await score.press("Enter");
  await score.press("Control+Enter");
  await expect.poll(() => requests.length, { timeout: 10_000 }).toBe(1);
  expect(JSON.parse(requests[0].body)).toMatchObject({ score: 4, expected_revision: 1, auto_submit: true });
  await page.reload();
  // 刷新后页面自动只读回执；不抢点正在自动核对的入口，用户核对冻结内容后才重放。
  await expect(page.getByText("可以继续提交此前内容。", { exact: true })).toBeVisible();
  await expect(page.getByText(/原提交内容：评分 4；/)).toBeVisible();
  expect(requests).toHaveLength(1);
  await page.getByRole("button", { name: "按原内容继续提交", exact: true }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]).toEqual(requests[0]);
});

test("refresh exposes recovery for terminal publish, arbitration and expired export links", async ({ page }) => {
  const duplicateKeys: string[] = [];
  page.on("console", message => {
    if (message.text().includes("Encountered two children with the same key")) duplicateKeys.push(message.text());
  });
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.addInitScript(() => {
    for (const operation of ["score.confirm", "score.publish", "review.arbitrate", "report.export"]) {
      localStorage.setItem(`business-command:demo-school:user-school_admin:${operation}:target`, JSON.stringify({ id: operation, payload: { reason: "original", expected_revision: 2 } }));
    }
  });
  let writes = 0;
  await page.route("**/api/v1/exams/target", route => route.fulfill({ json: { exam: { id: "target", name: "待恢复考试" } } }));
  await page.route("**/api/v1/arbitration-tasks/target", route => route.fulfill({ json: { arbitration_task: { question_no: "2" } } }));
  await page.route("**/api/v1/*-commands/*", async route => {
    const id = decodeURIComponent(route.request().url().split("/").at(-1)!);
    await route.fulfill({ json: { command_id: id, status: "succeeded", result: id === "report.export" ? { Content: btoa("original,csv\n1,2\n"), ContentType: "text/csv", Filename: "original.csv" } : {} } });
  });
  page.on("request", request => { if (request.method() === "POST" && /\/publish|\/confirm-grades|\/arbitration-tasks|\/reports\/export/.test(request.url())) writes++; });
  await page.goto("/#/dashboard");
  // 页面会自动读取遗留命令回执；确认成功后直接呈现结果，不应再要求逐条点击查询。
  for (const [label, objectLabel] of [["成绩确认", "待恢复考试"], ["成绩发布", "待恢复考试"], ["仲裁评分", "第 2 题"]]) {
    await expect(page.getByText(`${label}已完成 · 对象 ${objectLabel}`, { exact: true })).toBeVisible();
  }
  await expect(page.getByText("报表导出已完成 · 对象 待恢复考试", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "查看操作结果", exact: true })).toHaveCount(0);
  // 下载与完成结果在同一 Space 中展示，批量回执更新不能因重复 key 留下多余按钮。
  await expect(page.getByRole("button", { name: "下载已完成的报表", exact: true })).toHaveCount(1);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "下载已完成的报表", exact: true }).click();
  expect((await download).suggestedFilename()).toBe("original.csv");
  expect(writes).toBe(0);
  expect(duplicateKeys).toEqual([]);
  expect(await page.evaluate(() => Object.keys(localStorage).filter(key => key.startsWith("business-command:demo-school:user-school_admin:")))).toEqual([]);
});
