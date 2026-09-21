import { apiClient } from "./client";

export interface AuthUser {
  id: string;
  tenant_id: string;
  tenant_code: string;
  username: string;
  display_name: string;
  status: string;
  roles: string[];
  permissions: string[];
  data_scope: Record<string, unknown>;
  organization_scope?: {
    tenant_wide: boolean;
    school_ids: string[];
    grade_ids: string[];
    class_ids: string[];
  };
  current_session_type?: "standard" | "remembered_device" | "public_device" | "desktop_device" | "service";
}

export interface LoginRequest {
  tenant_code?: string;
  tenant_hint?: string;
  username?: string;
  identifier: string;
  password: string;
  remember_device?: boolean;
  public_device?: boolean;
  device_name?: string;
}

export interface ActivationPreview {
  display_name: string;
  tenant_code: string;
  school_id?: string;
  phone_masked?: string;
  expires_at: string;
}

export type RecoveryPreview = Omit<ActivationPreview, "school_id">;

export interface LoginResponse {
  expires_at: string;
  user: AuthUser;
}

export interface WechatLoginChallengeResponse {
  challenge_id: string;
  poll_token: string;
  qr_code_data_url: string;
  expires_at: string;
}

export type WechatLoginStatusResponse = {
  status: "pending" | "failed" | "expired" | "consumed" | "authenticated";
  error_code?: string;
  expires_at: string;
  user?: AuthUser;
};

export interface DeviceSession {
  id: string;
  session_type: "standard" | "remembered_device" | "public_device" | "desktop_device" | "service";
  device_name: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  current: boolean;
}

export interface SecurityEvent {
  id: string;
  event_type: string;
  risk_level: "low" | "medium" | "high";
  device_summary: string;
  occurred_at: string;
}

export async function login(input: LoginRequest) {
  return apiClient.request<LoginResponse>("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function startWechatLogin(input: {
  tenant_code?: string;
  tenant_hint?: string;
  remember_device?: boolean;
  public_device?: boolean;
}) {
  return apiClient.request<WechatLoginChallengeResponse>("/api/v1/auth/wechat/challenges", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function pollWechatLogin(input: { challenge_id: string; poll_token: string }, signal?: AbortSignal) {
  return apiClient.request<WechatLoginStatusResponse>("/api/v1/auth/wechat/session", {
    method: "POST",
    body: JSON.stringify(input),
    signal
  });
}

export function verifyActivation(token: string) {
  return apiClient.request<{ activation: ActivationPreview }>("/api/v1/auth/activation/verify", {
    method: "POST",
    body: JSON.stringify({ token })
  });
}

export function completeActivation(input: { token: string; password: string }) {
  return apiClient.request<{ status: "activated" }>("/api/v1/auth/activation/complete", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export function verifyRecovery(token: string) {
  return apiClient.request<{ recovery: RecoveryPreview }>("/api/v1/auth/recovery/verify", {
    method: "POST",
    body: JSON.stringify({ token })
  });
}

export function completeRecovery(input: { token: string; password: string }) {
  return apiClient.request<{ status: "password_reset" }>("/api/v1/auth/recovery/complete", {
    method: "POST",
    body: JSON.stringify(input)
  });
}

export async function getCurrentUser() {
  return apiClient.request<{ user: AuthUser }>("/api/v1/auth/me");
}

export async function logout() {
  return apiClient.request<{ status: string }>("/api/v1/auth/logout", {
    method: "POST"
  });
}

export function reauthenticate(password: string) {
  return apiClient.request<{ status: "reauthenticated"; reauthenticated_at: string }>("/api/v1/auth/reauthenticate", {
    method: "POST",
    body: JSON.stringify({ password })
  });
}

export function lockCurrentSession() {
  return apiClient.request<{ status: "locked"; locked_at: string }>("/api/v1/auth/lock", {
    method: "POST",
    keepalive: true
  });
}

export function listSessions() {
  return apiClient.request<{ sessions: DeviceSession[] }>("/api/v1/auth/sessions");
}

export function listSecurityEvents() {
  return apiClient.request<{ events: SecurityEvent[] }>("/api/v1/auth/security-events");
}

export function revokeSession(id: string) {
  return apiClient.request<{ status: string }>(`/api/v1/auth/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE"
  });
}

export function logoutAll() {
  return apiClient.request<{ status: string; revoked_count: number }>("/api/v1/auth/logout-all", {
    method: "POST"
  });
}

export function changePassword(input: { current_password: string; new_password: string }) {
  return apiClient.request<{ status: string; revoked_count: number }>("/api/v1/auth/password", {
    method: "POST",
    body: JSON.stringify(input)
  });
}
