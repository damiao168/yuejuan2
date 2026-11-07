import { expect, test, type Page } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

async function openModelSettings(page: Page, modelName: string) {
  const row = page.getByRole("row").filter({ has: page.getByText(modelName, { exact: true }) });
  const settings = row.locator("details.platform-model-settings");
  // 连接、能力和编辑共用行内设置入口；重复打开侧栏时不能把已展开的设置重新收起。
  const open = await settings.getAttribute("open");
  await expect(settings).toBeVisible();
  if (open === null) await settings.getByText("设置与诊断", { exact: true }).click();
  return row;
}

test("未保存草稿超过两分钟后自动清除", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  await page.goto("/#/platform/model-config");
  await page.clock.install();

  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "DeepSeek" }).click();
  await page.getByLabel("API Key").fill("expiring-draft-secret-at-least-16");
  await page.getByLabel("模型名称").fill("expiring-draft-model");
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  // 先结束侧栏关闭动画和滚动锁，再推进草稿时钟，避免旧表单遮挡下一次打开。
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.clock.fastForward(2 * 60 * 1000 + 1);

  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveValue("");
  await expect(page.getByLabel("模型名称")).toHaveValue("");
});

test("新建草稿按学校隔离且页面重新加载后清除密钥", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [
      { id: "school-a", code: "A", name: "甲学校", status: "active" },
      { id: "school-b", code: "B", name: "乙学校", status: "active" }
    ],
    has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  await page.goto("/#/platform/model-config");

  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "DeepSeek" }).click();
  await page.getByLabel("API Key").fill("school-a-draft-secret-at-least-16");
  await page.getByLabel("模型名称").fill("school-a-draft-model");
  await page.getByRole("button", { name: "关闭", exact: true }).click();

  const schoolSelect = page.locator(".platform-model-school-select .ant-select-selector");
  await schoolSelect.click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "乙学校 · B" }).click();
  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveValue("");
  await expect(page.getByLabel("模型名称")).toHaveValue("");
  await page.getByRole("button", { name: "关闭", exact: true }).click();

  await schoolSelect.click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "甲学校 · A" }).click();
  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveValue("school-a-draft-secret-at-least-16");
  await expect(page.getByLabel("模型名称")).toHaveValue("school-a-draft-model");
  await page.getByRole("button", { name: "关闭", exact: true }).click();

  await page.reload();
  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveValue("");
  await expect(page.getByLabel("模型名称")).toHaveValue("");
  const browserStorage = await page.evaluate(() => `${JSON.stringify(localStorage)}${JSON.stringify(sessionStorage)}`);
  expect(browserStorage).not.toContain("school-a-draft-secret-at-least-16");
});

test("同 Key 草稿可恢复且保存成功后立即清除", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-flash",
    model_version: "deepseek-flash", region: "global", credential_configured: true,
    credential_hint: "•••• st-16", status: "active", is_default: false, last_test_status: "success",
    last_capability_status: "untested", config_source: "auto",
    created_at: "2026-09-21T14:50:00Z", updated_at: "2026-09-21T14:50:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  await page.route("**/api/v1/platform/model-api-configs/auto", async route => {
    expect(route.request().postDataJSON()).toEqual({
      tenant_id: "school-a",
      credential_source_id: "model-1",
      model_name: "deepseek-reasoner",
      provider: "deepseek"
    });
    await route.fulfill({ status: 201, json: {
      config: { ...config, id: "model-2", model_name: "deepseek-reasoner", model_version: "deepseek-reasoner" },
      validation: { latency_ms: 20 }
    } });
  });

  await page.goto("/#/platform/model-config");
  // 保存后会增加同厂商模型行，始终从原模型的编辑入口复用密钥，避免选到新模型。
  const sourceRow = page.getByRole("row").filter({ hasText: "deepseek-flash" });
  const openWithSavedKey = async () => {
    await openModelSettings(page, "deepseek-flash");
    await sourceRow.getByRole("button", { name: "编辑DeepSeek", exact: true }).click();
    await page.getByRole("button", { name: "使用此密钥添加模型", exact: true }).click();
  };
  await openWithSavedKey();
  await expect(page.getByLabel("API Key")).toHaveCount(0);
  await page.getByLabel("模型名称").fill("deepseek-reasoner");
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await openWithSavedKey();
  await expect(page.getByLabel("模型名称")).toHaveValue("deepseek-reasoner");
  await expect(page.getByText("已恢复 2 分钟内未保存的模型配置", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "检测连接并保存", exact: true }).click();

  await openWithSavedKey();
  await expect(page.getByLabel("模型名称")).toHaveValue("");
});

