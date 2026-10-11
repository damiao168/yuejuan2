import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, normalizeBaseUrl } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("API 地址尾斜杠边界", () => {
  it.each([
    ["", ""], [" / ", ""], ["  https://school.example///  ", "https://school.example"],
    ["/proxy/school//", "/proxy/school"], ["//school.example///", "//school.example"],
    ["https://school.example/a//b", "https://school.example/a//b"],
    ["https://school.example" + "/".repeat(50_000), "https://school.example"],
    ["/".repeat(50_000) + "tail", "/".repeat(50_000) + "tail"]
  ])("保留第 %# 组地址语义", (input, expected) => {
    expect(normalizeBaseUrl(input)).toBe(expected);
  });

  it.each(["", "/school-proxy", "https://school.example"])("请求入口保留 %s 地址与 Cookie", async (baseUrl) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), {
      headers: { "Content-Type": "application/json" }
    }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(new ApiClient({ baseUrl: `${baseUrl}///` }).request("/api/v1/auth/me")).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledWith(`${baseUrl}/api/v1/auth/me`, expect.objectContaining({ credentials: "include" }));
  });
});
