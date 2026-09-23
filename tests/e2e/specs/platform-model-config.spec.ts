import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("切换学校后忽略过期配置响应，并保留新学校的配置", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" },
      { id: "school-b", code: "B", name: "乙学校", status: "active" }], has_more: false
  } }));
  let releaseA!: () => void;
  const pendingA = new Promise<void>(resolve => { releaseA = resolve; });
  let startedA = false;
  let finishedA = false;
  await page.route("**/api/v1/platform/model-api-configs?*", async route => {
    const tenant = new URL(route.request().url()).searchParams.get("tenant_id");
    if (tenant === "school-a") {
      startedA = true;
      await pendingA;
    }
    await route.fulfill({ json: { configs: [{ id: tenant, tenant_id: tenant,
      provider_key: "custom", display_name: tenant === "school-a" ? "甲校模型" : "乙校模型",
      model_name: "test-model", model_version: "v1", base_url: "https://example.test/v1",
      adapter_type: "openai_compatible", credential_configured: true, status: "active", is_default: true,
      last_test_status: "untested", region: "global" }] } });
    if (tenant === "school-a") finishedA = true;
  });
  await page.goto("/#/platform/model-config", { waitUntil: "domcontentloaded" });
  await expect.poll(() => startedA).toBe(true);
  const schoolSelect = page.locator(".platform-model-school-select .ant-select-selector");
  await schoolSelect.click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "乙学校 · B" }).click();
  await expect(page.getByText("乙校模型", { exact: true })).toBeVisible();
  releaseA();
  await expect.poll(() => finishedA).toBe(true);
  await expect(page.getByText("甲校模型", { exact: true })).toHaveCount(0);
  await expect(page.getByText("乙校模型", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByRole("combobox", { name: "配置学校" })).toBeDisabled();
  await page.getByRole("button", { name: "检测连接并保存", exact: true }).click();
  await expect(page.getByText("请选择供应商", { exact: true })).toBeVisible();
  await expect(page.getByText("请输入 API Key", { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toBeVisible();
});

test("只填供应商和 API Key 即可零 Token 获取模型并保存", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  await page.route("**/api/v1/platform/model-api-configs/models", async route => {
    expect(route.request().postDataJSON()).toEqual({
      tenant_id: "school-a",
      api_key: "deepseek-secret-at-least-16",
      provider: "deepseek"
    });
    await route.fulfill({ json: {
      provider: { key: "deepseek", display_name: "DeepSeek" },
      models: ["deepseek-v4-flash", "deepseek-v4-pro"],
      latency_ms: 12
    } });
  });
  await page.route("**/api/v1/platform/model-api-configs/auto", async route => {
    const body = route.request().postDataJSON();
    expect(body).toEqual({
      tenant_id: "school-a",
      api_key: "deepseek-secret-at-least-16",
      model_name: "deepseek-v4-pro",
      provider: "deepseek"
    });
    await route.fulfill({ status: 201, json: {
      config: {
        id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
        adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-v4-pro",
        model_version: "deepseek-v4-pro", region: "global", credential_configured: true,
        credential_hint: "•••• st-16", status: "active", is_default: false, last_test_status: "success",
        last_probe_mode: "quick", last_capability_status: "untested",
        last_test_message: "连接检查成功，本次未发送模型生成请求", last_test_latency_ms: 18, last_tested_at: "2026-09-11T09:00:00Z",
        config_source: "auto", provider_registry_version: "2026-09-11",
        created_at: "2026-09-11T09:00:00Z", updated_at: "2026-09-11T09:00:00Z"
      },
      validation: {
        ok: true, probe_mode: "quick", generated_request: false, provider: "deepseek", model: "deepseek-v4-pro", status_code: 200, latency_ms: 18,
        message: "连接检查成功，本次未发送模型生成请求", credential_check: { ok: true }, model_check: { ok: true }, capability_check: { code: "not_run" },
        usage: { input_tokens: 0, cached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0 }, diagnostic: {}
      }
    } });
  });

  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "DeepSeek" }).click({ force: true });
  await page.getByLabel("API Key").fill("deepseek-secret-at-least-16");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  const modelDropdown = page.locator(".ant-select-dropdown:visible");
  await expect(modelDropdown.locator(".ant-select-item-option-content", { hasText: "deepseek-v4-flash" })).toBeVisible();
  await expect(page.getByRole("button", { name: "已找到 2 个，点击选择" })).toBeVisible();
  await modelDropdown.locator(".ant-select-item-option-content", { hasText: "deepseek-v4-pro" }).click();
  await page.getByRole("button", { name: "检测连接并保存", exact: true }).click();

  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("DeepSeek", { exact: true })).toBeVisible();
  await expect(page.getByText("备用", { exact: true })).toBeVisible();
  await expect(page.getByText(/API Key 有效 · 模型可用 · 0 生成 Token · 18ms/)).toBeVisible();
});

