import { Modal } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import type { RefObject, Dispatch, SetStateAction } from "react";
import { ApiClientError } from "../../../../api/client";
import { createSubjectiveAiGrade } from "../../../../api/review";
import { isReviewTaskContextScopeCurrent, reviewTaskContextKeys, type ReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { mathSuggestionState, selectMathStep } from "../mathWorkbenchEvidence";
import type { MathStepSelection } from "../mathWorkbenchEvidence";
import { requiresExplicitSecondOpinion } from "../reviewContext";
import type { WorkbenchContext, ViewerMode } from "../gradingWorkbench.types";
import type { useMathWorkbenchEvidence } from "./useMathWorkbenchEvidence";

interface UseGradingMathActionsOptions {
  isCurrentScope: () => boolean;
  queryScope: ReviewTaskContextScope;
  ctx: WorkbenchContext | null;
  canGrade: boolean;
  canEditDraft: boolean;
  viewerMode: ViewerMode;
  math: ReturnType<typeof useMathWorkbenchEvidence>;
  activeTask: RefObject<string>;
  mathActionLock: RefObject<boolean>;
  setCtx: Dispatch<SetStateAction<WorkbenchContext | null>>;
  setMathRequesting: Dispatch<SetStateAction<boolean>>;
  setMathRequestNotice: Dispatch<SetStateAction<string>>;
  setMathSelection: Dispatch<SetStateAction<MathStepSelection | null>>;
  changeViewerMode: (mode: ViewerMode) => void;
  notifyInfo: (text: string) => void;
}

function mathSuggestionRequestErrorMessage(cause: unknown): string {
  // 人工确认错误码优先于 HTTP 409，不能把转人工误写成普通版本冲突。
  if (cause instanceof ApiClientError && cause.code === "math_human_review_required") {
    return "仍有未决评分点或风险，已转人工确认；未生成成功建议。";
  }
  if (cause instanceof ApiClientError && cause.status === 409) {
    return "数学证据已变化，本次建议未落库，请刷新后核对。";
  }
  return "数学建议服务暂不可用或未开放，请继续人工阅卷。";
}

export function useGradingMathActions({
  isCurrentScope,
  queryScope,
  ctx,
  canGrade,
  canEditDraft,
  viewerMode,
  math,
  activeTask,
  mathActionLock,
  setCtx,
  setMathRequesting,
  setMathRequestNotice,
  setMathSelection,
  changeViewerMode,
  notifyInfo
}: UseGradingMathActionsOptions) {
  const queryClient = useQueryClient();
  const isCurrentSession = () => isCurrentScope() && isReviewTaskContextScopeCurrent(queryScope);
  const refreshMath = () => {
    if (!isCurrentSession()) return;
    const taskId = ctx?.task.id;
    if (math.dirty) {
      Modal.confirm({ title: "刷新并放弃未保存的数学校正？", content: "教师评分草稿不会被重置。", onOk: async () => {
        if (!isCurrentSession() || activeTask.current !== taskId) return;
        math.setDirty(false);
        await math.refresh();
      } });
    } else void math.refresh();
  };

  const selectMathEvidenceStep = (stepId: string) => {
    if (!isCurrentSession()) return;
    if (!math.state.understanding || math.dirty || ["loading", "conflict", "unavailable"].includes(math.state.phase)) return;
    const selection = selectMathStep(math.state.understanding, stepId);
    if (!selection) { notifyInfo("该步骤暂无可靠位置，请按原图核对。"); return; }
    setMathSelection(selection);
    if (viewerMode !== "segment") changeViewerMode("segment");
  };

  const requestMathSuggestion = async () => {
    if (!isCurrentSession() || activeTask.current !== ctx?.task.id || !canGrade || !canEditDraft || requiresExplicitSecondOpinion(ctx.reviewContext) || math.dirty || math.state.phase !== "ready" || mathActionLock.current) return;
    const taskId = ctx.task.id, segmentId = ctx.task.answer_segment_id;
    mathActionLock.current = true;
    setMathRequesting(true);
    setMathRequestNotice("");
    try {
      // 生成前后重读证据；每次 await 后检查任务，防止旧任务的建议回填到新答卷。
      const evidence = await math.refresh();
      if (activeTask.current !== taskId || !isCurrentSession() || evidence?.phase !== "ready") return;
      const result = await createSubjectiveAiGrade(segmentId);
      if (activeTask.current !== taskId || !isCurrentSession()) return;
      const latest = await math.refresh();
      if (activeTask.current !== taskId || !isCurrentSession()) return;
      setCtx((current) => current?.task.id === taskId ? { ...current, aiGrades: [result.grade, ...current.aiGrades.filter((grade) => grade.id !== result.grade.id)] } : current);
      setMathRequestNotice(latest && mathSuggestionState(result.grade, latest).current ? "新版本数学建议已生成，仍需教师确认。" : "未生成可采纳的当前数学 v2 建议，请继续人工阅卷。");
      void queryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.task(queryScope, taskId) });
    } catch (cause) {
      if (activeTask.current !== taskId || !isCurrentSession()) return;
      setMathRequestNotice(mathSuggestionRequestErrorMessage(cause));
      await math.refresh();
    } finally {
      // 切考试会重置共享锁并允许新请求；旧请求结束不能解开新范围的数学操作锁。
      if (isCurrentSession()) mathActionLock.current = false;
      if (activeTask.current === taskId && isCurrentSession()) setMathRequesting(false);
    }
  };

  return { refreshMath, selectMathEvidenceStep, requestMathSuggestion };
}
