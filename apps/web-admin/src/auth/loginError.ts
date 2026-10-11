import { ApiClientError } from "../api/client";

export function loginErrorMessage(error: unknown): string {
  if (!(error instanceof ApiClientError)) return "暂时无法登录，请检查网络连接后重试。";
  if (error.code === "invalid_credentials") return "学校或登录信息不正确。";
  if (error.code === "login_rate_limited") {
    const retryTime = error.retryAfterSeconds ? `在 ${error.retryAfterSeconds} 秒后` : "稍后";
    return `尝试次数过多，请${retryTime}重试。`;
  }
  return "暂时无法登录，请检查网络连接后重试。";
}
