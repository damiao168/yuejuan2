import { describe, expect, it, vi } from "vitest";
import type { MFAChallengeStartResponse, MFARecoveryCodesResponse, TOTPEnrollmentResponse } from "@edugrade/sdk";
import { ApiClientError } from "../api/client";
import { type mfaAPI } from "../api/mfa";
import { MFAFlowController, safeTOTPQRCode, validMFACode, validMFAStatus } from "./mfaFlow";

const baseNow = Date.parse("2026-09-14T00:00:00Z");
const challengeID = "x".repeat(43); // 仅使用合成测试数据，不使用真实的注册凭据。
const enrollment: TOTPEnrollmentResponse = {
  enrollment_id: "11111111-1111-4111-8111-111111111111",
  secret: "A".repeat(32),
  qr_code_data_url: "data:image/png;base64,iVBORw0KGgo=",
  expires_at: new Date(baseNow + 600_000).toISOString()
};
const codes = Array.from({ length: 10 }, (_, index) => `${index.toString(16).padStart(8, "0")}-aaaaaaaa-bbbbbbbb-cccccccc`);
function fixture(operation: MFAChallengeStartResponse["operation"] = "mfa.disable") {
  const challenge: MFAChallengeStartResponse = {
    challenge_id: challengeID, operation, methods: operation === "mfa.disable" ? ["totp", "recovery_code"] : ["totp"],
    expires_at: new Date(baseNow + 300_000).toISOString()
  };
  const api: typeof mfaAPI = {
    status: vi.fn().mockResolvedValue({ available: true, enabled: true, recovery_codes_remaining: 10 }),
    enroll: vi.fn().mockResolvedValue(enrollment),
    confirm: vi.fn().mockResolvedValue({ status: "enabled", recovery_codes: codes }),
    start: vi.fn().mockResolvedValue(challenge),
    verify: vi.fn().mockResolvedValue({ status: "verified", operation, expires_at: challenge.expires_at }),
    disable: vi.fn().mockResolvedValue({ status: "disabled" }),
    rotate: vi.fn().mockResolvedValue({ status: "rotated", recovery_codes: codes })
  };
  let now = baseNow;
  const controller = new MFAFlowController(api, () => now);
  return { api, controller, challenge, advance: (ms: number) => { now += ms; controller.tick(); } };
}

