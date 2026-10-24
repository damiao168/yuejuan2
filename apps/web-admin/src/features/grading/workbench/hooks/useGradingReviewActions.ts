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
  const submitGrade = async (nominateAsGold = false, automatic = false) => {
    if (!isCurrentSession()) return;
    if (!ctx) {
      notify.error("请先选择任务");
      return;
    }
    if (!canSubmit || draft.score === null) { notify.error("请先填写最终分，0分也需要明确输入"); return; }
    if (!automatic && rubricPoints.length && Math.abs(draft.score - rubricPoints.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0)) > 0.001 && (!draft.reason.trim() || draft.reason === "教师复核完成")) {
      notify.error("最终分与评分细则合计不同，请填写人工调整原因"); return;
    }
    const score = Number(draft.score);
    if (!Number.isFinite(score) || score < 0 || score > maxScore) {
      notify.error("最终分必须在 0 到题目满分之间");
      return;
    }
    await runAction(
      "submit",
      async () => {
        if (!isCurrentSession()) return;
        const submittedTaskId = ctx.task.id;
        const selections = automatic ? [] : Object.entries(draft.rubricSelections)
          .filter(([, value]) => Number(value) > 0)
          .map(([point_id, value]) => ({ point_id, score: Number(value) }));
        const result = await submitHumanGrade(submittedTaskId, {
          ...(automatic ? { auto_submit: true } : {}),
          expected_revision: ctx.reviewContext.expected_revision,
          score,
          rubric_selections: selections,
          comments: draft.comments,
          private_note: draft.privateNote,
          student_feedback: draft.studentFeedback,
          reason: draft.reason || "教师复核完成"
        });
        // 查询缓存和任务队列属于当前阅卷作用域；迟到响应不能清掉或改写新考试的状态。
        if (!isCurrentSession()) return;
        removeReviewDraftFallback(currentUserId, submittedTaskId);
        queryClient.removeQueries({ queryKey: reviewTaskContextKeys.task(queryScope, submittedTaskId) });
        prefetchedTaskRef.current.delete(submittedTaskId);
        const submittedTask = result.task ?? { ...ctx.task, status: "submitted" };
        setTasks((current) => current.map((task) => task.id === submittedTaskId ? { ...task, ...submittedTask } : task));

        // 正式提交只更新原答卷，不再推进队列；上一份、下一份由教师明确选择。
        const stillActive = activeTask.current === submittedTaskId;
        if (nominateAsGold && stillActive) {
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
          setCtx(current => current?.task.id === submittedTaskId ? { ...current, task: submittedTask, reviewContext: { ...current.reviewContext, task: submittedTask, expected_revision: submittedTask.revision } } : current);
          setDraft(current => ({ ...current, score: result.human_grade.score ?? score }));
          setDraftHydrated(false);
          setDraftSaveStatus("saved");
          lastSavedDraft.current = "";
        }
        await refreshTaskAggregate();
        if (!isCurrentSession()) return;
        await examScoring.loadSummary();
      },
      automatic ? "" : "人工评分已提交"
    );
  };

  const markDispute = async () => {
    if (!isCurrentSession() || !ctx) {
      return;
    }
    const reason = draft.disputeReason.trim() || "教师标记争议，需要重新评阅";
    await runAction(
      "return",
      async () => {
        if (!isCurrentSession()) return;
        await returnReviewTask(ctx.task.id, reason, ctx.task.revision);
        if (!isCurrentSession()) return;
        await refreshCurrent();
      },
      "已标记争议并退回重评"
    );
  };

  // 只同步填入分数草稿；数学路径必须先完成证据和本地评分快照核对。
  const applyAiScore = (grade: AiGrade) => {
    scoreShortcuts.rememberCurrent();
    const selections = aiPointAdoption(grade, rubricPoints, ctx?.question?.rubric?.version);
    // 只采纳分数；学生反馈与内部备注必须由教师从独立入口审读后采用。
    setDraft((current) => {
      let adoption: Partial<Pick<ScoreDraft, "rubricSelections" | "reason">> = {};
      if (selections) {
        adoption = { rubricSelections: selections, reason: "" };
      } else if (rubricPoints.length) {
        adoption = { reason: `教师采用 AI 总分建议 ${grade.suggested_score} 分；未自动匹配冻结评分点，需人工核对。` };
      }
      return { ...current, score: grade.suggested_score, ...adoption };
    });
    notify.info(selections ? `已填入建议分 ${grade.suggested_score} 分及 ${rubricPoints.length} 个同版评分点；反馈未改变。`
      : `仅填入建议分 ${grade.suggested_score} 分；评分点和反馈未改变，请核对评分依据。`);
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

  const adoptAiFeedback = () => {
    if (!isCurrentSession() || activeTask.current !== ctx?.task.id || !selectedGrade || !canEditDraft || !canAdoptAiScore) return;
    if (!isAdoptionCurrent(selectedGrade)) return;
    // 该入口由展示全文的确认框调用，不把模型文字随分数一起默默提交。
    setDraft((current) => ({ ...current,
      studentFeedback: selectedGrade.student_feedback ?? current.studentFeedback,
      privateNote: selectedGrade.teacher_note ?? current.privateNote
    }));
    notify.info("AI 反馈已作为可编辑草稿填入，请在提交前审读学生可见文字。");
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

  return { submitGrade, markDispute, adoptAiScore, adoptAiFeedback, openDraftConflict, applyDraftConflict, confirmReturnTask };
}
