import { useGradingDraftConflict } from "./useGradingDraftConflict";
import type { UseGradingReviewActionsOptions } from "./gradingReviewActions.types";
export type { UseGradingReviewActionsOptions } from "./gradingReviewActions.types";
import { Modal } from "antd";
import { useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { removeReviewDraftFallback } from "../../../../auth/reviewDraftFallback";
import {
  returnReviewTask,
  submitHumanGrade,
  type AiGrade
} from "../../../../api/review";
import { isReviewTaskContextScopeCurrent, reviewTaskContextKeys } from "../../../../query/reviewTaskContext";
import type { ScoreDraft } from "../gradingWorkbench.types";
import { mathSuggestionState } from "../mathWorkbenchEvidence";
import { aiPointAdoption } from "../aiAdoption";
import { imageSuggestionState, isImageWorkbenchGrade } from "../workbenchReviewState";

export function useGradingReviewActions({
  queryScope,
  isCurrentScope,
  ctx,
  setCtx,
  draft,
  setDraft,
  draftConflict,
  canSubmit,
  canReturn,
  canEditDraft,
  canAdoptAiScore,
  canViewOriginalImage,
  currentUserId,
  selectedTaskId,
  selectedGrade,
  subjectCode,
  maxScore,
  rubricPoints,
  viewerMode,
  scale,
  rotation,
  offset,
  autoFit,
  math,
  scoreShortcuts,
  examScoring,
  runAction,
  refreshCurrent,
  refreshTaskAggregate,
  restoreViewer,
  setTasks,
  setDraftHydrated,
  setDraftSaveStatus,
  setDraftRevision,
  setDraftConflict,
  setResolvingConflict,
  setGoldPaperCandidate,
  setMathRequesting,
  contextRequestRef,
  activeTask,
  activeUserId,
  liveDraft,
  mathActionLock,
  conflictedDrafts,
  draftSaves,
  lastSavedDraft,
  conflictServerContext,
  prefetchedTaskRef,
  notify
}: UseGradingReviewActionsOptions) {
  const queryClient = useQueryClient();
  // 数学证据刷新期间，同一任务也可能收到当前失败或新建议；旧闭包不能把历史建议回填到草稿。
  const liveAdoption = useRef({ ctx, selectedGrade, canEditDraft, canAdoptAiScore, dirty: math.dirty });
  liveAdoption.current = { ctx, selectedGrade, canEditDraft, canAdoptAiScore, dirty: math.dirty };
  const isAdoptionCurrent = (grade: AiGrade) => {
    const live = liveAdoption.current;
    return live.ctx?.task.id === ctx?.task.id && !live.ctx?.reviewContext.ai_failure
      && live.selectedGrade?.id === grade.id && live.canEditDraft && live.canAdoptAiScore && !live.dirty;
  };
  // 同一账号切换考试或原图权限也会更换作用域；同考试内切任务仍允许更新原任务的提交结果。
  const isCurrentSession = () => isCurrentScope() && activeUserId.current === currentUserId && isReviewTaskContextScopeCurrent(queryScope);
  const { openDraftConflict, applyDraftConflict } = useGradingDraftConflict({
    ctx, currentUserId, draft, viewerMode, scale, rotation, offset, autoFit, setResolvingConflict, queryScope, prefetchedTaskRef, activeTask, activeUserId, canViewOriginalImage, conflictServerContext, setDraftConflict, notify, draftConflict, conflictedDrafts, draftSaves, setCtx, setDraftRevision, lastSavedDraft, setDraft, restoreViewer, setDraftSaveStatus, isCurrentSession
  });
  const submitGrade = async (nominateAsGold = false) => {
    if (!isCurrentSession()) return false;
    if (!ctx) {
      notify.error("请先选择任务");
      return false;
    }
    if (!canSubmit || draft.score === null) { notify.error("请先填写最终分，0分也需要明确输入"); return false; }
    const score = Number(draft.score);
    if (!Number.isFinite(score) || score < 0 || score > maxScore) {
      notify.error("最终分必须在 0 到题目满分之间");
      return false;
    }
    let confirmed = false;
    await runAction(
      "submit",
      async () => {
        if (!isCurrentSession()) return;
        const submittedTaskId = ctx.task.id;
        // 此入口由教师确认最终总分；不伪造采分点，也不再以自动提交绕过显式确认。
        const result = await submitHumanGrade(submittedTaskId, {
          score_only: true,
          expected_revision: ctx.reviewContext.expected_revision,
          score,
          rubric_selections: [],
          comments: draft.comments,
          private_note: draft.privateNote,
          student_feedback: draft.studentFeedback,
          reason: draft.reason || "教师复核完成"
        });
        // 查询缓存和任务队列属于当前阅卷作用域；迟到响应不能清掉或改写新考试的状态。
        if (!isCurrentSession()) return;
        confirmed = true;
        removeReviewDraftFallback(currentUserId, submittedTaskId);
        queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(queryScope, submittedTaskId) });
        prefetchedTaskRef.current.delete(submittedTaskId);
        const submittedTask = result.task ?? { ...ctx.task, status: "submitted" };
        setTasks((current) => current.map((task) => task.id === submittedTaskId ? { ...task, ...submittedTask } : task));

        // 只有提交回执确认成功后，调用方才能执行教师发起的“下一份”；失败须留在原题。
        const stillActive = activeTask.current === submittedTaskId;
        if (nominateAsGold && stillActive && result.human_grade) {
          setGoldPaperCandidate({
            examId: ctx.task.exam_id,
            questionId: ctx.task.question_id,
            questionNo: ctx.task.question_no,
            submissionId: ctx.task.submission_id,
            answerImageUrl: ctx.originalImageUrl ?? ctx.segmentImageUrl,
            referenceScore: score,
            maxScore,
            explanation: draft.comments,
            sourceGradeId: result.human_grade.id,
            rubricPoints: rubricPoints.map((point) => ({ id: point.id, description: point.description, score: point.score, required: point.required })),
            rubricSelections: { ...draft.rubricSelections }
          });
        }
        if (stillActive) {
          contextRequestRef.current += 1;
          setCtx(current => current?.task.id === submittedTaskId ? { ...current, task: submittedTask, reviewContext: { ...current.reviewContext, task: submittedTask,
            expected_revision: submittedTask.revision, human_grade: result.human_grade ?? current.reviewContext.human_grade, draft: null } } : current);
          setDraft(current => ({ ...current, score: result.human_grade?.score ?? score }));
          setDraftHydrated(false);
          setDraftSaveStatus("saved");
          lastSavedDraft.current = "";
        }
        await refreshTaskAggregate();
        if (!isCurrentSession()) return;
        await examScoring.loadSummary();
      },
      "人工评分已提交"
    );
    return confirmed && isCurrentSession();
  };

  const markDispute = async () => {
    if (!isCurrentSession() || !ctx) {
      return;
    }
    const reason = draft.disputeReason.trim() || "教师退回，需要重新评阅";
    await runAction(
      "return",
      async () => {
        if (!isCurrentSession()) return;
        await returnReviewTask(ctx.task.id, reason, ctx.task.revision);
        if (!isCurrentSession()) return;
        await refreshCurrent();
      },
      "已退回，等待重新安排教师"
    );
  };

  // 只同步填入分数草稿；数学路径必须先完成证据和本地评分快照核对。
  const applyAiScore = (grade: AiGrade) => {
    scoreShortcuts.rememberCurrent();
    const selections = aiPointAdoption(grade, rubricPoints, ctx?.question?.rubric?.version);
    // 采纳只更新分数与评分点；历史评语字段按原值保留，不能因入口删除而覆盖存量记录。
    setDraft((current) => {
      let adoption: Partial<Pick<ScoreDraft, "rubricSelections" | "reason">> = {};
      if (selections) {
        adoption = { rubricSelections: selections, reason: "" };
      } else if (rubricPoints.length) {
        adoption = { reason: `教师采用 AI 总分建议 ${grade.suggested_score} 分；未自动匹配冻结评分点。` };
      }
      return { ...current, score: grade.suggested_score, ...adoption };
    });
    notify.info(`已采纳 AI 建议 ${grade.suggested_score} 分`);
  };

  const adoptAiScore = async () => {
    if (!isCurrentSession() || activeTask.current !== ctx?.task.id) return;
    if (!selectedGrade) {
      notify.warning("当前任务没有 AI 建议分");
      return;
    }
    if (selectedGrade.delivery_mode === "shadow_only") {
      notify.warning("本场考试未开放 AI 建议，无法采纳");
      return;
    }
    if (!isAdoptionCurrent(selectedGrade)) return;
    if (!canEditDraft || !canAdoptAiScore) {
      notify.warning("当前建议未通过版本核对，请按原图人工判定或刷新证据。");
      return;
    }
    // 图像协议已经绑定当前裁图、题目与冻结细则；不能再要求它提供旧数学流水线的工件。
    if (isImageWorkbenchGrade(selectedGrade)) {
      if (imageSuggestionState(selectedGrade, liveAdoption.current.ctx).current) applyAiScore(selectedGrade);
      return;
    }
    if (subjectCode !== "mathematics") {
      applyAiScore(selectedGrade);
      return;
    }
    if (mathActionLock.current) return;
    const taskId = selectedTaskId;
    // 刷新证据期间教师仍可修改草稿；采纳建议前要同时确认任务、证据版本和本地评分快照仍未变化。
    const before = JSON.stringify({ score: draft.score, selections: draft.rubricSelections });
    mathActionLock.current = true;
    setMathRequesting(true);
    try {
      const evidence = await math.refresh();
      if (activeTask.current !== taskId || !isCurrentSession()) return;
      if (!isAdoptionCurrent(selectedGrade)) return;
      if (!evidence || !mathSuggestionState(selectedGrade, evidence, math.dirty).current) {
        notify.warning("数学证据或图片已更新，旧建议不可采纳。");
        return;
      }
      if (before !== JSON.stringify({ score: liveDraft.current.score, selections: liveDraft.current.rubricSelections })) {
        notify.warning("评分草稿已变化，请再次确认后采纳。");
        return;
      }
    } finally {
      if (isCurrentSession()) mathActionLock.current = false;
      if (activeTask.current === taskId && isCurrentSession()) setMathRequesting(false);
    }
    applyAiScore(selectedGrade);
  };

  const confirmReturnTask = (asException: boolean) => {
    if (!isCurrentSession() || !canReturn || !canEditDraft || !ctx) return;
    Modal.confirm({
      title: asException ? "标记异常并退回重评？" : "退回重新评阅？",
      content: asException
        ? "该答卷会进入重评流程，不会被丢弃。"
        : "该答卷会退出你的队列并进入重评流程。",
      okText: "确认退回",
      okButtonProps: { danger: true },
      cancelText: "取消",
      onOk: () => markDispute()
    });
  };

  return { submitGrade, markDispute, adoptAiScore, openDraftConflict, applyDraftConflict, confirmReturnTask };
}
