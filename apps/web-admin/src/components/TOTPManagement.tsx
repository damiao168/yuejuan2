import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Alert, App, Button, Checkbox, Input, Modal, Radio, Space, Tag } from "antd";
import { Copy, Download, RefreshCw, Smartphone } from "lucide-react";
import type { MFAStatusResponse } from "@edugrade/sdk";
import { getUserErrorMessage } from "../api/client";
import { mfaAPI, type MFAMethod } from "../api/mfa";
import { MFAFlowController, validMFACode, validMFAStatus, type MFAIntent } from "../auth/mfaFlow";

const intentLabels: Record<MFAIntent, string> = {
  enroll: "设置身份验证器",
  "mfa.disable": "关闭身份验证器",
  "mfa.recovery.rotate": "重新生成恢复码"
};

export function TOTPManagement({ personalSession, accountLabel, refreshKey, onChanged, onLoggedOut }: {
  personalSession: boolean;
  accountLabel: string;
  refreshKey: number;
  onChanged: () => void;
  onLoggedOut: () => void;
}) {
  const { message, modal } = App.useApp();
  const [controller] = useState(() => new MFAFlowController());
  const flow = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const [status, setStatus] = useState<MFAStatusResponse>();
  const [statusError, setStatusError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [method, setMethod] = useState<MFAMethod>("totp");
  const [saved, setSaved] = useState(false);
  const [clock, setClock] = useState(Date.now());
  const statusRequest = useRef<AbortController | undefined>(undefined);
  const mounted = useRef(false);
  const previousPhase = useRef(flow.phase);
  const discardDialog = useRef<{ destroy: () => void } | undefined>(undefined);

  const load = useCallback(async () => {
    statusRequest.current?.abort();
    const request = new AbortController();
    statusRequest.current = request;
    setLoading(true);
    setStatusError(undefined);
    try {
      const response = await mfaAPI.status(request.signal);
      if (!validMFAStatus(response)) throw new Error("invalid MFA status");
      if (!request.signal.aborted && mounted.current) setStatus(response);
    } catch (error) {
      if (!request.signal.aborted && mounted.current) {
        setStatus(undefined);
        setStatusError(getUserErrorMessage(error, "身份验证器状态读取失败"));
      }
    } finally {
      if (!request.signal.aborted && mounted.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      statusRequest.current?.abort();
      discardDialog.current?.destroy();
      controller.close();
    };
  }, [controller]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: The parent refresh revision intentionally triggers a status reload without remounting the secret-saving flow.
  useEffect(() => { void load(); }, [load, refreshKey]);
  useEffect(() => {
    if (!personalSession) controller.close();
  }, [personalSession, controller]);
  useEffect(() => {
    // 页面可能进入往返缓存而不卸载；离开时也要清除密钥界面和确认弹窗。
    const hide = () => {
      discardDialog.current?.destroy(); discardDialog.current = undefined;
      controller.close(); setPassword(""); setCode(""); setSaved(false);
    };
    const restore = () => { controller.tick(); void load(); };
    window.addEventListener("pagehide", hide);
    window.addEventListener("pageshow", restore);
    return () => { window.removeEventListener("pagehide", hide); window.removeEventListener("pageshow", restore); };
  }, [controller, load]);
  useEffect(() => {
    if (previousPhase.current !== flow.phase) {
      setPassword(""); setCode(""); setMethod("totp"); setSaved(false);
      if (flow.phase === "closed") void load();
      previousPhase.current = flow.phase;
    }
    if (flow.phase === "closed") {
      discardDialog.current?.destroy(); discardDialog.current = undefined;
    }
  }, [flow.phase, load]);
  const exposureDeadline = "expiresAt" in flow ? flow.expiresAt : undefined;
  useEffect(() => {
    if (!exposureDeadline) return;
    const interval = window.setInterval(() => { setClock(Date.now()); controller.tick(); }, 1000);
    return () => window.clearInterval(interval);
  }, [exposureDeadline, controller]);

  const close = () => {
    if (flow.busy) return;
    if (flow.phase === "recovery" && !saved) {
      if (discardDialog.current) return;
      discardDialog.current = modal.confirm({
        title: "尚未确认保存恢复码",
        content: "关闭后不能再次查看这批恢复码。仍可使用身份验证器重新生成；请先确认已安全保存，或明确放弃本次保存。",
        okText: "放弃保存并隐藏", okButtonProps: { danger: true }, cancelText: "返回保存",
        afterClose: () => { discardDialog.current = undefined; },
        onOk: () => { controller.close(); setPassword(""); setCode(""); }
      });
      return;
    }
    controller.close(); setPassword(""); setCode("");
  };

  const open = (intent: MFAIntent) => {
    if (!personalSession || !status?.available || loading || statusError) return;
    setClock(Date.now()); setPassword(""); setCode(""); setSaved(false); setMethod("totp");
    controller.open(intent);
  };

  const finish = async (task: Promise<"enabled" | "rotated" | "disabled" | "uncertain" | undefined>) => {
    const outcome = await task;
    if (!mounted.current) return;
    if (outcome === "disabled") {
      controller.close(); setPassword(""); setCode("");
      message.success("身份验证器已关闭，全部设备已退出");
      onLoggedOut();
    } else if (outcome) {
      void load(); onChanged();
    }
  };

  const submit = () => {
    if (flow.busy || !canSubmit) return;
    const currentPassword = password;
    const currentCode = code;
    setPassword(""); setCode("");
    if (flow.phase === "password") void finish(controller.start(currentPassword));
    if (flow.phase === "enrollment") void finish(controller.confirm(currentCode));
    if (flow.phase === "challenge") void finish(controller.verify(method, currentCode));
    if (flow.phase === "verified") void finish(controller.execute());
  };

  const copy = async () => {
    if (flow.phase !== "recovery" || flow.busy) return;
    try {
      await navigator.clipboard.writeText(`阅卷 EduGrade · ${accountLabel}\n身份验证器恢复码（每条仅可使用一次）\n${flow.codes.join("\n")}`);
      if (mounted.current) message.success("已复制，请粘贴到安全位置；复制不等于已保存");
    } catch {
      if (mounted.current) message.warning("无法访问剪贴板，请手动复制或下载恢复码");
    }
  };

  const download = () => {
    if (flow.phase !== "recovery" || flow.busy) return;
    const blob = new Blob([`阅卷 EduGrade · ${accountLabel}\n身份验证器恢复码\n\n每条仅可使用一次，仅用于当前试点的验证器关闭流程，不能用于登录或重置密码。\n请存入安全位置，不要留在公共电脑、聊天或共享文件夹中。重新生成后旧码全部失效。\n\n${flow.codes.join("\n")}\n`], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url; link.download = "edugrade-mfa-recovery-codes.txt";
    document.body.appendChild(link); link.click(); link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    message.info("这是明文文件，请移至安全位置后清理下载目录；下载不等于已安全保存");
  };

  const intent = flow.phase === "password" ? flow.intent : flow.phase === "challenge" || flow.phase === "verified" ? flow.challenge.operation : "enroll";
  const remaining = "expiresAt" in flow ? Math.max(0, Math.ceil((flow.expiresAt - clock) / 1000)) : 0;
  const deadline = `${Math.floor(remaining / 60)} 分 ${remaining % 60} 秒`;
  const canManage = personalSession && !!status?.available && !loading && !statusError;
  const canSubmit = flow.phase === "password" ? !!password : flow.phase === "enrollment" ? validMFACode("totp", code) : flow.phase === "challenge" ? validMFACode(method, code) : flow.phase === "verified";
  const submitLabel = flow.phase === "password" ? "验证密码并继续" : flow.phase === "enrollment" ? "验证并启用" : flow.phase === "verified" ? intent === "mfa.disable" ? "确认关闭并退出全部设备" : "生成新恢复码并作废旧码" : "验证身份";

  return <section className="workspace-section totp-management">
    <div className="section-heading compact totp-heading">
      <div><h2>身份验证器 <Tag>可选试点</Tag></h2><p>验证器生成 6 位动态码，无需短信。目前仅用于本页的验证器管理，尚未用于登录或阅卷操作的强制验证。</p></div>
      <Button aria-label="刷新身份验证器状态" icon={<RefreshCw size={16} />} loading={loading} disabled={flow.busy} onClick={() => void load()} />
    </div>
    {statusError ? <Alert type="error" showIcon message={statusError} action={<Button size="small" onClick={() => void load()}>重试</Button>} /> : <div className="totp-status-row">
      <Smartphone size={22} aria-hidden="true" />
      <div><strong>{!status ? "正在读取状态" : !status.available ? "暂未开放" : status.enabled ? "已设置身份验证器" : "尚未设置"}</strong>
        <p>{status?.enabled ? `剩余 ${status.recovery_codes_remaining} 条恢复码 · 如未保存，可验证后重新生成` : "设置时请准备个人手机上的 TOTP 验证器，并安全保存恢复码。"}</p></div>
      {status?.available && <Space wrap>{status.enabled ? <>
        <Button disabled={!canManage} onClick={() => open("mfa.recovery.rotate")}>重新生成恢复码</Button>
        <Button danger disabled={!canManage} onClick={() => open("mfa.disable")}>关闭验证器／手机丢失</Button>
      </> : <Button type="primary" disabled={!canManage} onClick={() => open("enroll")}>设置验证器</Button>}</Space>}
    </div>}
    {!personalSession && <Alert type="info" showIcon message="公共电脑和服务会话只能查看状态。请在个人设备上登录后设置、关闭或重新生成恢复码。" />}
    {status?.enabled && status.recovery_codes_remaining === 0 && <Alert type="warning" showIcon message="恢复码已用完，请在验证器仍可使用时重新生成并安全保存。" />}
    {flow.phase === "closed" && flow.error && <Alert type="warning" showIcon message={flow.error} />}
    <p className="totp-help">更换验证器：验证并关闭原验证器，重新登录后再设置。若手机和恢复码都丢失，请联系学校管理员记录问题；目前没有自动重置入口，密码恢复链接也不会清除验证器。当前试点不会因此阻断普通登录。</p>

    <Modal open={flow.phase !== "closed"} title={flow.phase === "recovery" ? flow.reason === "rotated" ? "恢复码已重新生成，请保存" : "验证器已启用，请保存恢复码" : intentLabels[intent]}
      className="totp-flow-modal" onCancel={close} footer={null} destroyOnHidden maskClosable={!flow.busy} keyboard={!flow.busy} closable={!flow.busy} width={560}>
      <form className="totp-dialog-body" onSubmit={(event) => { event.preventDefault(); submit(); }}>
        <input type="hidden" name="username" autoComplete="username" value={accountLabel} readOnly />
        {flow.error && <Alert role="alert" type="error" showIcon message={flow.error} />}
        {flow.phase === "password" && <>
          <p>{intent === "enroll" ? "此功能仍在试点。设置后请保留验证器；恢复码仅显示一次，不可用它登录或重置密码。" : "先输入当前密码，再验证动态码。验证成功后还需要你确认操作，不会自动执行。"}</p>
          <label htmlFor="totp-current-password">当前密码</label>
          <Input.Password id="totp-current-password" name="password" autoComplete="current-password" maxLength={1024} value={password} disabled={flow.busy} onChange={(event) => setPassword(event.target.value)} />
        </>}
        {flow.phase === "enrollment" && <>
          <p>在验证器中选择“添加账号”，扫描二维码；无法扫描时选择手动输入密钥。请勿截图、分享或将二维码上传到第三方网站。</p>
          <div className="totp-provisioning"><img width={216} height={216} src={flow.enrollment.qr_code_data_url} alt="身份验证器设置二维码，包含私密密钥" />
            <div><strong>手动添加</strong><p>账号：{accountLabel}</p><p>类型：基于时间 · SHA-1 · 6 位 · 30 秒</p><code className="totp-secret">{flow.enrollment.secret}</code></div></div>
          <label htmlFor="totp-enrollment-code">输入验证器当前的 6 位动态码</label>
          <Input id="totp-enrollment-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} disabled={flow.busy} onChange={(event) => setCode(event.target.value)} />
          <p className="totp-help">如果动态码即将刷新，请等待新的码。同一码成功验证后不能再用；如持续失败，请核对手机时间。设置有效期剩余 {deadline}。</p>
        </>}
        {flow.phase === "challenge" && <>
          <p>正在验证：{intentLabels[flow.challenge.operation]}。此验证只适用于这一次操作，不会将电脑标记为免验证设备。</p>
          {flow.challenge.methods.includes("recovery_code") && <Radio.Group value={method} disabled={flow.busy} onChange={(event) => { setMethod(event.target.value as MFAMethod); setCode(""); }}>
            <Radio.Button value="totp">验证器动态码</Radio.Button><Radio.Button value="recovery_code">手机丢失，使用恢复码</Radio.Button>
          </Radio.Group>}
          <label htmlFor="totp-challenge-code">{method === "totp" ? "6 位动态码" : "一条未使用的恢复码"}</label>
          <Input id="totp-challenge-code" inputMode={method === "totp" ? "numeric" : "text"} autoComplete={method === "totp" ? "one-time-code" : "off"} maxLength={method === "totp" ? 6 : 35} value={code} disabled={flow.busy} onChange={(event) => setCode(event.target.value)} />
          <p className="totp-help">{method === "totp" ? "请使用新的动态码。同一码成功后不能再用，输入失败不会自动新建挑战。" : "恢复码在验证成功时即被消耗，即使随后取消关闭操作，也不能再次使用。它仅能验证关闭，不能重新生成恢复码。"} 验证有效期剩余 {deadline}。</p>
        </>}
        {flow.phase === "verified" && <>
          <Alert type="success" showIcon message="身份验证成功，尚未执行操作" />
          <p>{intent === "mfa.disable" ? "确认关闭后，原验证器、全部恢复码及本次验证将失效，所有登录设备（包括当前设备）都会退出。" : "确认后旧恢复码将全部作废，新恢复码仅显示一次。请准备好安全的保存位置。"}</p>
          {flow.method === "recovery_code" && <p>本次使用的恢复码已消耗。关闭后请重新登录，再设置新的验证器。</p>}
          <p className="totp-help">本次确认有效期剩余 {deadline}，取消不会自动执行。</p>
        </>}
        {flow.phase === "recovery" && <>
          <Alert type="warning" showIcon message="这批恢复码只显示一次，每条仅能使用一次。关闭、离开页面或超时后不会再次显示。" />
          <p>保存到你能独立访问的安全位置，例如受保护的个人密码管理器或离线纸张。不要与验证器只放在同一部可能丢失的手机中，也不要留在公共电脑、共享文件夹或聊天记录中。</p>
          <ol className="totp-recovery-codes" aria-label="一次性恢复码">{flow.codes.map((item) => <li key={item}><code>{item}</code></li>)}</ol>
          <Space wrap><Button icon={<Copy size={16} />} onClick={() => void copy()}>复制恢复码</Button><Button icon={<Download size={16} />} onClick={download}>下载明文文件</Button></Space>
          <p className="totp-help">恢复码不能用于登录、修改密码或验证其他业务。此页将在 {deadline} 后隐藏；验证码验证成功时验证器已经启用，无需等待勾选才生效。</p>
          <Checkbox checked={saved} onChange={(event) => setSaved(event.target.checked)}>我已将恢复码保存到安全位置</Checkbox>
          <Button type="primary" disabled={!saved || flow.busy} onClick={close}>完成并隐藏恢复码</Button>
        </>}
        {flow.phase !== "recovery" && <Space className="totp-dialog-actions">
          <Button onClick={close} disabled={flow.busy}>取消</Button>
          <Button type="primary" htmlType="submit" aria-label={submitLabel} aria-busy={flow.busy} danger={flow.phase === "verified" && intent === "mfa.disable"} loading={flow.busy} disabled={!canSubmit || flow.busy}>
            {submitLabel}
          </Button>
        </Space>}
      </form>
    </Modal>
  </section>;
}
