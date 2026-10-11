import { describe, expect, it } from "vitest";
import { ApiClientError } from "../api/client";
import { loginErrorMessage } from "./loginError";

describe("login error messages", () => {
  it("uses the same credential error for an invalid school or account", () => {
    expect(loginErrorMessage(new ApiClientError(401, "invalid_credentials", "internal")))
      .toBe("学校或登录信息不正确。");
  });

  it.each([
    [undefined, "尝试次数过多，请稍后重试。"],
    [0, "尝试次数过多，请稍后重试。"],
    [45, "尝试次数过多，请在 45 秒后重试。"],
  ])("preserves the retry hint for %s seconds", (retryAfterSeconds, expected) => {
    expect(loginErrorMessage(new ApiClientError(429, "login_rate_limited", "internal", {}, retryAfterSeconds)))
      .toBe(expected);
  });

  it.each([new Error("network"), new ApiClientError(500, "internal_error", "internal"), null])
    ("does not display internal or unexpected error details", (error) => {
      expect(loginErrorMessage(error)).toBe("暂时无法登录，请检查网络连接后重试。");
    });
});
