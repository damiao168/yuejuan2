// 成绩详情维护当前 release 和刷新回执；发布变化后重新建立题目上下文。
import { useCallback, useEffect, useRef, useState } from "react";
import { getResult, type StudentResult } from "../../api";
import { ScoreHero } from "./ResultOverview";
import { PaperViewer } from "../questions/PaperViewer";
import { SubjectPerformance } from "./SubjectPerformance";
import { OldAppealDrafts } from "./OldAppealDrafts";
import { useStudentSession } from "../../studentSession";
import { friendlyError, sessionExpired } from "../../errors";
import { formatDateTime, formatScore } from "../../formatting";
import { ErrorNotice } from "../../components/PortalFeedback";
import { AppealStatus, AppealWindow } from "../appeals/AppealStatus";
import { QuestionTables } from "../questions/QuestionTables";

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

export function ResultDetail({ examID, onBack }: Readonly<{ examID: string; onBack: () => void }>) {
  const { submitting, isSubmitting, reauthenticate } = useStudentSession();
  const [appealRevision, setAppealRevision] = useState(0);
  const [result, setResult] = useState<StudentResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [pendingResult, setPendingResult] = useState<StudentResult>();
  const [checking, setChecking] = useState(false);
  const [verifiedCurrent, setVerifiedCurrent] = useState<StudentResult>();
  const [currentVerified, setCurrentVerified] = useState(false);
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
    setCurrentVerified(false);
    setAppealRevision(value => value + 1);
    setError("");
    setLoginRequired(false);
    try {
      const response = await getResult(examID);
      if (requestID !== detailRequestRef.current) return;
      setCheckedAt(new Date().toISOString());
      setVerifiedCurrent(response.result); setCurrentVerified(true);
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
        <div><p>最近核对：{checkedAt ? formatDateTime(checkedAt) : "尚未核实"}</p><button type="button" className="secondary-button" disabled={submitting || checking} onClick={() => void load()}>{checking ? "正在核对…" : "更新成绩与复核进度"}</button></div>
      </div>
      {error ? <p role="alert">未能核对最新成绩，现显示此前读取的第 {result.release_version} 版。{error}{loginRequired ? <button type="button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}</p> : null}
      {pendingResult ? <section className="appeal-notice"><p>第 {pendingResult.release_version} 版成绩已发布。旧草稿可复制；新版需重新核对后申请。</p><button type="button" disabled={submitting || checking} onClick={() => viewCurrent()}>查看新版成绩</button></section> : null}
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
      <AppealStatus examID={examID} revision={appealRevision} result={result} verifiedCurrentResult={verifiedCurrent} currentVerification={currentVerified ? "verified" : "unknown"} onViewCurrent={viewCurrent} />
    </section>
  );
}
