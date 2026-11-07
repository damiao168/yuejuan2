import { useRef, useState } from "react";
import { Alert, Button, Form, Input } from "antd";
import { getUserErrorMessage } from "../api/client";
import { newPasswordValidator } from "./loginSecurity";

type PasswordInput = { current_password: string; new_password: string };

/** 初始密码会话只提供改密和退出；业务内容由 AppShell 在挂载前隔离，服务端再检查同一标记。 */
export function FirstPasswordChange({ onChangePassword, onLogout, minimumPasswordLength = 8 }: Readonly<{
  onChangePassword: (input: PasswordInput) => Promise<void>;
  onLogout: () => void;
  minimumPasswordLength?: number;
}>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const submit = async (values: PasswordInput & { confirm_password: string }) => {
    if (pending.current) return;
    pending.current = true; setBusy(true); setError("");
    try {
      await onChangePassword({ current_password: values.current_password, new_password: values.new_password });
    } catch (cause) { setError(getUserErrorMessage(cause, "修改密码失败，请核对当前初始密码后重试。")); }
    finally { pending.current = false; setBusy(false); }
  };
  return <main className="login-screen"><section className="login-panel">
    <div className="login-brand"><div><h1>首次登录请修改密码</h1><p>请设置只有你本人知道的新密码。修改后需要重新登录。</p></div></div>
    {error ? <Alert type="error" showIcon message={error} /> : null}
    <Form layout="vertical" disabled={busy} onFinish={values => void submit(values)}>
      <Form.Item name="current_password" label="当前初始密码" rules={[{ required: true, message: "请输入当前初始密码" }]}><Input.Password autoComplete="current-password" /></Form.Item>
      <Form.Item name="new_password" label="新密码" extra={`至少${minimumPasswordLength}个字符，且与初始密码不同。`} rules={[
        { required: true, message: "请输入新密码" }, { validator: newPasswordValidator(minimumPasswordLength) },
        ({ getFieldValue }) => ({ validator: (_, value) => !value || value !== getFieldValue("current_password") ? Promise.resolve() : Promise.reject(new Error("新密码不能与初始密码相同")) })
      ]}><Input.Password autoComplete="new-password" /></Form.Item>
      <Form.Item name="confirm_password" label="确认新密码" dependencies={["new_password"]} rules={[
        { required: true, message: "请再次输入新密码" },
        ({ getFieldValue }) => ({ validator: (_, value) => !value || value === getFieldValue("new_password") ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致")) })
      ]}><Input.Password autoComplete="new-password" /></Form.Item>
      <Button block type="primary" htmlType="submit" loading={busy}>修改密码并重新登录</Button>
      <Button block disabled={busy} onClick={onLogout}>退出登录</Button>
    </Form>
  </section></main>;
}
