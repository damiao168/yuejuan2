// 题目列表与申诉核对共用当前成绩版本；迟到图片及批注响应不能解锁旧来源。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getQuestion, listQuestionAnnotations, PortalApiError, studentQuestionAnswerImageURL, type StudentQuestionAnnotation, type StudentQuestion, type StudentResult } from "../../api";
import { displaySubject, statisticsReason } from "../results/ResultOverview";
import { AnswerReview } from "./AnswerReview";
import { QuestionCohortScope } from "./QuestionCohortScope";
import { useStudentSession } from "../../studentSession";
import { friendlyError, sessionExpired, studentReleaseChanged } from "../../errors";
import { formatScore } from "../../formatting";
import { QuestionAppealForm } from "../appeals/QuestionAppealForm";
import { StudentText } from "./StudentText";

function QuestionStatistic({ label, value, unavailableReason }: Readonly<{ label: string; value?: number; unavailableReason?: string }>) {
  // 0 分是有效统计；缺失或非有限值不能拿本人得分、满分或其他范围的统计补齐。
  const available = typeof value === "number" && Number.isFinite(value);
  const reason = available ? undefined : unavailableReason ? statisticsReason(unavailableReason) : "暂无统计数据";
  return <span className="question-statistic-value" title={reason} aria-label={reason ? `${label}：${reason}` : undefined}>{available ? formatScore(value) : "—"}</span>;
}

