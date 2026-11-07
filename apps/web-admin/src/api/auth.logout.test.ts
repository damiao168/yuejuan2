import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClientError, apiClient } from "./client";
import { logout } from "./auth";

afterEach(() => vi.restoreAllMocks());

describe("后台退出的失效会话处理", () => {
  it("会话已撤销或缺失时允许完成退出", async () => {
    vi.spyOn(apiClient, "request").mockRejectedValue(new ApiClientError(401, "unauthenticated", "session expired"));
    await expect(logout()).resolves.toEqual({ status: "logged_out" });
  });
  it.each([new Error("offline"), new ApiClientError(500, "request_failed", "failed"), new ApiClientError(403, "forbidden", "denied")])("无法确认撤销时继续向上报告失败：%o", async error => {
    vi.spyOn(apiClient, "request").mockRejectedValue(error);
    await expect(logout()).rejects.toBe(error);
  });
});
