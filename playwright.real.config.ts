import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e/real",
  // 真实部署测试共享业务状态，串行且不自动重试，避免重放有副作用的操作。
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  outputDir: "output/playwright/real-test-results",
  use: {
    baseURL: process.env.EDUGRADE_REAL_E2E_BASE_URL ?? "http://127.0.0.1:8088",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off"
  },
  projects: [
    {
      name: "real-chromium",
      use: { ...devices["Desktop Chrome"] }
    }
  ]
});
