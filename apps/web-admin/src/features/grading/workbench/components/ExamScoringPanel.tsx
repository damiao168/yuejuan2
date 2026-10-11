import type { ReviewTask } from "../../../../api/review";
import { OcrWorkerAlert } from "../../../../components/OcrWorkerAlert";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { ExamScoringImageDialog, ExamScoringPlanDialog, ExamScoringResultsDrawer } from "./ExamScoringDialogs";
import { ExamScoringOverview } from "./ExamScoringOverview";
import { FastConfirmDrawer } from "./FastConfirmDrawer";

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
}

export function ExamScoringPanel({ initialExamId, canGrade, canWork, canManageTasks, currentQuestionId, scoring, onOpenGoldPapers, onOpenCalibration, tasks, currentUserId, onTasksChanged }: Readonly<ExamScoringPanelProps>) {
  const fallbackQuestionId = scoring.summary?.questions[0]?.question_id ?? "";
  const fastConfirmExamId = initialExamId || tasks.find((task) => task.assigned_to === currentUserId)?.exam_id || "";
  return <>
    {fastConfirmExamId && canWork ? <div className="grading-fast-confirm-entry"><FastConfirmDrawer key={fastConfirmExamId} tasks={tasks} examId={fastConfirmExamId} currentUserId={currentUserId} onTasksChanged={onTasksChanged} onScoringChanged={async () => { if (canGrade) await scoring.loadSummary(); }} /></div> : null}
    {initialExamId && canGrade ? <ExamScoringOverview scoring={scoring} canManageTasks={canManageTasks} calibrationQuestionId={currentQuestionId || fallbackQuestionId} onOpenGoldPapers={onOpenGoldPapers} onOpenCalibration={onOpenCalibration} /> : null}
    <OcrWorkerAlert enabled={canWork} />
    <ExamScoringPlanDialog scoring={scoring} />
    <ExamScoringResultsDrawer scoring={scoring} />
    <ExamScoringImageDialog scoring={scoring} />
  </>;
}
