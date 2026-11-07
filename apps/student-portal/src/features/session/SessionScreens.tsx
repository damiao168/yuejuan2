import { useEffect } from "react";
import type { AuthUser } from "../../api";
import { leaveForUnifiedLogin, unifiedLoginURL } from "../../loginEntry";

export function LoginRedirect({ notice }: Readonly<{ notice?: string }>) {
  const target = unifiedLoginURL();
  useEffect(() => {
    // 登录只由统一入口处理；门户先结束旧请求和回执保护，再卸载到登录页，草稿继续留在同标签会话。
    leaveForUnifiedLogin(target);
  }, [target]);
  return (
    <main className="portal-loading">
      {notice ? <p><output aria-live="polite">{notice}</output></p> : null}
      <p><output aria-live="polite">正在前往登录…</output></p>
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
