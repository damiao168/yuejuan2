import { useState } from "react";
import { Button, Modal } from "antd";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { OcrWorkerAlert } from "../../../../components/OcrWorkerAlert";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { ExamScoringImageDialog, ExamScoringPlanDialog, ExamScoringResultsDrawer, ExamScoringResultsView } from "./ExamScoringDialogs";
import { ExamScoringOverview } from "./ExamScoringOverview";
import { GradingSettingsView } from "./GradingSettingsView";
import type { ScoringReviewFilter } from "../scoringReviewCategories";
import { TrialScoringDrawer } from "./TrialScoringDrawer";
import { GradingAIPlanSettings } from "./GradingAIPlanSettings";

export interface ExamScoringPanelProps {
  initialExamId: string;
  canGrade: boolean;
  canWork: boolean;
  currentUserId: string;
  canManageTasks: boolean;
  currentQuestionId: string;
  scoring: ExamScoringController;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
  onOpenAssignments?: () => void;
  showOverview?: boolean;
  showResults?: boolean;
  showSettings?: boolean;
  onOpenResults?: (filter?: ScoringReviewFilter) => void;
}

export function ExamScoringPanel({ initialExamId, canGrade, canWork, currentUserId, canManageTasks, currentQuestionId, scoring, onOpenGoldPapers, onOpenCalibration, onOpenAssignments, showOverview = true, showResults = false, showSettings = false, onOpenResults }: Readonly<ExamScoringPanelProps>) {
  const fallbackQuestionId = scoring.summary?.questions[0]?.question_id ?? "";
  const session = getBusinessCommandSession();
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
  return <>
    {showSettings && initialExamId && canGrade && canManageTasks ? <GradingSettingsView key={aiPlanScope} examId={initialExamId} questionId={currentQuestionId || fallbackQuestionId} onOpenGoldPapers={onOpenGoldPapers} onOpenCalibration={onOpenCalibration} onAIPlanSaved={() => void scoring.loadSummary()} /> : null}
    {showOverview && initialExamId && canGrade && canManageTasks && aiPlanBlocksStart ? <div className="grading-focus-actions"><Button onClick={() => setAIPlanOpenFor({ scope: aiPlanScope, returnToStart: false })}>配置本场大题评分</Button></div> : null}
    {showOverview && initialExamId && canGrade ? <ExamScoringOverview scoring={scoring} canManageTasks={canManageTasks} onOpenTrial={canManageTasks && trialRun && ["queued", "processing", "needs_review", "failed"].includes(trialRun.status) ? () => setTrialOpenFor(trialScope) : undefined} onOpenAssignments={onOpenAssignments} onOpenResults={onOpenResults} /> : null}
    <OcrWorkerAlert enabled={canWork && !canManageTasks} />
    <ExamScoringPlanDialog scoring={scoring} examId={initialExamId} onConfigureAI={canManageTasks ? () => { scoring.setPlanOpen(false); setAIPlanOpenFor({ scope: aiPlanScope, returnToStart: true }); } : undefined} />
    {aiPlanOpenFor?.scope === aiPlanScope && canManageTasks ? <Modal title="配置本场大题 AI 评分" open onCancel={closeAIPlan} footer={null} destroyOnHidden width={740}><GradingAIPlanSettings key={aiPlanScope} examId={initialExamId} onSaved={() => { closeAIPlan(); void scoring.loadSummary(); }} /></Modal> : null}
    {canManageTasks ? showResults && canGrade ? <ExamScoringResultsView scoring={scoring} /> : null : <ExamScoringResultsDrawer scoring={scoring} />}
    <ExamScoringImageDialog scoring={scoring} />
    {initialExamId && canGrade && canManageTasks && trialRun && trialOpenFor === trialScope ? <TrialScoringDrawer key={trialScope} examId={initialExamId} runId={trialRun.id} scopeKey={trialScope} onClose={() => { setTrialOpenFor(""); void scoring.loadSummary(); }} /> : null}
  </>;
}
