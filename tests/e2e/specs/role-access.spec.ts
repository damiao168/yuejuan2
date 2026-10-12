import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("平台管理员不能从直达网址进入学校考试管理", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  const examReads: string[] = [];
  page.on("request", request => { if (/^\/api\/v1\/exams(?:\/|$)/.test(new URL(request.url()).pathname)) examReads.push(request.url()); });
  await page.goto("/#/admin/exams");
  await expect(page.getByText("暂时不能使用考试管理", { exact: true })).toBeVisible();
  await expect(page.locator("#primary-navigation").getByRole("menuitem", { name: "考试管理", exact: true })).toHaveCount(0);
  await expect(page.locator(".exam-workspace-canvas")).toHaveCount(0);
  expect(examReads).toEqual([]);
});

test("阅卷老师只能进入自己的阅卷工作区", async ({ page }) => {
  await installApiMocks(page, { role: "grader", initiallyAuthenticated: true });
  const examReads: string[] = [];
  page.on("request", request => { if (/^\/api\/v1\/exams(?:\/|$)/.test(new URL(request.url()).pathname)) examReads.push(request.url()); });
  await page.goto("/#/admin/exams");
  await expect(page.getByText("暂时不能使用考试管理", { exact: true })).toBeVisible();
  await expect(page.locator("#primary-navigation").getByRole("menuitem", { name: "我的阅卷", exact: true })).toBeVisible();
  await expect(page.locator(".exam-workspace-canvas")).toHaveCount(0);
  expect(examReads).toEqual([]);
});
