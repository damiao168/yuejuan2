import { useEffect, useState } from "react";
import { Alert, Button, Form, Input, Result, Spin } from "antd";
import { KeyRound } from "lucide-react";
import { completeRecovery, verifyRecovery, type RecoveryPreview } from "../api/auth";
import { getUserErrorMessage } from "../api/client";
import { validateNewPassword } from "../auth/loginSecurity";

interface RecoveryFormValues {
  password: string;
  confirmPassword: string;
}

export function RecoveryPage({ onComplete }: { onComplete: (tenantCode?: string) => void }) {
  const [token] = useState(() => new URLSearchParams(window.location.search).get("token")?.trim() ?? "");
  const [preview, setPreview] = useState<RecoveryPreview>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [complete, setComplete] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    if (!token) {
      setError("恢复链接无效或已过期，请联系学校管理员重新生成。");
      setLoading(false);
      return () => { active = false; };
    }
    // 保留本次重置所需令牌后清理地址栏查询串，后续渲染继续使用组件状态中的令牌。
    window.history.replaceState({}, "", "/recover");
    void verifyRecovery(token).then((response) => {
      if (active) setPreview(response.recovery);
    }).catch((reason) => {
      if (active) setError(getUserErrorMessage(reason, "恢复链接无效或已过期，请联系学校管理员重新生成。"));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [token]);

  const submit = async (values: RecoveryFormValues) => {
    setSaving(true);
    setError(undefined);
    try {
      await completeRecovery({ token, password: values.password });
      setComplete(true);
    } catch (reason) {
      setError(getUserErrorMessage(reason, "密码重置失败，请检查链接后重试。"));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <main className="login-screen"><section className="login-panel"><Spin tip="正在验证恢复链接" /></section></main>;
  if (complete) return <main className="login-screen"><section className="login-panel"><Result status="success" title="密码已重置" subTitle="所有原有设备均已退出，请使用新密码重新登录。" extra={<Button type="primary" onClick={() => onComplete(preview?.tenant_code)}>前往登录</Button>} /></section></main>;

  return (
    <main className="login-screen">
      <section className="login-panel">
        <div className="login-brand"><div className="brand-mark large">E</div><div><h1>恢复账号</h1><p>{preview ? `${preview.display_name} · ${preview.tenant_code}` : "EduGrade Enterprise"}</p></div></div>
        {error ? <Alert type="error" showIcon message="无法恢复账号" description={error} /> : null}
        {preview ? (
          <Form<RecoveryFormValues> layout="vertical" onFinish={(values) => void submit(values)}>
            {preview.phone_masked ? <Alert type="info" showIcon message={`账号手机号：${preview.phone_masked}`} /> : null}
            <Form.Item name="password" label="设置新密码" extra="建议使用一句只有你知道的长短语，至少 15 个字符。" rules={[{ required: true, message: "请设置新密码" }, { validator: validateNewPassword }]}>
              <Input.Password prefix={<KeyRound size={16} />} autoComplete="new-password" />
            </Form.Item>
            <Form.Item name="confirmPassword" label="确认新密码" dependencies={["password"]} rules={[{ required: true, message: "请再次输入新密码" }, ({ getFieldValue }) => ({ validator: (_, value) => !value || value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致")) })]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Button type="primary" htmlType="submit" block loading={saving}>重置密码并退出全部设备</Button>
          </Form>
        ) : <Button block onClick={() => onComplete()}>返回登录</Button>}
      </section>
    </main>
  );
}
