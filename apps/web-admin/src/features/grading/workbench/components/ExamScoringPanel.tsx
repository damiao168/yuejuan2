import { useEffect, useState } from "react";
import type { ReviewTask } from "../../../../api/review";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
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
  const session = getBusinessCommandSession();
  const queueExamId = initialExamId || tasks.find((task) => task.assigned_to === currentUserId)?.exam_id || "";
  const [lastExam, setLastExam] = useState({ session, userId: currentUserId, examId: queueExamId });
  const sameSession = lastExam.session === session && lastExam.userId === currentUserId;
  // 最后一份提交会清空队列；同一会话仍保留考试和回执，换人或重登后不能沿用。
  const fastConfirmExamId = queueExamId || (sameSession ? lastExam.examId : "");
  useEffect(() => {
    setLastExam(previous => {
      const examId = queueExamId || (previous.session === session && previous.userId === currentUserId ? previous.examId : "");
      return previous.session === session && previous.userId === currentUserId && previous.examId === examId ? previous : { session, userId: currentUserId, examId };
    });
  }, [currentUserId, queueExamId, session]);
  return <>
    {fastConfirmExamId && canWork ? <div className="grading-fast-confirm-entry"><FastConfirmDrawer key={fastConfirmExamId} tasks={tasks} examId={fastConfirmExamId} currentUserId={currentUserId} onTasksChanged={onTasksChanged} onScoringChanged={async () => { if (canGrade) await scoring.loadSummary(); }} /></div> : null}
    {initialExamId && canGrade ? <ExamScoringOverview scoring={scoring} canManageTasks={canManageTasks} calibrationQuestionId={currentQuestionId || fallbackQuestionId} onOpenGoldPapers={onOpenGoldPapers} onOpenCalibration={onOpenCalibration} /> : null}
    <OcrWorkerAlert enabled={canWork} />
    <ExamScoringPlanDialog scoring={scoring} />
    <ExamScoringResultsDrawer scoring={scoring} />
    <ExamScoringImageDialog scoring={scoring} />
  </>;
}