test("新建模型遇到网络超时时使用警告提示并保留当前输入", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  const timeoutPayload = {
    error: { code: "provider_timeout", message: "模型服务连接超时，请稍后重试" },
    validation: {
      ok: false, probe_mode: "quick", generated_request: false, provider: "deepseek", latency_ms: 12000,
      message: "模型服务连接超时，请稍后重试", error_code: "provider_timeout",
      credential_check: { ok: false }, model_check: { ok: false }, capability_check: { ok: false },
      usage: { input_tokens: 0, cached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0 },
      diagnostic: {}, connection_diagnostic: { attempts: 2, failure_stage: "ttfb" }
    }
  };
  await page.route("**/api/v1/platform/model-api-configs/models", route => route.fulfill({ status: 422, json: timeoutPayload }));
  await page.route("**/api/v1/platform/model-api-configs/auto", route => route.fulfill({ status: 422, json: timeoutPayload }));

  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "DeepSeek" }).click();
  await page.getByLabel("API Key").fill("deepseek-secret-at-least-16");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  await expect(page.locator(".ant-message-notice-warning")).toContainText("不代表 API Key 无效");

  await page.getByLabel("模型名称").fill("deepseek-v4-pro");
  await page.getByRole("button", { name: "检测连接并保存", exact: true }).click();
  await expect(page.locator(".ant-message-notice-warning").last()).toContainText("已保留当前填写内容");
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByLabel("API Key")).toHaveValue("deepseek-secret-at-least-16");
  await expect(page.getByLabel("模型名称")).toHaveValue("deepseek-v4-pro");
});

test("普通连接检测使用零生成 Token 快速模式", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-v4-pro",
    model_version: "deepseek-v4-pro", region: "global", credential_configured: true,
    credential_hint: "•••• st-16", status: "active", is_default: true, last_test_status: "success",
    last_probe_mode: "capability", last_test_message: "配置验证成功", last_test_latency_ms: 18,
    last_tested_at: "2026-09-11T09:00:00Z", last_capability_status: "success",
    last_capability_message: "结构化输出正常", last_capability_tested_at: "2026-09-11T09:00:00Z",
    last_capability_probe_version: "structured-json-v3",
    last_capability_usage: { input_tokens: 9, cached_input_tokens: 0, output_tokens: 5, reasoning_tokens: 0, total_tokens: 14 },
    config_source: "auto", provider_registry_version: "2026-09-11",
    created_at: "2026-09-11T09:00:00Z", updated_at: "2026-09-11T09:00:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  let quickRequestSeen = false;
  await page.route("**/api/v1/platform/model-api-configs/model-1/probe?*", route => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get("mode")).toBe("quick");
    expect(url.searchParams.has("force")).toBe(false);
    quickRequestSeen = true;
    return route.fulfill({ json: {
      result: {
        ok: true, probe_mode: "quick", generated_request: false, provider: "deepseek", model: "deepseek-v4-pro",
        latency_ms: 11, message: "连接检查成功，本次未发送模型生成请求",
        credential_check: { ok: true }, model_check: { ok: true }, capability_check: { ok: true, code: "reused" },
        usage: { input_tokens: 0, cached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0 }
      },
      config: { ...config, last_probe_mode: "quick", last_test_latency_ms: 11 }
    } });
  });

  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "检测连接" }).click();
  await expect.poll(() => quickRequestSeen).toBe(true);
  await expect(page.getByText("连接正常", { exact: true })).toBeVisible();
  await expect(page.getByText(/结构化能力：已验证/)).toBeVisible();
});

