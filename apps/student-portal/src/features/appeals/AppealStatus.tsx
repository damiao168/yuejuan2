// 申诉状态依据当前发布指针解释生效版本，不用版本号大小推断结果已生效。
import { useCallback, useEffect, useRef, useState } from "react";
import { getResult, listQuestionAppeals, type StudentQuestionAppeal, type StudentResult } from "../../api";
import { useStudentSession } from "../../studentSession";
import { friendlyError, sessionExpired } from "../../errors";
import { formatDateTime, formatScore } from "../../formatting";
import { appealReasonLabels } from "./appealPresentation";

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
  if (appeal.decision === "reject" || appeal.status === "rejected") return "学校决定：未支持本次申请。";
  if (appeal.decision === "refer_regrade" || appeal.status === "upheld_pending_regrade" || appeal.status === "resolved") return "学校决定：支持复核，交由重新评分。";
  return null;
}

function reviewedScoreText(reviewedScore: number | undefined, reviewedMaxScore: number | undefined, sourceScore: number) {
  // 历史复核分缺一项就保留缺失说明，不能由当前成绩补齐；0 分不是缺失。
  if (reviewedScore === undefined || reviewedMaxScore === undefined) return "历史复核分暂不可读取，请核对学校回复";
  const change = reviewedScore === sourceScore ? "分数未变更" : "分数已变更";
  return `${formatScore(reviewedScore)} / ${formatScore(reviewedMaxScore)}（${change}）`;
}

export function AppealStatus({ examID, revision, result, verifiedCurrentResult, currentVerification, onViewCurrent }: Readonly<{ examID: string; revision: number; result?: StudentResult; verifiedCurrentResult?: StudentResult; currentVerification?: "verified" | "unknown"; onViewCurrent?: (questionID?: string) => void }>) {
  const { reauthenticate, submitting } = useStudentSession();
  const [appeals, setAppeals] = useState<StudentQuestionAppeal[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [currentResult, setCurrentResult] = useState<StudentResult | undefined>(result);
  const [currentVerified, setCurrentVerified] = useState(false);
  const requestRef = useRef(0);
  const hasSharedVerification = currentVerification !== undefined;
  const load = useCallback(async (_revision: number) => {
    const requestID = ++requestRef.current;
    setLoading(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await listQuestionAppeals(examID);
      if (requestID !== requestRef.current) return;
      setAppeals(response.appeals);
      if (response.appeals.length && !hasSharedVerification) {
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
  }, [examID, hasSharedVerification]);
  useEffect(() => { void load(revision); }, [load, revision]);
  useEffect(() => () => { requestRef.current += 1; }, []);
  const effectiveCurrent = currentVerification === undefined ? currentResult : verifiedCurrentResult;
  const effectiveVerified = currentVerification === undefined ? currentVerified : currentVerification === "verified";
  if (loading) return null;
  if (error) return <section className="appeal-status"><h2>我的复核申请</h2><p className="detail-error" role="alert">申请记录暂时无法加载。{error}<button type="button" className="text-button" disabled={submitting} onClick={() => void load(revision)}>重试</button>{loginRequired ? <button type="button" className="text-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}</p></section>;
  if (appeals.length === 0) return null;
  return <section className="appeal-status" id="my-question-appeals" aria-labelledby="appeal-status-title"><header><h2 id="appeal-status-title">我的复核申请</h2>{currentVerification === undefined ? <button type="button" className="text-button" disabled={submitting} onClick={() => void load(revision)}>刷新</button> : null}</header>
    {effectiveVerified && effectiveCurrent ? <p className="muted">最近核对的当前生效成绩：第 {effectiveCurrent.release_version} 版。</p> : <p className="muted">当前生效成绩暂未核实，请重新刷新后查看。</p>}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : null}
    <div className="appeal-list">{appeals.map((appeal) => <article key={appeal.id}><div>
      <strong>第 {appeal.question_no} 题 · {appealReasonLabels[appeal.reason_code] ?? "复核申请"}</strong>
      {appeal.status === "resolved" ? <p>原得分：{formatScore(appeal.source_score)} / {formatScore(appeal.source_max_score)}；复核后得分：{reviewedScoreText(appeal.reviewed_score, appeal.reviewed_max_score, appeal.source_score)}。</p> : null}
      {appeal.public_response ? <p className="appeal-response">学校回复：{appeal.public_response}</p> : null}
      <details><summary>处理详情</summary>
      <p>针对第 {appeal.source_release_version} 版 · 当时得分 {formatScore(appeal.source_score)} / {formatScore(appeal.source_max_score)} 分</p>
      <p>申请于 {formatDateTime(appeal.created_at)}</p>
      {appealDecisionText(appeal) ? <p>{appealDecisionText(appeal)}</p> : null}
      <p className="appeal-release-state">{appealReleaseState(appeal, effectiveVerified ? effectiveCurrent : undefined)}</p>
      {effectiveVerified && effectiveCurrent && appeal.new_release_id !== effectiveCurrent.release_id ? <p>本题当前成绩：{effectiveCurrent.questions?.find((item)=>item.question_id===appeal.question_id) ? formatScore(effectiveCurrent.questions.find((item)=>item.question_id===appeal.question_id)!.score) : "学校当前未提供可查看的本题分数"}</p> : null}
      </details>
      {onViewCurrent ? <button type="button" className="text-button" disabled={submitting} onClick={()=>onViewCurrent(appeal.question_id)}>查看本题当前成绩</button> : null}
    </div><span className={`appeal-status-tag ${appeal.status}`}>{appealStatusLabel(appeal.status)}</span></article>)}</div></section>;
}

// 只有 release ID 与当前发布指针一致，才说明处理关联版本已在当前成绩中生效；版本号大小不能替代这个判断。
export function appealReleaseState(appeal: StudentQuestionAppeal, current?: StudentResult) {
  if (!appeal.new_release_id) return appeal.status === "upheld_pending_regrade" ? "重评尚未关联后续已发布成绩，请等待学校完成发布。" : "本次申请尚未关联后续成绩版本。";
  const version = appeal.new_release_version ? `第 ${appeal.new_release_version} 版` : "后续成绩版本";
  if (!current) return `已关联${version}；当前生效成绩暂未核实。`;
  if (appeal.new_release_id !== current.release_id) return `复核结果已关联${version}；当前生效为第 ${current.release_version} 版，请查看当前成绩。`;
  const question = current.questions?.find((item) => item.question_id === appeal.question_id);
  const scoreSuffix = question ? `，本题当前得分 ${formatScore(question.score)} / ${formatScore(question.max_score)} 分` : "";
  return `本次复核关联的${version}已是当前生效成绩${scoreSuffix}。`;
}

export function appealStatusLabel(status: string) {
  switch (status) {
    case "submitted": return "学校复核中";
    case "under_review": return "学校复核中";
    case "rejected": return "未支持申请";
    case "upheld_pending_regrade": return "重新评分中";
    case "resolved": return "结果已公布";
    default: return "处理中";
  }
}
