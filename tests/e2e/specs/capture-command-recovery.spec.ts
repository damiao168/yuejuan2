import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("capture creation reload and double click retain the original command", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page,{initiallyAuthenticated:true});
  const requests: {key:string;body:string}[]=[];
  await page.route("**/api/v1/exams/exam-1/capture-batches",async route=>{
    if(route.request().method()!=="POST") return route.fallback();
    requests.push({key:route.request().headers()["idempotency-key"],body:route.request().postData()!});
    await route.fulfill({status:503,contentType:"application/json",body:JSON.stringify({error:{code:"idempotency_persist_failed"}})});
  });
  await page.route("**/api/v1/exams/exam-1/capture-batches/commands/*",async route=>route.fulfill({status:200,contentType:"application/json",body:JSON.stringify({command:{command_id:route.request().url().split('/').at(-1),status:"not_accepted"}})}));
  await page.goto("/#/exams/exam-1/capture", { waitUntil: "domcontentloaded" });
  // 新流程先选文件再补充批次名称；创建失败后文件尚未上传，恢复仍重放原创建命令。
  await page.getByLabel("选择新批次答卷文件", { exact: true }).setInputFiles({
    name: "原始答卷.png", mimeType: "image/png",
    buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=", "base64"),
  });
  await page.getByText("导入记录名称（选填）", { exact: true }).click();
  await page.getByRole("textbox",{name:"批次名称"}).fill("原始采集批次");
  await page.getByRole("combobox", { name: "新批次每人页数", exact: true }).press("ArrowDown");
  await page.getByTitle("1 页", { exact: true }).click();
  const submit = page.getByRole("dialog").getByRole("button", { name: "确认导入并处理", exact: true });
  await expect(submit).toBeEnabled();
  await submit.evaluate(button=>{(button as HTMLButtonElement).click();(button as HTMLButtonElement).click();});
  await expect.poll(()=>requests.length).toBe(1);
  const key="capture-batch-command:v1:demo-school:user-school_admin:exam-1";
  await expect.poll(()=>page.evaluate(key=>Boolean(localStorage.getItem(key)),key)).toBe(true);
  await page.reload();
  await page.getByRole("button",{name:"导入答卷",exact:true}).first().click();
  // 新草稿故意与首次提交不同，恢复动作仍须重放已持久化的原请求和幂等键。
  await page.getByText("导入记录名称（选填）", { exact: true }).click();
  await page.getByRole("textbox",{name:"批次名称"}).fill("修改后的草稿");
  await page.getByRole("button",{name:"继续确认原操作"}).click();
  await expect.poll(()=>requests.length).toBe(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(JSON.parse(requests[0].body).idempotency_key).toBe(requests[0].key);
});
