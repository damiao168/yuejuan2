import { SubmitEvent, PointerEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { subscribeBeforeUnload } from "@edugrade/sdk";
import {
  createQuestionAppeal,
  currentUser,
  getQuestion,
  getResult,
  listQuestionAnnotations,
  listQuestionAppeals,
  listPublishedExams,
  login,
  logout,
  PortalApiError,
  studentQuestionAnswerImageURL,
  type AuthUser,
  type PublishedExam,
  type StudentQuestionAppeal,
  type StudentQuestionAnnotation,
  type StudentQuestion,
  type SelectedAppealRegion,
  type StudentResult
} from "./api";
import { ScoreHero, displaySubject, statisticsReason } from "./features/results/ResultOverview";
import { AnswerReview } from "./features/questions/AnswerReview";
import { PaperViewer } from "./features/questions/PaperViewer";
import { QuestionCohortScope } from "./features/questions/QuestionCohortScope";
import { SubjectPerformance } from "./features/results/SubjectPerformance";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import { appealDraftKey, clearStudentAppealDrafts, hasVolatileAppealDraft, isAppealDraftStored, readAppealDraft, removeAppealDraft, studentDraftOwner, writeAppealDraft, type AppealDraft } from "./appealDrafts";
import { OldAppealDrafts } from "./features/results/OldAppealDrafts";
import { StudentSessionContext, useStudentSession } from "./studentSession";
import { friendlyError, sessionExpired, studentReleaseChanged } from "./errors";
import { goExam, goHome, normalizeStudentLocation, readStudentPage, studentLocation, STUDENT_NAVIGATION_EVENT } from "./navigation";

export default function App() {
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loadingUser, setLoadingUser] = useState(true);
  const [page, setPage] = useState(readStudentPage);
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [exitingSession, setExitingSession] = useState(false);
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
    if (submittingRef.current || exitingSessionRef.current) return;
    // 先退出再提交也必须拦住：ref 关闭同批事件里的旧表单，界面隔离等待 logout 的整个过程。
    // 退出完成前不显示登录表单，避免新登录又被尚未返回的旧 logout 清掉。
    exitingSessionRef.current = true;
    setExitingSession(true);
    sessionRestoreRef.current += 1;
    clearStudentAppealDrafts();
    draftOwnerRef.current = "";
    setLoginNotice("");
    void logout().catch(() => undefined).finally(() => {
      setUser(null);
      exitingSessionRef.current = false;
      setExitingSession(false);
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

  useEffect(() => {
    void restore();
    // StrictMode 会清理后再恢复；卸载、退出或重登后的旧响应不能重建旧会话或覆盖新登录。
    return () => { sessionRestoreRef.current += 1; };
  }, [restore]);
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
    return <main className="portal-loading"><output aria-live="polite">正在退出登录…</output></main>;
  }
  if (!user) {
    return <LoginScreen onLoggedIn={acceptUser} notice={loginNotice} />;
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

function LoginScreen({ onLoggedIn, notice }: Readonly<{ onLoggedIn: (user: AuthUser) => void; notice?: string }>) {
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

function AccessDenied({ user, onLogout }: Readonly<{ user: AuthUser; onLogout: () => void }>) {
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

function ExamList({ userName, onOpen }: Readonly<{ userName: string; onOpen: (examID: string) => void }>) {
  const [items, setItems] = useState<PublishedExam[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);

  const [checkedAt, setCheckedAt] = useState<string>();
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const requestID = ++requestRef.current;
    setLoading(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await listPublishedExams();
      if (requestID !== requestRef.current) return;
      setItems([...response.exams].sort((a, b) => (Date.parse(b.published_at ?? "") || 0) - (Date.parse(a.published_at ?? "") || 0)));
      setCheckedAt(new Date().toISOString());
    } catch (reason) {
      if (requestID !== requestRef.current) return;
      setError(friendlyError(reason));
      setLoginRequired(sessionExpired(reason));
    } finally {
      if (requestID === requestRef.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
    // 回到标签页时重新查询学校的当前发布，页面不承诺在后台持续实时更新。
    const onFocus = () => { if (document.visibilityState !== "hidden") void load(); };
    window.addEventListener("focus", onFocus);
    return () => { requestRef.current += 1; window.removeEventListener("focus", onFocus); };
  }, [load]);

  const latest = items[0];
  const latestRate = latest?.max_score ? Math.round((latest.total_score ?? 0) / latest.max_score * 1000) / 10 : undefined;
  return (
    <section className="exam-list-page" aria-labelledby="exam-list-title">
      <div className="page-heading">
        <div><p className="eyebrow">学习概览</p><h1 id="exam-list-title">{greeting(userName)}</h1><p>从最近发布的考试开始复盘。</p></div>
        <button type="button" className="secondary-button" onClick={() => void load()} disabled={loading}>刷新</button>
      </div>
      {checkedAt ? <p className="muted">最近检查：{formatDateTime(checkedAt)}。回到此页面或点击刷新时会检查新成绩。</p> : null}
      {error ? <ErrorNotice message={error} onRetry={load} loginRequired={loginRequired} /> : null}
      {loading ? <div className="inline-status">正在加载已发布考试…</div> : null}
      {!loading && !error && items.length === 0 ? <EmptyState /> : null}
      {!loading && !error && latest ? <section className="latest-release" aria-labelledby="latest-release-title">
        <div><p>最近发布 · {displaySubject(latest.subject)}</p><h2 id="latest-release-title">{latest.name}</h2><span>{formatDate(latest.published_at)} · 第 {latest.release_version} 版</span></div>
        {latest.total_score !== undefined && latest.max_score ? <div className="latest-score"><strong>{formatScore(latest.total_score)}<small> / {formatScore(latest.max_score)}</small></strong><span>得分率 {latestRate}%</span></div> : null}
        <button type="button" className="primary-action" onClick={() => onOpen(latest.exam_id)}>查看考试分析 <span>→</span></button>
      </section> : null}
      {!loading && !error && items.length > 0 ? <section className="published-exams"><div className="section-heading"><div><p className="section-kicker">我的考试</p><h2>已发布成绩</h2></div><span>{items.length} 场考试</span></div><div className="exam-list">
        {items.map((exam) => <button className="exam-row" type="button" key={exam.exam_id} onClick={() => onOpen(exam.exam_id)}>
          <div><strong>{exam.name}</strong><span>{displaySubject(exam.subject)} · 已发布 {formatDate(exam.published_at)}</span></div>
          <div className="exam-row-action"><small>第 {exam.release_version} 版</small><span>查看成绩 →</span></div>
        </button>)}
      </div></section> : null}
    </section>
  );
}

function greeting(name: string) {
  const hour = new Date().getHours();
  if (hour < 6) return `${name}，夜深了`;
  if (hour < 11) return `${name}，早上好`;
  if (hour < 14) return `${name}，中午好`;
  if (hour < 18) return `${name}，下午好`;
  return `${name}，晚上好`;
}

function updatedResultNotice(current: StudentResult | null, next: StudentResult) {
  if (!current || current.release_id === next.release_id) return undefined;
  // 只比较两版共同公开的题目，不能把新增或隐藏的题目当作分数变化。
  const changed = (next.questions ?? []).filter((item) => {
    const old = current.questions?.find((candidate) => candidate.question_id === item.question_id);
    return old && old.score !== item.score;
  });
  const summary = `已更新为第 ${next.release_version} 版。本科成绩 ${formatScore(current.total_score)} → ${formatScore(next.total_score)} 分`;
  let questionChanges = "";
  if (changed.length) {
    const scores = changed.map((item) => `第${item.question_no}题 ${formatScore(item.score)}分`).join("、");
    questionChanges = `；${scores}`;
  }
  return `${summary}${questionChanges}。仅比较两版均公开的分数。`;
}

function ResultDetail({ examID, onBack }: Readonly<{ examID: string; onBack: () => void }>) {
  const { submitting, isSubmitting, reauthenticate } = useStudentSession();
  const [appealRevision, setAppealRevision] = useState(0);
  const [result, setResult] = useState<StudentResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [pendingResult, setPendingResult] = useState<StudentResult>();
  const [checking, setChecking] = useState(false);
  const [checkedAt, setCheckedAt] = useState<string>();
  const [changeNotice, setChangeNotice] = useState("");
  const [appealSelecting, setAppealSelecting] = useState(false);
  const [requestedQuestionID, setRequestedQuestionID] = useState<string>();
  const [requestedQuestionToken, setRequestedQuestionToken] = useState(0);
  const detailRequestRef = useRef(0);
  const resultRef = useRef<StudentResult | null>(null);
  const questionReviewRef = useRef<HTMLElement>(null);

  const load = useCallback(async (apply = false, questionID?: string) => {
    // 成绩重载会卸载申请表；同步查询提交标记，拦住同一轮事件里排队的重载。
    if (isSubmitting?.()) return;
    const requestID = ++detailRequestRef.current;
    if (!resultRef.current) setLoading(true);
    setChecking(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await getResult(examID);
      if (requestID !== detailRequestRef.current) return;
      setCheckedAt(new Date().toISOString());
      const current = resultRef.current;
      if (!current || (apply && !isSubmitting?.())) {
        const notice = updatedResultNotice(current, response.result);
        if (notice !== undefined) setChangeNotice(notice);
        resultRef.current = response.result;setResult(response.result);setPendingResult(undefined);setRequestedQuestionID(questionID);
        if (questionID) setRequestedQuestionToken((value) => value + 1);
      } else if (current.release_id !== response.result.release_id) setPendingResult(response.result);
      else setPendingResult(undefined);
    } catch (reason) {
      if (requestID !== detailRequestRef.current) return;
      setError(friendlyError(reason));
      setLoginRequired(sessionExpired(reason));
    } finally {
      if (requestID === detailRequestRef.current) { setLoading(false);setChecking(false); }
    }
  }, [examID, isSubmitting]);
  useEffect(() => { void load(); const onFocus = () => void load();window.addEventListener("focus",onFocus);return () => {detailRequestRef.current++;window.removeEventListener("focus",onFocus);}; }, [load]);
  const viewCurrent = (questionID?: string) => { if (!isSubmitting?.()) void load(true,questionID); };

  if (loading) return <div className="inline-status">正在加载成绩详情…</div>;
  if (!result) return <ErrorNotice message={error || "暂时无法取得成绩"} onRetry={() => void load()} onBack={onBack} loginRequired={loginRequired} />;
  const questionsVisible = Array.isArray(result.questions);
  const examName = result.exam?.name || "本次考试成绩";
  return (
    <section className="result-page" aria-labelledby="result-title">
      <button type="button" className="back-button" disabled={submitting} onClick={onBack}>← 返回我的考试</button>
      <div className="result-heading">
        <div><span className="release-state">已发布成绩 · 第 {result.release_version} 版</span><p className="eyebrow">成绩与逐题复盘</p><h1 id="result-title">{examName}</h1></div>
        <div><p>最近核对：{checkedAt ? formatDateTime(checkedAt) : "尚未核实"}</p><button type="button" className="secondary-button" disabled={submitting || checking} onClick={() => void load()}>{checking ? "正在核对…" : "检查最新成绩"}</button></div>
      </div>
      {error ? <p role="alert">最新成绩核对失败，当前显示此前读取的第 {result.release_version} 版。{error}{loginRequired ? <button type="button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}</p> : null}
      {pendingResult ? <section className="appeal-notice"><p>学校已更新成绩，第 {pendingResult.release_version} 版可查看。旧版草稿仍可复制，新版需重新核对。</p><button type="button" disabled={submitting || checking} onClick={() => viewCurrent()}>查看新版成绩</button></section> : null}
      {changeNotice ? <p><output aria-live="polite" aria-atomic="true">{changeNotice}</output></p> : null}
      <OldAppealDrafts examID={examID} releaseID={result.release_id} />
      <AppealWindow result={result} onStart={() => {setAppealSelecting(true);document.getElementById("question-review")?.scrollIntoView({behavior:"smooth"});document.querySelector<HTMLButtonElement>("[data-question-id]")?.focus({preventScroll:true});}} />
      <ScoreHero result={result} />
      {result.subject_balance?.length || result.session_summary ? <SubjectPerformance items={result.subject_balance ?? []} session={result.session_summary} /> : null}
      <section className="question-section" aria-labelledby="questions-title" id="question-review" tabIndex={-1} ref={questionReviewRef}>
        {questionsVisible && result.questions && result.questions.length > 0 ? <QuestionTables key={result.release_id} examID={examID} result={result} questions={result.questions} fallbackSubject={result.exam?.subject} appealSelecting={appealSelecting} requestedQuestionID={requestedQuestionID} requestedQuestionToken={requestedQuestionToken} onAppealSubmitted={() => setAppealRevision((value) => value + 1)} onViewCurrent={() => viewCurrent()} /> : null}
        {questionsVisible && result.questions?.length === 0 ? <p className="muted section-empty">学校暂未提供逐题结果。</p> : null}
      </section>
      <PaperViewer key={result.release_id} examID={examID} result={result} onViewCurrent={() => viewCurrent()} onViewTextFeedback={() => {
        if (submitting || isSubmitting?.()) return;
        questionReviewRef.current?.scrollIntoView({ behavior: "smooth" });
        questionReviewRef.current?.focus({ preventScroll: true });
      }} />
      <AppealStatus examID={examID} revision={appealRevision} result={result} onViewCurrent={viewCurrent} />
    </section>
  );
}

function cohortComparison(question: StudentQuestion) {
  const median = question.cohort?.median_score;
  // 只有有限数值的群体中位分可比较；这表示得分位置，不表示知识已掌握。
  if (typeof median !== "number" || !Number.isFinite(median)) {
    return { className: "unknown", label: question.cohort_unavailable_reason ? statisticsReason(question.cohort_unavailable_reason) : "暂无对比数据" };
  }
  if (question.score >= median) return { className: "mastered", label: "达到群体中位分" };
  return { className: "review", label: "低于群体中位分" };
}

// 行组件保持模块级定义；选中状态和详情请求仍由 QuestionTables 管理。
function QuestionRow({ question, expanded, onOpen, onAppeal }: Readonly<{
  question: StudentQuestion;
  expanded: boolean;
  onOpen: (question: StudentQuestion) => Promise<void>;
  onAppeal?: (question: StudentQuestion) => void;
}>) {
  const { submitting } = useStudentSession();
  const comparison = cohortComparison(question);
  const lost = question.score < question.max_score;
  return (
    <tr className={lost ? "lost" : ""}>
      <td data-label="题目">
        <button
          type="button"
          className="question-cell-link"
          aria-expanded={expanded}
          disabled={submitting}
          data-question-id={question.question_id}
          aria-label={`第 ${question.question_no} 题，${formatScore(question.score)} / ${formatScore(question.max_score)} 分`}
          onClick={() => void onOpen(question)}
        >
          <span>第 {question.question_no} 题</span>
          <small>{expanded ? "收起详情" : "查看作答与评分"}</small>
        </button>
        {onAppeal ? <button type="button" className="text-button" disabled={submitting} onClick={() => onAppeal(question)}>申请本题复核</button> : null}
      </td>
      <td data-label="正确答案" title={question.correct_answer}>{answerText(question.correct_answer)}</td>
      <td data-label="实际答案" className="actual-answer" title={question.actual_answer}>{answerText(question.actual_answer)}</td>
      <td data-label="得分" className="student-cell">
        <strong>{formatScore(question.score)}</strong><small> / {formatScore(question.max_score)}</small>
      </td>
      <td data-label="班级平均分">{question.cohort?.class_mean_score !== undefined ? scoreOrDash(question.cohort.class_mean_score) : statisticsReason(question.cohort?.class_unavailable_reason ?? question.cohort_unavailable_reason)}</td>
      <td data-label="参试学生平均分">{question.cohort?.school_mean_score !== undefined ? scoreOrDash(question.cohort.school_mean_score) : statisticsReason(question.cohort_unavailable_reason)}<QuestionCohortScope question={question} /></td>
      <td data-label="群体对比"><span className={`mastery ${comparison.className}`}>{comparison.label}</span><QuestionCohortScope question={question} /></td>
    </tr>
  );
}

function validatedQuestionEvidence(
  questionID: string,
  questionResponse: PromiseSettledResult<Awaited<ReturnType<typeof getQuestion>>>,
  annotationResponse: PromiseSettledResult<Awaited<ReturnType<typeof listQuestionAnnotations>>>
) {
  // 任一证据端发现发布切换，整份题目复盘都须重新核对；不能把另一份成功响应继续用于原版本申诉。
  if (questionResponse.status === "rejected" && studentReleaseChanged(questionResponse.reason)) throw questionResponse.reason;
  if (annotationResponse.status === "rejected" && studentReleaseChanged(annotationResponse.reason)) throw annotationResponse.reason;
  if (questionResponse.status === "rejected") throw questionResponse.reason;
  if (questionResponse.value.question.question_id !== questionID) throw new Error("题目详情与当前题号不一致，请重新加载");
  return questionResponse.value.question;
}

function QuestionTables({ examID, result, questions, fallbackSubject, appealSelecting = false, requestedQuestionID, requestedQuestionToken = 0, onAppealSubmitted, onViewCurrent }: Readonly<{ examID: string; result: StudentResult; questions: StudentQuestion[]; fallbackSubject?: string; appealSelecting?: boolean; requestedQuestionID?: string; requestedQuestionToken?: number; onAppealSubmitted: () => void; onViewCurrent: () => void }>) {
  const { submitting, isSubmitting, reauthenticate } = useStudentSession();
  const groups = useMemo(() => {
    const values = new Map<string, StudentQuestion[]>();
    for (const question of questions) {
      const subject = displaySubject(question.subject || fallbackSubject);
      values.set(subject, [...(values.get(subject) ?? []), question]);
    }
    return [...values.entries()].map(([subject, items]) => {
      items.sort((a, b) => a.question_no.localeCompare(b.question_no, "zh-CN", { numeric: true }));
      return [subject, items] as const;
    });
  }, [fallbackSubject, questions]);
  const [activeSubject, setActiveSubject] = useState(groups[0]?.[0] ?? "");
  const [selected, setSelected] = useState<StudentQuestion | null>(null);
  const [detail, setDetail] = useState<StudentQuestion | null>(null);
  const [appealQuestion, setAppealQuestion] = useState<Pick<StudentQuestion, "question_id" | "question_no"> | null>(null);
  const [annotations, setAnnotations] = useState<StudentQuestionAnnotation[]>([]);
  const [detailError, setDetailError] = useState("");
  const [detailLoading, setDetailLoading] = useState(false);
  const [annotationError, setAnnotationError] = useState("");
  const [detailNeedsLogin, setDetailNeedsLogin] = useState(false);
  const [annotationNeedsLogin, setAnnotationNeedsLogin] = useState(false);
  const [releaseChanged, setReleaseChanged] = useState(false);
  const [evidenceChecking, setEvidenceChecking] = useState(false);
  const evidenceVerificationRef = useRef({ requestID: 0, pending: false, changed: false });
  const [lostOnly, setLostOnly] = useState(false);
  const detailRegionRef = useRef<HTMLDivElement>(null);
  const tablesRef = useRef<HTMLDivElement>(null);
  const listPositionRef = useRef<number | undefined>(undefined);
  const appealFocusRef = useRef(false);
  const requestedOpenedRef = useRef<string | undefined>(undefined);
  useEffect(() => { if (selected) detailRegionRef.current?.scrollIntoView?.({ block: "nearest" }); }, [selected]);
  const requestRef = useRef(0);
  // 切题、收起或卸载都作废旧读取；迟到的冲突和收尾只能处理仍被选中的题目。
  useEffect(() => () => { requestRef.current += 1; }, []);
  const activeGroups = groups.filter(([subject]) => subject === activeSubject || groups.length === 1);
  const markReleaseChanged = useCallback((reason: unknown, requestID: number) => {
    const sourceUnavailable = reason instanceof PortalApiError && reason.code === "appeal_source_release_invalid";
    if (requestID !== requestRef.current || (!studentReleaseChanged(reason) && !sourceUnavailable)) return false;
    // 换版或来源失效立即隐藏证据，并用 ref 拦住旧版新提交；已发申请的回执不随证据一起卸载。
    evidenceVerificationRef.current.changed = true;
    evidenceVerificationRef.current.pending = false;
    setEvidenceChecking(false); setReleaseChanged(true); setDetail(null); setDetailLoading(false);
    setAnnotations([]); setAnnotationError(""); setAnnotationNeedsLogin(false); setDetailNeedsLogin(false); setDetailError(friendlyError(reason));
    return true;
  }, []);
  const openQuestion = useCallback(async (question: StudentQuestion, retry = false) => {
    if (submitting || isSubmitting?.()) return;
    if (!selected) listPositionRef.current = window.scrollY;
    const requestId = ++requestRef.current;
    evidenceVerificationRef.current = { requestID: requestId, pending: false, changed: false };
    setEvidenceChecking(false);
    if (!retry && selected?.question_id === question.question_id) { setSelected(null); setDetail(null); setAppealQuestion(null); setDetailLoading(false); return; }
    setSelected(question); setDetail(null); setAppealQuestion(null); setReleaseChanged(false); setAnnotations([]); setAnnotationError(""); setAnnotationNeedsLogin(false); setDetailNeedsLogin(false); setDetailError(""); setDetailLoading(true);
    const rejectEvidence = (reason: unknown): never => { markReleaseChanged(reason, requestId); throw reason; };
    try {
      const [questionResponse, annotationResponse] = await Promise.allSettled([
        getQuestion(examID, question.question_id, result.release_id).catch(rejectEvidence),
        listQuestionAnnotations(examID, question.question_id, result.release_id).catch(rejectEvidence)
      ]);
      // 冲突已提前显示时，另一份迟到成功或普通失败不能恢复原版本证据。
      if (requestId !== requestRef.current || evidenceVerificationRef.current.changed) return;
      const verifiedQuestion = validatedQuestionEvidence(question.question_id, questionResponse, annotationResponse);
      setDetail(verifiedQuestion);
      // 这里只保留申请身份；评分证据可被清空，回执仍属于原题、原发布版本和原账户。
      setAppealQuestion({ question_id: verifiedQuestion.question_id, question_no: verifiedQuestion.question_no });
      if (annotationResponse.status === "fulfilled") setAnnotations(annotationResponse.value.annotations);
      else { setAnnotationNeedsLogin(sessionExpired(annotationResponse.reason)); setAnnotationError(sessionExpired(annotationResponse.reason) ? friendlyError(annotationResponse.reason) : "教师批注暂时无法加载，当前显示的评分详情仍可查看。"); }
    } catch (reason) { if (requestId === requestRef.current && !evidenceVerificationRef.current.changed) { setDetailError(friendlyError(reason, "question")); setDetailNeedsLogin(sessionExpired(reason)); setReleaseChanged(studentReleaseChanged(reason)); } }
    finally { if (requestId === requestRef.current) setDetailLoading(false); }
  }, [examID, isSubmitting, markReleaseChanged, result.release_id, selected, submitting]);
  useEffect(() => {
    // 同一定位请求只打开一次；显式再次查看本题递增token，普通重绘或关闭详情不应重新打开。
    if (!requestedQuestionID || requestedOpenedRef.current === `${requestedQuestionID}:${requestedQuestionToken}`) return;
    const question=questions.find((item)=>item.question_id===requestedQuestionID);
    if (!question) return;
    requestedOpenedRef.current=`${requestedQuestionID}:${requestedQuestionToken}`;
    const group=groups.find(([,items])=>items.some((item)=>item.question_id===question.question_id)); if(group) setActiveSubject(group[0]);
    void openQuestion(question,true);
  }, [requestedQuestionID, requestedQuestionToken, questions, groups, openQuestion]);
  useEffect(() => { if (appealFocusRef.current && !detailLoading && detail && !releaseChanged) { const field=detailRegionRef.current?.querySelector<HTMLTextAreaElement>(".question-appeal-form textarea"); if(field) {field.focus();appealFocusRef.current=false;} } }, [detail, detailLoading, releaseChanged]);
  const retryAnnotations = async () => {
    if (!selected || submitting || isSubmitting?.() || evidenceVerificationRef.current.changed) return;
    const requestID = requestRef.current;
    setAnnotationError("");
    try {
      const response = await listQuestionAnnotations(examID, selected.question_id, result.release_id);
      if (requestID === requestRef.current && !evidenceVerificationRef.current.changed) { setAnnotations(response.annotations); setAnnotationNeedsLogin(false); }
    } catch (reason) {
      if (requestID === requestRef.current && !markReleaseChanged(reason, requestID) && !evidenceVerificationRef.current.changed) {
        setAnnotationError(friendlyError(reason)); setAnnotationNeedsLogin(sessionExpired(reason));
      }
    }
  };
  const verifyFailedImage = async (questionID: string, requestID: number) => {
    if (requestID !== requestRef.current) return;
    const verification = evidenceVerificationRef.current;
    if (verification.pending || verification.changed) return;
    // 同步标记拦截重绘前的提交；两个图片入口失败时共用一次版本复核。
    verification.pending = true;
    setEvidenceChecking(true);
    // img 的错误事件没有 HTTP 状态；用同版题目重新核对，只有明确的版本冲突才关闭文字申诉。
    // 网络故障或坏图片仍可写文字说明，旧题/旧页面的迟到复核不能影响当前题目。
    try { await getQuestion(examID, questionID, result.release_id); }
    catch (reason) {
      markReleaseChanged(reason, requestID);
    } finally {
      // 切题已建立新的复核状态；旧题的 finally 不能解除新题的提交保护。
      if (requestID === requestRef.current) {
        verification.pending = false;
        setEvidenceChecking(false);
      }
    }
  };
  const selectedRequestID = requestRef.current;
  const isEvidenceBlocked = () => selectedRequestID !== requestRef.current || evidenceVerificationRef.current.pending || evidenceVerificationRef.current.changed;
  const lostQuestions = activeGroups.flatMap(([, items]) => items.filter((question) => question.score < question.max_score));
  const selectedLostIndex = lostQuestions.findIndex((question) => question.question_id === selected?.question_id);
  const previousLost = selectedLostIndex >= 0 ? lostQuestions[selectedLostIndex - 1] : undefined;
  const nextLost = selectedLostIndex >= 0 ? lostQuestions[selectedLostIndex + 1] : lostQuestions[0];
  const viewCurrent = () => {
    if (submitting || isSubmitting?.()) return;
    // 重新核对会卸载本题；先作废另一份未完成读取，再让父级明确加载当前成绩，草稿仍按旧发布隔离。
    requestRef.current += 1;
    onViewCurrent();
  };
  const returnToList = () => {
    if (submitting || isSubmitting?.()) return;
    const id = selected?.question_id;
    requestRef.current += 1; setSelected(null); setDetail(null); setAppealQuestion(null); setDetailLoading(false);
    const row = [...(tablesRef.current?.querySelectorAll<HTMLButtonElement>("[data-question-id]") ?? [])].find((button) => button.dataset.questionId === id);
    row?.focus({ preventScroll: true });
    window.scrollTo({ top: listPositionRef.current ?? 0, behavior: "auto" });
  };
  return <div className={appealSelecting ? "subject-tables selecting-appeal" : "subject-tables"} ref={tablesRef}>
    {appealSelecting ? <p><output>选择需要复核的题目，核对本版作答和评分后填写申请。</output></p> : null}
    {groups.length > 1 ? <nav className="subject-tabs" aria-label="科目">{groups.map(([subject]) => <button type="button" disabled={submitting} aria-pressed={activeSubject === subject} className={activeSubject === subject ? "active" : ""} onClick={() => {
      // 切科会卸载原题；同步提交标记拦住同批事件，随后作废旧题读取。
      if (submitting || isSubmitting?.()) return;
      requestRef.current += 1;
      setActiveSubject(subject); setSelected(null); setDetail(null); setAppealQuestion(null); listPositionRef.current = undefined;
    }} key={subject}>{subject}</button>)}</nav> : null}
    <div className="question-filter"><label><input type="checkbox" disabled={submitting} checked={lostOnly} onChange={(event) => setLostOnly(event.target.checked)} /> 只看有失分的题目</label><span>群体对比仅表示本次得分位置，不代表知识掌握程度。</span></div>
    {activeGroups.map(([subject, items]) => <section className="subject-table" key={subject}>
    <header><h2 id="questions-title">{subject}逐题分析</h2><span>学生得分：{formatScore(items.reduce((sum, item) => sum + item.score, 0))} 分　满分：{formatScore(items.reduce((sum, item) => sum + item.max_score, 0))} 分</span></header>
    <div className="paper-table-scroll"><table className="paper-score-table">
      <thead><tr><th>题号</th><th>正确答案</th><th>实际答案</th><th>得分</th><th>班级平均分</th><th>参试学生平均分</th><th title="中位分将本次参试成绩分为高低两半，同分同学可能位于同一分值。">群体中位分对比</th></tr></thead>
      <tbody>{items.filter((question) => !lostOnly || question.score < question.max_score).map((question) => <QuestionRow key={question.question_id} question={question} expanded={selected?.question_id === question.question_id} onOpen={openQuestion} onAppeal={result.appeal_window.open ? (item) => {appealFocusRef.current=true;void openQuestion(item,true);} : undefined} />)}</tbody>
    </table></div>
    {lostOnly && !items.some((item) => item.score < item.max_score) ? <p><output>本学科没有失分题目，可以切换为查看全部题目。</output></p> : null}
    {selected && items.some((item) => item.question_id === selected.question_id) ? <div className="question-drilldown" ref={detailRegionRef}>
      <nav className="question-review-navigation" aria-label="连续复盘">
        <button type="button" className="secondary-button" disabled={submitting || !previousLost} onClick={() => previousLost && void openQuestion(previousLost)}>上一失分题</button>
        <button type="button" className="secondary-button" disabled={submitting || !nextLost} onClick={() => nextLost && void openQuestion(nextLost)}>下一失分题</button>
        <button type="button" className="text-button" disabled={submitting} onClick={returnToList}>返回题目列表</button>
      </nav>
      {(detailNeedsLogin || annotationNeedsLogin) ? <button type="button" className="secondary-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}
      <QuestionDrilldownContent evidenceChecking={evidenceChecking} isEvidenceBlocked={isEvidenceBlocked} onImageUnavailable={() => void verifyFailedImage(selected.question_id, selectedRequestID)} releaseChanged={releaseChanged} onSourceUnavailable={(reason) => { markReleaseChanged(reason, selectedRequestID); }} onViewCurrent={viewCurrent} examID={examID} result={result} selected={selected} detail={detail} appealQuestion={appealQuestion} detailLoading={detailLoading} detailError={detailError} annotationError={annotationError} annotations={annotations} onRetry={() => void openQuestion(selected, true)} onRetryAnnotations={() => void retryAnnotations()} onAppealSubmitted={onAppealSubmitted} />
    </div> : null}
  </section>)}</div>;
}

interface QuestionDrilldownContentProps {
  evidenceChecking: boolean;
  isEvidenceBlocked: () => boolean;
  onImageUnavailable: () => void;
  releaseChanged: boolean;
  onSourceUnavailable: (reason: unknown) => void;
  onViewCurrent: () => void;
  examID: string;
  result: StudentResult;
  selected: StudentQuestion;
  detail: StudentQuestion | null;
  appealQuestion: Pick<StudentQuestion, "question_id" | "question_no"> | null;
  detailLoading: boolean;
  detailError: string;
  annotationError: string;
  annotations: StudentQuestionAnnotation[];
  onRetry: () => void;
  onRetryAnnotations: () => void;
  onAppealSubmitted: () => void;
}

// 这里只展示当前题目的状态；请求代次、重试和滚动引用仍由父组件维护。
function QuestionDrilldownContent({ evidenceChecking, isEvidenceBlocked, onImageUnavailable, releaseChanged, onSourceUnavailable, onViewCurrent, examID, result, selected, detail, appealQuestion, detailLoading, detailError, annotationError, annotations, onRetry, onRetryAnnotations, onAppealSubmitted }: Readonly<QuestionDrilldownContentProps>) {
  const { submitting } = useStudentSession();
  return <>
    <h3>第 {selected.question_no} 题 · 作答与评分依据</h3>
    {detailLoading ? <p className="muted"><output>正在加载本题…</output></p> : null}
    {detailError ? <p className="detail-error" role="alert">{detailError} <button type="button" className="text-button" disabled={submitting} onClick={releaseChanged ? onViewCurrent : onRetry}>{releaseChanged ? "查看当前成绩并重新核对" : "重试本题"}</button></p> : null}
    {annotationError ? <p role="alert">{annotationError} <button type="button" className="text-button" onClick={onRetryAnnotations}>重试批注</button></p> : null}
    {detail?.question_id === selected.question_id ? <AnswerReview onImageUnavailable={onImageUnavailable} key={detail.question_id} question={detail} annotations={annotations} imageURL={studentQuestionAnswerImageURL(examID,detail.question_id, result.release_id)} /> : null}
    {result.appeal_window.open && appealQuestion?.question_id === selected.question_id ? <QuestionAppealForm evidenceChecking={evidenceChecking} evidenceUnavailable={releaseChanged} isEvidenceBlocked={isEvidenceBlocked} onSourceUnavailable={onSourceUnavailable} onImageUnavailable={onImageUnavailable} key={`${result.release_id}:${appealQuestion.question_id}`} examID={examID} releaseID={result.release_id} releaseVersion={result.release_version} question={appealQuestion} allowedReasonCodes={result.appeal_window.allowed_reason_codes ?? []} onSubmitted={onAppealSubmitted} /> : null}
  </>;
}

function answerText(value?: string) { return value?.trim() || "—"; }
function scoreOrDash(value?: number) { return value === undefined ? "—" : formatScore(value); }

export function questionTypeLabel(type: string) {
  return ({ single_choice: "单选题", multiple_choice: "多选题", true_false: "判断题", fill_blank: "填空题", numeric: "数值题", formula: "公式题", short_answer: "简答题", calculation: "计算题", essay: "解答题", discussion: "论述题", coding: "编程题" } as Record<string, string>)[type] ?? type;
}

const appealReasonLabels: Record<string, string> = {
  recognition_error: "答题内容识别不完整",
  missing_step_credit: "作答步骤疑似漏评",
  rubric_disagreement: "评分标准适用有异议",
  calculation_error: "得分记录或合计有误",
  annotation_issue: "批注与实际扣分不一致",
  other: "其他明确评分问题"
};

function AppealDraftNotice({ stored }: Readonly<{ stored: boolean }>) {
  // 这里只展示存储结果；同步写入和离开保护仍由表单及 App 执行。
  return <p className={stored ? "muted" : "detail-error"}><output aria-live="polite">{stored ? "草稿已保存在当前标签页会话，切题、返回或重新登录后可恢复；退出登录将清除。" : "浏览器未允许保存草稿，当前输入仅在本页保留。刷新或关闭前请先复制说明。"}</output></p>;
}

export function QuestionAppealForm({ examID, releaseID, releaseVersion, question, allowedReasonCodes, onSubmitted, onImageUnavailable, evidenceChecking = false, evidenceUnavailable = false, isEvidenceBlocked, onSourceUnavailable }: Readonly<{
  examID: string;
  releaseID: string;
  releaseVersion: number;
  question: Pick<StudentQuestion, "question_id" | "question_no">;
  allowedReasonCodes: string[];
  onSubmitted: () => void;
  evidenceChecking?: boolean;
  evidenceUnavailable?: boolean;
  isEvidenceBlocked?: () => boolean;
  onSourceUnavailable?: (reason: unknown) => void;
  onImageUnavailable?: () => void;
}>) {
  const { draftOwner, submitting: sessionSubmitting, isSubmitting, setSubmitting: setSessionSubmitting, reauthenticate } = useStudentSession();
  // 服务端将空白名单解释为不限制原因；学生端必须按同一规则提供六种已有原因。
  const availableReasons = (allowedReasonCodes.length ? allowedReasonCodes : Object.keys(appealReasonLabels)).filter((code) => appealReasonLabels[code]);
  const storageKey = draftOwner ? appealDraftKey(draftOwner, examID, releaseID, question.question_id) : "";
  const [draft, setDraft] = useState<AppealDraft>(() => {
    const saved = storageKey ? readAppealDraft(storageKey) : undefined;
    return { reasonCode: saved && availableReasons.includes(saved.reasonCode) ? saved.reasonCode : availableReasons[0] ?? "", reason: saved?.reason ?? "", ...(saved?.selectedRegion ? { selectedRegion: saved.selectedRegion } : {}) };
  });
  const draftRef = useRef(draft);
  const [draftStored, setDraftStored] = useState(() => !storageKey || isAppealDraftStored(storageKey));
  const updateDraft = (patch: Partial<AppealDraft>) => {
    const next = { ...draftRef.current, ...patch, questionNo:question.question_no, releaseVersion };
    draftRef.current = next;
    setDraft(next);
    // 在输入事件中同步存储，不能等 effect；快速切题或刷新时最后一次输入也必须属于原题原版本。
    if (storageKey) setDraftStored(writeAppealDraft(storageKey, next));
  };
  const { reasonCode, reason, selectedRegion } = draft;
  const [submitting, setSubmitting] = useState(false);
  const submissionStartedRef = useRef(false);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [closed, setClosed] = useState(false);
  const [duplicateDetected, setDuplicateDetected] = useState(false);
  const [existingLoading, setExistingLoading] = useState(true);
  const [existingError, setExistingError] = useState("");
  const [existingAppeal, setExistingAppeal] = useState<StudentQuestionAppeal | null>(null);

  const checkExisting = useCallback(async () => {
    setExistingLoading(true); setExistingError("");
    try {
      const response = await listQuestionAppeals(examID);
      const existing = response.appeals.find((appeal) => appeal.source_release_id === releaseID && appeal.question_id === question.question_id) ?? null;
      setExistingAppeal(existing);
      if (existing && storageKey) removeAppealDraft(storageKey);
    } catch (reason) { setExistingError(friendlyError(reason, "appeal")); setLoginRequired(sessionExpired(reason)); }
    finally { setExistingLoading(false); }
  }, [examID, question.question_id, releaseID, storageKey]);
  useEffect(() => {
    let active = true;
    setExistingLoading(true);
    void listQuestionAppeals(examID).then((response) => {
      if (!active) return;
      const existing = response.appeals.find((appeal) => appeal.source_release_id === releaseID && appeal.question_id === question.question_id) ?? null;
      setExistingAppeal(existing);
      if (existing && storageKey) removeAppealDraft(storageKey);
    }).catch((reason) => { if (active) { setExistingError(friendlyError(reason, "appeal")); setLoginRequired(sessionExpired(reason)); } }).finally(() => { if (active) setExistingLoading(false); });
    return () => { active = false; };
  }, [examID, question.question_id, releaseID, storageKey]);

  if (existingAppeal) {
    return <output className="question-appeal-confirmation"><strong>{appealStatusLabel(existingAppeal.status)}</strong><span>学校已收到本题复核申请。</span><span>本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。</span><a href="#my-question-appeals" onClick={(event) => { event.preventDefault(); document.getElementById("my-question-appeals")?.scrollIntoView({ behavior: "smooth" }); }}>查看我的申请</a>{evidenceUnavailable ? <span>此申请对应第 {releaseVersion} 版成绩，当前成绩需重新核对。</span> : null}</output>;
  }
  if (availableReasons.length === 0) {
    return <output className="question-appeal-confirmation" aria-live="polite" aria-atomic="true"><strong>暂时无法申请复核</strong><span>学校配置的复核原因当前不可用，请联系学校考试负责人核对设置。</span></output>;
  }
  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    // 图片失败后的版本复核可能尚未重绘；直接提交事件也必须读取同步保护。
    if (submissionStartedRef.current || isSubmitting?.() || evidenceChecking || evidenceUnavailable || isEvidenceBlocked?.() || existingLoading || existingError || closed || duplicateDetected) return;
    submissionStartedRef.current = true;
    setSubmitting(true);
    setSessionSubmitting(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await createQuestionAppeal(examID, {
        source_release_id: releaseID,
        question_id: question.question_id,
        reason_code: reasonCode,
        reason: reason.trim(),
        ...(selectedRegion ? { selected_region: selectedRegion } : {})
      });
      setExistingAppeal(response.appeal);
      if (storageKey) removeAppealDraft(storageKey);
      onSubmitted();
    } catch (error_) {
      setError(friendlyError(error_));
      setLoginRequired(sessionExpired(error_));
      onSourceUnavailable?.(error_);
      if (error_ instanceof PortalApiError && error_.code === "appeal_window_closed") setClosed(true);
      if (error_ instanceof PortalApiError && error_.code === "appeal_already_filed") { setDuplicateDetected(true); void checkExisting(); onSubmitted(); }
    } finally {
      submissionStartedRef.current = false;
      setSubmitting(false);
      setSessionSubmitting(false);
    }
  };
  if (evidenceUnavailable) {
    // 证据失效时仍保留只读原说明；复制不恢复旧圈选，也不能重发旧版申请。
    // 已发 POST 不跟着证据换版消失；这里只显示原版回执，不提供旧版重发或把草稿迁移到新版。
    return <QuestionAppealReceipt hasSubmission={submitting || Boolean(error) || duplicateDetected} questionNo={question.question_no} releaseVersion={releaseVersion} submitting={submitting} sessionSubmitting={sessionSubmitting} error={error} storageKey={storageKey} reason={reason} selectedRegion={selectedRegion} draftStored={draftStored} existingError={existingError} loginRequired={loginRequired} reauthenticate={reauthenticate} duplicateDetected={duplicateDetected} existingLoading={existingLoading} checkExisting={checkExisting} />;
  }
  return <form className="question-appeal-form" onSubmit={submit}>
    <QuestionAppealFields examID={examID} questionID={question.question_id} releaseID={releaseID} releaseVersion={releaseVersion} storageKey={storageKey} reason={reason} selectedRegion={selectedRegion} reasonCode={reasonCode} draftStored={draftStored} evidenceChecking={evidenceChecking} existingLoading={existingLoading} existingError={existingError} checkExisting={checkExisting} submitting={submitting} sessionSubmitting={sessionSubmitting} onImageUnavailable={onImageUnavailable} updateDraft={updateDraft} availableReasons={availableReasons} error={error} loginRequired={loginRequired} reauthenticate={reauthenticate} duplicateDetected={duplicateDetected} existingAppeal={existingAppeal} closed={closed} />
  </form>;
}

interface QuestionAppealFeedbackProps {
  releaseVersion: number;
  submitting: boolean;
  sessionSubmitting: boolean;
  error: string;
  storageKey: string;
  reason: string;
  selectedRegion?: SelectedAppealRegion;
  draftStored: boolean;
  existingError: string;
  loginRequired: boolean;
  reauthenticate: () => void;
  duplicateDetected: boolean;
  existingLoading: boolean;
  checkExisting: () => Promise<void>;
}

// 只展示原版本回执；提交事实、草稿清理与重查状态仍由父表单持有。
function QuestionAppealReceipt({ questionNo, hasSubmission, releaseVersion, submitting, sessionSubmitting, error, storageKey, reason, selectedRegion, draftStored, existingError, loginRequired, reauthenticate, duplicateDetected, existingLoading, checkExisting }: Readonly<QuestionAppealFeedbackProps & { questionNo: string; hasSubmission: boolean }>) {
  const originalReasonRef = useRef<HTMLTextAreaElement>(null);
  return <section className="question-appeal-form" aria-label={`第 ${questionNo} 题复核申请回执`}>
    <h3>{hasSubmission ? "复核申请回执" : "原版本复核草稿"}</h3>
    <p>{hasSubmission ? "此申请" : "此未提交草稿"}对应第 {releaseVersion} 版成绩。当前成绩需重新核对，原版本草稿不会自动迁移。</p>
    {submitting ? <p><output aria-live="polite">正在提交第 {releaseVersion} 版的复核申请，请等待回执。</output></p> : null}
    {error ? <p className="detail-error" role="alert">{error}</p> : null}
    {!submitting && storageKey && (reason || selectedRegion) ? <AppealDraftNotice stored={draftStored} /> : null}
    {/* 存储被拒绝时原说明只在内存中；只读取回区保留原题原版文本，不恢复过期证据或提供旧版重发。 */}
    {reason ? <>
      <label>第 {questionNo} 题 · 第 {releaseVersion} 版原说明（只读）<textarea ref={originalReasonRef} readOnly rows={5} value={reason} /></label>
      <button type="button" className="secondary-button" onClick={() => { originalReasonRef.current?.focus(); originalReasonRef.current?.select(); }}>选中原说明</button>
      <p>选中后按 Ctrl+C 或 ⌘C 复制，手机可长按复制。请自行保存原说明，再重新核对当前成绩。</p>
    </> : null}
    {existingError ? <p role="alert">无法确认是否已提交。{existingError}</p> : null}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting || sessionSubmitting} onClick={reauthenticate}>重新登录</button> : null}
    {duplicateDetected ? <button type="button" className="text-button" disabled={existingLoading} onClick={() => void checkExisting()}>查看已有申请记录</button> : null}
  </section>;
}

interface QuestionAppealFieldsProps extends QuestionAppealFeedbackProps {
  examID: string;
  questionID: string;
  releaseID: string;
  reasonCode: string;
  evidenceChecking: boolean;
  onImageUnavailable?: () => void;
  updateDraft: (patch: Partial<AppealDraft>) => void;
  availableReasons: string[];
  existingAppeal: StudentQuestionAppeal | null;
  closed: boolean;
}

// 输入区没有独立草稿或提交状态，避免展示组件重挂载后改变原题原版本的请求边界。
function QuestionAppealFields({ examID, questionID, releaseID, releaseVersion, storageKey, reason, selectedRegion, reasonCode, draftStored, evidenceChecking, existingLoading, existingError, checkExisting, submitting, sessionSubmitting, onImageUnavailable, updateDraft, availableReasons, error, loginRequired, reauthenticate, duplicateDetected, existingAppeal, closed }: Readonly<QuestionAppealFieldsProps>) {
  return <>
    <h3>申请复核</h3>
    <p>每道题在同一版成绩下只能提交一次。学校将按第 {releaseVersion} 版成绩核对原卷和评分标准。</p>
    {storageKey && (reason || selectedRegion) ? <AppealDraftNotice stored={draftStored} /> : null}
    {evidenceChecking ? <p><output aria-live="polite">正在核对答卷所属成绩版本，完成前暂不能提交。</output></p> : null}
    {existingLoading ? <p><output aria-live="polite">正在核对已有复核申请…</output></p> : null}
    {existingError ? <p role="alert">无法确认是否已提交。{existingError} <button type="button" className="text-button" onClick={() => void checkExisting()}>重试申请记录</button></p> : null}
    <fieldset disabled={submitting} className="appeal-form-fields">
    <AppealRegionSelector
      onImageUnavailable={onImageUnavailable}
      imageURL={studentQuestionAnswerImageURL(examID, questionID, releaseID)}
      value={selectedRegion}
      onChange={(selectedRegion) => updateDraft({ selectedRegion })}
    />
    <label>复核原因<select value={reasonCode} onChange={(event) => updateDraft({ reasonCode: event.target.value })}>{availableReasons.map((code) => <option key={code} value={code}>{appealReasonLabels[code]}</option>)}</select></label>
    <label>具体说明<textarea required minLength={10} maxLength={500} value={reason} onChange={(event) => updateDraft({ reason: event.target.value })} placeholder="请具体说明哪一步、哪一评分点或哪段识别内容需要复核（10—500字）" /></label>
    </fieldset>
    {error ? <p className="detail-error" role="alert">{error}</p> : null}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting || sessionSubmitting} onClick={reauthenticate}>重新登录</button> : null}
    {duplicateDetected && !existingAppeal ? <button type="button" className="text-button" onClick={() => void checkExisting()}>查看已有申请记录</button> : null}
    <button className="secondary-button" type="submit" disabled={submitting || evidenceChecking || existingLoading || Boolean(existingError) || closed || duplicateDetected || reason.trim().length < 10}>{submitting ? "正在提交…" : "确认提交（仅一次）"}</button>
  </>;
}

function AppealRegionSelector({ imageURL, value, onChange, onImageUnavailable }: Readonly<{
  imageURL: string;
  value?: SelectedAppealRegion;
  onChange: (value: SelectedAppealRegion | undefined) => void;
  onImageUnavailable?: () => void;
}>) {
  const { submitting } = useStudentSession();
  const [imageState, setImageState] = useState<"loading" | "ready" | "unavailable">("loading");
  const [start, setStart] = useState<{ x: number; y: number } | null>(null);
  const [pointMode, setPointMode] = useState(false);

  const point = (event: PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return {
      x: Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width)),
      y: Math.max(0, Math.min(1, (event.clientY - bounds.top) / bounds.height))
    };
  };
  const regionFrom = (origin: { x: number; y: number }, end: { x: number; y: number }): SelectedAppealRegion => ({
    coordinate_space: "canonical_image_normalized",
    x: Math.min(origin.x, end.x), y: Math.min(origin.y, end.y),
    width: Math.abs(end.x - origin.x), height: Math.abs(end.y - origin.y)
  });
  const begin = (event: PointerEvent<HTMLDivElement>) => {
    // fieldset 不会禁用 div 的指针事件；提交中保持圈选内容与发送的草稿一致。
    if (submitting || imageState !== "ready") return;
    if (pointMode) {
      if (!start) setStart(point(event));
      else { const region = regionFrom(start, point(event)); setStart(null); if (region.width >= .01 && region.height >= .01) onChange(region); }
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    setStart(point(event));
  };
  const finish = (event: PointerEvent<HTMLDivElement>) => {
    if (submitting) { setStart(null); return; }
    if (pointMode) return;
    if (!start) return;
    const region = regionFrom(start, point(event));
    setStart(null);
    if (region.width >= 0.01 && region.height >= 0.01) onChange(region);
  };
  return <section className="appeal-region-selector" aria-labelledby="appeal-region-title">
    <div><h4 id="appeal-region-title">圈选争议区域（可选）</h4><p>可拖动框选，也可依次点击区域的两个对角；未圈选也可以提交。</p></div>
    <label><input type="checkbox" checked={pointMode} onChange={(event) => { setPointMode(event.target.checked); setStart(null); }} /> 使用两点点击框选</label>
    {pointMode && start ? <p><output>起点已选，请点击区域的另一角。</output></p> : null}
    <div className="appeal-region-image-wrap">
      <img src={imageURL} alt="本题答题区域" onLoad={() => setImageState("ready")} onError={() => { setImageState("unavailable"); onImageUnavailable?.(); }} />
      {imageState === "ready" ? <div
        className="appeal-region-canvas"
        onPointerDown={begin}
        onPointerUp={finish}
        onPointerCancel={() => setStart(null)}
        aria-label="在答题图上框选争议区域"
        role="application"
        aria-disabled={submitting}
      >
        {value ? <span className="appeal-region-box" style={{ left: `${value.x * 100}%`, top: `${value.y * 100}%`, width: `${value.width * 100}%`, height: `${value.height * 100}%` }} /> : null}
      </div> : null}
      {imageState === "loading" ? <span className="appeal-region-loading">正在加载本题答题区域…</span> : null}
    </div>
    {imageState === "unavailable" ? <p className="detail-error">本题答题图暂时不可用，仍可通过文字说明提交复核。</p> : null}
    <button type="button" className="secondary-button" disabled={imageState !== "ready"} onClick={() => onChange({ coordinate_space: "canonical_image_normalized", x: 0, y: 0, width: 1, height: 1 })}>选择整个答题区域</button>
    {value ? <details><summary>精确调整区域（百分比）</summary><div className="region-coordinate-fields">{(["x", "y", "width", "height"] as const).map((key) => <label key={key}>{{ x: "距左侧", y: "距顶部", width: "宽度", height: "高度" }[key]}<input type="number" min={key === "x" || key === "y" ? 0 : 1} max={100} value={Math.round(value[key] * 100)} onChange={(event) => { const next = { ...value, [key]: Math.max(key === "x" || key === "y" ? 0 : .01, Math.min(1, Number(event.target.value) / 100)) }; next.x = Math.min(.99, next.x); next.y = Math.min(.99, next.y); next.width = Math.min(next.width, 1 - next.x); next.height = Math.min(next.height, 1 - next.y); onChange(next); }} /></label>)}</div></details> : null}
    {value ? <div className="appeal-region-summary"><span>已圈选争议区域</span><button type="button" className="text-button" onClick={() => onChange(undefined)}>清除</button></div> : null}
  </section>;
}

function closedAppealWindowText(state: StudentResult["appeal_window"]["state"], opensAt: string | undefined, closesAt: string | undefined) {
  // 状态以服务端为准；只格式化当前分支用到的日期，未开放时不读取无关日期。
  if (state === "upcoming") {
    const start = opensAt ? formatDateTime(opensAt) : "学校指定时间";
    const deadline = closesAt ? `，截止至 ${formatDateTime(closesAt)}` : "";
    return `复核将在 ${start} 开始${deadline}。`;
  }
  if (state === "closed") {
    const closedAt = closesAt ? `于 ${formatDateTime(closesAt)}` : "";
    return `本次成绩的复核窗口已${closedAt}结束。`;
  }
  return "学校当前未开放本次成绩的复核窗口。";
}

export function AppealWindow({ result, onStart }: Readonly<{ result: StudentResult; onStart?: () => void }>) {
  const { submitting }=useStudentSession();
  const windowState = result.appeal_window;
  if (windowState.open) {
    if (!result.questions?.length) return <section className="appeal-notice"><div><h2>成绩复核</h2><p>学校已开启复核窗口，但当前未公开可申请的逐题成绩。请联系学校考试负责人公开逐题得分后，再选择题目申请复核。</p></div></section>;
    return <section className="appeal-notice open"><div><h2>成绩复核</h2><p>本次成绩的复核窗口已开启{windowState.closes_at ? `，截止至 ${formatDateTime(windowState.closes_at)}` : ""}。请在需要复核的题目下提交申请。</p>{onStart ? <button type="button" className="primary-action" disabled={submitting} onClick={onStart}>申请成绩复核</button> : null}</div><span>对应第 {result.release_version} 版成绩</span></section>;
  }
  return <section className="appeal-notice"><div><h2>成绩复核</h2><p>{closedAppealWindowText(windowState.state, windowState.opens_at, windowState.closes_at)}</p></div></section>;
}

function appealDecisionText(appeal: StudentQuestionAppeal) {
  // 拒绝结论优先于转入重评，保持决定与状态不一致时的原显示顺序。
  if (appeal.decision === "reject" || appeal.status === "rejected") return "处理结论：本次申请未被支持。";
  if (appeal.decision === "refer_regrade" || appeal.status === "upheld_pending_regrade" || appeal.status === "resolved") return "处理结论：已支持复核，转入重评。";
  return "处理结论：等待学校复核。";
}

function reviewedScoreText(reviewedScore: number | undefined, reviewedMaxScore: number | undefined, sourceScore: number) {
  // 历史复核分缺一项就保留缺失说明，不能由当前成绩补齐；0 分不是缺失。
  if (reviewedScore === undefined || reviewedMaxScore === undefined) return "历史复核分暂不可读取，请核对学校回复";
  const change = reviewedScore === sourceScore ? "分数未变更" : "分数已变更";
  return `${formatScore(reviewedScore)} / ${formatScore(reviewedMaxScore)}（${change}）`;
}

export function AppealStatus({ examID, revision, result, onViewCurrent }: Readonly<{ examID: string; revision: number; result?: StudentResult; onViewCurrent?: (questionID?: string) => void }>) {
  const { reauthenticate, submitting } = useStudentSession();
  const [appeals, setAppeals] = useState<StudentQuestionAppeal[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [currentResult, setCurrentResult] = useState<StudentResult | undefined>(result);
  const [currentVerified, setCurrentVerified] = useState(false);
  const requestRef = useRef(0);
  const load = useCallback(async (_revision: number) => {
    const requestID = ++requestRef.current;
    setLoading(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await listQuestionAppeals(examID);
      if (requestID !== requestRef.current) return;
      setAppeals(response.appeals);
      if (response.appeals.length) {
        try {
          const current = await getResult(examID);
          if (requestID !== requestRef.current) return;
          setCurrentResult(current.result); setCurrentVerified(true);
        } catch (reason) { if (requestID === requestRef.current) { setCurrentVerified(false); setLoginRequired(sessionExpired(reason)); } }
      }
    } catch (error_) {
      if (requestID !== requestRef.current) return;
      setError(friendlyError(error_, "appeal"));
      setLoginRequired(sessionExpired(error_));
    } finally {
      if (requestID === requestRef.current) setLoading(false);
    }
  }, [examID]);
  useEffect(() => { void load(revision); }, [load, revision]);
  useEffect(() => () => { requestRef.current += 1; }, []);
  if (loading) return null;
  if (error) return <section className="appeal-status"><h2>我的复核申请</h2><p className="detail-error" role="alert">申请记录暂时无法加载。{error}<button type="button" className="text-button" disabled={submitting} onClick={() => void load(revision)}>重试</button>{loginRequired ? <button type="button" className="text-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}</p></section>;
  if (appeals.length === 0) return null;
  return <section className="appeal-status" id="my-question-appeals" aria-labelledby="appeal-status-title"><header><h2 id="appeal-status-title">我的复核申请</h2><button type="button" className="text-button" disabled={submitting} onClick={() => void load(revision)}>刷新</button></header>
    {currentVerified && currentResult ? <p className="muted">最近核对的当前生效成绩：第 {currentResult.release_version} 版。</p> : <p className="muted">当前生效成绩暂未核实，请重新刷新后查看。</p>}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}
    <div className="appeal-list">{appeals.map((appeal) => <article key={appeal.id}><div>
      <strong>第 {appeal.question_no} 题 · {appealReasonLabels[appeal.reason_code] ?? "复核申请"}</strong>
      <p>针对第 {appeal.source_release_version} 版 · 当时得分 {formatScore(appeal.source_score)} / {formatScore(appeal.source_max_score)} 分</p>
      {appeal.status === "resolved" ? <p>原得分：{formatScore(appeal.source_score)} / {formatScore(appeal.source_max_score)}；复核后得分：{reviewedScoreText(appeal.reviewed_score, appeal.reviewed_max_score, appeal.source_score)}。</p> : null}
      <p>{appealStatusLabel(appeal.status)} · 申请于 {formatDateTime(appeal.created_at)}</p>
      <p>{appealDecisionText(appeal)}</p>
      {appeal.public_response ? <p className="appeal-response">学校回复：{appeal.public_response}</p> : null}
      <p className="appeal-release-state">{appealReleaseState(appeal, currentVerified ? currentResult : undefined)}</p>
      {currentVerified && currentResult ? <p>本题当前成绩：{currentResult.questions?.find((item)=>item.question_id===appeal.question_id) ? formatScore(currentResult.questions.find((item)=>item.question_id===appeal.question_id)!.score) : "学校当前未提供可查看的本题分数"}</p> : null}
      {onViewCurrent ? <button type="button" className="text-button" disabled={submitting} onClick={()=>onViewCurrent(appeal.question_id)}>查看本题当前成绩</button> : null}
    </div><span className={`appeal-status-tag ${appeal.status}`}>{appealStatusLabel(appeal.status)}</span></article>)}</div></section>;
}

// 只有 release ID 与当前发布指针一致，才说明处理关联版本已在当前成绩中生效；版本号大小不能替代这个判断。
export function appealReleaseState(appeal: StudentQuestionAppeal, current?: StudentResult) {
  if (!appeal.new_release_id) return appeal.status === "upheld_pending_regrade" ? "重评尚未关联后续已发布成绩，请等待学校完成发布。" : "本次申请尚未关联后续成绩版本。";
  const version = appeal.new_release_version ? `第 ${appeal.new_release_version} 版` : "后续成绩版本";
  if (!current) return `已关联${version}；当前生效成绩暂未核实。`;
  if (appeal.new_release_id !== current.release_id) return `此复核结果已公布（${version}），当前生效成绩为第 ${current.release_version} 版，不是本次处理关联的发布。请以当前成绩为准。`;
  const question = current.questions?.find((item) => item.question_id === appeal.question_id);
  const scoreSuffix = question ? `，本题当前得分 ${formatScore(question.score)} / ${formatScore(question.max_score)} 分` : "";
  return `本次处理关联的${version}已是当前生效成绩${scoreSuffix}。`;
}

function appealStatusLabel(status: string) {
  switch (status) {
    case "submitted": return "学校复核中";
    case "under_review": return "学校复核中";
    case "rejected": return "未支持申请";
    case "upheld_pending_regrade": return "重新评分中";
    case "resolved": return "结果已公布";
    default: return "处理中";
  }
}

function EmptyState() {
  return <div className="empty-state">
    <p className="empty-state-kicker">当前状态</p>
    <h2>学校尚未发布你的成绩</h2>
    <p>学校正式发布成绩后，回到此页面或点击刷新即可检查新成绩，无需重复提交资料。</p>
    <ul>
      <li>如果老师还未通知发布，请等待学校完成阅卷与成绩确认。</li>
      <li>如果已经收到发布通知但仍看不到，请联系学校管理员核对学号绑定。</li>
    </ul>
  </div>;
}

function ErrorNotice({ message, onRetry, onBack, loginRequired }: Readonly<{ message: string; onRetry: () => void; onBack?: () => void; loginRequired?: boolean }>) {
  const { reauthenticate, submitting } = useStudentSession();
  return <div className="error-notice" role="alert"><strong>暂时无法显示内容</strong><p>{message}</p><div>{loginRequired ? <button className="secondary-button" type="button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : <button className="secondary-button" type="button" onClick={onRetry}>重试</button>}{onBack ? <button className="text-button" type="button" onClick={onBack}>返回</button> : null}</div></div>;
}

function formatScore(score: number) { return Number.isInteger(score) ? String(score) : score.toFixed(1); }
function formatDate(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long", day: "numeric" }).format(new Date(value)); }
function formatDateTime(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)); }
