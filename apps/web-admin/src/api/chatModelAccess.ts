import { apiClient } from "./client";

export interface ChatModelAccess { revision: string; model_ids: string[] }
export function getChatModelAccess(tenantID: string) {
  return apiClient.request<{ access: ChatModelAccess }>(`/api/v1/platform/chat-model-access?tenant_id=${encodeURIComponent(tenantID)}`);
}
export function saveChatModelAccess(tenantID: string, access: ChatModelAccess, modelIDs: string[]) {
  return apiClient.request<{ access: ChatModelAccess }>("/api/v1/platform/chat-model-access", {
    method: "PUT", body: JSON.stringify({ tenant_id: tenantID, expected_revision: access.revision, model_ids: modelIDs })
  });
}
