import type { AuthUser, LoginResult } from "../types";
import type { DesktopApiClient } from "./client";

export interface LoginPayload {
  tenant_code: string;
  username: string;
  password: string;
}

export async function login(client: DesktopApiClient, payload: LoginPayload) {
  return client.request<LoginResult>("/api/v1/auth/token", {
    method: "POST",
    body: JSON.stringify({ ...payload, client_type: "desktop", device_name: "EduGrade Desktop" })
  });
}

export async function getCurrentUser(client: DesktopApiClient) {
  return client.request<{ user: AuthUser }>("/api/v1/auth/me");
}

export async function logout(client: DesktopApiClient, signal?: AbortSignal) {
  return client.request<{ status: "logged_out" }>("/api/v1/auth/logout", { method: "POST", signal });
}
