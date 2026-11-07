import { expect, test } from "@playwright/test";
import { captureFailedRequests, installApiMocks } from "../fixtures/apiMocks";
import { loginAsSchoolAdmin } from "../fixtures/schoolAdminLogin";

test("学校管理员登录后从可信工作台进入正式答题卡导入", async ({ page }, testInfo) => {
  const failedRequests = captureFailedRequests(page);
  await installApiMocks(page);

  await loginAsSchoolAdmin(page);
  await expect(page.getByRole("region", { name: "当前状态" })).toContainText("4 件事会卡住出成绩");
  await expect(page.getByRole("heading", { name: "需要我处理" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "考试进度" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "阅卷进度" })).toBeVisible();

  await page.getByRole("button", { name: "处理答卷" }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-1\/capture$/);
  // 导入页不再单独放“答卷导入”标题；以批次记录区和本场核对区确认进入的是正式导入页。
  await expect(page.getByRole("region", { name: "批次文件与处理记录" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "导入答卷", exact: true })).toBeVisible();
  // 本场核对已收进“全场学生核对”页签；默认页签下它只预渲染、不显示，需要先切换。
  await page.getByRole("tab", { name: "全场学生核对" }).click();
  await expect(page.getByRole("region", { name: "本场学生答卷核对" })).toContainText("待完成");
  await expect(page.getByRole("button", { name: "结束本场导入", exact: true })).toBeDisabled();
  await expect(page.getByText("当前页面是兼容模式")).toHaveCount(0);
  await page.screenshot({ path: "output/playwright/capture-collection-overview.png", fullPage: true });

  await testInfo.attach("request-failures", {
    body: failedRequests.length ? failedRequests.join("\n") : "none",
    contentType: "text/plain"
  });
});
