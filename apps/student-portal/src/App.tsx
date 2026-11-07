// 学生端会话和导航壳；身份恢复、提交回执与离开保护继续由同一入口协调。
import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeBeforeUnload } from "@edugrade/sdk";
import { changePassword, currentUser, logout, type AuthUser } from "./api";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import { clearStudentAppealDrafts, hasVolatileAppealDraft, studentDraftOwner } from "./appealDrafts";
import { StudentSessionContext } from "./studentSession";
import { goExam, goHome, normalizeStudentLocation, readStudentPage, studentLocation, STUDENT_NAVIGATION_EVENT } from "./navigation";
import { AccessDenied, LoginRedirect } from "./features/session/SessionScreens";
import { ExamList } from "./features/results/ExamList";
import { ResultDetail } from "./features/results/ResultDetail";
import { hasPendingLogout, rememberPendingLogout } from "./pendingLogout";
import { FirstPasswordChange } from "./features/session/FirstPasswordChange";

export default function App() {
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loadingUser, setLoadingUser] = useState(true);
  const [page, setPage] = useState(readStudentPage);
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [exitingSession, setExitingSession] = useState(false);
  const [logoutError, setLogoutError] = useState(false);
  const logoutErrorRef = useRef(false);
  const exitingSessionRef = useRef(false);
  const sessionRestoreRef = useRef(0);
  const acceptedLocationRef = useRef(studentLocation());
  const draftOwnerRef = useRef("");
  const [loginNotice, setLoginNotice] = useState("");
  const setSubmissionBusy = useCallback((value: boolean) => { submittingRef.current = value; setSubmitting(value); }, []);
  const isSubmissionBusy = useCallback(() => submittingRef.current || exitingSessionRef.current, []);
  const acceptUser = useCallback((value: AuthUser) => {
    // 登录和有效恢复共用入口；接受账户后作废其他恢复，也在这里结束 loading，避免自身 finally 已过期。
    sessionRestoreRef.current += 1;
    setLoadingUser(false);
    const owner = studentDraftOwner(value);
    clearStudentAppealDrafts(owner);
    draftOwnerRef.current = owner;
    setLoginNotice("");
    setUser(value);
  }, []);
  const reauthenticate = useCallback(() => {
    // 迟到的登录失效可发生在复核 POST 等待回执时；同步拦住卸载，保留成功确认或失败后的草稿。
    // 这里只阻止界面切换，不撤销已经发出的申请；回执结束后才能重新登录。
    if (submittingRef.current || exitingSessionRef.current) return;
    sessionRestoreRef.current += 1;
    setLoginNotice("请重新登录。未提交的复核草稿会在同一账号登录后恢复。");
    setUser(null);
  }, []);
  const exitSession = useCallback(() => {
    if (submittingRef.current || (exitingSessionRef.current && !logoutErrorRef.current)) return;
    // 先退出再提交也必须拦住：ref 关闭同批事件里的旧表单，界面隔离等待 logout 的整个过程。
    // 退出完成前不显示登录表单，避免新登录又被尚未返回的旧 logout 清掉。
    exitingSessionRef.current = true;
    setExitingSession(true);
    setLogoutError(false);
    logoutErrorRef.current = false;
    setLoadingUser(false);
    sessionRestoreRef.current += 1;
    clearStudentAppealDrafts();
    draftOwnerRef.current = "";
    setLoginNotice("");
    rememberPendingLogout(true);
    void logout().then(() => {
      rememberPendingLogout(false);
      setUser(null);
      exitingSessionRef.current = false;
      setExitingSession(false);
    }).catch(() => {
      // Cookie 撤销未确认时保持页面隔离，并提供可重试的明确状态。
      setUser(null);
      setLogoutError(true);
      logoutErrorRef.current = true;
    });
  }, []);

  const restore = useCallback(async () => {
    const requestID = ++sessionRestoreRef.current;
    setLoadingUser(true);
    try {
      const response = await currentUser();
      if (requestID !== sessionRestoreRef.current) return;
      acceptUser(response.user);
    } catch {
      if (requestID === sessionRestoreRef.current) setUser(null);
    } finally {
      if (requestID === sessionRestoreRef.current) setLoadingUser(false);
    }
  }, [acceptUser]);

  const changeInitialPassword = async (input: { current_password: string; new_password: string }) => {
    const generation = sessionRestoreRef.current;
    setSubmissionBusy(true);
    try {
      await changePassword(input);
      if (generation !== sessionRestoreRef.current) return;
      // 改密已撤销服务端全部会话；不再用旧Cookie读取成绩，也不保留初始密码会话的草稿。
      sessionRestoreRef.current += 1;
      clearStudentAppealDrafts(); draftOwnerRef.current = "";
      rememberPendingLogout(false);
      setUser(null); setLoginNotice("密码已更新，请使用新密码重新登录。");
    } finally { setSubmissionBusy(false); }
  };

  useEffect(() => {
    if (hasPendingLogout()) exitSession();
    else void restore();
    // StrictMode 会清理后再恢复；卸载、退出或重登后的旧响应不能重建旧会话或覆盖新登录。
    return () => { sessionRestoreRef.current += 1; };
  }, [restore, exitSession]);
  useEffect(() => {
    normalizeStudentLocation();
    // 提交中阻止按钮和浏览器前进后退卸载表单；等待服务端回执，避免把成功结果误当失败。
    const onChange = () => {
      if (submittingRef.current) { window.history.replaceState(window.history.state, "", acceptedLocationRef.current); return; }
      normalizeStudentLocation();
      acceptedLocationRef.current = studentLocation();
      setPage(readStudentPage());
    };
    // 已写入会话存储的草稿可恢复；只保护等待回执和当前身份仅留在内存的理由或圈选。
    const unsubscribeBeforeUnload = subscribeBeforeUnload(() => submittingRef.current || hasVolatileAppealDraft(draftOwnerRef.current));
    window.addEventListener("popstate", onChange);
    window.addEventListener("hashchange", onChange);
    window.addEventListener(STUDENT_NAVIGATION_EVENT, onChange);
    return () => {
      window.removeEventListener("popstate", onChange);
      window.removeEventListener("hashchange", onChange);
      window.removeEventListener(STUDENT_NAVIGATION_EVENT, onChange);
      unsubscribeBeforeUnload();
    };
  }, []);

  if (loadingUser) {
    return <main className="portal-loading">正在确认登录状态…</main>;
  }
  if (exitingSession) {
    return <main className="portal-loading"><output aria-live="polite">{logoutError ? "退出尚未完成，服务端会话可能仍有效。请连接网络后重试。" : "正在退出登录…"}</output>{logoutError ? <button type="button" onClick={exitSession}>重试退出登录</button> : null}</main>;
  }
  if (!user) {
    return <LoginRedirect notice={loginNotice} />;
  }
  if (user.must_change_password) {
    return <FirstPasswordChange key={`${user.tenant_id}:${user.id}`} onChangePassword={changeInitialPassword} onLogout={exitSession} />;
  }
  // 学生端同时校验角色和成绩读取权限；登录成功不代表可以查看成绩。
