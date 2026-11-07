import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Form, Input, Spin } from "antd";
import type { FormInstance } from "antd";
import { LockKeyhole, LogIn, RefreshCw, ScanLine, School, ShieldCheck } from "lucide-react";
import { isWithinUtf8ByteLimit, LOGIN_FIELD_LIMITS } from "../auth/loginSecurity";
import { getCurrentUser, pollWechatLogin, startWechatLogin, type LoginResponse, type WechatLoginChallengeResponse } from "../api/auth";
import { ApiClientError } from "../api/client";

export interface LoginFormValues {
  tenant_code?: string;
  tenant_hint?: string;
  identifier: string;
  password: string;
  remember_device: boolean;
  public_device: boolean;
}

type LoginMethod = "password" | "wechat";
type WechatFormValues = Pick<LoginFormValues, "tenant_code" | "tenant_hint" | "remember_device" | "public_device">;

function byteLimitRule(limit: number, message: string) {
  return {
    validator: (_: unknown, value?: string) => (!value || isWithinUtf8ByteLimit(value, limit)
      ? Promise.resolve()
      : Promise.reject(new Error(message)))
  };
}

function wechatErrorMessage(error: unknown) {
  if (error instanceof ApiClientError && error.code === "wechat_login_unavailable") return "学校暂未开通微信登录，请使用账号密码登录。";
  if (error instanceof ApiClientError && error.code === "wechat_challenge_invalid") return "二维码已失效，请刷新后重新扫码。";
  return "暂时无法连接微信登录服务，请稍后重试。";
}

function wechatStatusMessage(code?: string) {
  if (code === "wechat_account_unbound") return "该微信尚未绑定学校账号，请联系学校管理员绑定后再试。";
  if (code === "wechat_authorization_failed") return "微信授权未完成，请刷新二维码后重试。";
  return "微信登录未完成，请刷新二维码或使用账号密码登录。";
}

