import { FormEvent, PointerEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  studentPaperPageImageURL,
  type AuthUser,
  type PublishedExam,
  type StudentQuestionAppeal,
  type StudentQuestionAnnotation,
  type StudentQuestion,
  type SelectedAppealRegion,
  type StudentResult
} from "./api";
import { ScoreHero, displaySubject } from "./features/results/ResultOverview";
import { AnswerReview } from "./features/questions/AnswerReview";
import { SubjectPerformance } from "./features/results/SubjectPerformance";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";

type Page = { kind: "home" } | { kind: "exam"; examID: string };

// 只从 hash 读取考试 ID，解码失败时回到首页，避免坏链接阻塞学生端。
function readPage(): Page {
  const value = window.location.hash.replace(/^#/, "");
  const match = value.match(/^\/exams\/([^/?#]+)$/);
  if (match) {
    try {
      return { kind: "exam", examID: decodeURIComponent(match[1]) };
    } catch {
      return { kind: "home" };
    }
  }
  return { kind: "home" };
}

function goHome() { window.location.hash = "/"; }
function goExam(examID: string) { window.location.hash = `/exams/${encodeURIComponent(examID)}`; }

export default function App() {
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loadingUser, setLoadingUser] = useState(true);
  const [page, setPage] = useState<Page>(readPage);

  const restore = useCallback(async () => {
    setLoadingUser(true);
    try {
      const response = await currentUser();
      setUser(response.user);
    } catch {
      setUser(null);
    } finally {
      setLoadingUser(false);
    }
  }, []);

  useEffect(() => { void restore(); }, [restore]);
  useEffect(() => {
    const onChange = () => setPage(readPage());
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);

  if (loadingUser) {
    return <main className="portal-loading">正在确认登录状态…</main>;
  }
  if (!user) {
    return <LoginScreen onLoggedIn={setUser} />;
  }
  // 学生端同时校验角色和成绩读取权限；登录成功不代表可以查看成绩。
const eligible = user.roles.includes("student") && user.permissions.includes("student:grade:read");
  if (!eligible) {
    return <AccessDenied user={user} onLogout={() => void logout().finally(() => setUser(null))} />;
  }
  return (
    <div className="portal-shell">
      <header className="portal-header">
        <button className="brand" type="button" onClick={goHome} aria-label="回到我的考试">
          <span className="brand-mark">E</span>
          <span><strong>EduGrade</strong><small>学生端</small></span>
        </button>
        <div className="account-area">
          <button type="button" className="text-button" aria-pressed={readingSize === "large"} onClick={() => setReadingSize((size) => size === "large" ? "standard" : "large")}>{readingSize === "large" ? "标准字号" : "大字阅读"}</button>
          <span>{user.display_name || user.username}</span>
          <button type="button" className="text-button" onClick={() => void logout().finally(() => setUser(null))}>退出登录</button>
        </div>
      </header>
      <main className="portal-content">
        {page.kind === "home" ? <ExamList userName={user.display_name || user.username} onOpen={goExam} /> : <ResultDetail key={page.examID} examID={page.examID} onBack={goHome} />}
      </main>
    </div>
  );
}

function LoginScreen({ onLoggedIn }: { onLoggedIn: (user: AuthUser) => void }) {
  const [tenantCode, setTenantCode] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
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
        <form onSubmit={submit}>
          <label>学校代码<input required value={tenantCode} onChange={(event) => setTenantCode(event.target.value)} autoComplete="organization" /></label>
          <label>账号<input required value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" /></label>
          <label>密码<input required type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></label>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <button className="primary-button" disabled={submitting} type="submit">{submitting ? "正在登录…" : "登录"}</button>
        </form>
      </section>
    </main>
  );
}

function AccessDenied({ user, onLogout }: { user: AuthUser; onLogout: () => void }) {
  return (
    <main className="portal-loading access-denied">
      <h1>此账号不是学生账号</h1>
      <p>学生端只显示本人已发布的成绩。请使用学校发放的学生账号登录。</p>
      <p className="muted">当前账号：{user.display_name || user.username}</p>
      <button className="primary-button" type="button" onClick={onLogout}>退出登录</button>
    </main>
  );
}

function ExamList({ userName, onOpen }: { userName: string; onOpen: (examID: string) => void }) {
  const [items, setItems] = useState<PublishedExam[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await listPublishedExams();
      setItems([...response.exams].sort((a, b) => (Date.parse(b.published_at ?? "") || 0) - (Date.parse(a.published_at ?? "") || 0)));
    } catch (reason) {
      setError(friendlyError(reason));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const latest = items[0];
  const latestRate = latest?.max_score ? Math.round((latest.total_score ?? 0) / latest.max_score * 1000) / 10 : undefined;
  return (
    <section className="exam-list-page" aria-labelledby="exam-list-title">
      <div className="page-heading">
        <div><p className="eyebrow">学习概览</p><h1 id="exam-list-title">{greeting(userName)}</h1><p>从最近发布的考试开始复盘。</p></div>
        <button type="button" className="secondary-button" onClick={() => void load()} disabled={loading}>刷新</button>
      </div>
      {error ? <ErrorNotice message={error} onRetry={load} /> : null}
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
  const time = hour < 6 ? "夜深了" : hour < 11 ? "早上好" : hour < 14 ? "中午好" : hour < 18 ? "下午好" : "晚上好";
  return `${name}，${time}`;
}

function ResultDetail({ examID, onBack }: { examID: string; onBack: () => void }) {
  const [appealRevision, setAppealRevision] = useState(0);
  const [result, setResult] = useState<StudentResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await getResult(examID);
      setResult(response.result);
    } catch (reason) {
      setError(friendlyError(reason));
    } finally {
      setLoading(false);
    }
  }, [examID]);
  useEffect(() => { void load(); }, [load]);

  if (loading) return <div className="inline-status">正在加载成绩详情…</div>;
  if (error || !result) return <ErrorNotice message={error || "暂时无法取得成绩"} onRetry={load} onBack={onBack} />;
  const questionsVisible = Array.isArray(result.questions);
  const examName = result.exam?.name || "本次考试成绩";
  return (
    <section className="result-page" aria-labelledby="result-title">
      <button type="button" className="back-button" onClick={onBack}>← 返回我的考试</button>
      <div className="result-heading">
        <div><span className="release-state">已发布成绩 · 第 {result.release_version} 版</span><p className="eyebrow">成绩与逐题复盘</p><h1 id="result-title">{examName}</h1></div>
      </div>
      <ScoreHero result={result} />
      {result.subject_balance?.length ? <SubjectPerformance items={result.subject_balance} /> : null}
      <section className="question-section" aria-labelledby="questions-title" id="question-review">
        {questionsVisible && result.questions && result.questions.length > 0 ? <QuestionTables examID={examID} result={result} questions={result.questions} fallbackSubject={result.exam?.subject} onAppealSubmitted={() => setAppealRevision((value) => value + 1)} /> : null}
        {questionsVisible && result.questions?.length === 0 ? <p className="muted section-empty">学校暂未提供逐题结果。</p> : null}
      </section>
      <PaperViewer examID={examID} result={result} />
      <AppealWindow result={result} />
      <AppealStatus examID={examID} revision={appealRevision} />
    </section>
  );
}

function QuestionTables({ examID, result, questions, fallbackSubject, onAppealSubmitted }: { examID: string; result: StudentResult; questions: StudentQuestion[]; fallbackSubject?: string; onAppealSubmitted: () => void }) {
  const groups = useMemo(() => {
    const values = new Map<string, StudentQuestion[]>();
    for (const question of questions) {
      const subject = displaySubject(question.subject || fallbackSubject);
      values.set(subject, [...(values.get(subject) ?? []), question]);
    }
    return [...values.entries()].map(([subject, items]) => [subject, items.sort((a, b) => a.question_no.localeCompare(b.question_no, "zh-CN", { numeric: true }))] as const);
  }, [fallbackSubject, questions]);
  const [activeSubject, setActiveSubject] = useState(groups[0]?.[0] ?? "");
  const [selected, setSelected] = useState<StudentQuestion | null>(null);
  const [detail, setDetail] = useState<StudentQuestion | null>(null);
  const [annotations, setAnnotations] = useState<StudentQuestionAnnotation[]>([]);
  const [detailError, setDetailError] = useState("");
  const [detailLoading, setDetailLoading] = useState(false);
  const [annotationError, setAnnotationError] = useState("");
  const [lostOnly, setLostOnly] = useState(false);
  const detailRegionRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (selected) detailRegionRef.current?.scrollIntoView?.({ block: "nearest" }); }, [selected]);
  const requestRef = useRef(0);
  useEffect(() => () => { requestRef.current += 1; }, [examID]);
  const activeGroups = groups.filter(([subject]) => subject === activeSubject || groups.length === 1);
  const openQuestion = async (question: StudentQuestion, retry = false) => {
    const requestId = ++requestRef.current;
    if (!retry && selected?.question_id === question.question_id) { setSelected(null); setDetail(null); setDetailLoading(false); return; }
    setSelected(question); setDetail(null); setAnnotations([]); setAnnotationError(""); setDetailError(""); setDetailLoading(true);
    try {
      const [questionResponse, annotationResponse] = await Promise.allSettled([
        getQuestion(examID, question.question_id),
        listQuestionAnnotations(examID, question.question_id)
      ]);
      if (requestId !== requestRef.current) return;
      if (questionResponse.status === "rejected") throw questionResponse.reason;
      if (questionResponse.value.question.question_id !== question.question_id) throw new Error("题目详情与当前题号不一致，请重新加载");
      setDetail(questionResponse.value.question);
      if (annotationResponse.status === "fulfilled") setAnnotations(annotationResponse.value.annotations);
      else setAnnotationError("教师批注暂时无法加载，当前显示的评分详情仍可查看。");
    } catch (reason) { if (requestId === requestRef.current) setDetailError(friendlyError(reason)); }
    finally { if (requestId === requestRef.current) setDetailLoading(false); }
  };
  return <div className="subject-tables">
    {groups.length > 1 ? <nav className="subject-tabs" aria-label="科目">{groups.map(([subject]) => <button type="button" aria-pressed={activeSubject === subject} className={activeSubject === subject ? "active" : ""} onClick={() => { requestRef.current += 1; setActiveSubject(subject); setSelected(null); setDetail(null); }} key={subject}>{subject}</button>)}</nav> : null}
    <div className="question-filter"><label><input type="checkbox" checked={lostOnly} onChange={(event) => setLostOnly(event.target.checked)} /> 只看有失分的题目</label><span>群体对比仅表示本次得分位置，不代表知识掌握程度。</span></div>
    {activeGroups.map(([subject, items]) => <section className="subject-table" key={subject}>
    <header><h2 id="questions-title">{subject}逐题分析</h2><span>学生得分：{formatScore(items.reduce((sum, item) => sum + item.score, 0))} 分　满分：{formatScore(items.reduce((sum, item) => sum + item.max_score, 0))} 分</span></header>
    <div className="paper-table-scroll"><table className="paper-score-table">
      <thead><tr><th>题号</th><th>正确答案</th><th>实际答案</th><th>得分</th><th>班级平均分</th><th>学校平均分</th><th>群体中位分对比</th></tr></thead>
      <tbody>{items.filter((question) => !lostOnly || question.score < question.max_score).map((question) => {
        const known = typeof question.cohort?.median_score === "number" && Number.isFinite(question.cohort.median_score);
        const mastered = known && question.score >= Number(question.cohort?.median_score);
        const lost = question.score < question.max_score;
        return <tr key={question.question_id} className={lost ? "lost" : ""}>
          <td data-label="题目"><button type="button" className="question-cell-link" aria-expanded={selected?.question_id === question.question_id} aria-label={`第 ${question.question_no} 题，${formatScore(question.score)} / ${formatScore(question.max_score)} 分`} onClick={() => void openQuestion(question)}><span>第 {question.question_no} 题</span><small>{selected?.question_id === question.question_id ? "收起详情" : "查看作答与评分"}</small></button></td><td data-label="正确答案" title={question.correct_answer}>{answerText(question.correct_answer)}</td><td data-label="实际答案" className="actual-answer" title={question.actual_answer}>{answerText(question.actual_answer)}</td>
          <td data-label="得分" className="student-cell"><strong>{formatScore(question.score)}</strong><small> / {formatScore(question.max_score)}</small></td>
          <td data-label="班级平均分">{scoreOrDash(question.cohort?.class_mean_score)}</td><td data-label="学校平均分">{scoreOrDash(question.cohort?.school_mean_score)}</td>
          <td data-label="群体对比"><span className={`mastery ${known ? (mastered ? "mastered" : "review") : "unknown"}`}>{known ? (mastered ? "达到群体中位分" : "低于群体中位分") : "暂无对比数据"}</span></td>
        </tr>;
      })}</tbody>
    </table></div>
    {lostOnly && !items.some((item) => item.score < item.max_score) ? <p role="status">本学科没有失分题目，可以切换为查看全部题目。</p> : null}
    {selected && items.some((item) => item.question_id === selected.question_id) ? <div className="question-drilldown" ref={detailRegionRef}>
      <h3>第 {selected.question_no} 题 · 作答与评分依据</h3>
      {detailLoading ? <p className="muted" role="status">正在加载本题…</p> : null}
      {detailError ? <p className="detail-error" role="alert">{detailError} <button type="button" className="text-button" onClick={() => void openQuestion(selected, true)}>重新加载</button></p> : null}
      {annotationError ? <p role="alert">{annotationError} <button type="button" className="text-button" onClick={() => void openQuestion(selected, true)}>重试批注</button></p> : null}
      {detail && detail.question_id === selected.question_id ? <><AnswerReview key={detail.question_id} question={detail} annotations={annotations} imageURL={studentQuestionAnswerImageURL(examID,detail.question_id)} />{result.appeal_window.open ? <QuestionAppealForm key={`${result.release_id}:${detail.question_id}`} examID={examID} releaseID={result.release_id} releaseVersion={result.release_version} question={detail} allowedReasonCodes={result.appeal_window.allowed_reason_codes ?? []} onSubmitted={onAppealSubmitted} /> : null}</> : null}
    </div> : null}
  </section>)}</div>;
}