test("连接超时仅标记暂时无法验证，保留上次成功时间，重测成功后恢复", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-flash",
    model_version: "deepseek-flash", region: "global", credential_configured: true,
    credential_hint: "•••• st-16", status: "active", is_default: true, last_test_status: "success",
    last_test_message: "连接检查成功", last_test_latency_ms: 18,
    last_tested_at: "2026-09-21T14:50:00Z", last_successful_tested_at: "2026-09-21T14:50:00Z",
    last_capability_status: "success", last_capability_probe_version: "structured-json-v3",
    last_capability_tested_at: "2026-09-21T14:50:00Z",
    config_source: "auto", created_at: "2026-09-21T14:50:00Z", updated_at: "2026-09-21T14:50:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  let probeCount = 0;
  await page.route("**/api/v1/platform/model-api-configs/model-1/probe?*", route => {
    probeCount += 1;
    const temporary = probeCount === 1;
    return route.fulfill({ json: {
      result: {
        ok: !temporary, probe_mode: "quick", generated_request: false, provider: "deepseek", model: "deepseek-flash",
        latency_ms: temporary ? 12000 : 19,
        message: temporary ? "模型服务连接超时，请稍后重试" : "连接检查成功",
        ...(temporary ? { error_code: "provider_timeout" } : {}),
        credential_check: { ok: !temporary }, model_check: { ok: !temporary }, capability_check: { code: "not_run" },
        usage: { input_tokens: 0, cached_input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0 }, diagnostic: {}
      },
      config: {
        ...config,
        last_test_status: temporary ? "temporary_unavailable" : "success",
        last_test_message: temporary ? "模型服务连接超时，请稍后重试" : "连接检查成功",
        last_tested_at: temporary ? "2026-09-22T06:00:00Z" : "2026-09-22T06:02:00Z",
        last_successful_tested_at: temporary ? config.last_successful_tested_at : "2026-09-22T06:02:00Z"
      }
    } });
  });

  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "检测连接" }).click();
  await expect(page.locator(".platform-model-probe .ant-tag-warning")).toHaveText("暂时无法验证");
  await expect(page.locator(".platform-model-probe")).toContainText("模型服务连接超时，请稍后重试");
  await expect(page.locator(".platform-model-probe")).toContainText(/上次连接正常：\d{2}\/\d{2} \d{2}:\d{2}/);
  await expect(page.locator(".platform-model-school-summary")).toContainText("DeepSeek · deepseek-flash · 暂时无法验证");
  await expect(page.locator(".platform-model-school-summary")).toContainText("1 个暂时无法验证");
  await expect(page.locator(".platform-model-probe .ant-tag-error")).toHaveCount(0);
  await expect(page.locator(".ant-message-notice-warning")).toContainText("不代表模型已失效");

  await page.getByRole("button", { name: "检测连接" }).click();
  await expect(page.locator(".platform-model-probe .ant-tag-success")).toHaveText("连接正常");
  await expect(page.locator(".platform-model-school-summary")).toContainText("DeepSeek · deepseek-flash · 正常");
  await expect(page.locator(".platform-model-probe")).not.toContainText("上次连接正常：");
});

test("完整能力检测失败时保留配置并展示具体原因", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = {
    id: "model-1", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-flash",
    model_version: "deepseek-flash", region: "global", credential_configured: true,
    credential_hint: "•••• st-16", status: "active", is_default: false, last_test_status: "success",
    last_probe_mode: "quick", last_test_message: "连接正常", last_test_latency_ms: 12,
    last_tested_at: "2026-09-11T09:00:00Z", last_capability_status: "untested",
    config_source: "auto", provider_registry_version: "2026-09-11",
    created_at: "2026-09-11T09:00:00Z", updated_at: "2026-09-11T09:00:00Z"
  };
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [config] } }));
  await page.route("**/api/v1/platform/model-api-configs/model-1/probe?*", route => route.fulfill({ json: {
    result: {
      ok: false, probe_mode: "capability", generated_request: true, provider: "deepseek", model: "deepseek-flash",
      status_code: 200, latency_ms: 420, message: "模型返回内容不是合法 JSON", error_code: "invalid_json",
      credential_check: { ok: true }, model_check: { ok: true },
      capability_check: { ok: false, code: "invalid_json", message: "模型返回内容不是合法 JSON" },
      usage: { input_tokens: 28, cached_input_tokens: 0, output_tokens: 3, reasoning_tokens: 0, total_tokens: 31 },
      diagnostic: { finish_reason: "stop", response_format: "json_object", content_length: 8, content_preview: "not json" }
    },
    config: { ...config, last_capability_status: "failed", last_capability_message: "模型返回内容不是合法 JSON",
      last_capability_probe_version: "structured-json-v3", last_capability_diagnostic: {
        finish_reason: "stop", response_format: "json_object", content_length: 8
      } }
  } }));

  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "DeepSeek更多操作" }).click();
  await page.getByText("完整能力检测", { exact: true }).click();
  const diagnosticDialog = page.getByRole("dialog");
  await expect(diagnosticDialog.locator(".ant-modal-confirm-title")).toHaveText("DeepSeek：结构化输出检测未通过");
  await expect(diagnosticDialog.getByText("not json", { exact: true })).toBeVisible();
  await expect(page.getByText("验证失败", { exact: false })).toBeVisible();
  await expect(page.getByText("DeepSeek", { exact: true })).toBeVisible();
});

