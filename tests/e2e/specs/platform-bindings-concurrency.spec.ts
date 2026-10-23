import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("角色配置冲突保留所有草稿，明确刷新核对后才使用新修订保存", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  const first = "10000000-0000-4000-8000-000000000001";
  const second = "10000000-0000-4000-8000-000000000002";
  let binding: Record<string, unknown> = { id: "binding-a", tenant_id: "school-a", education_stage: "senior", subject_code: "mathematics", archetype_code: "*", agent_role: "primary_a", managed_model_api_config_id: "model-a", prompt_version: "p1", strength_rank: 1, status: "active", revision: first };
  const saves: Array<Record<string, unknown>> = [];
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: { tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false } }));
  await page.route("**/api/v1/model-prompts/current", route => route.fulfill({ json: { prompt: { prompt_version: "p1", bundle_sha256: "a".repeat(64), activation_mode: "deployment_manifest", mutable_at_runtime: false, components: [] } } }));
  await page.route("**/api/v1/platform/model-api-configs?*", route => route.fulfill({ json: { configs: ["a", "b"].map(key => ({
    id: `model-${key}`, tenant_id: "school-a", provider_key: "custom", display_name: `学校模型${key}`, model_name: `model-${key}`, model_version: `version-${key}`,
    region: "global", base_url: "https://example.test", adapter_type: "openai_compatible", credential_configured: true, status: "active", is_default: false,
    last_test_status: "success", last_capability_status: "success", last_capability_probe_version: "structured-json-v4"
  })) } }));
  await page.route("**/api/v1/platform/panel-model-bindings?*", route => route.fulfill({ json: { bindings: [binding] } }));
  await page.route("**/api/v1/platform/panel-model-bindings", route => {
    const body = route.request().postDataJSON();
    saves.push(body);
    if (body.expected_revision !== binding.revision) return route.fulfill({ status: 409, json: { error: { code: "panel_model_binding_changed", message: "角色配置已变化" } } });
    binding = { ...binding, ...body, revision: "10000000-0000-4000-8000-000000000003" };
    return route.fulfill({ json: { binding } });
  });
  // DOMContentLoaded 不代表惰性页面已读到当前学校配置；先订阅这两个初始 GET，
  // 再用原有按钮断言核对业务就绪，避免把模块装载耗时算进冲突场景的 5 秒断言。
  const [modelsResponse, bindingsResponse] = await Promise.all([
    page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === "GET" && url.pathname === "/api/v1/platform/model-api-configs" &&
        url.searchParams.get("tenant_id") === "school-a";
    }),
    page.waitForResponse(response => {
      const url = new URL(response.url());
      return response.request().method() === "GET" && url.pathname === "/api/v1/platform/panel-model-bindings" &&
        url.searchParams.get("tenant_id") === "school-a" && url.searchParams.get("education_stage") === "senior" &&
        url.searchParams.get("subject_code") === "mathematics" && url.searchParams.get("archetype_code") === "*";
    }),
    page.goto("/#/platform/model-config?tab=bindings", { waitUntil: "domcontentloaded" })
  ]);
  for (const response of [modelsResponse, bindingsResponse]) expect(response.status()).toBe(200);
  await expect(page.getByRole("button", { name: "保存主评 A" })).toBeEnabled();
  await page.getByRole("button", { name: "高级设置", exact: true }).click();
  await page.getByRole("textbox", { name: "主评 A自定义 Prompt 版本" }).fill("A-local-draft");
  await page.getByRole("textbox", { name: "主评 B自定义 Prompt 版本" }).fill("B-local-draft");
  // API 夹具模拟另一管理员先提交新配置；当前页面仍保留首次读取的修订。
  binding = { ...binding, managed_model_api_config_id: "model-b", prompt_version: "other-admin-prompt", revision: second };
  await page.getByRole("button", { name: "保存主评 A" }).click();
  await expect(page.getByText("角色配置已变化，暂时不能保存", { exact: true })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "主评 A自定义 Prompt 版本" })).toHaveValue("A-local-draft");
  await expect(page.getByRole("textbox", { name: "主评 B自定义 Prompt 版本" })).toHaveValue("B-local-draft");
  await expect(page.getByRole("button", { name: "保存主评 A" })).toBeDisabled();
  expect(binding.managed_model_api_config_id).toBe("model-b");
  expect(saves[0].expected_revision).toBe(first);
  await page.getByRole("button", { name: "刷新绑定", exact: true }).click();
  await page.getByRole("button", { name: "继续编辑", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "主评 A自定义 Prompt 版本" })).toHaveValue("A-local-draft");
  await page.getByRole("button", { name: "刷新绑定", exact: true }).click();
  await page.getByRole("button", { name: "放弃并继续", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "主评 A自定义 Prompt 版本" })).toHaveValue("other-admin-prompt");
  await expect(page.getByRole("button", { name: "保存主评 A" })).toBeEnabled();
  await page.getByRole("textbox", { name: "主评 A自定义 Prompt 版本" }).fill("A-reviewed-prompt");
  await page.getByRole("button", { name: "保存主评 A" }).click();
  await expect.poll(() => binding.prompt_version).toBe("A-reviewed-prompt");
  expect(binding.managed_model_api_config_id).toBe("model-b");
  expect(saves[1].expected_revision).toBe(second);
});
