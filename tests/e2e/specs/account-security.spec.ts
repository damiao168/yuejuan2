import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("安全活动合并设备名称并精简侧栏品牌区", async ({ page }) => {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });

  await page.goto("/#/admin/account/sessions", { waitUntil: "networkidle" });

  await expect(page.getByRole("heading", { name: "账户安全" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "安全活动" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "登录设备" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: /办公室 Windows 电脑/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: /教务处平板/ })).toBeVisible();

  const brand = page.getByRole("button", { name: "返回管理端工作台" });
  await expect(brand).toHaveText("EduGrade");
  const toggle = page.getByRole("button", { name: "收起导航" });
  const [brandBox, toggleBox] = await Promise.all([brand.boundingBox(), toggle.boundingBox()]);
  expect(brandBox).not.toBeNull();
  expect(toggleBox).not.toBeNull();
  expect(Math.abs((brandBox!.y + brandBox!.height / 2) - (toggleBox!.y + toggleBox!.height / 2))).toBeLessThanOrEqual(1);
});
