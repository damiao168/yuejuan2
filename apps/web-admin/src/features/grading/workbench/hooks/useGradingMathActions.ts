import { Modal } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useRef, type RefObject, type Dispatch, type SetStateAction } from "react";
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
  if (cause instanceof ApiClientError && cause.code === "ai_output_incomplete") {
    return "AI 建议未完成，请人工评分。";
  }
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
  // 同任务的上下文也会刷新；数学请求只能解除开始时已知的失败，不能覆盖等待期间收到的新失败。
  const liveContext = useRef(ctx);
  liveContext.current = ctx;
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
      const failureAdvanced = Boolean(liveContext.current?.reviewContext.ai_failure
        && liveContext.current.reviewContext.ai_failure !== ctx.reviewContext.ai_failure);
      const suggestionCurrent = Boolean(latest && mathSuggestionState(result.grade, latest).current && !failureAdvanced);
      setCtx((current) => {
        if (current?.task.id !== taskId) return current;
        const canReplaceFailure = suggestionCurrent && (!current.reviewContext.ai_failure
          || current.reviewContext.ai_failure === ctx.reviewContext.ai_failure);
        return {
          ...current,
          // 当前失败尚未被同版成功替代时，不把刚返回但已过期的建议重新展示为历史建议。
          aiGrades: current.reviewContext.ai_failure && !canReplaceFailure ? []
            : [result.grade, ...current.aiGrades.filter((grade) => grade.id !== result.grade.id)],
          // 新成功建议通过当前数学证据绑定后才移除失败提示；教师评分草稿始终由独立状态保留。
          reviewContext: canReplaceFailure ? { ...current.reviewContext, ai_failure: undefined } : current.reviewContext,
          warnings: canReplaceFailure ? current.warnings?.filter((warning) => warning !== "AI 建议未完成，请人工评分。") ?? [] : current.warnings
        };
      });
      setMathRequestNotice(suggestionCurrent ? "新版本数学建议已生成，仍需教师确认。" : "未生成可采纳的当前数学 v2 建议，请继续人工阅卷。");
      void queryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.task(queryScope, taskId) });
    } catch (cause) {
      if (activeTask.current !== taskId || !isCurrentSession()) return;
      if (cause instanceof ApiClientError && cause.code === "ai_output_incomplete") {
        // 同步错误只有“不完整”事实，不能猜成截断；刷新前先停用旧建议，保留所有人工输入。
        // output_incomplete 是 UI 临时归因，后续任务上下文的服务端诊断优先。
        setCtx((current) => current?.task.id === taskId ? {
          ...current,
          aiGrades: [],
          reviewContext: { ...current.reviewContext, ai_failure: { failure_kind: "output_incomplete" } },
          warnings: ["AI 建议未完成，请人工评分。", ...(current.warnings ?? []).filter((warning) => warning !== "AI 建议未完成，请人工评分。")]
        } : current);
        void queryClient.invalidateQueries({ queryKey: reviewTaskContextKeys.task(queryScope, taskId) });
      }
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
