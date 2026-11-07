import { expect, type Page } from "@playwright/test";

interface SchoolAdminCredentials {
  schoolCode?: string;
  identifier?: string;
  password?: string;
}

export async function loginAsSchoolAdmin(
  page: Page,
  {
    schoolCode = "demo-school",
    identifier = "school_admin",
    password = "password",
  }: SchoolAdminCredentials = {},
) {
  await page.goto("/");
  await page.getByLabel("学校代码").fill(schoolCode);
  await page.getByLabel("用户名").fill(identifier);
  await page.getByRole("textbox", { name: /密码/ }).fill(password);
  // 登录页另有“公共电脑登录”按钮，名称包含“登录”；必须精确匹配，否则严格模式下同时命中两个按钮。
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "考试工作台" })).toBeVisible({ timeout: 30_000 });
}
