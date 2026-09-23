import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";

test("学校模型与治理使用同一学校配置，旧治理地址转入模型管理", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route("**/api/v1/platform/model-api-configs?*", route => route.fulfill({ json: { configs: [{
    id: "model-a", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek",
    adapter_type: "openai_compatible", base_url: "https://api.deepseek.com", model_name: "deepseek-v4",
    model_version: "deepseek-v4", region: "global", credential_configured: true,
    status: "active", is_default: false, last_test_status: "success",
    last_capability_status: "success", last_capability_probe_version: "structured-json-v3"
  }] } }));
  let bindingBody: Record<string, unknown> | undefined;
  await page.route("**/api/v1/platform/panel-model-bindings?*", route => route.fulfill({ json: { bindings: [] } }));
  await page.route("**/api/v1/platform/panel-model-bindings", route => {
    bindingBody = route.request().postDataJSON();
    return route.fulfill({ json: { binding: { ...bindingBody, id: "binding-a" } } });
  });
  const tenantsSeen: string[] = [];
  await page.route(/\/api\/v1\/(model-evaluation-runs|model-approvals|model-policy)(?:\?|$)/, route => {
    tenantsSeen.push(new URL(route.request().url()).searchParams.get("tenant_id") ?? "");
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("model-evaluation-runs")) return route.fulfill({ json: { evaluation_runs: [], next_cursor: "", has_more: false } });
    if (path.endsWith("model-approvals")) return route.fulfill({ json: { model_approvals: [] } });
    return route.fulfill({ json: { policy: {
      id: "policy-a", tenant_id: "school-a", policy_key: "default", display_name: "默认策略",
      mode: "local_only", external_enabled: false, text_export_enabled: false, image_export_enabled: false,
      allowed_deployments: [], allowed_model_config_ids: [], max_cost_micros_per_question: 0,
      max_cost_micros_per_exam: 0, fallback_mode: "manual_only", status: "active", version: 1,
      updated_at: "2026-09-23T00:00:00Z"
    } } });
  });

  await page.goto("/#/system/models", { waitUntil: "domcontentloaded" });
  await expect(page).toHaveURL(/platform\/model-config\?tab=governance/);
  await expect(page.getByRole("heading", { name: "模型管理" })).toBeVisible();
  await expect(page.getByText("模型评测", { exact: true })).toBeVisible();
  await expect.poll(() => tenantsSeen.length).toBeGreaterThanOrEqual(3);
  expect(tenantsSeen.every(id => id === "school-a")).toBe(true);
  await expect(page.getByText("登记供应商")).toHaveCount(0);
  await expect(page.getByText("登记部署")).toHaveCount(0);
  const assuranceTenants: string[] = [];
  page.on("request", request => {
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/ai-eligibility/policy" || url.pathname === "/api/v1/grading-evaluations") {
      assuranceTenants.push(url.searchParams.get("tenant_id") ?? "");
    }
  });
  await page.getByText("评分保障", { exact: true }).click();
  await expect.poll(() => assuranceTenants.length).toBeGreaterThan(0);
  expect(assuranceTenants.every(id => id === "school-a")).toBe(true);
  await page.getByText("三智能体配置", { exact: true }).click();
  await page.getByRole("combobox", { name: "主评 A评分模型" }).click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click();
  await page.getByRole("textbox", { name: "主评 A Prompt 版本" }).fill("prompt-v1");
  await page.getByRole("button", { name: "保存主评 A" }).click();
  await expect.poll(() => bindingBody?.managed_model_api_config_id).toBe("model-a");
  expect(bindingBody?.tenant_id).toBe("school-a");
  expect(bindingBody).not.toHaveProperty("deployment_id");
});

