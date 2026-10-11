import type { MFAChallengeStartResponse, MFAChallengeVerifyResponse, MFADisableResponse, MFARecoveryCodesResponse, MFAStatusResponse, TOTPEnrollmentResponse } from "@edugrade/sdk";
import { apiClient } from "./client";

export type MFAOperation = MFAChallengeStartResponse["operation"];
export type MFAMethod = MFAChallengeStartResponse["methods"][number];

// 此接口有意不使用查询缓存：注册密钥、验证凭证和恢复码
// 不能保留在共享缓存、存储或幂等回执中。
async function request<T>(path: string, init: RequestInit, signal?: AbortSignal) {
  const transport = new AbortController();
  const abort = () => transport.abort();
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  // 中止请求不等于回滚。流程必须核对状态，不能仅因浏览器
  // 等待超时，就重试包含密钥的变更操作。
  const timeout = setTimeout(abort, 30_000);
  try {
    return await apiClient.request<T>(path, { ...init, cache: "no-store", signal: transport.signal });
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", abort);
  }
}

function post<T>(path: string, body: object, signal?: AbortSignal) {
  return request<T>(path, { method: "POST", body: JSON.stringify(body) }, signal);
}

export const mfaAPI = {
  status: (signal?: AbortSignal) => request<MFAStatusResponse>("/api/v1/auth/mfa", {}, signal),
  enroll: (password: string, signal?: AbortSignal) => post<TOTPEnrollmentResponse>("/api/v1/auth/mfa/totp/enroll", { password }, signal),
  confirm: (enrollmentID: string, code: string, signal?: AbortSignal) => post<MFARecoveryCodesResponse>("/api/v1/auth/mfa/totp/confirm", { enrollment_id: enrollmentID, code }, signal),
  start: (operation: MFAOperation, password: string, signal?: AbortSignal) => post<MFAChallengeStartResponse>("/api/v1/auth/step-up/start", { operation, password }, signal),
  verify: (challengeID: string, method: MFAMethod, code: string, signal?: AbortSignal) => post<MFAChallengeVerifyResponse>("/api/v1/auth/step-up/verify", { challenge_id: challengeID, method, code }, signal),
  disable: (challengeID: string, signal?: AbortSignal) => post<MFADisableResponse>("/api/v1/auth/mfa/totp/disable", { challenge_id: challengeID }, signal),
  rotate: (challengeID: string, signal?: AbortSignal) => post<MFARecoveryCodesResponse>("/api/v1/auth/mfa/recovery-codes/rotate", { challenge_id: challengeID }, signal)
};
