import type { useExamScoring } from "../hooks/useExamScoring";
import type { useGradingQueue } from "../hooks/useGradingQueue";
import type { ScoringReviewFilter } from "../scoringReviewCategories";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { ExamScoringPanel } from "./ExamScoringPanel";
import { GradingManagementNav, type GradingManagementView } from "./GradingManagementNav";
import { ReviewerProgressView } from "./ReviewerProgressView";

interface WorkbenchManagementProps {
  canManageTasks: boolean;
  managementView: GradingManagementView;
  initialExamId: string;
  canGrade: boolean;
  canWork: boolean;
  currentUserId: string;
  managing: boolean;
  context: WorkbenchContext | null;
  scoring: ReturnType<typeof useExamScoring>;
  queue: Pick<ReturnType<typeof useGradingQueue>, "myProgress" | "reviewerProgress" | "taskAggregate" | "loadingTasks" | "taskError" | "gradersError" | "loadTasks" | "loadGraders">;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
  onOpenAssignments: () => void;
  onViewChange: (view: GradingManagementView) => void;
  onOpenResults: (filter?: ScoringReviewFilter) => void;
}

// 概览、评分结果、设置与进度复用原评分 controller；这里只组合展示，不新建请求或状态生命周期。
export function WorkbenchManagement({
  canManageTasks, managementView, initialExamId, canGrade, canWork, currentUserId, managing,
  context: ctx, scoring: examScoring, queue, onOpenGoldPapers, onOpenCalibration, onOpenAssignments,
  onViewChange, onOpenResults: openResults
}: Readonly<WorkbenchManagementProps>) {
  const { myProgress, reviewerProgress, taskAggregate, loadingTasks, taskError, gradersError, loadTasks, loadGraders } = queue;
  return <>
      {canManageTasks ? <GradingManagementNav view={managementView} hasExamOverview={Boolean(initialExamId) && canGrade} canViewResults={Boolean(initialExamId) && canGrade}
        canOpenResults={Boolean(examScoring.summary?.run)} resultsOpen={examScoring.detailOpen} resultsLoading={examScoring.actioning === "scoring-detail"}
        hasOwnWork={canWork && (myProgress?.total ?? 0) > 0}
        canManageSettings={Boolean(initialExamId) && canGrade}
        onViewChange={onViewChange}
        onOpenResults={() => openResults()} /> : null}
      {/* 概览与独立结果视图共用控制器，切换管理视图时保留已选筛选。 */}
      <ExamScoringPanel
        initialExamId={initialExamId}
        canGrade={canGrade}
        canWork={canWork}
        currentUserId={currentUserId}
        canManageTasks={canManageTasks}
        currentQuestionId={ctx?.task.question_id ?? ""}
        scoring={examScoring}
        onOpenGoldPapers={onOpenGoldPapers}
        onOpenCalibration={onOpenCalibration}
        onOpenAssignments={managing ? onOpenAssignments : undefined}
        onOpenResults={canManageTasks ? openResults : undefined}
        showResults={managing && managementView === "results"}
        showSettings={managing && managementView === "settings"}
        showOverview={managing && managementView === "overview"}
      />
      {managing && managementView === "progress" ? <ReviewerProgressView reviewers={reviewerProgress}
        progressComplete={Boolean(taskAggregate) && !loadingTasks && !taskError} loading={loadingTasks} error={taskError} gradersError={gradersError}
        onRefresh={async () => { await Promise.all([loadTasks(), loadGraders()]); }} /> : null}
  </>;
}