test("治理操作只提交学校模型配置 ID", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  await page.route("**/api/v1/platform/model-api-configs?*", route => route.fulfill({ json: { configs: [
    { id: "model-a", tenant_id: "school-a", provider_key: "deepseek", display_name: "DeepSeek", model_name: "deepseek-v4", model_version: "v4", region: "global", base_url: "https://example.test", adapter_type: "openai_compatible", credential_configured: true, status: "active", is_default: false, last_test_status: "success", last_capability_status: "success", last_capability_probe_version: "structured-json-v3" },
    { id: "model-b", tenant_id: "school-a", provider_key: "aliyun", display_name: "阿里云百炼", model_name: "qwen-vl", model_version: "v2", region: "cn", base_url: "https://example.test", adapter_type: "openai_compatible", credential_configured: true, status: "active", is_default: false, last_test_status: "success", last_capability_status: "success", last_capability_probe_version: "structured-json-v3" }
  ] } }));
  const candidate = { id: "candidate-a", model_config_id: "model-a", model_name: "deepseek-v4", provider_key: "deepseek", model_version: "v4", prompt_version: "prompt-v1", rubric_version: "rubric-v1", metrics: { teacher_acceptance_rate: 0.9, serious_error_rate: 0, evidence_validity_rate: 1, stability_rate: 1, average_cost_micros: 10 }, p95_latency_ms: 100 };
  const draftRun = { id: "run-draft", run_key: "draft", display_name: "待录入", dataset_reference: "frozen", dataset_sha256: "a".repeat(64), authorization_reference: "approved-data", evidence_class: "authorized_frozen_set", subject: "数学", grade: "九年级", question_type: "essay", modality: "text", sample_count: 10, repeat_count: 1, status: "draft", candidates: [], created_at: "2026-09-23T00:00:00Z" };
  const completedRun = { ...draftRun, id: "run-done", run_key: "done", display_name: "已冻结", status: "completed", candidates: [candidate, { ...candidate, id: "candidate-b", model_config_id: "model-b", model_name: "qwen-vl", provider_key: "aliyun" }] };
  await page.route("**/api/v1/model-evaluation-runs?*", route => route.fulfill({ json: { evaluation_runs: [draftRun, completedRun], next_cursor: "", has_more: false } }));
  await page.route("**/api/v1/model-approvals?*", route => route.fulfill({ json: { model_approvals: [] } }));
  await page.route("**/api/v1/model-policy?*", route => route.fulfill({ json: { policy: { id: "policy", tenant_id: "school-a", display_name: "默认策略", mode: "local_only", external_enabled: false, text_export_enabled: false, image_export_enabled: false, allowed_deployments: [], allowed_model_config_ids: [], max_cost_micros_per_question: 0, max_cost_micros_per_exam: 0, fallback_mode: "manual_only", version: 1, status: "active", updated_at: "2026-09-23T00:00:00Z" } } }));
  let candidateBody: Record<string, unknown> | undefined;
  let approvalBody: Record<string, unknown> | undefined;
  let policyBody: Record<string, unknown> | undefined;
  await page.route("**/api/v1/model-policy?*", route => {
    if (route.request().method() !== "PUT") return route.fallback();
    policyBody = route.request().postDataJSON();
    return route.fulfill({ json: { policy: { id: "policy", version: 2 } } });
  });
  await page.route("**/api/v1/model-evaluation-runs/run-draft/candidates?*", route => {
    candidateBody = route.request().postDataJSON();
    return route.fulfill({ status: 201, json: { candidate } });
  });
  await page.route("**/api/v1/model-approvals", route => {
    approvalBody = route.request().postDataJSON();
    return route.fulfill({ status: 201, json: { model_approval: { id: "approval-a" } } });
  });
  await page.goto("/#/platform/model-config?tab=governance", { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("button", { name: "添加候选" })).toBeEnabled({ timeout: 20_000 });
  await page.getByRole("button", { name: "添加候选" }).click();
  await page.getByLabel("评测模型").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click({ force: true });
  await page.getByLabel("提示词版本").fill("prompt-v1");
  await page.getByLabel("Rubric 版本").fill("rubric-v1");
  await page.getByLabel("录入原因").fill("学校模型评测证据");
  await page.getByRole("button", { name: "保存候选" }).click();
  await expect.poll(() => candidateBody?.model_config_id).toBe("model-a");
  expect(candidateBody).not.toHaveProperty("deployment_id");
  await page.getByText("模型批准", { exact: true }).click();
  await page.getByRole("button", { name: "新建批准" }).click();
  await page.getByLabel("授权冻结集评测").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "已冻结" }).click({ force: true });
  await page.getByLabel("评测模型").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click({ force: true });
  await page.getByLabel("决策引用").fill("school-decision");
  await page.getByLabel("批准原因").fill("授权冻结集复核完成");
  await page.getByRole("button", { name: "确认批准" }).click();
  await expect.poll(() => approvalBody?.model_config_id).toBe("model-a");
  expect(approvalBody?.tenant_id).toBe("school-a");
  expect(approvalBody).not.toHaveProperty("deployment_id");
  await page.getByText("使用策略", { exact: true }).click();
  await page.getByLabel("允许外部模型").click();
  await page.getByLabel("允许发送文本").click();
  await page.getByLabel("允许使用的学校模型").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click({ force: true });
  await page.getByLabel("修改原因").fill("学校模型使用策略更新");
  await page.getByRole("button", { name: "保存策略" }).click();
  await expect.poll(() => policyBody?.allowed_model_config_ids).toEqual(["model-a"]);
  expect(policyBody).not.toHaveProperty("allowed_deployments");
});

