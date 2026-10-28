import { apiClient } from "./client";
import { buildQueryString } from "./query";

export interface AuditLog {
  id: string;
  tenant_id: string;
  actor_id?: string;
  action: string;
  target_type: string;
  target_id?: string;
  before_value?: Record<string, unknown>;
  after_value?: Record<string, unknown>;
  reason?: string;
  ip_address?: string;
  user_agent?: string;
  request_id?: string;
  created_at: string;
}

export interface AuditLogFilter {
  q?: string;
  action?: string;
  actor_id?: string;
  target_type?: string;
  target_id?: string;
  exam_id?: string;
  ip_address?: string;
  created_from?: string;
  created_to?: string;
  limit?: number;
  cursor?: string;
}

export async function listAuditLogs(filter: AuditLogFilter = {}) {
  return apiClient.request<{ audit_logs: AuditLog[]; next_cursor: string; has_more: boolean }>(`/api/v1/audit-logs${buildQueryString(filter)}`);
}

export async function exportAuditLogs(filter: AuditLogFilter = {}) {
  return apiClient.requestBlob(`/api/v1/audit-logs/export${buildQueryString(filter)}`, { method: "POST" });
}
