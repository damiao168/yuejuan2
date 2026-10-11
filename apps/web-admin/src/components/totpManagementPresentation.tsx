import { Alert, Button, Checkbox, Input, Radio, Space } from "antd";
import { Copy, Download, Smartphone } from "lucide-react";
import type { MFAStatusResponse } from "@edugrade/sdk";
import type { MFAMethod } from "../api/mfa";
import { validMFACode, type MFAFlowState, type MFAIntent } from "../auth/mfaFlow";

const intentLabels: Record<MFAIntent, string> = {
  enroll: "设置身份验证器",
  "mfa.disable": "关闭身份验证器",
  "mfa.recovery.rotate": "重新生成恢复码"
};

export function totpFlowIntent(flow: MFAFlowState): MFAIntent {
  if (flow.phase === "password") return flow.intent;
  if (flow.phase === "challenge" || flow.phase === "verified") return flow.challenge.operation;
  return "enroll";
}

export function canSubmitTOTPFlow(flow: MFAFlowState, password: string, method: MFAMethod, code: string): boolean {
  switch (flow.phase) {
    case "password": return !!password;
    case "enrollment": return validMFACode("totp", code);
    case "challenge": return validMFACode(method, code);
    case "verified": return true;
    default: return false;
  }
}

export function totpSubmitLabel(flow: MFAFlowState): string {
  if (flow.phase === "password") return "验证密码并继续";
  if (flow.phase === "enrollment") return "验证并启用";
  if (flow.phase === "verified") {
    return flow.challenge.operation === "mfa.disable" ? "确认关闭并退出全部设备" : "生成新恢复码并作废旧码";
  }
  return "验证身份";
}

export function totpStatusLabel(status: MFAStatusResponse | undefined): string {
  if (!status) return "正在读取状态";
  if (!status.available) return "暂未开放";
  return status.enabled ? "已设置身份验证器" : "尚未设置";
}

export function totpModalTitle(flow: MFAFlowState): string {
  if (flow.phase === "recovery") {
    return flow.reason === "rotated" ? "恢复码已重新生成，请保存" : "验证器已启用，请保存恢复码";
  }
  return intentLabels[totpFlowIntent(flow)];
}

export function totpDeadlineLabel(flow: MFAFlowState, clock: number): string {
  const remaining = "expiresAt" in flow ? Math.max(0, Math.ceil((flow.expiresAt - clock) / 1000)) : 0;
  return `${Math.floor(remaining / 60)} 分 ${remaining % 60} 秒`;
}

export function TOTPStatusRow({ status, canManage, onOpen }: Readonly<{
  status: MFAStatusResponse | undefined;
  canManage: boolean;
  onOpen: (intent: MFAIntent) => void;
}>) {
  return <div className="totp-status-row">
    <Smartphone size={22} aria-hidden="true" />
    <div><strong>{totpStatusLabel(status)}</strong>
      <p>{status?.enabled ? `剩余 ${status.recovery_codes_remaining} 条恢复码 · 如未保存，可验证后重新生成` : "设置时请准备个人手机上的 TOTP 验证器，并安全保存恢复码。"}</p></div>
    {status?.available && <Space wrap>{status.enabled ? <>
      <Button disabled={!canManage} onClick={() => onOpen("mfa.recovery.rotate")}>重新生成恢复码</Button>
      <Button danger disabled={!canManage} onClick={() => onOpen("mfa.disable")}>关闭验证器／手机丢失</Button>
    </> : <Button type="primary" disabled={!canManage} onClick={() => onOpen("enroll")}>设置验证器</Button>}</Space>}
  </div>;
}

interface FlowContentProps {
  flow: MFAFlowState;
  accountLabel: string;
  deadline: string;
  password: string;
  code: string;
  method: MFAMethod;
  saved: boolean;
  onPassword: (password: string) => void;
  onCode: (code: string) => void;
  onMethod: (method: MFAMethod) => void;
  onSaved: (saved: boolean) => void;
  onCopy: () => void;
  onDownload: () => void;
  onClose: () => void;
}

// 这里只显示控制器给出的当前阶段；切换阶段、清理敏感输入和执行操作仍由父组件负责。
export function TOTPFlowContent(props: Readonly<FlowContentProps>) {
  const { flow } = props;
  switch (flow.phase) {
    case "password": return <PasswordStep {...props} flow={flow} />;
    case "enrollment": return <EnrollmentStep {...props} flow={flow} />;
    case "challenge": return <ChallengeStep {...props} flow={flow} />;
    case "verified": return <VerifiedStep {...props} flow={flow} />;
    case "recovery": return <RecoveryStep {...props} flow={flow} />;
    default: return null;
  }
}

function PasswordStep({ flow, password, onPassword }: Readonly<FlowContentProps & { flow: Extract<MFAFlowState, { phase: "password" }> }>) {
  return <>
    <p>{flow.intent === "enroll" ? "此功能仍在试点。设置后请保留验证器；恢复码仅显示一次，不可用它登录或重置密码。" : "先输入当前密码，再验证动态码。验证成功后还需要你确认操作，不会自动执行。"}</p>
    <label htmlFor="totp-current-password">当前密码</label>
    <Input.Password id="totp-current-password" name="password" autoComplete="current-password" maxLength={1024} value={password} disabled={flow.busy} onChange={(event) => onPassword(event.target.value)} />
  </>;
}