test("从添加模型到三智能体、评测、批准和策略使用同一学校模型", async ({ page }) => {
  test.setTimeout(60_000);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "platform_admin", initiallyAuthenticated: true });
  await page.route("**/api/v1/tenants?*", route => route.fulfill({ json: {
    tenants: [{ id: "school-a", code: "A", name: "甲学校", status: "active" }], has_more: false
  } }));
  const config = (id: string, provider: string, display: string, model: string) => ({
    id, tenant_id: "school-a", provider_key: provider, display_name: display,
    adapter_type: "openai_compatible", base_url: "https://example.test", model_name: model,
    model_version: model, region: "global", credential_configured: true,
    status: "active", is_default: false, last_test_status: "success",
    last_capability_status: "success", last_capability_probe_version: "structured-json-v3"
  });
  const configs = [
    config("model-a", "deepseek", "DeepSeek", "deepseek-v4"),
    config("model-b", "aliyun", "阿里云百炼", "qwen-vl")
  ];
  await page.route(/\/api\/v1\/platform\/model-api-configs(?:\?.*)?$/, route => route.fulfill({ json: { configs } }));
  await page.route("**/api/v1/platform/model-api-configs/models", route => route.fulfill({ json: {
    provider: { key: "zhipu", display_name: "智谱 AI" }, models: ["glm-4.6v"], latency_ms: 10
  } }));
  await page.route("**/api/v1/platform/model-api-configs/auto", route => {
    const body = route.request().postDataJSON();
    expect(body.tenant_id).toBe("school-a");
    expect(body.api_key).toBe("zhipu-secret-at-least-16");
    const created = { ...config("model-c", "zhipu", "智谱 AI", "glm-4.6v"),
      last_capability_status: "untested", last_capability_probe_version: "" };
    configs.unshift(created);
    return route.fulfill({ status: 201, json: {
      config: created, validation: { ok: true, latency_ms: 10, probe_mode: "quick",
        generated_request: false, usage: { input_tokens: 0, output_tokens: 0, total_tokens: 0 } }
    } });
  });
  let probed = false;
  await page.route("**/api/v1/platform/model-api-configs/model-c/probe?*", route => {
    expect(new URL(route.request().url()).searchParams.get("mode")).toBe("capability");
    probed = true;
    configs[0] = config("model-c", "zhipu", "智谱 AI", "glm-4.6v");
    return route.fulfill({ json: {
      result: { ok: true, probe_mode: "capability", generated_request: true,
        provider: "zhipu", model: "glm-4.6v", latency_ms: 20,
        credential_check: { ok: true }, model_check: { ok: true },
        capability_check: { ok: true, code: "success" },
        usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } },
      config: configs[0]
    } });
  });
  const bindings: Array<Record<string, unknown>> = [];
  await page.route("**/api/v1/platform/panel-model-bindings?*", route => route.fulfill({ json: { bindings } }));
  await page.route("**/api/v1/platform/panel-model-bindings", route => {
    const body = route.request().postDataJSON();
    expect(body.tenant_id).toBe("school-a");
    expect(body).not.toHaveProperty("deployment_id");
    const binding = { ...body, id: `binding-${body.agent_role}` };
    bindings.push(binding);
    return route.fulfill({ json: { binding } });
  });
  const runs: Array<Record<string, unknown>> = [];
  await page.route(/\/api\/v1\/model-evaluation-runs(?:\?.*)?$/, route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { evaluation_runs: runs, next_cursor: "", has_more: false } });
    const body = route.request().postDataJSON();
    expect(body.tenant_id).toBe("school-a");
    runs.push({ ...body, id: "run-full", status: "draft", candidates: [], created_at: "2026-09-23T00:00:00Z" });
    return route.fulfill({ status: 201, json: { evaluation_run: runs[0] } });
  });
  await page.route("**/api/v1/model-evaluation-runs/run-full/candidates?*", route => {
    const body = route.request().postDataJSON();
    expect(body).not.toHaveProperty("deployment_id");
    const model = configs.find(item => item.id === body.model_config_id)!;
    const candidate = { ...body, id: `candidate-${model.id}`, model_config_id: model.id,
      model_name: model.model_name, provider_key: model.provider_key, model_version: model.model_version,
      metrics: { teacher_acceptance_rate: 0.9, serious_error_rate: 0,
        evidence_validity_rate: 1, stability_rate: 1, average_cost_micros: 10 } };
    (runs[0].candidates as Array<Record<string, unknown>>).push(candidate);
    return route.fulfill({ status: 201, json: { candidate } });
  });
  await page.route("**/api/v1/model-evaluation-runs/run-full/complete?*", route => {
    expect((runs[0].candidates as unknown[]).length).toBe(2);
    runs[0] = { ...runs[0], status: "completed" };
    return route.fulfill({ json: { evaluation_run: runs[0] } });
  });
  await page.route("**/api/v1/model-approvals?*", route => route.fulfill({ json: { model_approvals: [] } }));
  let approvalBody: Record<string, unknown> | undefined;
  await page.route("**/api/v1/model-approvals", route => {
    approvalBody = route.request().postDataJSON();
    return route.fulfill({ status: 201, json: { model_approval: { id: "approval-full" } } });
  });
  const policy = { id: "policy-full", tenant_id: "school-a", display_name: "默认策略",
    mode: "local_only", external_enabled: false, text_export_enabled: false, image_export_enabled: false,
    allowed_deployments: [], allowed_model_config_ids: [] as string[],
    max_cost_micros_per_question: 0, max_cost_micros_per_exam: 0,
    fallback_mode: "manual_only", version: 1, status: "active", updated_at: "2026-09-23T00:00:00Z" };
  await page.route("**/api/v1/model-policy?*", route => {
    if (route.request().method() === "GET") return route.fulfill({ json: { policy } });
    const body = route.request().postDataJSON();
    expect(body).not.toHaveProperty("allowed_deployments");
    policy.allowed_model_config_ids = body.allowed_model_config_ids;
    policy.version += 1;
    return route.fulfill({ json: { policy } });
  });

  await page.goto("/#/platform/model-config", { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "添加模型" }).click();
  await page.getByLabel("供应商").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "智谱" }).click();
  await page.getByLabel("API Key").fill("zhipu-secret-at-least-16");
  await page.getByRole("button", { name: "获取可用模型" }).click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "glm-4.6v" }).click();
  await page.getByRole("button", { name: "检测连接并保存" }).click();
  await page.getByRole("button", { name: "智谱 AI更多操作" }).click();
  await page.getByText("完整能力检测", { exact: true }).click();
  await expect.poll(() => probed).toBe(true);

  await page.getByText("三智能体配置", { exact: true }).click();
  for (const [role, model] of [["主评 A", "deepseek-v4"], ["主评 B", "qwen-vl"], ["仲裁 C", "glm-4.6v"]] as const) {
    await page.getByRole("combobox", { name: `${role}评分模型` }).click();
    await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: model }).click();
    await page.getByRole("textbox", { name: `${role} Prompt 版本` }).fill("prompt-v1");
    await page.getByRole("button", { name: `保存${role}` }).click();
  }
  await expect.poll(() => bindings.length).toBe(3);
  expect(bindings.map(item => item.managed_model_api_config_id)).toEqual(["model-a", "model-b", "model-c"]);

  await page.getByText("治理与评测", { exact: true }).click();
  await page.getByRole("button", { name: "新建评测" }).click();
  await page.getByLabel("批次标识").fill("full-school-eval");
  await page.getByLabel("批次名称").fill("完整学校评测");
  await page.getByLabel("数据集引用").fill("frozen-school-set");
  await page.getByLabel("数据集 SHA-256").fill("a".repeat(64));
  await page.getByText("协议样例（不代表模型效果）", { exact: true }).click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "授权冻结集" }).click();
  await page.getByLabel("授权引用").fill("school-approved-set");
  await page.getByLabel("学科").fill("mathematics");
  await page.getByLabel("年级").fill("senior");
  await page.getByLabel("题型标识").fill("short_answer");
  await page.getByLabel("创建原因").fill("学校模型完整流程评测");
  await page.getByRole("button", { name: "创建批次" }).click();
  await expect(page.getByRole("heading", { name: "完整学校评测" })).toBeVisible();
  for (const model of ["deepseek-v4", "qwen-vl"]) {
    await page.getByRole("button", { name: "添加候选" }).click();
    await page.getByLabel("评测模型").click();
    await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: model }).click();
    await page.getByLabel("提示词版本").fill("prompt-v1");
    await page.getByLabel("Rubric 版本").fill("rubric-v1");
    await page.getByPlaceholder("说明离线运行来源和数据核验情况").fill("学校模型冻结证据");
    await page.getByRole("button", { name: "保存候选" }).click();
    await expect.poll(() => (runs[0].candidates as unknown[]).length).toBe(model === "deepseek-v4" ? 1 : 2);
  }
  await page.getByRole("button", { name: "冻结完成" }).click();
  await page.getByRole("dialog", { name: "冻结完成评测" }).getByPlaceholder("填写可审计的操作原因").fill("两候选评测已复核");
  await page.getByRole("button", { name: "确认冻结" }).click();
  await expect.poll(() => runs[0].status).toBe("completed");

  await page.getByText("模型批准", { exact: true }).click();
  await page.getByRole("button", { name: "新建批准" }).click();
  const approvalDrawer = page.getByRole("dialog", { name: "新建题型级模型批准" });
  await approvalDrawer.getByLabel("授权冻结集评测").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "完整学校评测" }).click();
  await approvalDrawer.getByLabel("评测模型").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click();
  await approvalDrawer.getByLabel("决策引用").fill("school-full-decision");
  await approvalDrawer.getByLabel("批准原因").fill("学校模型评测符合批准条件");
  await page.getByRole("button", { name: "确认批准" }).click();
  await expect.poll(() => approvalBody?.model_config_id).toBe("model-a");
  expect(approvalBody).not.toHaveProperty("deployment_id");

  await page.getByText("使用策略", { exact: true }).click();
  await page.getByLabel("允许外部模型").click();
  await page.getByLabel("允许发送文本").click();
  await page.getByLabel("允许使用的学校模型").click();
  await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content", { hasText: "deepseek-v4" }).click();
  await page.getByLabel("修改原因").fill("学校模型完整流程授权");
  await page.getByRole("button", { name: "保存策略" }).click();
  await expect.poll(() => policy.allowed_model_config_ids).toEqual(["model-a"]);
});

