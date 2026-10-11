import type { MFAChallengeStartResponse, MFARecoveryCodesResponse, MFAStatusResponse, TOTPEnrollmentResponse } from "@edugrade/sdk";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { mfaAPI, type MFAMethod, type MFAOperation } from "../api/mfa";

export type MFAIntent = "enroll" | MFAOperation;
type Challenge = MFAChallengeStartResponse;
type Flow =
  | { phase: "closed" }
  | { phase: "password"; intent: MFAIntent }
  | { phase: "enrollment"; enrollment: TOTPEnrollmentResponse; expiresAt: number }
  | { phase: "challenge"; challenge: Challenge; expiresAt: number }
  | { phase: "verified"; challenge: Challenge; method: MFAMethod; expiresAt: number }
  | { phase: "recovery"; reason: "enabled" | "rotated"; codes: string[]; expiresAt: number };
export type MFAFlowState = Flow & { busy: boolean; error?: string };
type Outcome = "enabled" | "rotated" | "disabled" | "uncertain" | undefined;

export function validMFAStatus(value: MFAStatusResponse): boolean {
  return !!value && typeof value.available === "boolean" && typeof value.enabled === "boolean" &&
    Number.isInteger(value.recovery_codes_remaining) && value.recovery_codes_remaining >= 0 && value.recovery_codes_remaining <= 10 &&
    (!value.enabled ? value.recovery_codes_remaining === 0 : value.available);
}

export function safeTOTPQRCode(value: string): boolean {
  // 只接受本地生成的 PNG，不请求携带种子密钥的二维码端点。
  return typeof value === "string" && value.length <= 200_000 && /^data:image\/png;base64,iVBORw0KGgo[A-Za-z0-9+/]*={0,2}$/.test(value);
}

function expiresAt(value: string, now: number, ttl: number): number {
  const expiry = Date.parse(value);
  if (!Number.isFinite(expiry) || expiry <= now || expiry > now + ttl + 5000) throw new Error("invalid MFA expiry");
  return expiry;
}

function recoveryCodes(value: MFARecoveryCodesResponse, expected: "enabled" | "rotated"): string[] {
  if (value?.status !== expected || !Array.isArray(value.recovery_codes) || value.recovery_codes.length !== 10 ||
    value.recovery_codes.some((code) => typeof code !== "string" || !/^[a-f0-9]{8}(?:-[a-f0-9]{8}){3}$/.test(code)) ||
    new Set(value.recovery_codes).size !== 10) throw new Error("invalid recovery response");
  return [...value.recovery_codes];
}

export function validMFACode(method: MFAMethod, code: string): boolean {
  return method === "totp" ? /^\d{6}$/.test(code.trim()) : /^[a-f0-9]{32}$/i.test(code.trim().replace(/-/g, ""));
}

// 单次短暂流程，不保留密码或 OTP。修订号和中止机制
// 在过期、导航、锁定或取消对话框后拦截迟到响应。
// 中止请求不会撤销服务端变更；结果不明确的操作不会重放。
export class MFAFlowController {
  private state: MFAFlowState = { phase: "closed", busy: false };
  private readonly listeners = new Set<() => void>();
  private revision = 0;
  private request?: AbortController;