function answerText(value?: string) { return value?.trim() || "—"; }
function scoreOrDash(value?: number) { return value === undefined ? "—" : formatScore(value); }

function PaperViewer({ examID, result }: { examID: string; result: StudentResult }) {
  const ownPages = useMemo(() => result.paper_pages?.length ? result.paper_pages : fallbackPaperPages(result.questions ?? []), [result.paper_pages, result.questions]);
  const highPages = result.high_score_paper?.pages ?? [];
  const [highScore, setHighScore] = useState(false);
  const [pageIndex, setPageIndex] = useState(0);
  const [zoom, setZoom] = useState(1);
  const [annotations, setAnnotations] = useState<StudentQuestionAnnotation[]>([]);
  const pages = highScore ? highPages : ownPages;
  const page = pages[Math.min(pageIndex, Math.max(0, pages.length - 1))];
  const pageQuestions = (result.questions ?? []).filter((question) => question.page_no === page?.page_no);
  const scoreMarks = highScore ? (result.high_score_paper?.score_marks ?? []).filter((mark) => mark.page_no === page?.page_no) : pageQuestions;

  useEffect(() => {
    setPageIndex(0);
    setAnnotations([]);
  }, [highScore]);
  useEffect(() => {
    let active = true;
    if (!page || highScore) { setAnnotations([]); return () => { active = false; }; }
    void Promise.all(pageQuestions.map((question) => listQuestionAnnotations(examID, question.question_id).then((response) => response.annotations).catch(() => [])))
      .then((values) => { if (active) setAnnotations(values.flat()); });
    return () => { active = false; };
  }, [examID, highScore, page?.page_no]);

  return <section className="paper-viewer" aria-labelledby="paper-viewer-title">
    <header><h2 id="paper-viewer-title">答卷图像</h2></header>
    <div className="paper-toolbar">
      <div className="zoom-controls"><button type="button" onClick={() => setZoom((value) => Math.min(1.6, value + .15))}>放大</button><button type="button" onClick={() => setZoom(1)}>默认</button><button type="button" onClick={() => setZoom((value) => Math.max(.7, value - .15))}>缩小</button></div>
      <div className="page-controls"><button type="button" disabled={pageIndex <= 0} onClick={() => setPageIndex((value) => value - 1)}>上一页</button><label>第 <select value={pageIndex} onChange={(event) => setPageIndex(Number(event.target.value))}>{pages.map((item, index) => <option key={`${item.page_no}-${index}`} value={index}>{item.page_no}</option>)}</select> 页</label><button type="button" disabled={pageIndex >= pages.length - 1} onClick={() => setPageIndex((value) => value + 1)}>下一页</button></div>
      <div className="paper-actions"><button type="button" className={highScore ? "high-score active" : "high-score"} disabled={!result.high_score_paper?.available} onClick={() => setHighScore((value) => !value)}>{highScore ? "查看我的试卷" : "查看高分试卷"}</button>{page ? <a href={studentPaperPageImageURL(examID, page.question_id, highScore)} download>下载当前页</a> : null}</div>
    </div>
    <div className="paper-stage">
      {page ? <div className="paper-page" style={{ width: `${zoom * 100}%` }}>
        <img src={studentPaperPageImageURL(examID, page.question_id, highScore)} alt={`${highScore ? "高分" : "本人"}试卷第 ${page.page_no} 页`} />
        {scoreMarks.map((question) => question.answer_geometry ? <span className={`paper-score-mark ${question.score < question.max_score ? "lost" : ""}`} key={question.question_id} style={{ left: `${Math.min(.96, question.answer_geometry.x + question.answer_geometry.width) * 100}%`, top: `${question.answer_geometry.y * 100}%` }}>{question.question_no}：{formatScore(question.score)}分（满分{formatScore(question.max_score)}分）</span> : null)}
        {!highScore ? annotations.map((annotation) => <span className={`paper-annotation ${annotation.type}`} key={annotation.id} title={annotation.content} style={{ left: `${annotation.geometry.x * 100}%`, top: `${annotation.geometry.y * 100}%`, width: `${annotation.geometry.width * 100}%`, height: `${annotation.geometry.height * 100}%` }}><span className="paper-annotation-text">{annotation.content}</span></span>) : null}
      </div> : <div className="paper-empty">暂无答卷图像</div>}
    </div>
  </section>;
}

