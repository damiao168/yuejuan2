import { Button } from "antd";
import { BookmarkCheck, LogOut } from "lucide-react";
import { BackmarkQueue } from "../../../../components/BackmarkQueue";
import { RegradeQueue } from "../../../../components/RegradeQueue";
import type { ReviewTask } from "../../../../api/review";
import type { DraftSaveStatus } from "../gradingWorkbench.types";
import type { useGradingQueue } from "../hooks/useGradingQueue";
import { TaskRail } from "./TaskRail";
import { TaskRailContainer } from "./WorkbenchContainers";

type WorkbenchTaskQueueState = Pick<ReturnType<typeof useGradingQueue>, "effectiveExamId" | "subjectFilter" | "questionFilter" | "unassignedOnly" | "subjectOptions" | "examOptions" | "questionOptions" | "matchedAggregate" | "unassignedCount" | "selectingAll" | "selectAllMatchingAssignments" | "setExamFilter" | "setSubjectFilter" | "setQuestionFilter" | "setUnassignedOnly" | "queueScope" | "setQueueScope" | "taskFilter" | "setTaskFilter" | "keyword" | "setKeyword" | "tasks" | "filteredTasks" | "assignableTasks" | "assignmentTaskIds" | "setAssignmentTaskIds" | "assignmentUserId" | "setAssignmentUserId" | "graderOptions" | "graderNames" | "gradersError" | "selectedTaskId" | "setSelectedTaskId" | "loadingTasks" | "taskError" | "hasMoreTasks" | "pageError" | "loadingMoreTasks" | "loadTasks" | "loadMoreTasks" | "pendingNextRef" | "suppressAutoSelectRef">;

interface WorkbenchTaskQueueProps {
  queue: WorkbenchTaskQueueState;
  managing: boolean;
  canWork: boolean;
  fixedExam: boolean;
  open: boolean;
  onClose: () => void;
  onOpenBookmarks: () => void;
  onPreviewTask: () => void;
  onPreviewAssignments: (tasks: ReviewTask[]) => void;
  actioning: string | null;
  ownsSelectedTask: boolean;
  bookmarksBusy: boolean;
  regionEditing: boolean;
  regionBusy: boolean;
  regionDirty: boolean;
  draftSaveStatus: DraftSaveStatus;
  releaseCurrentTask: () => Promise<void>;
  changeQueueScope: (change: () => void) => void;
}

// 任务列表与辅助评分入口只消费队列及显式动作；草稿、租约和身份失效仍由任务会话 hook 负责。
export function WorkbenchTaskQueue({
  queue, managing, canWork, fixedExam, open, onClose, onOpenBookmarks, onPreviewTask, onPreviewAssignments,
  actioning, ownsSelectedTask, bookmarksBusy, regionEditing, regionBusy, regionDirty,
  draftSaveStatus, releaseCurrentTask, changeQueueScope
}: Readonly<WorkbenchTaskQueueProps>) {
  const {
    queueScope, setQueueScope, taskFilter, setTaskFilter, keyword, setKeyword, tasks, filteredTasks,
    assignableTasks, assignmentTaskIds, setAssignmentTaskIds, assignmentUserId, setAssignmentUserId,
    graderOptions, graderNames, gradersError, selectedTaskId, setSelectedTaskId, loadingTasks, taskError,
    hasMoreTasks, pageError, loadingMoreTasks, loadTasks, loadMoreTasks, pendingNextRef, suppressAutoSelectRef
  } = queue;
  return (
        <TaskRailContainer managed={managing} open={open} onClose={onClose}>
        {!managing && canWork ? <div className="grading-task-tools"><Button block icon={<BookmarkCheck size={16} />} onClick={() => changeQueueScope(() => { onClose(); onOpenBookmarks(); })}>已标记试卷</Button></div> : null}
        <TaskRail
          scope={queue}
          fixedExam={fixedExam}
          onExamChange={(id) => changeQueueScope(() => { queue.setExamFilter(id); queue.setQuestionFilter(""); })}
          onSubjectChange={(subject) => changeQueueScope(() => { queue.setSubjectFilter(subject); queue.setExamFilter(""); queue.setQuestionFilter(""); })}
          onQuestionChange={(id) => changeQueueScope(() => queue.setQuestionFilter(id))}
          onUnassignedChange={(value) => changeQueueScope(() => queue.setUnassignedOnly(value))}
          canManageTasks={managing}
          canWork={canWork}
          queueScope={queueScope}
          taskFilter={taskFilter}
          keyword={keyword}
          tasks={tasks}
          filteredTasks={filteredTasks}
          assignableTasks={assignableTasks}
          assignmentTaskIds={assignmentTaskIds}
          assignmentUserId={assignmentUserId}
          graderOptions={graderOptions}
          graderNames={graderNames}
          gradersError={gradersError}
          selectedTaskId={selectedTaskId}
          loadingTasks={loadingTasks}
          taskError={taskError}
          hasMoreTasks={hasMoreTasks}
          pageError={pageError}
          loadingMoreTasks={loadingMoreTasks}
          actioning={actioning}
          onQueueScopeChange={(scope) => changeQueueScope(() => setQueueScope(scope))}
          onTaskFilterChange={(filter) => changeQueueScope(() => setTaskFilter(filter))}
          onKeywordChange={(text) => changeQueueScope(() => setKeyword(text))}
          onAssignmentUserChange={setAssignmentUserId}
          onSelectAllAssignments={(selected) => setAssignmentTaskIds(selected ? assignableTasks.map((task) => task.id) : [])}
          onToggleAssignment={(taskId, selected) => setAssignmentTaskIds((current) => selected ? [...new Set([...current, taskId])] : current.filter((id) => id !== taskId))}
          onPreviewAssignments={() => onPreviewAssignments(assignableTasks.filter((task) => assignmentTaskIds.includes(task.id)).map((task) => ({ ...task })))}
          onSelectTask={(taskId) => {
            changeQueueScope(() => { pendingNextRef.current = null; suppressAutoSelectRef.current = false; setSelectedTaskId(taskId); onClose(); if (managing) onPreviewTask(); });
          }}
          onLoadTasks={loadTasks}
          onLoadMoreTasks={loadMoreTasks}
        />
        {!managing && canWork ? <section className="grading-task-secondary" aria-label="其他评分任务">
          {ownsSelectedTask ? <div><Button icon={<LogOut size={16} />} disabled={Boolean(actioning) || bookmarksBusy || (regionEditing ? regionBusy || regionDirty : !["saved", "idle"].includes(draftSaveStatus))} loading={actioning === "release"} onClick={() => changeQueueScope(() => { void releaseCurrentTask(); })}>放回任务队列</Button><p>先保存草稿，再将当前答卷放回任务列表。</p></div> : null}
          <h3>其他评分任务</h3>
          {/* 质量复评和新版细则重评各有独立候选结果，不能作为个人标记改变普通评分。 */}
          <div><BackmarkQueue canWork={canWork} label="质量检查重评" /><p>管理员安排的独立复评，用于检查评分质量。</p></div>
          <div><RegradeQueue canWork={canWork} label="细则更新重评" /><p>使用更新后的评分细则重新评分。</p></div>
        </section> : null}
        </TaskRailContainer>
  );
}
