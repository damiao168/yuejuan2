import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError, getUserErrorMessage } from "../../../../api/client";
import { getMathRubricScore, getMathUnderstanding, type MathCorrectionResponse, type MathRubricScoreResponse, type MathUnderstandingResponse } from "../../../../api/mathUnderstanding";
import { isFormulaEvidenceSubject } from "../mathEvidenceSubjects";
import { mathScoreMatchesUnderstanding, type MathWorkbenchEvidence } from "../mathWorkbenchEvidence";

function applyMathRubricScore(next: MathWorkbenchEvidence, score: MathRubricScoreResponse, understanding: MathUnderstandingResponse): void {
  // 两次读取的版本不一致时保留理解投影供核对，但不能展示为可采纳评分。
  if (!mathScoreMatchesUnderstanding(score, understanding)) {
    next.phase = "conflict";
    next.message = "数学证据在读取期间已更新，请刷新。";
    return;
  }
  next.score = score;
  next.phase = understanding.artifact.stage === "verified" && !(understanding.correction_revision ?? 0) ? "ready" : "pending";
  next.message = next.phase === "pending" ? "校正/识别已保存，等待数学步骤重新验证。旧建议不可采纳。" : "";
}

function mathEvidenceFailure(cause: unknown): Pick<MathWorkbenchEvidence, "phase" | "message"> {
  if (cause instanceof ApiClientError && cause.status === 404) {
    return { phase: "unavailable", message: "暂无线性化数学证据，请按原图阅卷。" };
  }
  if (cause instanceof ApiClientError && cause.status === 409) {
    return { phase: "conflict", message: "答题图片或数学证据已变化，等待新证据或刷新。旧建议不可采纳。" };
  }
  return { phase: "unavailable", message: getUserErrorMessage(cause, "数学证据暂不可用，请按原图人工判定。") };
}

export function useMathWorkbenchEvidence(segmentId: string, subjectCode: string, snapshotId: string) {
  const enabled = isFormulaEvidenceSubject(subjectCode);
  const [state, setState] = useState<MathWorkbenchEvidence>({ segmentId, phase: "loading", understanding: null, score: null, message: "" });
  const [dirty, setDirty] = useState(false);
  const request = useRef(0);
  const abort = useRef<AbortController | null>(null);
  const active = useRef(segmentId);
  active.current = segmentId;

  const refresh = useCallback(async (): Promise<MathWorkbenchEvidence | null> => {
    if (!enabled || !segmentId) return null;
    const revision = ++request.current;
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    setState((current) => ({ ...current, phase: "loading", score: null, message: "" }));
    let next: MathWorkbenchEvidence = { segmentId, phase: "unavailable", understanding: null, score: null, message: "" };
    try {
      const understanding = await getMathUnderstanding(segmentId, controller.signal);
      if (understanding.artifact.answer_segment_id !== segmentId || understanding.artifact.subject_code !== subjectCode
        || understanding.artifact.exam_question_snapshot_id !== snapshotId
        || (understanding.effective_artifact && (understanding.effective_artifact.answer_segment_id !== segmentId
          || understanding.effective_artifact.exam_question_snapshot_id !== snapshotId || understanding.effective_artifact.subject_code !== subjectCode))) {
        throw new Error("数学证据与当前冻结题目不匹配。");
      }
      next.understanding = understanding;
      if (subjectCode === "mathematics") {
        const score = await getMathRubricScore(segmentId, controller.signal);
        applyMathRubricScore(next, score, understanding);
      } else {
        next.phase = "ready";
      }
    } catch (cause) {
      if (controller.signal.aborted) return null;
      // 评分读取失败时，已取得的理解投影仍保留，方便按原图核对。
      const failure = mathEvidenceFailure(cause);
      next.phase = failure.phase;
      next.message = failure.message;
    }
    if (revision !== request.current || active.current !== segmentId) return null;
    setState(next);
    return next;
  }, [enabled, segmentId, snapshotId, subjectCode]);

  useEffect(() => {
    setDirty(false);
    setState({ segmentId, phase: "loading", understanding: null, score: null, message: "" });
    void refresh();
    return () => { request.current += 1; abort.current?.abort(); };
  }, [refresh, segmentId]);

  // 只刷新当前挂载的任务，编辑草稿时暂停后台读取；
  // 定时器或焦点事件不会发起模型请求。
  useEffect(() => {
    if (!enabled || dirty) return;
    const onFocus = () => { if (document.visibilityState === "visible") void refresh(); };
    window.addEventListener("focus", onFocus);
    const timer = state.phase === "pending" || state.phase === "conflict" ? window.setInterval(onFocus, 10000) : undefined;
    return () => { window.removeEventListener("focus", onFocus); if (timer !== undefined) window.clearInterval(timer); };
  }, [dirty, enabled, refresh, state.phase]);

  const correctionSaved = useCallback((result: MathCorrectionResponse) => {
    if (active.current !== segmentId) return;
    setDirty(false);
    setState((current) => ({ ...current, phase: "pending", score: null, message: result.verification_status === "queued"
      ? "校正已保存，已进入数学验证队列。旧建议不可采纳。"
      : "校正已保存，但未确认重新验证任务已入队。请刷新检查，必要时联系管理员。" }));
    // 编辑器不要等待此刷新：刷新失败不能撤销已保存的更正。
    // 返回的调度状态会在编辑器中单独显示。
    void refresh();
  }, [refresh, segmentId]);

  return { enabled, state: state.segmentId === segmentId ? state : { segmentId, phase: "loading" as const, understanding: null, score: null, message: "" }, dirty, setDirty, refresh, correctionSaved };
}
