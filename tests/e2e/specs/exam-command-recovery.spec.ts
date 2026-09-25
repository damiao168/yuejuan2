import { expect, test, type Page } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

const commandKey = "exam-create-command:v1:demo-school:user-school_admin";

async function prepare(page: Page) {
  await page.goto("/#/admin/exams/new");
  await page.getByRole("button", { name: /快速创建/ }).click();
  await page.getByRole("textbox", { name: "考试名称 *" }).fill("命令恢复验收考试");
  await page.getByRole("combobox", { name: /考试类型/ }).click();
  await page.getByTitle("期中考试").click();
  await page.getByRole("button", { name: /高二（1）班/ }).click();
  await page.getByRole("button", { name: "数学", exact: true }).click();
}

test("CMD browser refresh retains unknown command and replays its original request", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page, { initiallyAuthenticated: true });
  const sent: { key: string; body: string }[] = [];
  await page.route("**/api/v1/exam-sessions", async (route) => {
    sent.push({key:route.request().headers()["idempotency-key"],body:route.request().postData()!});
    if (sent.length === 1) return route.fulfill({status:503,contentType:"application/json",body:JSON.stringify({error:{code:"idempotency_persist_failed"}})});
    await route.fallback();
  });
  await page.route("**/api/v1/exam-sessions/commands/*", async (route) => route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({command:{command_id:route.request().url().split("/").at(-1),status:"unknown"}})}));
  await prepare(page);
  await page.getByRole("button", { name: "创建考试", exact: true }).click();
  await expect(page.getByText("正在恢复上一次考试创建命令")).toBeVisible();
  await expect.poll(() => page.evaluate((key) => JSON.parse(localStorage.getItem(key)!).state,commandKey)).toBe("unknown");
  const stored = await page.evaluate((key) => localStorage.getItem(key),commandKey);
  await page.reload();
  await expect(page.getByRole("button", { name: "继续确认原操作" })).toBeVisible();
  await page.getByRole("textbox", { name: "考试名称 *" }).fill("刷新后的草稿修改");
  await page.getByRole("button", { name: "继续确认原操作" }).click();
  await expect(page).toHaveURL(/exam-created-math\/students/);
  expect(sent).toHaveLength(2);
  expect(sent[1]).toEqual(sent[0]);
  expect(JSON.parse(stored!).commandId).toBe(sent[0].key);
});

test("CMD browser double click and failed draft cleanup preserve a successful command", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page, { initiallyAuthenticated: true });
  let submissions = 0;
  await page.route("**/api/v1/exam-sessions", async (route) => { submissions++; await route.fallback(); });
  await prepare(page);
  await page.evaluate(() => {
    const original = Storage.prototype.removeItem;
    Storage.prototype.removeItem = function(key) {
      if (key.startsWith("exam-create-draft:")) throw new Error("injected local cleanup failure");
      return original.call(this,key);
    };
  });
  await page.getByRole("button", { name: "创建考试", exact: true }).evaluate((button) => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click(); });
  await expect(page).toHaveURL(/exam-created-math\/students/);
  expect(submissions).toBe(1);
  const receipt = await page.evaluate((key) => JSON.parse(localStorage.getItem(key)!),commandKey);
  expect(receipt).toMatchObject({state:"succeeded",result:{examSessionId:"session-created"}});
});