function fallbackPaperPages(questions: StudentQuestion[]) {
  const byPage = new Map<number, { page_no: number; question_id: string; submission_page_id?: string }>();
  for (const question of questions) {
    if (question.page_no && !byPage.has(question.page_no)) byPage.set(question.page_no, { page_no: question.page_no, question_id: question.question_id, submission_page_id: question.submission_page_id });
  }
  return [...byPage.values()].sort((a, b) => a.page_no - b.page_no);
}

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

export function QuestionAppealForm({ examID, releaseID, releaseVersion, question, allowedReasonCodes, onSubmitted }: {
  examID: string;
  releaseID: string;
  releaseVersion: number;
  question: StudentQuestion;
  allowedReasonCodes: string[];
  onSubmitted: () => void;
}) {
  const availableReasons = allowedReasonCodes.filter((code) => appealReasonLabels[code]);
  const [reasonCode, setReasonCode] = useState(availableReasons[0] ?? "");
  const [reason, setReason] = useState("");
  const [selectedRegion, setSelectedRegion] = useState<SelectedAppealRegion | undefined>();
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [existingAppeal, setExistingAppeal] = useState<StudentQuestionAppeal | null>(null);

  useEffect(() => {
    let active = true;
    void listQuestionAppeals(examID).then((response) => {
      if (active) setExistingAppeal(response.appeals.find((appeal) => appeal.source_release_id === releaseID && appeal.question_id === question.question_id) ?? null);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [examID, question.question_id, releaseID]);

  if (existingAppeal) {
    return <div className="question-appeal-confirmation" role="status"><strong>{appealStatusLabel(existingAppeal.status)}</strong><span>本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。</span></div>;
  }
  if (availableReasons.length === 0) {
    return null;
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      const response = await createQuestionAppeal(examID, {
        source_release_id: releaseID,
        question_id: question.question_id,
        reason_code: reasonCode,
        reason: reason.trim(),
        ...(selectedRegion ? { selected_region: selectedRegion } : {})
      });
      setExistingAppeal(response.appeal);
      onSubmitted();
    } catch (failure) {
      setError(friendlyError(failure));
    } finally {
      setSubmitting(false);
    }
  };
  return <form className="question-appeal-form" onSubmit={submit}>
    <h3>申请复核</h3>
    <p>每道题只能提交一次。学校将按第 {releaseVersion} 版成绩核对原卷和评分标准。</p>
    <AppealRegionSelector
      imageURL={studentQuestionAnswerImageURL(examID, question.question_id)}
      value={selectedRegion}
      onChange={setSelectedRegion}
    />
    <label>复核原因<select value={reasonCode} onChange={(event) => setReasonCode(event.target.value)}>{availableReasons.map((code) => <option key={code} value={code}>{appealReasonLabels[code]}</option>)}</select></label>
    <label>具体说明<textarea required minLength={10} maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} placeholder="请具体说明哪一步、哪一评分点或哪段识别内容需要复核（10—500字）" /></label>
    {error ? <p className="detail-error" role="alert">{error}</p> : null}
    <button className="secondary-button" type="submit" disabled={submitting || reason.trim().length < 10}>{submitting ? "正在提交…" : "确认提交（仅一次）"}</button>
  </form>;
}

function AppealRegionSelector({ imageURL, value, onChange }: {
  imageURL: string;
  value?: SelectedAppealRegion;
  onChange: (value: SelectedAppealRegion | undefined) => void;
}) {
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
    if (imageState !== "ready") return;
    if (pointMode) {
      if (!start) setStart(point(event));
      else { const region = regionFrom(start, point(event)); setStart(null); if (region.width >= .01 && region.height >= .01) onChange(region); }
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    setStart(point(event));
  };
  const finish = (event: PointerEvent<HTMLDivElement>) => {
    if (pointMode) return;
    if (!start) return;
    const region = regionFrom(start, point(event));
    setStart(null);
    if (region.width >= 0.01 && region.height >= 0.01) onChange(region);
  };
  return <section className="appeal-region-selector" aria-labelledby="appeal-region-title">
    <div><h4 id="appeal-region-title">圈选争议区域（可选）</h4><p>可拖动框选，也可依次点击区域的两个对角；未圈选也可以提交。</p></div>
    <label><input type="checkbox" checked={pointMode} onChange={(event) => { setPointMode(event.target.checked); setStart(null); }} /> 使用两点点击框选</label>
    {pointMode && start ? <p role="status">起点已选，请点击区域的另一角。</p> : null}
    <div className="appeal-region-image-wrap">
      <img src={imageURL} alt="本题答题区域" onLoad={() => setImageState("ready")} onError={() => setImageState("unavailable")} />
      {imageState === "ready" ? <div
        className="appeal-region-canvas"
        onPointerDown={begin}
        onPointerUp={finish}
        onPointerCancel={() => setStart(null)}
        aria-label="在答题图上框选争议区域"
        role="application"
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

export function AppealWindow({ result }: { result: StudentResult }) {
  const windowState = result.appeal_window;
  if (windowState.open) {
    return <section className="appeal-notice open"><div><h2>成绩复核</h2><p>本次成绩的复核窗口已开启{windowState.closes_at ? `，截止至 ${formatDateTime(windowState.closes_at)}` : ""}。请在需要复核的题目下提交申请。</p></div><span>对应第 {result.release_version} 版成绩</span></section>;
  }
  return <section className="appeal-notice"><div><h2>成绩复核</h2><p>{windowState.closes_at ? `本次成绩的复核窗口已于 ${formatDateTime(windowState.closes_at)} 结束。` : "学校未开放本次成绩的复核窗口。"}</p></div></section>;
}

export function AppealStatus({ examID, revision }: { examID: string; revision: number }) {
  const [appeals, setAppeals] = useState<StudentQuestionAppeal[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await listQuestionAppeals(examID);
      setAppeals(response.appeals);
    } catch (failure) {
      setError(friendlyError(failure));
    } finally {
      setLoading(false);
    }
  }, [examID]);
  useEffect(() => { void load(); }, [load, revision]);
  if (loading) return null;
  if (error) return <section className="appeal-status"><h2>我的复核申请</h2><p className="detail-error">申请记录暂时无法加载。<button type="button" className="text-button" onClick={() => void load()}>重试</button></p></section>;
  if (appeals.length === 0) return null;
  return <section className="appeal-status" aria-labelledby="appeal-status-title"><header><h2 id="appeal-status-title">我的复核申请</h2><button type="button" className="text-button" onClick={() => void load()}>刷新</button></header><div className="appeal-list">{appeals.map((appeal) => <article key={appeal.id}><div><strong>{appeal.question_no} · {appealReasonLabels[appeal.reason_code] ?? "复核申请"}</strong><p>{appealStatusLabel(appeal.status)} · 申请于 {formatDateTime(appeal.created_at)}</p>{appeal.public_response ? <p className="appeal-response">处理结果：{appeal.public_response}</p> : null}</div><span className={`appeal-status-tag ${appeal.status}`}>{appealStatusLabel(appeal.status)}</span></article>)}</div></section>;
}

function appealStatusLabel(status: string) {
  switch (status) {
    case "submitted": return "已提交";
    case "under_review": return "复核中";
    case "rejected": return "已答复";
    case "upheld_pending_regrade": return "正在重评";
    case "resolved": return "已处理";
    default: return "处理中";
  }
}

function EmptyState() {
  return <div className="empty-state">
    <p className="empty-state-kicker">当前状态</p>
    <h2>学校尚未发布你的成绩</h2>
    <p>成绩由学校正式发布后会自动出现在这里，不需要重复提交或刷新。</p>
    <ul>
      <li>如果老师还未通知发布，请等待学校完成阅卷与成绩确认。</li>
      <li>如果已经收到发布通知但仍看不到，请联系学校管理员核对学号绑定。</li>
    </ul>
  </div>;
}

function ErrorNotice({ message, onRetry, onBack }: { message: string; onRetry: () => void; onBack?: () => void }) {
  return <div className="error-notice" role="alert"><strong>暂时无法显示内容</strong><p>{message}</p><div><button className="secondary-button" type="button" onClick={onRetry}>重试</button>{onBack ? <button className="text-button" type="button" onClick={onBack}>返回</button> : null}</div></div>;
}

function friendlyError(reason: unknown) {
  if (reason instanceof PortalApiError) {
    if (reason.status === 401) return "登录状态已失效，请重新登录。";
    if (reason.status === 403 && reason.code === "student_score_scope_required") return "学生账号尚未关联本人学籍，请联系学校管理员核对账号与学号。";
    if (reason.status === 403) return "你无权查看此内容。";
    if (reason.status === 404) return "学校尚未发布这场考试的成绩。";
  }
  return "暂时无法连接服务，请稍后重试。";
}

function formatScore(score: number) { return Number.isInteger(score) ? String(score) : score.toFixed(1); }
function formatDate(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long", day: "numeric" }).format(new Date(value)); }
function formatDateTime(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)); }
