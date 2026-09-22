import { afterEach, expect, it, vi } from "vitest";
import { listPanelModelRoleBindings, savePanelModelRoleBinding } from "./modelApiConfig";

afterEach(() => vi.unstubAllGlobals());

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
    status: "active"
  });
  expect(new URL(fetch.mock.calls[1][0], "http://local").pathname).toBe("/api/v1/platform/panel-model-bindings");
  expect(fetch.mock.calls[1][1].method).toBe("PUT");
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({
    tenant_id: "school-1", education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps",
    agent_role: "arbiter", managed_model_api_config_id: "model-c", prompt_version: "arbiter-v1", strength_rank: 2,
    status: "active"
  });
  expect(JSON.parse(fetch.mock.calls[1][1].body)).not.toHaveProperty("is_default");
});
