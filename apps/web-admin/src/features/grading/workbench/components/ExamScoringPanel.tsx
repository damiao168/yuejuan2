import { useEffect, useState } from "react";
import { Button, Modal } from "antd";
import type { ReviewTask } from "../../../../api/review";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { OcrWorkerAlert } from "../../../../components/OcrWorkerAlert";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { ExamScoringImageDialog, ExamScoringPlanDialog, ExamScoringResultsDrawer, ExamScoringResultsView } from "./ExamScoringDialogs";
import { ExamScoringOverview } from "./ExamScoringOverview";
import { GradingSettingsView } from "./GradingSettingsView";
import type { ScoringReviewFilter } from "../scoringReviewCategories";
import { FastConfirmDrawer } from "./FastConfirmDrawer";
import { TrialScoringDrawer } from "./TrialScoringDrawer";
import { GradingAIPlanSettings } from "./GradingAIPlanSettings";

export interface ExamScoringPanelProps {
  initialExamId: string;
  canGrade: boolean;
  canWork: boolean;
  canManageTasks: boolean;
  currentQuestionId: string;
  scoring: ExamScoringController;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
  tasks: ReviewTask[];
  currentUserId: string;
  onTasksChanged: () => Promise<void>;
  onOpenAssignments?: () => void;
  showOverview?: boolean;
  showFastConfirmEntry?: boolean;
  showResults?: boolean;
  showSettings?: boolean;
  onOpenResults?: (filter?: ScoringReviewFilter) => void;
}

export function ExamScoringPanel({ initialExamId, canGrade, canWork, canManageTasks, currentQuestionId, scoring, onOpenGoldPapers, onOpenCalibration, tasks, currentUserId, onTasksChanged, onOpenAssignments, showOverview = true, showFastConfirmEntry = true, showResults = false, showSettings = false, onOpenResults }: Readonly<ExamScoringPanelProps>) {
  const fallbackQuestionId = scoring.summary?.questions[0]?.question_id ?? "";
  const session = getBusinessCommandSession();
  const queueExamId = initialExamId || tasks.find((task) => task.assigned_to === currentUserId)?.exam_id || "";
  const hasOwnWork = tasks.some(task => task.assigned_to === currentUserId && task.exam_id === queueExamId && ["assigned", "in_progress", "returned"].includes(task.status));
  const [lastExam, setLastExam] = useState({ session, userId: currentUserId, examId: queueExamId, eligible: hasOwnWork });
  const trialRun = scoring.summary?.run;
  const trialScope = `${session.scope}:${session.generation}:${currentUserId}:${initialExamId}:${trialRun?.id ?? ""}`;
  const [trialOpenFor, setTrialOpenFor] = useState("");
  const [aiPlanOpenFor, setAIPlanOpenFor] = useState<{ scope: string; returnToStart: boolean } | null>(null);
  const aiPlanScope = `${session.scope}:${session.generation}:${initialExamId}`;
  const aiPlanBlocksStart = scoring.blockingChecks.some(check => ["grading_ai_plan_confirmation_required", "grading_ai_threshold_required"].includes(check.code));
  const closeAIPlan = () => {
    setAIPlanOpenFor(null);
    // 概览也能在未生成评分路线时配置方案；只有从启动窗口进入才返回原窗口。
    if (aiPlanOpenFor?.returnToStart) scoring.setPlanOpen(true);
  };
  const sameSession = lastExam.session === session && lastExam.userId === currentUserId;
  // 最后一份提交会清空队列；同一会话仍保留考试和回执，换人或重登后不能沿用。
  const fastConfirmExamId = queueExamId || (sameSession ? lastExam.examId : "");
  useEffect(() => {
    setLastExam(previous => {
      const examId = queueExamId || (previous.session === session && previous.userId === currentUserId ? previous.examId : "");
      const eligible = hasOwnWork || (previous.session === session && previous.userId === currentUserId && previous.examId === examId && previous.eligible);
      return previous.session === session && previous.userId === currentUserId && previous.examId === examId && previous.eligible === eligible ? previous : { session, userId: currentUserId, examId, eligible };
    });
  }, [currentUserId, queueExamId, session, hasOwnWork]);
  const showFastConfirm = hasOwnWork || (sameSession && lastExam.examId === fastConfirmExamId && lastExam.eligible);
  const fastConfirm = showFastConfirmEntry && fastConfirmExamId && canWork && showFastConfirm ? <FastConfirmDrawer key={fastConfirmExamId} tasks={tasks} examId={fastConfirmExamId} currentUserId={currentUserId} onTasksChanged={onTasksChanged} onScoringChanged={async () => { if (canGrade) await scoring.loadSummary(); }} /> : null;
  return <>
    {showSettings && initialExamId && canGrade && canManageTasks ? <GradingSettingsView key={aiPlanScope} examId={initialExamId} questionId={currentQuestionId || fallbackQuestionId} onOpenGoldPapers={onOpenGoldPapers} onOpenCalibration={onOpenCalibration} onAIPlanSaved={() => void scoring.loadSummary()} /> : null}
    {showOverview && initialExamId && canGrade && canManageTasks && aiPlanBlocksStart ? <div className="grading-focus-actions"><Button onClick={() => setAIPlanOpenFor({ scope: aiPlanScope, returnToStart: false })}>配置本场大题评分</Button></div> : null}
    {showOverview && initialExamId && canGrade ? <ExamScoringOverview scoring={scoring} canManageTasks={canManageTasks} onOpenTrial={canManageTasks && trialRun && ["queued", "processing", "needs_review", "failed"].includes(trialRun.status) ? () => setTrialOpenFor(trialScope) : undefined} fastConfirm={fastConfirm} onOpenAssignments={onOpenAssignments} onOpenResults={onOpenResults} /> : fastConfirm ? <div className="grading-fast-confirm-entry">{fastConfirm}</div> : null}
    <OcrWorkerAlert enabled={canWork && !canManageTasks} />
    <ExamScoringPlanDialog scoring={scoring} examId={initialExamId} onConfigureAI={canManageTasks ? () => { scoring.setPlanOpen(false); setAIPlanOpenFor({ scope: aiPlanScope, returnToStart: true }); } : undefined} />
    {aiPlanOpenFor?.scope === aiPlanScope && canManageTasks ? <Modal title="配置本场大题 AI 评分" open onCancel={closeAIPlan} footer={null} destroyOnHidden width={740}><GradingAIPlanSettings key={aiPlanScope} examId={initialExamId} onSaved={() => { closeAIPlan(); void scoring.loadSummary(); }} /></Modal> : null}
    {canManageTasks ? showResults && canGrade ? <ExamScoringResultsView scoring={scoring} /> : null : <ExamScoringResultsDrawer scoring={scoring} />}
    <ExamScoringImageDialog scoring={scoring} />
    {initialExamId && canGrade && canManageTasks && trialRun && trialOpenFor === trialScope ? <TrialScoringDrawer key={trialScope} examId={initialExamId} runId={trialRun.id} scopeKey={trialScope} onClose={() => { setTrialOpenFor(""); void scoring.loadSummary(); }} /> : null}
  </>;
}
