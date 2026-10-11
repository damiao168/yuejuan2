import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Alert, App, Button, Modal, Tag } from "antd";
import { RefreshCw } from "lucide-react";
import type { MFAStatusResponse } from "@edugrade/sdk";
import { getUserErrorMessage } from "../api/client";
import { mfaAPI, type MFAMethod } from "../api/mfa";
import { MFAFlowController, validMFAStatus, type MFAIntent } from "../auth/mfaFlow";
import { canSubmitTOTPFlow, TOTPFlowContent, TOTPStatusRow, TOTPSubmitActions, totpDeadlineLabel, totpModalTitle } from "./totpManagementPresentation";

export function TOTPManagement({ personalSession, accountLabel, refreshKey, onChanged, onLoggedOut }: Readonly<{
  personalSession: boolean;
  accountLabel: string;
  refreshKey: number;
  onChanged: () => void;
  onLoggedOut: () => void;
}>) {
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
    // 刷新或卸载会取消旧请求；已取消的状态响应不能覆盖当前界面。
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
  // biome-ignore lint/correctness/useExhaustiveDependencies: 父组件的刷新修订号有意触发状态重载，不重新挂载保存密钥的流程。
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
    // 提交前请求清空密码和验证码状态；失败后不自动回填敏感输入。
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

  const deadline = totpDeadlineLabel(flow, clock);
  const canManage = personalSession && !!status?.available && !loading && !statusError;
  const canSubmit = canSubmitTOTPFlow(flow, password, method, code);

  return <section className="workspace-section totp-management">
    <div className="section-heading compact totp-heading">
      <div><h2>身份验证器 <Tag>可选试点</Tag></h2><p>验证器生成 6 位动态码，无需短信。目前仅用于本页的验证器管理，尚未用于登录或阅卷操作的强制验证。</p></div>
      <Button aria-label="刷新身份验证器状态" icon={<RefreshCw size={16} />} loading={loading} disabled={flow.busy} onClick={() => void load()} />
    </div>
    {statusError ? <Alert type="error" showIcon message={statusError} action={<Button size="small" onClick={() => void load()}>重试</Button>} /> : <TOTPStatusRow status={status} canManage={canManage} onOpen={open} />}
    {!personalSession && <Alert type="info" showIcon message="公共电脑和服务会话只能查看状态。请在个人设备上登录后设置、关闭或重新生成恢复码。" />}
    {status?.enabled && status.recovery_codes_remaining === 0 && <Alert type="warning" showIcon message="恢复码已用完，请在验证器仍可使用时重新生成并安全保存。" />}
    {flow.phase === "closed" && flow.error && <Alert type="warning" showIcon message={flow.error} />}
    <p className="totp-help">更换验证器：验证并关闭原验证器，重新登录后再设置。若手机和恢复码都丢失，请联系学校管理员记录问题；目前没有自动重置入口，密码恢复链接也不会清除验证器。当前试点不会因此阻断普通登录。</p>

    <Modal open={flow.phase !== "closed"} title={totpModalTitle(flow)}
      className="totp-flow-modal" onCancel={close} footer={null} destroyOnHidden maskClosable={!flow.busy} keyboard={!flow.busy} closable={!flow.busy} width={560}>
      <form className="totp-dialog-body" onSubmit={(event) => { event.preventDefault(); submit(); }}>
        <input type="hidden" name="username" autoComplete="username" value={accountLabel} readOnly />
        {flow.error && <Alert role="alert" type="error" showIcon message={flow.error} />}
        <TOTPFlowContent flow={flow} accountLabel={accountLabel} deadline={deadline} password={password} code={code} method={method} saved={saved}
          onPassword={setPassword} onCode={setCode} onMethod={setMethod} onSaved={setSaved} onCopy={() => void copy()} onDownload={download} onClose={close} />
        <TOTPSubmitActions flow={flow} canSubmit={canSubmit} onClose={close} />
      </form>
    </Modal>
  </section>;
}
