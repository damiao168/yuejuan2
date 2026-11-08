import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("学校管理员从答卷导入和阅卷页面直达本场成绩发布", async ({ page }) => {
  test.setTimeout(60_000);
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  let published = 0;
  page.on("request", request => { if (request.method() === "POST" && /\/publish(?:\?|$)/.test(request.url())) published++; });
  await page.goto("/#/admin/exams/exam-1/capture");
  const header = page.locator(".exam-context-header");
  await expect(header.getByRole("button", { name: "成绩发布", exact: true })).toBeVisible();
  await header.getByRole("button", { name: "阅卷进度", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-1\/grading$/);
  await header.getByRole("button", { name: "成绩发布", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-1\/scores$/);
  const publication = page.getByRole("region", { name: "成绩发布任务", exact: true });
  await expect(publication).toBeVisible({ timeout: 30_000 });
  await expect(publication.getByRole("region", { name: "学生端发布状态", exact: true })).toBeVisible();
  // 打开发布页只展示当前考试检查与发布流程，导航本身不产生发布操作。
  expect(published).toBe(0);
});
