import { afterEach, expect, it, vi } from "vitest";
import { ApiClientError } from "./client";
import { listAvailableManagedModels, listPanelModelRoleBindings, managedModelDiscoveryErrorMessage, probeManagedModelAPIConfig, saveAnswerSheetModelSelection, savePanelModelRoleBinding } from "./modelApiConfig";

afterEach(() => vi.unstubAllGlobals());

it.each(["quick", "text", "structured", "capability"] as const)("探测 %s 保留学校和模式，结构化兼容旧模式调用", async (mode) => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ result: {}, config: {} })));
  vi.stubGlobal("fetch", fetch);
  await probeManagedModelAPIConfig("model-a", "school-1", mode, mode === "structured");
  const requestURL = new URL(fetch.mock.calls[0][0], "http://local");
  expect(requestURL.pathname).toBe("/api/v1/platform/model-api-configs/model-a/probe");
  expect(requestURL.searchParams.get("tenant_id")).toBe("school-1");
  expect(requestURL.searchParams.get("mode")).toBe(mode);
  expect(requestURL.searchParams.has("force")).toBe(mode === "structured");
  expect(fetch.mock.calls[0][1].method).toBe("POST");
});

it.each(["model", "unconfigured"] as const)("答题卡用途 %s 单独携带学校与修订，不更改聊天默认", async mode => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ selection: {} })));
  vi.stubGlobal("fetch", fetch);
  await saveAnswerSheetModelSelection("school-1", { expected_revision: "vision-selection-2", mode,
    model_config_id: mode === "model" ? "vision-config" : undefined });
  expect(new URL(fetch.mock.calls[0][0], "http://local").pathname).toBe("/api/v1/platform/answer-sheet-model-selection");
  expect(fetch.mock.calls[0][1].method).toBe("PUT");
  const body = JSON.parse(fetch.mock.calls[0][1].body);
  expect(body).toEqual({ tenant_id: "school-1", expected_revision: "vision-selection-2", mode,
    ...(mode === "model" ? { model_config_id: "vision-config" } : {}) });
  expect(body).not.toHaveProperty("is_default");
});

it("keeps panel-role API calls scoped to a school and separate from the chat default", async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ bindings: [] })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ binding: { id: "binding-1" } })));
  vi.stubGlobal("fetch", fetch);

  await listPanelModelRoleBindings("school-1", "senior", "mathematics", "structured_steps");
  const listURL = new URL(fetch.mock.calls[0][0], "http://local");
  expect(listURL.pathname).toBe("/api/v1/platform/panel-model-bindings");
  expect(Object.fromEntries(listURL.searchParams)).toEqual({
    tenant_id: "school-1", education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps"
  });

  await savePanelModelRoleBinding({
    tenant_id: "school-1", education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps",
    agent_role: "arbiter", managed_model_api_config_id: "model-c", prompt_version: "arbiter-v1", strength_rank: 2,
    status: "active", expected_revision: "absent"
  });
  expect(new URL(fetch.mock.calls[1][0], "http://local").pathname).toBe("/api/v1/platform/panel-model-bindings");
  expect(fetch.mock.calls[1][1].method).toBe("PUT");
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({
    tenant_id: "school-1", education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps",
    agent_role: "arbiter", managed_model_api_config_id: "model-c", prompt_version: "arbiter-v1", strength_rank: 2,
    status: "active", expected_revision: "absent"
  });
  expect(JSON.parse(fetch.mock.calls[1][1].body)).not.toHaveProperty("is_default");
});

it("uses the saved credential reference for same-key discovery and explains an old API", async () => {
  const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ provider: { key: "deepseek" }, models: ["model-a"] })));
  vi.stubGlobal("fetch", fetch);

  await listAvailableManagedModels({ tenant_id: "school-1", provider: "deepseek", credential_source_id: "existing-model" });
  const body = JSON.parse(fetch.mock.calls[0][1].body);
  expect(body).toEqual({ tenant_id: "school-1", provider: "deepseek", credential_source_id: "existing-model" });
  expect(body).not.toHaveProperty("api_key");
  expect(managedModelDiscoveryErrorMessage(new ApiClientError(400, "invalid_request", "invalid json body"), true))
    .toContain("API 服务未识别同 Key 模型请求");
  expect(managedModelDiscoveryErrorMessage(new ApiClientError(400, "invalid_request", "invalid json body"), false))
    .toBe("请检查必填项和填写格式。");
});

it.each([null, []])("空模型列表 %j 统一返回数组，保留手工输入入口", async (models) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ provider: { key: "deepseek" }, models, latency_ms: 12 }))));

  const response = await listAvailableManagedModels({ tenant_id: "school-1", provider: "deepseek", credential_source_id: "existing-model" });

  expect(response.models).toEqual([]);
  expect(response.latency_ms).toBe(12);
});

it.each([{ id: "model-a" }, ["model-a", { id: "model-b" }], undefined])("异常模型列表 %j 在进入表单前拒绝", async (models) => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ provider: { key: "deepseek" }, models }))));

  await expect(listAvailableManagedModels({ tenant_id: "school-1", provider: "deepseek", credential_source_id: "existing-model" }))
    .rejects.toMatchObject({ code: "provider_invalid_response" });
});
