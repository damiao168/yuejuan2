import { afterEach, describe, expect, it, vi } from "vitest";
import { pollWechatLogin, startWechatLogin } from "./auth";

afterEach(() => vi.unstubAllGlobals());

describe("WeChat browser login requests", () => {
  it("starts and polls one-time challenges with cookies and CSRF protection", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ challenge_id: "challenge", poll_token: "poll", qr_code_data_url: "data:image/png;base64,AA==", expires_at: "2026-09-20T00:00:00Z" }), { status: 201 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: "pending", expires_at: "2026-09-20T00:00:00Z" }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    const challenge = await startWechatLogin({ tenant_code: "demo", remember_device: true });
    const controller = new AbortController();
    await pollWechatLogin({ challenge_id: challenge.challenge_id, poll_token: challenge.poll_token }, controller.signal);

    expect(fetch.mock.calls.map((call) => call[0])).toEqual(["/api/v1/auth/wechat/challenges", "/api/v1/auth/wechat/session"]);
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({ tenant_code: "demo", remember_device: true });
    expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual({ challenge_id: "challenge", poll_token: "poll" });
    for (const [, options] of fetch.mock.calls) {
      expect(options.credentials).toBe("include");
      expect(options.headers.get("X-EduGrade-CSRF")).toBe("1");
      expect(options.headers.has("Idempotency-Key")).toBe(false);
    }
    expect(fetch.mock.calls[1]![1].signal).toBe(controller.signal);
  });
});