describe("ephemeral TOTP management flow", () => {
  it("discards the seed after confirmation and never retains passwords or OTPs", async () => {
    const { api, controller } = fixture();
    controller.open("enroll"); await controller.start("synthetic-password");
    expect(controller.getSnapshot().phase).toBe("enrollment");
    expect(JSON.stringify(controller.getSnapshot())).not.toContain("synthetic-password");
    expect(await controller.confirm("012345")).toBe("enabled");
    expect(controller.getSnapshot()).toMatchObject({ phase: "recovery", codes });
    expect(JSON.stringify(controller.getSnapshot())).not.toContain(enrollment.secret);
    expect(JSON.stringify(controller.getSnapshot())).not.toContain("012345");
    controller.close();
    expect(controller.getSnapshot()).toEqual({ phase: "closed", busy: false });
    await controller.confirm("012345");
    expect(api.confirm).toHaveBeenCalledTimes(1);
  });

  it("requires an explicit second click after verification, without promoting the session", async () => {
    const { api, controller } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    await controller.execute(); expect(api.disable).not.toHaveBeenCalled();
    await controller.verify("totp", "012345");
    expect(controller.getSnapshot().phase).toBe("verified");
    expect(api.disable).not.toHaveBeenCalled(); expect(api.rotate).not.toHaveBeenCalled();
    expect(await controller.execute()).toBe("disabled");
    await controller.execute(); expect(api.disable).toHaveBeenCalledTimes(1);
    expect(api.disable).toHaveBeenCalledWith(challengeID, expect.any(AbortSignal));
  });

  it("retries a rejected OTP on the same challenge, not by submitting the password again", async () => {
    const { api, controller } = fixture();
    vi.mocked(api.verify).mockRejectedValueOnce(new ApiClientError(401, "mfa_verification_failed", "internal secret"));
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    await controller.verify("totp", "012345");
    expect(controller.getSnapshot()).toMatchObject({ phase: "challenge", busy: false });
    expect(JSON.stringify(controller.getSnapshot())).not.toContain("internal secret");
    await controller.verify("totp", "123456");
    expect(api.start).toHaveBeenCalledTimes(1);
    expect(api.verify).toHaveBeenCalledTimes(2);
  });

  it("permits recovery only for disable, and consumes no command on verification", async () => {
    const { api, controller } = fixture("mfa.recovery.rotate");
    controller.open("mfa.recovery.rotate"); await controller.start("synthetic-password");
    await controller.verify("recovery_code", codes[0]!);
    expect(api.verify).not.toHaveBeenCalled();
    await controller.verify("totp", "012345");
    expect(api.rotate).not.toHaveBeenCalled();
    expect(await controller.execute()).toBe("rotated");
    expect(controller.getSnapshot()).toMatchObject({ phase: "recovery", codes });
  });

  it("never executes after an expired or cancelled proof", async () => {
    const { api, controller, advance } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    await controller.verify("recovery_code", codes[0]!);
    advance(300_000); await controller.execute();
    expect(api.disable).not.toHaveBeenCalled();
    expect(controller.getSnapshot()).toMatchObject({ phase: "closed", error: expect.stringContaining("过期") });
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    controller.close(); await controller.execute(); expect(api.disable).not.toHaveBeenCalled();
  });

  it("expires provisioning secrets and hides displayed recovery codes after ten minutes", async () => {
    const first = fixture(); first.controller.open("enroll"); await first.controller.start("synthetic-password");
    first.advance(600_000);
    expect(JSON.stringify(first.controller.getSnapshot())).not.toContain(enrollment.secret);
    const second = fixture(); second.controller.open("enroll"); await second.controller.start("synthetic-password"); await second.controller.confirm("012345");
    second.advance(600_000);
    expect(second.controller.getSnapshot().phase).toBe("closed");
    expect(JSON.stringify(second.controller.getSnapshot())).not.toContain(codes[0]!);
  });

  it("fences late responses after close, including servers that ignore abort", async () => {
    const { api, controller } = fixture();
    let resolve!: (result: TOTPEnrollmentResponse) => void;
    vi.mocked(api.enroll).mockImplementation((_password, signal) => new Promise((done) => { resolve = done; expect(signal).toBeInstanceOf(AbortSignal); }));
    controller.open("enroll"); const pending = controller.start("synthetic-password");
    const signal = vi.mocked(api.enroll).mock.calls[0]![1]!;
    controller.close(); controller.open("mfa.disable");
    expect(signal.aborted).toBe(true);
    resolve(enrollment); await pending;
    expect(controller.getSnapshot()).toEqual({ phase: "password", intent: "mfa.disable", busy: false });
  });

  it("does not double-submit and never replays a command after an ambiguous network result", async () => {
    const { api, controller } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password"); await controller.verify("totp", "012345");
    vi.mocked(api.disable).mockRejectedValue(new TypeError("network dropped"));
    const pending = controller.execute(); await controller.execute();
    expect(await pending).toBe("uncertain");
    await controller.execute(); expect(api.disable).toHaveBeenCalledTimes(1);
    expect(controller.getSnapshot()).toMatchObject({ phase: "closed", error: expect.stringContaining("不要重放") });
  });

  it("makes lost confirmation and rotation responses recoverable without fetching old codes", async () => {
    const { api, controller } = fixture();
    controller.open("enroll"); await controller.start("synthetic-password");
    vi.mocked(api.confirm).mockRejectedValue(new TypeError("network dropped"));
    expect(await controller.confirm("012345")).toBe("uncertain");
    expect(controller.getSnapshot()).toMatchObject({ phase: "closed", error: expect.stringContaining("重新生成") });
    await controller.confirm("012345"); expect(api.confirm).toHaveBeenCalledTimes(1);
  });

  it("does not replay a lost rotation response or retain the verified proof", async () => {
    const { api, controller } = fixture("mfa.recovery.rotate");
    controller.open("mfa.recovery.rotate"); await controller.start("synthetic-password"); await controller.verify("totp", "012345");
    vi.mocked(api.rotate).mockRejectedValue(new TypeError("network dropped"));
    expect(await controller.execute()).toBe("uncertain");
    expect(controller.getSnapshot()).toMatchObject({ phase: "closed", error: expect.stringContaining("重新生成") });
    await controller.execute(); expect(api.rotate).toHaveBeenCalledTimes(1);
  });

  it("does not reset the challenge or retry automatically when aggregate limits reject verification", async () => {
    const { api, controller } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    vi.mocked(api.verify).mockRejectedValue(new ApiClientError(429, "login_rate_limited", "internal detail", {}, 60));
    await controller.verify("totp", "012345");
    expect(controller.getSnapshot()).toMatchObject({ phase: "challenge", error: expect.stringContaining("稍后") });
    expect(api.start).toHaveBeenCalledTimes(1); expect(api.verify).toHaveBeenCalledTimes(1);
  });

  it("drops a late verification result on expiry, even if the request cannot be cancelled", async () => {
    const { api, controller, challenge, advance } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    let resolve!: (value: Awaited<ReturnType<typeof api.verify>>) => void;
    vi.mocked(api.verify).mockImplementation(() => new Promise((done) => { resolve = done; }));
    const pending = controller.verify("totp", "012345");
    advance(300_000); resolve({ status: "verified", operation: "mfa.disable", expires_at: challenge.expires_at });
    expect(await pending).toBeUndefined(); await controller.execute();
    expect(controller.getSnapshot()).toMatchObject({ phase: "closed" }); expect(api.disable).not.toHaveBeenCalled();
  });

  it("never shows a late secret-bearing execution response after navigation", async () => {
    const { api, controller } = fixture("mfa.recovery.rotate");
    controller.open("mfa.recovery.rotate"); await controller.start("synthetic-password"); await controller.verify("totp", "012345");
    let resolve!: (value: MFARecoveryCodesResponse) => void;
    vi.mocked(api.rotate).mockImplementation(() => new Promise((done) => { resolve = done; }));
    const pending = controller.execute(); controller.close();
    resolve({ status: "rotated", recovery_codes: codes }); expect(await pending).toBeUndefined();
    expect(controller.getSnapshot()).toEqual({ phase: "closed", busy: false });
  });

  it.each(["wrong-operation", "recovery-on-rotate", "extended-expiry"])("fails closed on %s from a malformed challenge response", async (variant) => {
    const { api, controller, challenge } = fixture("mfa.recovery.rotate");
    vi.mocked(api.start).mockResolvedValue({ ...challenge,
      ...(variant === "wrong-operation" ? { operation: "mfa.disable" } : variant === "recovery-on-rotate" ? { methods: ["totp", "recovery_code"] } : { expires_at: new Date(baseNow + 600_000).toISOString() })
    } as MFAChallengeStartResponse);
    controller.open("mfa.recovery.rotate"); expect(await controller.start("synthetic-password")).toBe("uncertain");
    expect(controller.getSnapshot().phase).toBe("closed");
  });

  it("rejects a verification response for another operation or an extended proof lifetime", async () => {
    const { api, controller, challenge } = fixture();
    controller.open("mfa.disable"); await controller.start("synthetic-password");
    vi.mocked(api.verify).mockResolvedValue({ status: "verified", operation: "mfa.recovery.rotate", expires_at: challenge.expires_at });
    expect(await controller.verify("totp", "012345")).toBe("uncertain");
    await controller.execute(); expect(api.disable).not.toHaveBeenCalled();
  });

  it.each(["duplicate", "invalid-code", "wrong-status"])("does not display malformed recovery data: %s", async (variant) => {
    const { api, controller } = fixture(); controller.open("enroll"); await controller.start("synthetic-password");
    vi.mocked(api.confirm).mockResolvedValue({ status: variant === "wrong-status" ? "rotated" : "enabled", recovery_codes: variant === "duplicate" ? Array(10).fill(codes[0]) : variant === "invalid-code" ? [...codes.slice(1), "<script>"] : codes } as MFARecoveryCodesResponse);
    expect(await controller.confirm("012345")).toBe("uncertain");
    expect(controller.getSnapshot().phase).toBe("closed");
  });
});