export function LoginPage({ onLogin, onWechatAuthenticate, loading = false, error }: Readonly<{
  onLogin: (values: LoginFormValues) => void | Promise<void>;
  onWechatAuthenticate: (authenticate: () => Promise<LoginResponse | undefined>) => Promise<void>;
  loading?: boolean;
  error?: string;
}>) {
  const tenantHint = tenantHintFromLocation();
  const [method, setMethod] = useState<LoginMethod>("password");
  const [form] = Form.useForm<LoginFormValues>();
  const [wechatForm] = Form.useForm<WechatFormValues>();
  const [challenge, setChallenge] = useState<WechatLoginChallengeResponse>();
  const [wechatLoading, setWechatLoading] = useState(false);
  const [wechatError, setWechatError] = useState<string>();
  const pollGeneration = useRef(0);
  const challengeGeneration = useRef(0);

  useEffect(() => () => { challengeGeneration.current += 1; }, []);

  const changeMethod = (next: LoginMethod) => {
    if (next === method) return;
    // 生成二维码只冻结本次策略；切换方式后，旧生成回执不能再带回已放弃的策略和二维码。
    challengeGeneration.current += 1;
    pollGeneration.current += 1;
    setChallenge(undefined);
    setWechatLoading(false);
    setMethod(next);
    // 学校专属入口已确定范围；首次二维码沿用普通会话策略，后续改设备须显式重建挑战。
    if (next === "wechat" && tenantHint) {
      void beginWechatLogin({ tenant_hint: tenantHint, remember_device: false, public_device: false });
      wechatForm.setFieldsValue({ remember_device: false, public_device: false });
    }
  };

  useEffect(() => {
    if (method !== "wechat" || !challenge) return;
    // 切换登录方式或刷新二维码后，旧轮询结果不能继续完成当前登录流程。
    const generation = ++pollGeneration.current;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        await onWechatAuthenticate(async () => {
          if (controller.signal.aborted || generation !== pollGeneration.current) return;
          // 发出的认证请求要完整等待响应；中止 fetch 不能撤回服务端的 Cookie 写入，反而会提前释放认证队列。
          const response = await pollWechatLogin({ challenge_id: challenge.challenge_id, poll_token: challenge.poll_token });
          if (controller.signal.aborted || generation !== pollGeneration.current) return;
          if (response.status === "authenticated" && response.user) {
            setWechatLoading(true);
            return { expires_at: response.expires_at, user: response.user };
          }
          if (response.status === "failed") {
            setChallenge(undefined);
            setWechatError(wechatStatusMessage(response.error_code));
            return;
          }
          if (response.status === "expired") {
            setChallenge(undefined);
            setWechatError("二维码已过期，请刷新后重新扫码。");
            return;
          }
          // 二维码已消费仍可能已有 Cookie 会话；先读取当前身份，恢复成功的登录结果。
          if (response.status === "consumed") {
            try {
              const recovered = await getCurrentUser();
              // consumed 后的身份恢复还有第二次等待；切换方式后这一回执也必须废弃。
              if (controller.signal.aborted || generation !== pollGeneration.current) return;
              return { expires_at: response.expires_at, user: recovered.user };
            } catch {
              if (controller.signal.aborted || generation !== pollGeneration.current) return;
              setChallenge(undefined);
              setWechatError("二维码已使用，请刷新后重新扫码。");
            }
            return;
          }
          timer = setTimeout(poll, 1800);
          return undefined;
        });
      } catch (pollError) {
        if (controller.signal.aborted || generation !== pollGeneration.current) return;
        setChallenge(undefined);
        setWechatError(wechatErrorMessage(pollError));
      }
    };
    timer = setTimeout(poll, 900);
    return () => {
      controller.abort();
      if (timer) clearTimeout(timer);
      pollGeneration.current += 1;
    };
  }, [challenge, method, onWechatAuthenticate]);

  const beginWechatLogin = async (values: WechatFormValues) => {
    const generation = ++challengeGeneration.current;
    setWechatLoading(true);
    setWechatError(undefined);
    setChallenge(undefined);
    try {
      const response = await startWechatLogin(values);
      if (generation !== challengeGeneration.current) return;
      if (!response.qr_code_data_url.startsWith("data:image/png;base64,")) throw new Error("invalid QR response");
      setChallenge(response);
    } catch (startError) {
      if (generation === challengeGeneration.current) setWechatError(wechatErrorMessage(startError));
    } finally {
      if (generation === challengeGeneration.current) setWechatLoading(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-panel">
        <div className="login-brand">
          <div className="brand-mark large">E</div>
          <div><h1>EduGrade Enterprise</h1><p>智能阅卷与学情诊断平台</p></div>
        </div>
        <div className="login-method-switch" role="tablist" aria-label="登录方式">
          <button type="button" role="tab" aria-selected={method === "password"} className={method === "password" ? "is-active" : ""} disabled={loading} onClick={() => changeMethod("password")}><LockKeyhole size={16} />账号密码</button>
          <button type="button" role="tab" aria-selected={method === "wechat"} className={method === "wechat" ? "is-active" : ""} disabled={loading} onClick={() => changeMethod("wechat")}><ScanLine size={16} />微信扫码</button>
        </div>

        {method === "password" ? (
          <div className="login-method-panel" role="tabpanel">
            {error ? <Alert type="error" showIcon message="登录失败" description={error} /> : null}
            <Form key="password" form={form} clearOnDestroy layout="vertical" onFinish={onLogin} className="login-form" initialValues={{ remember_device: false, public_device: false, tenant_hint: tenantHint }}>
              <TenantField tenantHint={tenantHint} />
              {/* 所有身份共用 identifier 协议；服务端核实账号后决定身份，不能由登录页选择权限。 */}
              <Form.Item label="账号" name="identifier" rules={[{ required: true, message: "请输入账号" }, byteLimitRule(LOGIN_FIELD_LIMITS.identifier, "账号过长")]}>
                <Input placeholder="学号、用户名、教职工号或手机号" autoComplete="username" maxLength={LOGIN_FIELD_LIMITS.identifier} />
              </Form.Item>
              <Form.Item label="密码" name="password" rules={[{ required: true, message: "请输入密码" }, byteLimitRule(LOGIN_FIELD_LIMITS.password, "密码过长")]}>
                <Input.Password prefix={<LockKeyhole size={16} />} placeholder="请输入密码" autoComplete="current-password" />
              </Form.Item>
              <DeviceOptions form={form} />
              <Button type="primary" htmlType="submit" block icon={<LogIn size={17} />} loading={loading}>登录</Button>
              <p className="login-recovery-help">忘记密码？请联系学校管理员；平台账号请联系运维人员。</p>
            </Form>
          </div>
        ) : (
          <div className="login-method-panel login-wechat-panel" role="tabpanel">
            {/* 挑战创建时会冻结会话策略；等待期间禁用策略输入，避免显示值和二维码策略不一致。 */}
            <Form key="wechat" form={wechatForm} clearOnDestroy layout="vertical" onFinish={beginWechatLogin} disabled={wechatLoading} className="login-form login-wechat-form" initialValues={{ remember_device: false, public_device: false, tenant_hint: tenantHint }}>
              <TenantField tenantHint={tenantHint} />
              {!challenge ? <DeviceOptions form={wechatForm} /> : null}
              {wechatError ? <Alert type="warning" showIcon message="微信登录未完成" description={wechatError} /> : null}
              {challenge ? (
                <div className="wechat-qr-stage" aria-live="polite">
                  <img src={challenge.qr_code_data_url} width={224} height={224} alt="微信授权登录二维码" />
                  <strong>使用微信扫一扫</strong>
                  <span>在手机上确认授权后，此页面将自动登录</span>
                  <Button icon={<RefreshCw size={16} />} onClick={() => void beginWechatLogin(wechatForm.getFieldsValue())}>刷新二维码</Button>
                  <Button onClick={() => {
                    challengeGeneration.current += 1;
                    setChallenge(undefined);
                    setWechatError(undefined);
                  }}>更改登录选项</Button>
                </div>
              ) : <Button type="primary" htmlType="submit" block icon={<ScanLine size={17} />} loading={wechatLoading}>生成微信登录二维码</Button>}
              {wechatLoading && challenge ? <Spin size="small" /> : null}
              {challenge ? <p className="wechat-security-note"><ShieldCheck size={15} />二维码 5 分钟内有效，仅供本次登录。</p> : null}
            </Form>
          </div>
        )}
      </section>
    </main>
  );
}

function TenantField({ tenantHint }: { tenantHint?: string }) {
  return tenantHint ? <><div className="login-tenant-context"><School size={16} /><span>学校入口：{tenantHint}</span></div><Form.Item name="tenant_hint" hidden><Input /></Form.Item></> : (
    <Form.Item label="学校代码" name="tenant_code" rules={[{ required: true, message: "请输入学校代码" }, byteLimitRule(LOGIN_FIELD_LIMITS.tenant_code, "代码过长")]}>
      <Input prefix={<School size={16} />} placeholder="请输入学校或平台代码（由管理员提供）" autoComplete="organization" maxLength={LOGIN_FIELD_LIMITS.tenant_code} />
    </Form.Item>
  );
}

function DeviceOptions({ form }: Readonly<{ form: FormInstance }>) {
  return <>
    <Form.Item name="remember_device" hidden><Input /></Form.Item>
    <Form.Item name="public_device" hidden><Input /></Form.Item>
    <Form.Item noStyle shouldUpdate>{({ getFieldValue }) => {
      const publicDevice = Boolean(getFieldValue("public_device"));
      return <>
        <div className="login-options" role="group" aria-label="登录选项">
          {publicDevice ? <span className="login-public-mode"><ShieldCheck size={15} />公共电脑模式</span> : <Checkbox checked={Boolean(getFieldValue("remember_device"))} onChange={event => {
            form.setFieldsValue({ remember_device: event.target.checked, public_device: false });
          }}>保持登录</Checkbox>}
          <Button type="link" className="login-device-switch" aria-pressed={publicDevice} onClick={() => {
            // 公共模式必须清除持久登录；切回时也不暗中恢复先前的勾选，两字段不能同时为真。
            form.setFieldsValue({ remember_device: false, public_device: !publicDevice });
          }}>{publicDevice ? "切回个人电脑" : "公共电脑登录"}</Button>
        </div>
        {publicDevice ? <Alert className="login-public-device-alert" type="warning" showIcon message="公共电脑模式已启用，离开前请退出登录。" /> : null}
      </>;
    }}</Form.Item>
  </>;
}

function tenantHintFromLocation(): string | undefined {
  if (typeof window === "undefined") return undefined;
  const queryHint = new URLSearchParams(window.location.search).get("tenant")?.trim();
  const pathHint = /^\/school\/([^/]+)\/?$/.exec(window.location.pathname)?.[1];
  let decodedPathHint = "";
  try { decodedPathHint = pathHint ? decodeURIComponent(pathHint) : ""; } catch { return undefined; }
  const candidate = queryHint || decodedPathHint;
  return candidate && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(candidate) ? candidate : undefined;
}
