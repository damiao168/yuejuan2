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

export function LoginPage({ onLogin, onWechatLogin, loading = false, error }: {
  onLogin: (values: LoginFormValues) => void | Promise<void>;
  onWechatLogin: (response: LoginResponse) => void | Promise<void>;
  loading?: boolean;
  error?: string;
}) {
  const tenantHint = tenantHintFromLocation();
  const [method, setMethod] = useState<LoginMethod>("password");
  const [form] = Form.useForm<LoginFormValues>();
  const [wechatForm] = Form.useForm<WechatFormValues>();
  const [challenge, setChallenge] = useState<WechatLoginChallengeResponse>();
  const [wechatLoading, setWechatLoading] = useState(false);
  const [wechatError, setWechatError] = useState<string>();
  const pollGeneration = useRef(0);

  useEffect(() => {
    if (method !== "wechat" || !challenge) return;
    const generation = ++pollGeneration.current;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const response = await pollWechatLogin({ challenge_id: challenge.challenge_id, poll_token: challenge.poll_token }, controller.signal);
        if (generation !== pollGeneration.current) return;
        if (response.status === "authenticated" && response.user) {
          setWechatLoading(true);
          await onWechatLogin({ expires_at: response.expires_at, user: response.user });
          return;
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
        if (response.status === "consumed") {
          try {
            const recovered = await getCurrentUser();
            await onWechatLogin({ expires_at: response.expires_at, user: recovered.user });
          } catch {
            setChallenge(undefined);
            setWechatError("二维码已使用，请刷新后重新扫码。");
          }
          return;
        }
        timer = setTimeout(poll, 1800);
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
  }, [challenge, method, onWechatLogin]);

  const beginWechatLogin = async (values: WechatFormValues) => {
    setWechatLoading(true);
    setWechatError(undefined);
    setChallenge(undefined);
    try {
      const response = await startWechatLogin(values);
      if (!response.qr_code_data_url.startsWith("data:image/png;base64,")) throw new Error("invalid QR response");
      setChallenge(response);
    } catch (startError) {
      setWechatError(wechatErrorMessage(startError));
    } finally {
      setWechatLoading(false);
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
          <button type="button" role="tab" aria-selected={method === "password"} className={method === "password" ? "is-active" : ""} onClick={() => setMethod("password")}><LockKeyhole size={16} />账号登录</button>
          <button type="button" role="tab" aria-selected={method === "wechat"} className={method === "wechat" ? "is-active" : ""} onClick={() => setMethod("wechat")}><ScanLine size={16} />微信扫码</button>
        </div>

        {method === "password" ? (
          <div className="login-method-panel" role="tabpanel">
            {error ? <Alert type="error" showIcon message="登录失败" description={error} /> : null}
            <Form form={form} layout="vertical" onFinish={onLogin} className="login-form" initialValues={{ remember_device: false, public_device: false, tenant_hint: tenantHint }}>
              <TenantField tenantHint={tenantHint} />
              <Form.Item label="手机号 / 教职工号" name="identifier" rules={[{ required: true, message: "请输入手机号、教职工号或原登录账号" }, byteLimitRule(LOGIN_FIELD_LIMITS.identifier, "登录标识过长")]}>
                <Input placeholder="手机号、教职工号或原登录账号" autoComplete="username" maxLength={LOGIN_FIELD_LIMITS.identifier} />
              </Form.Item>
              <Form.Item label="密码" name="password" rules={[{ required: true, message: "请输入密码" }, byteLimitRule(LOGIN_FIELD_LIMITS.password, "密码过长")]}>
                <Input.Password prefix={<LockKeyhole size={16} />} placeholder="请输入密码" autoComplete="current-password" />
              </Form.Item>
              <DeviceOptions form={form} />
              <Button type="primary" htmlType="submit" block icon={<LogIn size={17} />} loading={loading}>登录</Button>
              <p className="login-recovery-help">忘记密码？请联系学校管理员核对身份并生成一次性恢复链接。</p>
            </Form>
          </div>
        ) : (
          <div className="login-method-panel login-wechat-panel" role="tabpanel">
            <Form form={wechatForm} layout="vertical" onFinish={beginWechatLogin} className="login-form login-wechat-form" initialValues={{ remember_device: false, public_device: false, tenant_hint: tenantHint }}>
              <TenantField tenantHint={tenantHint} />
              {!challenge ? <DeviceOptions form={wechatForm} /> : null}
              {wechatError ? <Alert type="warning" showIcon message="微信登录未完成" description={wechatError} /> : null}
              {challenge ? (
                <div className="wechat-qr-stage" aria-live="polite">
                  <img src={challenge.qr_code_data_url} width={224} height={224} alt="微信授权登录二维码" />
                  <strong>使用微信扫一扫</strong>
                  <span>在手机上确认授权后，此页面将自动登录</span>
                  <Button icon={<RefreshCw size={16} />} onClick={() => void beginWechatLogin(wechatForm.getFieldsValue())}>刷新二维码</Button>
                </div>
              ) : <Button type="primary" htmlType="submit" block icon={<ScanLine size={17} />} loading={wechatLoading}>生成微信登录二维码</Button>}
              {wechatLoading && challenge ? <Spin size="small" /> : null}
              <p className="wechat-security-note"><ShieldCheck size={15} />二维码 5 分钟内有效，仅用于本次登录，不会读取微信好友或聊天信息。</p>
            </Form>
          </div>
        )}
      </section>
    </main>
  );
}

function TenantField({ tenantHint }: { tenantHint?: string }) {
  return tenantHint ? <><div className="login-tenant-context"><School size={16} /><span>学校入口：{tenantHint}</span></div><Form.Item name="tenant_hint" hidden><Input /></Form.Item></> : (
    <Form.Item label="学校代码" name="tenant_code" rules={[{ required: true, message: "请输入学校代码" }, byteLimitRule(LOGIN_FIELD_LIMITS.tenant_code, "学校代码过长")]}>
      <Input prefix={<School size={16} />} placeholder="请输入学校代码（由管理员提供）" autoComplete="organization" maxLength={LOGIN_FIELD_LIMITS.tenant_code} />
    </Form.Item>
  );
}

function DeviceOptions({ form }: { form: FormInstance }) {
  return <>
    <div className="login-options">
      <Form.Item name="remember_device" valuePropName="checked" noStyle><Checkbox onChange={(event) => { if (event.target.checked) form.setFieldValue("public_device", false); }}>这是我的常用电脑，30 天内减少验证</Checkbox></Form.Item>
      <Form.Item name="public_device" valuePropName="checked" noStyle><Checkbox onChange={(event) => { if (event.target.checked) form.setFieldValue("remember_device", false); }}>这是学校公共电脑</Checkbox></Form.Item>
    </div>
    <Form.Item noStyle shouldUpdate={(previous, current) => previous.public_device !== current.public_device}>
      {({ getFieldValue }) => getFieldValue("public_device") ? <Alert className="login-public-device-alert" type="warning" showIcon message="公共电脑会话最长 4 小时；页面会持续提示，离开前请点退出。" /> : null}
    </Form.Item>
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