// 答案直接展示；单题原卷只在申诉核对中读取，常规浏览沿用下方全卷预览。
function QuestionRow({ question, subject, located, onAppeal, showComparisons }: Readonly<{
  question: StudentQuestion;
  subject: string;
  located: boolean;
  onAppeal?: (question: StudentQuestion) => void;
  showComparisons: boolean;
}>) {
  const { submitting } = useStudentSession();
  // 新比较值随题目得分公开，不受旧 cohort 的人数和展示策略限制；仅旧接口响应沿用旧范围及原因。
  const comparison = question.score_comparison ?? question.cohort;
  const schoolUnavailableReason = question.score_comparison ? undefined : question.cohort_unavailable_reason;
  const classUnavailableReason = question.score_comparison ? undefined : question.cohort?.class_unavailable_reason ?? question.cohort_unavailable_reason;
  const lost = question.score < question.max_score;
  return (
    <tr className={`${lost ? "lost" : ""}${located ? " is-located" : ""}`}>
      <td data-label="题号">
        <span className="question-number" tabIndex={-1} data-question-row-id={question.question_id}
          aria-label={`第 ${question.question_no} 题，${formatScore(question.score)} / ${formatScore(question.max_score)} 分`}>第 {question.question_no} 题</span>
      </td>
      <td data-label="标准答案" className="question-answer"><StudentText>{answerText(question.correct_answer, "未提供或未公开", question.question_type, subject)}</StudentText></td>
      <td data-label="实际作答" className="question-answer actual-answer"><StudentText>{answerText(question.actual_answer, "暂无可显示的文字作答", question.question_type, subject)}</StudentText></td>
      <td data-label="得分" className="student-cell">
        <strong>{formatScore(question.score)}</strong><small> / {formatScore(question.max_score)}</small>
      </td>
      {showComparisons ? <><td data-label="班级最高分" className="question-statistic"><QuestionStatistic label="班级最高分" value={question.score_comparison?.class_max_score} unavailableReason={classUnavailableReason} /></td>
      <td data-label="全校最高分" className="question-statistic"><QuestionStatistic label="全校最高分" value={question.score_comparison?.school_max_score} unavailableReason={schoolUnavailableReason} /></td>
      <td data-label="班级平均分" className="question-statistic"><QuestionStatistic label="班级平均分" value={comparison?.class_mean_score} unavailableReason={classUnavailableReason} /></td>
      <td data-label="全校平均分" className="question-statistic"><QuestionStatistic label="全校平均分" value={comparison?.school_mean_score} unavailableReason={schoolUnavailableReason} /><QuestionCohortScope question={question} compact /></td></> : null}
      <td data-label="申诉" className="question-appeal-cell">{onAppeal ? <button type="button" className="text-button" data-question-id={question.question_id} disabled={submitting} onClick={() => onAppeal(question)}>申请本题复核</button> : <span className="muted">未开放</span>}</td>
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
  const [locatedQuestionID, setLocatedQuestionID] = useState<string>();
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
  const [showComparisons, setShowComparisons] = useState(true);
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
  const openQuestion = useCallback(async (question: StudentQuestion) => {
    if (submitting || isSubmitting?.() || !result.appeal_window.open) return;
    if (!selected) listPositionRef.current = window.scrollY;
    const requestId = ++requestRef.current;
    evidenceVerificationRef.current = { requestID: requestId, pending: false, changed: false };
    setEvidenceChecking(false);
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
  }, [examID, isSubmitting, markReleaseChanged, result.release_id, result.appeal_window.open, selected, submitting]);
  useEffect(() => {
    // 查看本题当前成绩只定位表格行；不能借旧链接重新打开已经移除的普通单题查看功能。
    if (submitting || isSubmitting?.() || !requestedQuestionID || requestedOpenedRef.current === `${requestedQuestionID}:${requestedQuestionToken}`) return;
    const question=questions.find((item)=>item.question_id===requestedQuestionID);
    if (!question) return;
    requestedOpenedRef.current=`${requestedQuestionID}:${requestedQuestionToken}`;
    // 定位的是列表中的当前成绩，先关闭旧申请并取消可能隐藏满分目标的筛选。
    requestRef.current += 1; setSelected(null); setDetail(null); setAppealQuestion(null); setDetailLoading(false); setLostOnly(false);
    const group=groups.find(([,items])=>items.some((item)=>item.question_id===question.question_id)); if(group) setActiveSubject(group[0]);
    setLocatedQuestionID(question.question_id);
  }, [requestedQuestionID, requestedQuestionToken, questions, groups, submitting, isSubmitting]);
  useEffect(() => {
    if (!locatedQuestionID || requestedOpenedRef.current !== `${locatedQuestionID}:${requestedQuestionToken}`) return;
    const group = groups.find(([, items]) => items.some(question => question.question_id === locatedQuestionID));
    if (groups.length > 1 && group?.[0] !== activeSubject) return;
    const row = [...(tablesRef.current?.querySelectorAll<HTMLElement>("[data-question-row-id]") ?? [])].find(item => item.dataset.questionRowId === locatedQuestionID);
    row?.scrollIntoView?.({ block: "nearest" }); row?.focus({ preventScroll: true });
  }, [locatedQuestionID, activeSubject, requestedQuestionToken, groups]);
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
    {showComparisons ? <p className="muted">统计范围：本版成绩中，本次考试本题已发布的成绩；班级统计只含本班，全校统计涵盖本校参试学生。</p> : null}
    <div className="question-filter"><label><input type="checkbox" disabled={submitting} checked={lostOnly} onChange={(event) => setLostOnly(event.target.checked)} /> 只看有失分的题目</label><label><input type="checkbox" checked={showComparisons} onChange={(event) => setShowComparisons(event.target.checked)} /> 显示比较数据</label><span>群体对比仅表示本次得分位置，不代表知识掌握程度。</span></div>
    {activeGroups.map(([subject, items]) => <section className="subject-table" key={subject}>
    <header><h2 id="questions-title">{subject}逐题分析</h2><span>学生得分：{formatScore(items.reduce((sum, item) => sum + item.score, 0))} 分　满分：{formatScore(items.reduce((sum, item) => sum + item.max_score, 0))} 分</span></header>
    <div className="paper-table-scroll"><table className={`paper-score-table${showComparisons ? " has-comparisons" : ""}`}>
      <thead><tr><th>题号</th><th>标准答案</th><th>实际作答</th><th>我的得分</th>{showComparisons ? <><th className="question-statistic">班级最高分</th><th className="question-statistic">全校最高分</th><th className="question-statistic">班级平均分</th><th className="question-statistic">全校平均分</th></> : null}<th className="question-appeal-cell">申诉</th></tr></thead>
      <tbody>{items.filter((question) => !lostOnly || question.score < question.max_score).map((question) => <QuestionRow key={question.question_id} question={question} subject={subject} showComparisons={showComparisons} located={locatedQuestionID === question.question_id} onAppeal={result.appeal_window.open ? (item) => {appealFocusRef.current=true;void openQuestion(item);} : undefined} />)}</tbody>
    </table></div>
    {lostOnly && !items.some((item) => item.score < item.max_score) ? <p><output>本学科没有失分题目，可以切换为查看全部题目。</output></p> : null}
    {selected && items.some((item) => item.question_id === selected.question_id) ? <div className="question-drilldown" ref={detailRegionRef}>
      <nav className="question-review-navigation" aria-label="连续复盘">
        <button type="button" className="secondary-button" disabled={submitting || !previousLost} onClick={() => previousLost && void openQuestion(previousLost)}>上一失分题</button>
        <button type="button" className="secondary-button" disabled={submitting || !nextLost} onClick={() => nextLost && void openQuestion(nextLost)}>下一失分题</button>
        <button type="button" className="text-button" disabled={submitting} onClick={returnToList}>返回题目列表</button>
      </nav>
      {(detailNeedsLogin || annotationNeedsLogin) ? <button type="button" className="secondary-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}
      <QuestionDrilldownContent evidenceChecking={evidenceChecking} isEvidenceBlocked={isEvidenceBlocked} onImageUnavailable={() => void verifyFailedImage(selected.question_id, selectedRequestID)} releaseChanged={releaseChanged} onSourceUnavailable={(reason) => { markReleaseChanged(reason, selectedRequestID); }} onViewCurrent={viewCurrent} examID={examID} result={result} selected={selected} detail={detail} appealQuestion={appealQuestion} detailLoading={detailLoading} detailError={detailError} annotationError={annotationError} annotations={annotations} onRetry={() => void openQuestion(selected)} onRetryAnnotations={() => void retryAnnotations()} onAppealSubmitted={onAppealSubmitted} />
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
  const showAppeal = result.appeal_window.open && appealQuestion?.question_id === selected.question_id;
  return <>
    <h3>第 {selected.question_no} 题 · 成绩复核申请</h3>
    {detailLoading ? <p className="muted"><output>正在加载本题…</output></p> : null}
    {detailError ? <p className="detail-error" role="alert">{detailError} <button type="button" className="text-button" disabled={submitting} onClick={releaseChanged ? onViewCurrent : onRetry}>{releaseChanged ? "查看当前成绩并重新核对" : "重试本题"}</button></p> : null}
    {annotationError ? <p role="alert">{annotationError} <button type="button" className="text-button" onClick={onRetryAnnotations}>重试批注</button></p> : null}
    {showAppeal && appealQuestion ? <QuestionAppealForm renderReview={detail?.question_id === selected.question_id ? (imageEditor) => <AnswerReview imageEditor={imageEditor} onImageUnavailable={onImageUnavailable} key={detail.question_id} question={detail} annotations={annotations} imageURL={studentQuestionAnswerImageURL(examID,detail.question_id, result.release_id)} /> : undefined} evidenceChecking={evidenceChecking} evidenceUnavailable={releaseChanged} isEvidenceBlocked={isEvidenceBlocked} onSourceUnavailable={onSourceUnavailable} onImageUnavailable={onImageUnavailable} key={`${result.release_id}:${appealQuestion.question_id}`} examID={examID} releaseID={result.release_id} releaseVersion={result.release_version} question={appealQuestion} allowedReasonCodes={result.appeal_window.allowed_reason_codes ?? []} onSubmitted={onAppealSubmitted} /> : null}
  </>;
}

function answerText(value: string | undefined, fallback: string, questionType?: string, subject?: string) {
  const text = value?.trim();
  if (!text) return fallback;
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "string") return mathChoiceText(parsed.trim(), questionType, subject) || fallback;
    if (subject === "数学" && ["single_choice", "multiple_choice", "fill_blank"].includes(questionType ?? "")) {
      if (parsed === null) return fallback;
      if (questionType === "fill_blank") {
        const blanks = numberedMathBlanks(parsed);
        if (blanks !== undefined) return blanks || fallback;
      }
    }
    // 数字数组也可能是数学区间；仅明确选择题可合并数字，不猜测未知对象里的答案字段。
    if (Array.isArray(parsed)) {
      const choice = questionType === "single_choice" || questionType === "multiple_choice";
      if (parsed.every(item => typeof item === "string" || choice && typeof item === "number" && Number.isFinite(item))) {
        return parsed.map(item => String(item).trim()).filter(Boolean).join("、") || fallback;
      }
      return text;
    }
    if (parsed !== null && typeof parsed === "object") return "暂无法直接显示此答案";
  } catch { /* 普通文本和数学公式不是JSON，按原文显示。 */ }
  return mathChoiceText(text, questionType, subject);
}

function mathChoiceText(text: string, questionType?: string, subject?: string) {
  // OMR多选可能记录为AC或A,C；只对明确的数学多选选项整理间隔，填空区间中的逗号保留原样。
  return subject === "数学" && questionType === "multiple_choice" && /^[A-H](?:[\s,，、;；]*[A-H])+$/.test(text)
    ? (text.match(/[A-H]/g) ?? []).join("、") : text;
}

function numberedMathBlanks(value: unknown): string | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const entry = Object.entries(value);
  // 只读取题目编辑和试卷识别支持的各空容器；裸数字数组可能是区间或向量，不能猜成多个空。
  if (entry.length !== 1 || !["blanks", "answers"].includes(entry[0][0]) || !Array.isArray(entry[0][1])) return undefined;
  const answers = entry[0][1] as unknown[];
  if (!answers.every(answer => answer == null || typeof answer === "string" || typeof answer === "number" && Number.isFinite(answer))) return undefined;
  // 空项也保留原序号，避免把后一个空的答案挪到前面；未记录不等于确认未作答。
  return answers.map((answer, index) => `第 ${index + 1} 空：${answer == null || typeof answer === "string" && !answer.trim() ? "未记录" : String(answer).trim()}`).join("\n");
}
