import { afterEach, describe, expect, it, vi } from "vitest";
import { logout } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("学生退出的失效会话处理", () => {
  it("服务端已无会话时完成退出，不阻塞下一位学生登录", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: "unauthenticated" } }), { status: 401 })));
    await expect(logout()).resolves.toBeUndefined();
  });
  it("首次退出回执丢失后，重试得到401可以收尾", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: "unauthenticated" } }), { status: 401 })));
    await expect(logout()).rejects.toThrow("response lost");
    await expect(logout()).resolves.toBeUndefined();
  });
  it("网络不可达不当作已成功退出", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    await expect(logout()).rejects.toThrow("offline");
  });
});