test("选择自定义供应商后才显示 API 地址", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  await page.goto("/#/platform/model-config");
  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.getByLabel("API 地址")).toHaveCount(0);
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "其他兼容接口" }).click();
  await expect(page.getByLabel("API 地址")).toBeVisible();
  await expect(page.getByRole("dialog")).toBeVisible();
});

test("关闭新建侧边栏后两分钟内恢复未保存内容且不写入浏览器存储", async ({ page }) => {
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs: [] } }));
  await page.goto("/#/platform/model-config");

  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "其他兼容接口" }).click();
  await page.getByLabel("API Key").fill("draft-secret-at-least-16");
  await page.getByLabel("API 地址").fill("https://models.example.test/v1");
  await page.getByLabel("模型名称").fill("draft-model");
  await page.getByRole("button", { name: "关闭", exact: true }).click({ force: true });
  await expect(page.getByRole("dialog")).toHaveCount(0);

  const browserStorage = await page.evaluate(() => `${JSON.stringify(localStorage)}${JSON.stringify(sessionStorage)}`);
  expect(browserStorage).not.toContain("draft-secret-at-least-16");

  await page.getByRole("button", { name: "添加模型" }).click();
  await expect(page.locator(".ant-drawer .ant-select-selection-item").first()).toHaveText("其他兼容接口");
  await expect(page.getByLabel("API Key")).toHaveValue("draft-secret-at-least-16");
  await expect(page.getByLabel("API 地址")).toHaveValue("https://models.example.test/v1");
  await expect(page.getByLabel("模型名称")).toHaveValue("draft-model");
  await expect(page.getByText("已恢复 2 分钟内未保存的模型配置", { exact: true })).toBeVisible();
});

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
  await page.getByRole("button", { name: "关闭", exact: true }).click({ force: true });

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
  await page.getByRole("button", { name: "同 Key 模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveCount(0);
  await page.getByLabel("模型名称").fill("deepseek-reasoner");
  await page.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("button", { name: "同 Key 模型" }).click();
  await expect(page.getByLabel("模型名称")).toHaveValue("deepseek-reasoner");
  await expect(page.getByText("已恢复 2 分钟内未保存的模型配置", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "检测连接并保存", exact: true }).click();

  await page.getByRole("button", { name: "同 Key 模型" }).first().click();
  await expect(page.getByLabel("模型名称")).toHaveValue("");
});

test("编辑自定义 API 地址时保存也必须提交新密钥", async ({ page }) => {
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
  await page.getByLabel("编辑自定义模型").click();
  await page.getByLabel("API 地址").fill("https://new.example.test/v1");
  await page.getByRole("button", { name: "检测连接并更新", exact: true }).click();
  await expect(page.getByText("更换 API 地址时请输入新 API Key", { exact: true })).toBeVisible();
  expect(updateRequests).toBe(0);

  await page.getByLabel("API Key").fill("new-endpoint-secret-at-least-16");
  await page.getByRole("button", { name: "检测连接并更新", exact: true }).click();
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
  await page.getByRole("button", { name: "检测连接" }).click();
  await expect(page.locator(".ant-message-notice-warning")).toContainText("模型服务连接超时");
  await expect(page.locator(".platform-model-probe .ant-tag-warning")).toHaveText("暂时无法验证");
  await expect(page.locator(".platform-model-probe")).toContainText("上次连接正常：");
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
  await expect(page.getByText("更换 API 地址时请输入新 API Key", { exact: true })).toBeVisible();
  await page.getByLabel("API 地址").fill("https://ark.cn-beijing.volces.com/api/coding/v3");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "doubao-seed-2.1-turbo" }).click();
  await page.getByRole("button", { name: "检测连接并更新", exact: true }).click();

  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("doubao-seed-2.1-turbo", { exact: true })).toBeVisible();
});