describe("MFA browser response boundaries", () => {
  it("accepts only a bounded, inline PNG QR, never a remote seed-collecting URL or SVG", () => {
    expect(safeTOTPQRCode(enrollment.qr_code_data_url)).toBe(true);
    for (const value of ["https://qr.example/secret", "data:image/svg+xml;base64,PHN2Zz4=", "javascript:alert(1)", "data:image/png;base64,not-a-png", enrollment.qr_code_data_url + "a".repeat(200_000)]) expect(safeTOTPQRCode(value)).toBe(false);
  });
  it("validates non-enumerating status and does not display impossible recovery counts", () => {
    expect(validMFAStatus({ available: false, enabled: false, recovery_codes_remaining: 0 })).toBe(true);
    expect(validMFAStatus({ available: true, enabled: true, recovery_codes_remaining: 0 })).toBe(true);
    for (const count of [-1, 11, NaN, 0.5]) expect(validMFAStatus({ available: true, enabled: true, recovery_codes_remaining: count })).toBe(false);
    expect(validMFAStatus({ available: false, enabled: true, recovery_codes_remaining: 10 })).toBe(false);
  });
  it("preserves leading zeros and accepts typed, hyphenated or uppercase recovery codes", () => {
    expect(validMFACode("totp", "012345")).toBe(true);
    expect(validMFACode("totp", "12345")).toBe(false);
    expect(validMFACode("recovery_code", codes[0]!.toUpperCase())).toBe(true);
    expect(validMFACode("recovery_code", codes[0]!.replace(/-/g, ""))).toBe(true);
    expect(validMFACode("recovery_code", "012345")).toBe(false);
  });
});
