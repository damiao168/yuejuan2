// 登录与权限说明只通过回调交还身份；会话接受和旧请求隔离仍由 App 管理。
import { SubmitEvent, useEffect, useRef, useState } from "react";
import { getPublicSchool, login, type AuthUser } from "../../api";
import { friendlyError } from "../../errors";

export function LoginScreen({ onLoggedIn, notice }: Readonly<{ onLoggedIn: (user: AuthUser) => void; notice?: string }>) {
  // 学校分享链接只预填租户定位；账号与学校仍由登录接口核实。
  const [tenantCode, setTenantCode] = useState(() => new URLSearchParams(window.location.search).get("school")?.trim() ?? "");
  const [schoolCode, setSchoolCode] = useState(tenantCode);
  const [schoolName, setSchoolName] = useState("");
  const [switchingSchool, setSwitchingSchool] = useState(!tenantCode);
  const schoolRequest = useRef(0);
  useEffect(() => {
    const request = ++schoolRequest.current;
    setSchoolName("");
    if (schoolCode) void getPublicSchool(schoolCode).then(school => {
      if (request === schoolRequest.current) setSchoolName(school.name);
    }).catch(() => { /* 名称解析失败仍保留原学校代码；不能伪造已核实的学校名，也不阻断原登录。 */ });
    return () => { schoolRequest.current++; };
  }, [schoolCode]);
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
        <p>使用学校发放的学生账号，查看本人已发布成绩。</p>
        {notice ? <p><output aria-live="polite">{notice}</output></p> : null}
        <form onSubmit={submit}>
          {switchingSchool ? <div>
            <label>学校代码<input required value={tenantCode} onChange={(event) => setTenantCode(event.target.value)} autoComplete="organization" /></label>
            {tenantCode.trim() ? <button type="button" disabled={submitting} onClick={() => { setSchoolCode(tenantCode.trim()); setSwitchingSchool(false); setError(""); }}>使用此学校</button> : null}
          </div> : <p>学校：<strong>{schoolName || schoolCode}</strong> <button type="button" disabled={submitting} onClick={() => { setPassword(""); setSwitchingSchool(true); }}>切换学校</button></p>}
          <label>账号<input required value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" /></label>
          <label>密码<input required type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></label>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <button className="primary-button" disabled={submitting} type="submit">{submitting ? "正在登录…" : "登录"}</button>
        </form>
        <p className="muted">学校代码、账号或密码有问题？请联系学校管理员。</p>
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
