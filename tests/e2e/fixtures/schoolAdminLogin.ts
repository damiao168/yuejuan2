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
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "考试工作台" })).toBeVisible({ timeout: 30_000 });
}
