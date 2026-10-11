import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { MFAChallengeStartResponse, TOTPEnrollmentResponse } from "@edugrade/sdk";
import type { MFAFlowState } from "../auth/mfaFlow";
import {
  canSubmitTOTPFlow, TOTPFlowContent, TOTPStatusRow, TOTPSubmitActions,
  totpDeadlineLabel, totpFlowIntent, totpModalTitle, totpStatusLabel, totpSubmitLabel
} from "./totpManagementPresentation";

const challenge: MFAChallengeStartResponse = {
  challenge_id: "synthetic-challenge",
  operation: "mfa.disable",
  methods: ["totp", "recovery_code"],
  expires_at: "2026-10-03T00:05:00Z"
};
const enrollment: TOTPEnrollmentResponse = {
  enrollment_id: "11111111-1111-4111-8111-111111111111",
  secret: "SYNTHETIC-ENROLLMENT-SECRET",
  qr_code_data_url: "data:image/png;base64,iVBORw0KGgo=",
  expires_at: "2026-10-03T00:10:00Z"
};
const recovery: MFAFlowState = {
  phase: "recovery", reason: "enabled", busy: false, expiresAt: 10_000,
  codes: ["00000001-aaaaaaaa-bbbbbbbb-cccccccc", "00000002-aaaaaaaa-bbbbbbbb-cccccccc"]
};

function flowProps(flow: MFAFlowState): Parameters<typeof TOTPFlowContent>[0] {
  return {
    flow, accountLabel: "synthetic-account", deadline: "1 分 2 秒",
    password: "synthetic-password", code: "012345", method: "totp", saved: false,
    onPassword: vi.fn(), onCode: vi.fn(), onMethod: vi.fn(), onSaved: vi.fn(),
    onCopy: vi.fn(), onDownload: vi.fn(), onClose: vi.fn()
  };
}