function EnrollmentStep({ flow, accountLabel, deadline, code, onCode }: Readonly<FlowContentProps & { flow: Extract<MFAFlowState, { phase: "enrollment" }> }>) {
  return <>
    <p>在验证器中选择“添加账号”，扫描二维码；无法扫描时选择手动输入密钥。请勿截图、分享或将二维码上传到第三方网站。</p>
    <div className="totp-provisioning"><img width={216} height={216} src={flow.enrollment.qr_code_data_url} alt="身份验证器设置二维码，包含私密密钥" />
      <div><strong>手动添加</strong><p>账号：{accountLabel}</p><p>类型：基于时间 · SHA-1 · 6 位 · 30 秒</p><code className="totp-secret">{flow.enrollment.secret}</code></div></div>
    <label htmlFor="totp-enrollment-code">输入验证器当前的 6 位动态码</label>
    <Input id="totp-enrollment-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} disabled={flow.busy} onChange={(event) => onCode(event.target.value)} />
    <p className="totp-help">如果动态码即将刷新，请等待新的码。同一码成功验证后不能再用；如持续失败，请核对手机时间。设置有效期剩余 {deadline}。</p>
  </>;
}

function ChallengeStep({ flow, deadline, method, code, onMethod, onCode }: Readonly<FlowContentProps & { flow: Extract<MFAFlowState, { phase: "challenge" }> }>) {
  return <>
    <p>正在验证：{intentLabels[flow.challenge.operation]}。此验证只适用于这一次操作，不会将电脑标记为免验证设备。</p>
    {flow.challenge.methods.includes("recovery_code") && <Radio.Group value={method} disabled={flow.busy} onChange={(event) => { onMethod(event.target.value as MFAMethod); onCode(""); }}>
      <Radio.Button value="totp">验证器动态码</Radio.Button><Radio.Button value="recovery_code">手机丢失，使用恢复码</Radio.Button>
    </Radio.Group>}
    <label htmlFor="totp-challenge-code">{method === "totp" ? "6 位动态码" : "一条未使用的恢复码"}</label>
    <Input id="totp-challenge-code" inputMode={method === "totp" ? "numeric" : "text"} autoComplete={method === "totp" ? "one-time-code" : "off"} maxLength={method === "totp" ? 6 : 35} value={code} disabled={flow.busy} onChange={(event) => onCode(event.target.value)} />
    <p className="totp-help">{method === "totp" ? "请使用新的动态码。同一码成功后不能再用，输入失败不会自动新建挑战。" : "恢复码在验证成功时即被消耗，即使随后取消关闭操作，也不能再次使用。它仅能验证关闭，不能重新生成恢复码。"} 验证有效期剩余 {deadline}。</p>
  </>;
}

function VerifiedStep({ flow, deadline }: Readonly<FlowContentProps & { flow: Extract<MFAFlowState, { phase: "verified" }> }>) {
  return <>
    <Alert type="success" showIcon message="身份验证成功，尚未执行操作" />
    <p>{flow.challenge.operation === "mfa.disable" ? "确认关闭后，原验证器、全部恢复码及本次验证将失效，所有登录设备（包括当前设备）都会退出。" : "确认后旧恢复码将全部作废，新恢复码仅显示一次。请准备好安全的保存位置。"}</p>
    {flow.method === "recovery_code" && <p>本次使用的恢复码已消耗。关闭后请重新登录，再设置新的验证器。</p>}
    <p className="totp-help">本次确认有效期剩余 {deadline}，取消不会自动执行。</p>
  </>;
}

function RecoveryStep({ flow, deadline, saved, onSaved, onCopy, onDownload, onClose }: Readonly<FlowContentProps & { flow: Extract<MFAFlowState, { phase: "recovery" }> }>) {
  return <>
    <Alert type="warning" showIcon message="这批恢复码只显示一次，每条仅能使用一次。关闭、离开页面或超时后不会再次显示。" />
    <p>保存到你能独立访问的安全位置，例如受保护的个人密码管理器或离线纸张。不要与验证器只放在同一部可能丢失的手机中，也不要留在公共电脑、共享文件夹或聊天记录中。</p>
    <ol className="totp-recovery-codes" aria-label="一次性恢复码">{flow.codes.map((item) => <li key={item}><code>{item}</code></li>)}</ol>
    <Space wrap><Button icon={<Copy size={16} />} onClick={onCopy}>复制恢复码</Button><Button icon={<Download size={16} />} onClick={onDownload}>下载明文文件</Button></Space>
    <p className="totp-help">恢复码不能用于登录、修改密码或验证其他业务。此页将在 {deadline} 后隐藏；验证码验证成功时验证器已经启用，无需等待勾选才生效。</p>
    <Checkbox checked={saved} onChange={(event) => onSaved(event.target.checked)}>我已将恢复码保存到安全位置</Checkbox>
    <Button type="primary" disabled={!saved || flow.busy} onClick={onClose}>完成并隐藏恢复码</Button>
  </>;
}

export function TOTPSubmitActions({ flow, canSubmit, onClose }: Readonly<{
  flow: MFAFlowState;
  canSubmit: boolean;
  onClose: () => void;
}>) {
  if (flow.phase === "recovery") return null;
  const label = totpSubmitLabel(flow);
  return <Space className="totp-dialog-actions">
    <Button onClick={onClose} disabled={flow.busy}>取消</Button>
    <Button type="primary" htmlType="submit" aria-label={label} aria-busy={flow.busy} danger={flow.phase === "verified" && flow.challenge.operation === "mfa.disable"} loading={flow.busy} disabled={!canSubmit || flow.busy}>
      {label}
    </Button>
  </Space>;
}
