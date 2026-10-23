import { expect, type Page } from "@playwright/test";

// 评分方式在题目页行内编辑；高级字段折叠时仍保留真实表单，未知学段需显式选择。
export async function openAssessmentConfiguration(page: Page, frozen = false) {
  const configuration = page.getByRole("region", { name: "本题判分方式", exact: true });
  await expect(configuration.getByLabel("判分方式", { exact: true })).toBeVisible();
  const advanced = configuration.locator("details.assessment-advanced-settings");
  if (await advanced.getAttribute("open") === null) {
    await advanced.getByText("模板、证据与风险设置", { exact: true }).click();
  }
  const stage = configuration.getByLabel("学段", { exact: true });
  await expect(stage).toBeVisible();
  if (frozen) {
    await expect(configuration.getByText("考试配置已冻结", { exact: true })).toBeVisible();
    await expect(stage).toBeDisabled();
  } else {
    await expect(stage).toBeEnabled();
    await stage.press("ArrowDown");
    await page.getByTitle("初中", { exact: true }).click();
    await expect(configuration.getByRole("button", { name: "保存判分方式", exact: true })).toBeVisible();
  }
  return configuration;
}
