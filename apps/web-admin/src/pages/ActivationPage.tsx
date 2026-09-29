import { useEffect, useState } from "react";
import { Alert, Button, Form, Input, Result, Spin } from "antd";
import { KeyRound } from "lucide-react";
import { completeActivation, verifyActivation, type ActivationPreview } from "../api/auth";
import { getUserErrorMessage } from "../api/client";
import { validateNewPassword } from "../auth/loginSecurity";

interface ActivationFormValues {
  password: string;
  confirmPassword: string;
}

export function ActivationPage({ onComplete }: { onComplete: (tenantCode?: string) => void }) {
  const [token] = useState(() => new URLSearchParams(window.location.search).get("token")?.trim() ?? "");
  const [preview, setPreview] = useState<ActivationPreview>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [complete, setComplete] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    if (!token) {
      setError("激活链接无效或已过期，请联系学校管理员重新生成。");
      setLoading(false);
      return () => { active = false; };
    }
    // 令牌已保存在本次组件状态中；移除地址栏查询串，减少后续浏览记录和复制链接暴露。
    window.history.replaceState({}, "", "/activate");
    void verifyActivation(token).then((response) => {
      if (active) setPreview(response.activation);
    }).catch((reason) => {
      if (active) setError(getUserErrorMessage(reason, "激活链接无效或已过期，请联系学校管理员重新生成。"));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [token]);

  const submit = async (values: ActivationFormValues) => {
    setSaving(true);
    setError(undefined);
    try {
      await completeActivation({ token, password: values.password });
      setComplete(true);
    } catch (reason) {
      setError(getUserErrorMessage(reason, "账号激活失败，请检查链接后重试。"));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <main className="login-screen"><section className="login-panel"><Spin tip="正在验证邀请" /></section></main>;
  if (complete) return <main className="login-screen"><section className="login-panel"><Result status="success" title="账号已激活" subTitle="请使用手机号、教职工号或原登录账号登录。" extra={<Button type="primary" onClick={() => onComplete(preview?.tenant_code)}>前往登录</Button>} /></section></main>;

  return (
    <main className="login-screen">
      <section className="login-panel">
        <div className="login-brand"><div className="brand-mark large">E</div><div><h1>激活教师账号</h1><p>{preview ? `${preview.display_name} · ${preview.tenant_code}` : "EduGrade Enterprise"}</p></div></div>
        {error ? <Alert type="error" showIcon message="无法完成激活" description={error} /> : null}
        {preview ? (
          <Form<ActivationFormValues> layout="vertical" onFinish={(values) => void submit(values)}>
            {preview.phone_masked ? <Alert type="info" showIcon message={`邀请手机号：${preview.phone_masked}`} /> : null}
            <Form.Item name="password" label="设置密码" extra="建议使用一句只有你知道的长短语，至少 15 个字符。" rules={[{ required: true, message: "请设置密码" }, { validator: validateNewPassword }]}>
              <Input.Password prefix={<KeyRound size={16} />} autoComplete="new-password" />
            </Form.Item>
            <Form.Item name="confirmPassword" label="确认密码" dependencies={["password"]} rules={[{ required: true, message: "请再次输入密码" }, ({ getFieldValue }) => ({ validator: (_, value) => !value || value === getFieldValue("password") ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致")) })]}>
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Button type="primary" htmlType="submit" block loading={saving}>完成激活</Button>
          </Form>
        ) : <Button block onClick={() => onComplete()}>返回登录</Button>}
      </section>
    </main>
  );
}
