import { useRef, useState, type SubmitEvent } from "react";
import { friendlyError } from "../../errors";

/** 初始密码会话先改密再进入成绩页；字段只留在当前表单内存，不存入浏览器草稿。 */
export function FirstPasswordChange({ onChangePassword, onLogout }: Readonly<{
  onChangePassword: (input: { current_password: string; new_password: string }) => Promise<void>;
  onLogout: () => void;
}>) {
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef(false);
  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending.current) return;
    if (!current || !password || !confirmation) { setError("请填写当前初始密码、新密码和确认密码。"); return; }
    if (Array.from(password).length < 8 || !password.trim() || new TextEncoder().encode(password).byteLength > 1024) { setError("新密码至少8个字符，且不能超过1024字节或全部为空格。"); return; }
    if (password === current) { setError("新密码不能与初始密码相同。"); return; }
    if (password !== confirmation) { setError("两次输入的密码不一致。"); return; }
    pending.current = true; setBusy(true); setError("");
    try { await onChangePassword({ current_password: current, new_password: password }); }
    catch (cause) { setError(friendlyError(cause)); }
    finally { pending.current = false; setBusy(false); }
  };
  return <main className="login-screen"><section className="login-panel" aria-labelledby="first-password-title">
    <h1 id="first-password-title">首次登录请修改密码</h1>
    <p>请设置只有你本人知道的新密码。修改后需要重新登录。</p>
    <form onSubmit={event => void submit(event)}>
      <label>当前初始密码<input required type="password" autoComplete="current-password" disabled={busy} value={current} onChange={event => setCurrent(event.target.value)} /></label>
      <label>新密码<input required type="password" autoComplete="new-password" disabled={busy} value={password} onChange={event => setPassword(event.target.value)} /></label>
      <label>确认新密码<input required type="password" autoComplete="new-password" disabled={busy} value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label>
      <p className="muted">新密码至少8个字符，且与初始密码不同。</p>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <button className="primary-button" disabled={busy} type="submit">{busy ? "正在修改密码…" : "修改密码并重新登录"}</button>
      <button className="secondary-button" disabled={busy} type="button" onClick={onLogout}>退出登录</button>
    </form>
  </section></main>;
}
