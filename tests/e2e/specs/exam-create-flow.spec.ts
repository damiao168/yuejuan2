import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("资料优先创建允许空试卷结构并直接进入资料配置", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  let submittedBody: Record<string, any> | undefined;
  await page.route("**/api/v1/exam-sessions", async (route) => {
    submittedBody = route.request().postDataJSON();
    await route.fallback();
  });

  await page.goto("/#/admin/exams/new", { waitUntil: "domcontentloaded" });
  const creationModes = page.getByRole("region", { name: "创建方式", exact: true });
  await expect(creationModes).toBeVisible({ timeout: 30_000 });
  await expect(creationModes.getByRole("button", { name: /^使用考试方案/ })).toHaveAttribute("aria-pressed", "false");
  await expect(creationModes.getByRole("button")).toHaveCount(1);
  await expect(page.locator(".ant-steps")).toHaveCount(0);

  await page.getByRole("button", { name: /创建考试并上传试卷/ }).click();
  await expect(page.getByText("请填写考试名称")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "考试名称 *" })).toBeFocused();

  await page.getByRole("textbox", { name: "考试名称 *" }).fill("高二第一学期期中考试");
  await page.getByRole("button", { name: /高二（1）班/ }).click();
  await page.getByRole("button", { name: "数学", exact: true }).click();
  await page.getByRole("button", { name: /创建考试并上传试卷/ }).click();

  await expect(page).toHaveURL(/exam-created-math\/paper/);
  expect(submittedBody?.subjects).toMatchObject([{ subject: "math", sections: [] }]);
  // 建考表单已去掉“考试类型”；创建请求不再携带 exam_type，避免旧草稿残留值被提交。
  expect(submittedBody).not.toHaveProperty("exam_type");
});

test("考试方案通过抽屉带入科目结构", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  await page.goto("/#/admin/exams/new", { waitUntil: "domcontentloaded" });
  await expect(page.locator(".exam-composer")).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: /使用考试方案/ }).click();
  await expect(page.getByRole("dialog", { name: "选择考试方案" })).toBeVisible();
  await page.getByRole("button", { name: /系统通用高中考试方案/ }).click();
  await expect(page.getByText("已套用试卷结构")).toHaveCount(3);
  await expect(page.getByRole("button", { name: "使用方案创建考试" })).toBeVisible();
});
