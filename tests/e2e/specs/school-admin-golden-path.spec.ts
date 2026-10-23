import { expect, test } from "@playwright/test";
import { captureFailedRequests, installApiMocks } from "../fixtures/apiMocks";
import { loginAsSchoolAdmin } from "../fixtures/schoolAdminLogin";

test("学校管理员登录后从可信工作台进入正式答题卡导入", async ({ page }, testInfo) => {
  const failedRequests = captureFailedRequests(page);
  await installApiMocks(page);

  await loginAsSchoolAdmin(page);
  await expect(page.getByRole("heading", { name: "考试管理" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "阅卷与成绩" })).toBeVisible();
  await expect(page.locator(".grading-metric-strip").getByText("107", { exact: true })).toBeVisible();
  await expect(page.getByText("主观题等待确认")).toBeVisible();

  await page.getByRole("button", { name: "处理答卷" }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-1\/capture$/);
  await expect(page.getByRole("heading", { name: "答卷导入" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "新建批次" })).toBeVisible();
  await expect(page.getByText("当前页面是兼容模式")).toHaveCount(0);

  await testInfo.attach("request-failures", {
    body: failedRequests.length ? failedRequests.join("\n") : "none",
    contentType: "text/plain"
  });
});
