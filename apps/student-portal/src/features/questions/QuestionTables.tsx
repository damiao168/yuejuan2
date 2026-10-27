// 题目列表和证据钻取共用当前成绩版本；迟到图片及批注响应不能解锁旧来源。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getQuestion, listQuestionAnnotations, PortalApiError, studentQuestionAnswerImageURL, type StudentQuestionAnnotation, type StudentQuestion, type StudentResult } from "../../api";
import { displaySubject, statisticsReason } from "../results/ResultOverview";
import { AnswerReview } from "./AnswerReview";
import { QuestionCohortScope } from "./QuestionCohortScope";
import { useStudentSession } from "../../studentSession";
import { friendlyError, sessionExpired, studentReleaseChanged } from "../../errors";
import { formatScore } from "../../formatting";
import { QuestionAppealForm } from "../appeals/QuestionAppealForm";

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

export function QuestionTables({ examID, result, questions, fallbackSubject, appealSelecting = false, requestedQuestionID, requestedQuestionToken = 0, onAppealSubmitted, onViewCurrent }: Readonly<{ examID: string; result: StudentResult; questions: StudentQuestion[]; fallbackSubject?: string; appealSelecting?: boolean; requestedQuestionID?: string; requestedQuestionToken?: number; onAppealSubmitted: () => void; onViewCurrent: () => void }>) {
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
