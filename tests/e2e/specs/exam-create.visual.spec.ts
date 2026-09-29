import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

const viewports = [
  { name: "1440x900", width: 1440, height: 900 },
  { name: "1024x768", width: 1024, height: 768 },
  { name: "390x844", width: 390, height: 844 }
] as const;

test.use({ colorScheme: "light", locale: "zh-CN", timezoneId: "Asia/Shanghai" });

for (const viewport of viewports) {
  test(`lightweight exam composer is stable at ${viewport.name}`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    await page.setViewportSize(viewport);
    await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
    await page.goto("/#/admin/exams/new", { waitUntil: "networkidle" });
    await expect(page.locator(".exam-composer")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(".sidebar")).toHaveCount(0);
    await expect(page.locator(".ant-steps")).toHaveCount(0);
    await page.evaluate(async () => document.fonts.ready);

    const dimensions = await page.evaluate(() => ({ viewportWidth: window.innerWidth, documentWidth: document.documentElement.scrollWidth }));
    expect(dimensions.documentWidth).toBeLessThanOrEqual(dimensions.viewportWidth);
    if (viewport.width < 960) await expect(page.locator(".exam-composer-mobile-action")).toBeVisible();
    else await expect(page.locator(".exam-composer-submit")).toBeVisible();

    const image = await page.screenshot({ fullPage: false, animations: "disabled", scale: "css" });
    await testInfo.attach(`exam-create-${viewport.name}`, { body: image, contentType: "image/png" });
    // 像素基线来自 Windows；其他平台继续验证布局并附截图，避免字体栅格差异误报。
    if (process.platform === "win32") {
      await expect(page).toHaveScreenshot(`exam-create-${viewport.name}.png`, { animations: "disabled", caret: "hide", scale: "css" });
    }
  });
}
