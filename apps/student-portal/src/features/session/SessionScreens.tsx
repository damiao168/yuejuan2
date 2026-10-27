// 登录与权限说明只通过回调交还身份；会话接受和旧请求隔离仍由 App 管理。
import { SubmitEvent, useState } from "react";
import { login, type AuthUser } from "../../api";
import { friendlyError } from "../../errors";

export function LoginScreen({ onLoggedIn, notice }: Readonly<{ onLoggedIn: (user: AuthUser) => void; notice?: string }>) {
  const [tenantCode, setTenantCode] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const response = await login({ tenant_code: tenantCode.trim(), username: username.trim(), password });
      onLoggedIn(response.user);
    } catch (reason) {
      setError(friendlyError(reason));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-panel" aria-labelledby="login-title">
        <div className="brand login-brand"><span className="brand-mark">E</span><span><strong>EduGrade</strong><small>学生端</small></span></div>
        <h1 id="login-title">查看已发布成绩</h1>
        <p>使用学校发放的学生账号登录。未发布的成绩不会在此显示。</p>
        {notice ? <p><output aria-live="polite">{notice}</output></p> : null}
        <form onSubmit={submit}>
          <label>学校代码<input required value={tenantCode} onChange={(event) => setTenantCode(event.target.value)} autoComplete="organization" /></label>
          <label>账号<input required value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" /></label>
          <label>密码<input required type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></label>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <button className="primary-button" disabled={submitting} type="submit">{submitting ? "正在登录…" : "登录"}</button>
        </form>
        <p className="muted">学校代码或账号不清楚、忘记密码时，请联系学校管理员获取登录信息或重置密码。</p>
      </section>
    </main>
  );
}

export function AccessDenied({ user, onLogout }: Readonly<{ user: AuthUser; onLogout: () => void }>) {
  const isStudent = user.roles.includes("student");
  return (
    <main className="portal-loading access-denied">
      <h1>{isStudent ? "学生账号尚未开通查分权限" : "此账号不是学生账号"}</h1>
      <p>{isStudent ? "请联系学校管理员核对本账号的成绩读取权限。权限开通后可重新登录查分。" : "学生端只显示本人已发布的成绩。请使用学校发放的学生账号登录。"}</p>
      <p className="muted">当前账号：{user.display_name || user.username}（{user.username}）</p>
      <button className="primary-button" type="button" onClick={onLogout}>切换账号</button>
    </main>
  );
}