describe("TOTP flow presentation", () => {
  it("keeps the explicit action title and submit label after identity verification", () => {
    const disabled: MFAFlowState = { phase: "verified", challenge, method: "totp", expiresAt: 10_000, busy: false };
    const rotated: MFAFlowState = { ...disabled, challenge: { ...challenge, operation: "mfa.recovery.rotate", methods: ["totp"] } };
    expect(totpFlowIntent(disabled)).toBe("mfa.disable");
    expect(totpModalTitle(disabled)).toBe("关闭身份验证器");
    expect(totpSubmitLabel(disabled)).toBe("确认关闭并退出全部设备");
    expect(totpModalTitle(rotated)).toBe("重新生成恢复码");
    expect(totpSubmitLabel(rotated)).toBe("生成新恢复码并作废旧码");
    expect(totpModalTitle(recovery)).toBe("验证器已启用，请保存恢复码");
    expect(totpModalTitle({ ...recovery, reason: "rotated" })).toBe("恢复码已重新生成，请保存");
  });

  it("uses only the current phase inputs when deciding whether submission is available", () => {
    const password: MFAFlowState = { phase: "password", intent: "enroll", busy: false };
    const enrolling: MFAFlowState = { phase: "enrollment", enrollment, expiresAt: 10_000, busy: false };
    const verifying: MFAFlowState = { phase: "challenge", challenge, expiresAt: 10_000, busy: false };
    expect(canSubmitTOTPFlow(password, "", "totp", "012345")).toBe(false);
    expect(canSubmitTOTPFlow(password, "synthetic-password", "totp", "")).toBe(true);
    expect(canSubmitTOTPFlow(enrolling, "synthetic-password", "recovery_code", "012345")).toBe(true);
    expect(canSubmitTOTPFlow(enrolling, "synthetic-password", "totp", "12345")).toBe(false);
    expect(canSubmitTOTPFlow(verifying, "", "recovery_code", recovery.codes[0])).toBe(true);
    expect(canSubmitTOTPFlow(verifying, "", "totp", recovery.codes[0])).toBe(false);
    expect(canSubmitTOTPFlow({ phase: "verified", challenge, method: "totp", expiresAt: 10_000, busy: false }, "", "totp", "")).toBe(true);
    expect(canSubmitTOTPFlow(recovery, "synthetic-password", "totp", "012345")).toBe(false);
    expect(canSubmitTOTPFlow({ phase: "closed", busy: false }, "synthetic-password", "totp", "012345")).toBe(false);
  });

  it("rounds the visible deadline upward and never shows a negative countdown", () => {
    expect(totpDeadlineLabel(recovery, 8_999)).toBe("0 分 2 秒");
    expect(totpDeadlineLabel(recovery, 10_001)).toBe("0 分 0 秒");
    expect(totpDeadlineLabel({ phase: "closed", busy: false }, 0)).toBe("0 分 0 秒");
  });

  it("shows provisioning secrets only in the enrollment phase and recovery codes only in recovery", () => {
    const enrolling = renderToStaticMarkup(<TOTPFlowContent {...flowProps({ phase: "enrollment", enrollment, expiresAt: 10_000, busy: false })} />);
    const recovering = renderToStaticMarkup(<TOTPFlowContent {...flowProps(recovery)} />);
    const verified = renderToStaticMarkup(<TOTPFlowContent {...flowProps({ phase: "verified", challenge, method: "totp", expiresAt: 10_000, busy: false })} />);
    const closed = renderToStaticMarkup(<TOTPFlowContent {...flowProps({ phase: "closed", busy: false })} />);
    expect(enrolling).toContain(enrollment.secret);
    expect(enrolling).not.toContain(recovery.codes[0]);
    expect(recovering).toContain(recovery.codes[0]);
    expect(recovering).not.toContain(enrollment.secret);
    expect(recovering).not.toContain("synthetic-password");
    expect(verified).not.toContain(enrollment.secret);
    expect(verified).not.toContain(recovery.codes[0]);
    expect(closed).toBe("");
  });

  it("requires a saved acknowledgement before hiding recovery codes and never renders a submit action in recovery", () => {
    const unsaved = renderToStaticMarkup(<TOTPFlowContent {...flowProps(recovery)} />);
    const saved = renderToStaticMarkup(<TOTPFlowContent {...flowProps(recovery)} saved />);
    expect(unsaved).toMatch(/<button[^>]*disabled[^>]*><span>完成并隐藏恢复码<\/span>/);
    expect(saved).not.toMatch(/<button[^>]*disabled[^>]*><span>完成并隐藏恢复码<\/span>/);
    expect(renderToStaticMarkup(<TOTPSubmitActions flow={recovery} canSubmit onClose={vi.fn()} />)).toBe("");
  });

  it("keeps cancel and submit disabled while a request is busy", () => {
    const flow: MFAFlowState = { phase: "verified", challenge, method: "totp", expiresAt: 10_000, busy: true };
    const markup = renderToStaticMarkup(<TOTPSubmitActions flow={flow} canSubmit onClose={vi.fn()} />);
    expect(markup.match(/disabled=""/g)).toHaveLength(2);
    expect(markup).toContain('aria-busy="true"');
    expect(markup).toContain("确认关闭并退出全部设备");
  });

  it("offers recovery-code verification only when the challenge permits it", () => {
    const allowed = renderToStaticMarkup(<TOTPFlowContent {...flowProps({ phase: "challenge", challenge, expiresAt: 10_000, busy: false })} method="recovery_code" />);
    const totpOnly = renderToStaticMarkup(<TOTPFlowContent {...flowProps({ phase: "challenge", challenge: { ...challenge, operation: "mfa.recovery.rotate", methods: ["totp"] }, expiresAt: 10_000, busy: false })} />);
    expect(allowed).toContain("手机丢失，使用恢复码");
    expect(allowed).toContain("即使随后取消关闭操作，也不能再次使用");
    expect(totpOnly).not.toContain("手机丢失，使用恢复码");
  });

  it("does not perform an operation just by rendering the verified or recovery phase", () => {
    const verified = flowProps({ phase: "verified", challenge, method: "totp", expiresAt: 10_000, busy: false });
    const recovering = flowProps(recovery);
    expect(renderToStaticMarkup(<TOTPFlowContent {...verified} />)).toContain("身份验证成功，尚未执行操作");
    renderToStaticMarkup(<TOTPFlowContent {...recovering} />);
    expect(verified.onClose).not.toHaveBeenCalled();
    expect(recovering.onCopy).not.toHaveBeenCalled();
    expect(recovering.onDownload).not.toHaveBeenCalled();
    expect(recovering.onClose).not.toHaveBeenCalled();
  });
});

describe("TOTP status presentation", () => {
  it("preserves unavailable status precedence and hides management commands", () => {
    expect(totpStatusLabel(undefined)).toBe("正在读取状态");
    expect(totpStatusLabel({ available: false, enabled: false, recovery_codes_remaining: 0 })).toBe("暂未开放");
    expect(totpStatusLabel({ available: true, enabled: false, recovery_codes_remaining: 0 })).toBe("尚未设置");
    expect(totpStatusLabel({ available: true, enabled: true, recovery_codes_remaining: 3 })).toBe("已设置身份验证器");
    const unavailable = renderToStaticMarkup(<TOTPStatusRow status={{ available: false, enabled: false, recovery_codes_remaining: 0 }} canManage={false} onOpen={vi.fn()} />);
    expect(unavailable).not.toContain("<button");
  });

  it("disables management commands for a view-only session", () => {
    const markup = renderToStaticMarkup(<TOTPStatusRow status={{ available: true, enabled: true, recovery_codes_remaining: 3 }} canManage={false} onOpen={vi.fn()} />);
    expect(markup.match(/disabled=""/g)).toHaveLength(2);
    expect(markup).toContain("剩余 3 条恢复码");
  });
});