test("编辑自定义 API 域名时保存也必须提交密钥", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "custom", display_name: "自定义模型",
    adapter_type: "openai_compatible", base_url: "https://old.example.test/v1", model_name: "old-model",
    model_version: "old-model", region: "global", credential_configured: true, credential_hint: "•••• old1",
    status: "active", is_default: false, last_test_status: "success", last_capability_status: "untested",
    config_source: "manual", created_at: "2026-09-21T14:50:00Z", updated_at: "2026-09-21T14:50:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  let updateRequests = 0;
  await page.route(/\/api\/v1\/platform\/model-api-configs\/model-1\?tenant_id=school-a$/, async route => {
    updateRequests += 1;
    const body = route.request().postDataJSON();
    expect(body.base_url).toBe("https://new.example.test/v1");
    expect(body.api_key).toBe("new-endpoint-secret-at-least-16");
    await route.fulfill({ json: { config: { ...config, ...body } } });
  });

  await page.goto("/#/platform/model-config");
  await openModelSettings(page, "old-model");
  await page.getByLabel("编辑自定义模型").click();
  const update = page.getByRole("dialog").getByRole("button", { name: /检测连接并更新/ });
  await page.getByLabel("API 地址").fill("https://new.example.test/v1");
  await update.click();
  await expect(page.getByText("更换 API 域名或端口时，请重新输入 API Key（可以使用原密钥）", { exact: true })).toBeVisible();
  // 字段错误先显示，整表校验随后才释放保存锁；按实际忙碌状态等候，不依赖退场图标的无障碍名称。
  await expect(update).not.toHaveClass(/ant-btn-loading/);
  expect(updateRequests).toBe(0);

  await page.getByLabel("API Key").fill("new-endpoint-secret-at-least-16");
  await update.click();
  await expect.poll(() => updateRequests).toBe(1);
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("检测接口以错误响应返回超时时仍使用警告并刷新临时状态", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const successfulConfig = {
    id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-flash",
    model_version: "deepseek-flash", region: "global", credential_configured: true, credential_hint: "•••• st-16",
    status: "active", is_default: true, last_test_status: "success", last_test_message: "连接正常",
    last_successful_tested_at: "2026-09-21T14:50:00Z", last_capability_status: "untested",
    config_source: "auto", created_at: "2026-09-21T14:50:00Z", updated_at: "2026-09-21T14:50:00Z"
  };
  let timedOut = false;
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: {
    configs: [{
      ...successfulConfig,
      ...(timedOut ? {
        last_test_status: "temporary_unavailable",
        last_test_message: "模型服务连接超时，请稍后重试",
        last_tested_at: "2026-09-22T06:00:00Z"
      } : {})
    }]
  } }));
  await page.route("**/api/v1/platform/model-api-configs/model-1/probe?*", route => {
    timedOut = true;
    return route.fulfill({
      status: 504,
      json: { error: { code: "provider_timeout", message: "模型服务连接超时，请稍后重试" } }
    });
  });

  await page.goto("/#/platform/model-config");
  await openModelSettings(page, "deepseek-flash");
  await page.getByRole("button", { name: "deepseek-flash：检测连接", exact: true }).click();
  await expect(page.locator(".ant-message-notice-warning")).toContainText("模型服务连接超时");
  const connection = page.getByRole("button", { name: "deepseek-flash：查看连接详情", exact: true });
  await expect(connection).toHaveText("连接超时");
  await connection.click();
  await expect(page.getByRole("dialog").locator("dl > div").filter({ has: page.getByText("上次连接成功", { exact: true }) })).toContainText("09/21");
});

