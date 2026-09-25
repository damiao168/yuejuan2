import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("scoring start double click and reload retain the same command", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  const requests: { key: string; body: string }[] = [];
  await page.route("**/api/v1/exams/exam-1/scoring-readiness", route => route.fulfill({ json: { scoring_readiness: { ready: true, checks: [], metrics: {} } } }));
  await page.route("**/api/v1/exams/exam-1/scoring-summary", route => route.fulfill({ json: { scoring_summary: { questions: [] } } }));
  await page.route("**/api/v1/exams/exam-1/scoring-runs", async route => {
    requests.push({ key: route.request().headers()["idempotency-key"], body: route.request().postData()! });
    await route.fulfill({ status: 503, json: { error: { code: "idempotency_persist_failed" } } });
  });
  await page.route("**/api/v1/exams/exam-1/scoring-runs/commands/*", route => route.fulfill({ json: { command_id: route.request().url().split("/").at(-1), status: "not_accepted" } }));
  await page.goto("/#/exams/exam-1/grading");
  const start = page.getByRole("button", { name: "开始评分", exact: true });
  await expect(start).toBeEnabled();
  await start.click();
  const plan = page.getByRole("dialog", { name: "确认本次评分路线" });
  await expect(plan).toBeVisible();
  await plan.getByRole("button", { name: "按此路线启动" }).evaluate(button => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click(); });
  await expect.poll(() => requests.length).toBe(1);
  await page.reload();
  await page.getByRole("button", { name: "继续确认评分" }).click();
  await expect.poll(() => requests.length).toBe(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(JSON.parse(requests[0].body).idempotency_key).toBe(requests[0].key);
});