  constructor(private readonly api: typeof mfaAPI = mfaAPI, private readonly now: () => number = Date.now) {}

  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };

  private publish(state: MFAFlowState) { this.state = state; this.listeners.forEach((listener) => listener()); }

  close = () => {
    this.revision++;
    this.request?.abort();
    this.request = undefined;
    this.publish({ phase: "closed", busy: false });
  };

  open(intent: MFAIntent) {
    if (this.state.busy) return;
    this.close();
    this.publish({ phase: "password", intent, busy: false });
  }

  tick = () => {
    if ("expiresAt" in this.state && this.state.expiresAt <= this.now()) {
      const recovery = this.state.phase === "recovery";
      this.close();
      this.publish({ phase: "closed", busy: false, error: recovery ? "恢复码已从此页隐藏。如未保存，请使用验证器重新生成。" : "验证已过期，请重新开始；不会自动执行原操作。" });
    }
  };

  private async run(task: (signal: AbortSignal) => Promise<Outcome>, retry: Flow, ambiguous: string): Promise<Outcome> {
    if (this.state.busy) return;
    const revision = this.revision;
    const request = new AbortController();
    this.request = request;
    this.publish({ ...this.state, busy: true, error: undefined });
    try {
      const outcome = await task(request.signal);
      return revision === this.revision ? outcome : undefined;
    } catch (error) {
      if (revision !== this.revision) return;
      if (error instanceof ApiClientError && error.status < 500) {
        this.publish({ ...retry, busy: false, error: getUserErrorMessage(error, "验证失败，请稍后重试") });
      } else {
        this.publish({ phase: "closed", busy: false, error: ambiguous });
        return "uncertain";
      }
      return undefined;
    } finally {
      if (revision === this.revision) {
        this.request = undefined;
        this.publish({ ...this.state, busy: false });
        this.tick();
      }
    }
  }

  async start(password: string): Promise<Outcome> {
    const state = this.state;
    if (state.phase !== "password" || state.busy || !password) return;
    const revision = this.revision;
    return this.run(async (signal) => {
      if (state.intent === "enroll") {
        const enrollment = await this.api.enroll(password, signal);
        const expiry = expiresAt(enrollment.expires_at, this.now(), 600_000);
        if (!/^[0-9a-f-]{36}$/i.test(enrollment.enrollment_id) || !/^[A-Z2-7]{32}$/.test(enrollment.secret) || !safeTOTPQRCode(enrollment.qr_code_data_url)) throw new Error("invalid enrollment response");
        if (revision === this.revision) this.publish({ phase: "enrollment", enrollment, expiresAt: expiry, busy: true });
      } else {
        const challenge = await this.api.start(state.intent, password, signal);
        const expiry = expiresAt(challenge.expires_at, this.now(), 300_000);
        if (challenge.operation !== state.intent || typeof challenge.challenge_id !== "string" || !/^[A-Za-z0-9_-]{32,128}$/.test(challenge.challenge_id) ||
          !Array.isArray(challenge.methods) || !challenge.methods.includes("totp") || new Set(challenge.methods).size !== challenge.methods.length ||
          challenge.methods.some((method) => method !== "totp" && !(method === "recovery_code" && state.intent === "mfa.disable"))) throw new Error("invalid challenge response");
        if (revision === this.revision) this.publish({ phase: "challenge", challenge, expiresAt: expiry, busy: true });
      }
      return undefined;
    }, state, "无法确认注册或挑战是否创建成功，请刷新状态后重新开始。不会自动重试请求。");
  }

  async confirm(code: string): Promise<Outcome> {
    this.tick();
    const state = this.state;
    if (state.phase !== "enrollment" || state.busy || !validMFACode("totp", code)) return;
    const revision = this.revision;
    return this.run(async (signal) => {
      const codes = recoveryCodes(await this.api.confirm(state.enrollment.enrollment_id, code.trim(), signal), "enabled");
      if (revision === this.revision) this.publish({ phase: "recovery", reason: "enabled", codes, expiresAt: this.now() + 600_000, busy: true });
      return "enabled";
    }, state, "启用结果不确定，原请求不会重放。请刷新状态；若已启用但未收到恢复码，请使用验证器重新生成。");
  }

  async verify(method: MFAMethod, code: string): Promise<Outcome> {
    this.tick();
    const state = this.state;
    if (state.phase !== "challenge" || state.busy || !state.challenge.methods.includes(method) || !validMFACode(method, code)) return;
    const revision = this.revision;
    return this.run(async (signal) => {
      const result = await this.api.verify(state.challenge.challenge_id, method, code.trim(), signal);
      const expiry = expiresAt(result.expires_at, this.now(), 300_000);
      if (result.status !== "verified" || result.operation !== state.challenge.operation || expiry > state.expiresAt) throw new Error("invalid verification response");
      if (revision === this.revision) this.publish({ phase: "verified", challenge: state.challenge, method, expiresAt: expiry, busy: true });
      // 此处不调用 disable 或 rotate，执行操作需要用户再次明确点击。
      return undefined;
    }, state, "验证结果不确定，验证码或恢复码可能已经使用。原操作尚未由此页执行，请重新开始并使用新的码。");
  }

  async execute(): Promise<Outcome> {
    this.tick();
    const state = this.state;
    if (state.phase !== "verified" || state.busy) return;
    const revision = this.revision;
    return this.run(async (signal) => {
      if (state.challenge.operation === "mfa.disable") {
        const result = await this.api.disable(state.challenge.challenge_id, signal);
        if (result.status !== "disabled") throw new Error("invalid disable response");
        if (revision === this.revision) this.publish({ phase: "closed", busy: true });
        return "disabled";
      }
      const codes = recoveryCodes(await this.api.rotate(state.challenge.challenge_id, signal), "rotated");
      if (revision === this.revision) this.publish({ phase: "recovery", reason: "rotated", codes, expiresAt: this.now() + 600_000, busy: true });
      return "rotated";
    }, { phase: "closed" }, "操作结果不确定，请刷新状态，不要重放原请求。若恢复码已更新但未收到，请用验证器重新生成；若已关闭，请重新登录。");
  }
}