test("编辑侧边栏复用已保存密钥获取模型并可更新完整配置", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "custom", display_name: "火山方舟 Coding Plan",
    adapter_type: "openai_compatible", base_url: "https://ark.cn-beijing.volces.com/api/coding/v3",
    model_name: "ark-code-latest", model_version: "ark-code-latest", region: "custom",
    credential_configured: true, credential_hint: "•••• 610c", status: "active", is_default: false,
    last_test_status: "failed", last_test_message: "模型服务连接超时，请稍后重试", last_capability_status: "untested",
    config_source: "auto", provider_registry_version: "2026-09-11",
    created_at: "2026-09-11T09:00:00Z", updated_at: "2026-09-11T09:00:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  await page.route("**/api/v1/platform/model-api-configs/models", async route => {
    expect(route.request().postDataJSON()).toEqual({
      tenant_id: "school-a",
      credential_source_id: "model-1",
      provider: "custom",
      base_url: "https://ark.cn-beijing.volces.com/api/coding/v3"
    });
    await route.fulfill({ json: {
      provider: { key: "custom", display_name: "其他兼容接口" },
      models: ["ark-code-latest", "doubao-seed-2.1-turbo"], latency_ms: 21
    } });
  });
  await page.route(/\/api\/v1\/platform\/model-api-configs\/model-1\?tenant_id=school-a$/, async route => {
    const body = route.request().postDataJSON();
    expect(body.api_key).toBe("");
    expect(body.base_url).toBe("https://ark.cn-beijing.volces.com/api/coding/v3");
    expect(body.model_name).toBe("doubao-seed-2.1-turbo");
    await route.fulfill({ json: { config: { ...config, ...body, last_test_status: "success" } } });
  });

  await page.goto("/#/platform/model-config");
  await openModelSettings(page, "ark-code-latest");
  await page.getByLabel("编辑火山方舟 Coding Plan").click();
  await expect(page.getByLabel("供应商")).toBeDisabled();
  await expect(page.getByLabel("API 地址")).toBeEditable();
  await expect(page.getByLabel("API 地址")).toHaveValue("https://ark.cn-beijing.volces.com/api/coding/v3");
  await expect(page.getByLabel("API Key")).toHaveValue("");
  await expect(page.getByLabel("模型名称")).toHaveValue("ark-code-latest");
  await page.getByLabel("API Key").fill("replacement-secret-at-least-16");
  await page.getByLabel("模型名称").fill("draft-edit-model");
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByLabel("编辑火山方舟 Coding Plan").click();
  await expect(page.getByLabel("API Key")).toHaveValue("replacement-secret-at-least-16");
  await expect(page.getByLabel("模型名称")).toHaveValue("draft-edit-model");
  await expect(page.getByText("已恢复 2 分钟内未保存的编辑内容", { exact: true })).toBeVisible();
  await page.getByLabel("API Key").fill("");
  await page.getByLabel("模型名称").fill("ark-code-latest");
  await page.getByLabel("API 地址").fill("https://ark.example.test/v1");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  await expect(page.getByText("更换 API 域名或端口时，请重新输入 API Key（可以使用原密钥）", { exact: true })).toBeVisible();
  await page.getByLabel("API 地址").fill("https://ark.cn-beijing.volces.com/api/coding/v3");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "doubao-seed-2.1-turbo" }).click();
  await page.getByRole("button", { name: "检测连接并更新", exact: true }).click();

  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("doubao-seed-2.1-turbo", { exact: true })).toBeVisible();
});