const eligible = user.roles.includes("student") && user.permissions.includes("student:grade:read");
  if (!eligible) {
    return <AccessDenied user={user} onLogout={exitSession} />;
  }
  return (
    <StudentSessionContext value={{ draftOwner: studentDraftOwner(user), submitting, isSubmitting: isSubmissionBusy, setSubmitting: setSubmissionBusy, reauthenticate }}><div className="portal-shell">
      <header className="portal-header">
        <button className="brand" type="button" onClick={goHome} disabled={submitting} aria-label="回到我的考试">
          <span className="brand-mark">E</span>
          <span><strong>EduGrade</strong><small>学生端</small></span>
        </button>
        <div className="account-area">
          <button type="button" className="text-button" aria-pressed={readingSize === "large"} onClick={() => setReadingSize((size) => size === "large" ? "standard" : "large")}>{readingSize === "large" ? "标准字号" : "大字阅读"}</button>
          <span>{user.display_name || user.username}</span>
          <button type="button" className="text-button" onClick={exitSession} disabled={submitting}>退出登录</button>
        </div>
      </header>
      <main className="portal-content">
        {submitting ? <p className="submission-notice"><output aria-live="polite">正在提交复核申请，请等待回执后再切换题目或离开。</output></p> : null}
        {page.kind === "home" ? <ExamList userName={user.display_name || user.username} onOpen={goExam} /> : <ResultDetail key={page.examID} examID={page.examID} onBack={goHome} />}
      </main>
    </div></StudentSessionContext>
  );
}

// 保留原公开入口，既有调用方与回归可逐步迁移到具体功能模块。
export { QuestionAppealForm } from "./features/appeals/QuestionAppealForm";
export { AppealWindow, AppealStatus, appealReleaseState } from "./features/appeals/AppealStatus";
export { questionTypeLabel } from "./formatting";
