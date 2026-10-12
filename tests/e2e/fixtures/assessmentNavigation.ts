import { expect, type Page } from "@playwright/test";

// 学科配置现在直接打开；未知学段必须由管理员显式选择，不能让测试假定系统默认初中。
export async function openAssessmentConfiguration(page: Page, frozen = false) {
  await page.locator(".assessment-profile-section").getByRole("button", { name: /^(?:查看)?配置$/ }).click();
  const drawer = page.getByRole("dialog", { name: /学科评分配置$/ });
  const stage = drawer.getByLabel("学段", { exact: true });
  await expect(stage).toBeVisible();
  if (frozen) {
    await expect(drawer.getByText("考试配置已冻结", { exact: true })).toBeVisible();
    await expect(stage).toBeDisabled();
  } else {
    await expect(stage).toBeEnabled();
    await stage.press("ArrowDown");
    await page.getByTitle("初中", { exact: true }).click();
    await expect(drawer.getByRole("button", { name: "保存配置", exact: true })).toBeVisible();
  }
  return drawer;
}
