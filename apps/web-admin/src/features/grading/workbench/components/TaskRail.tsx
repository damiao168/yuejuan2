import { Button, Checkbox, Input, List, Segmented, Select, Tooltip } from "antd";
import { Search, UserRoundCheck } from "lucide-react";
import type { ReviewTask } from "../../../../api/review";
import { EmptyState, ErrorState, LoadingState } from "../../../../components/PageState";
import { StatusTag } from "../../../../components/StatusTag";
import { sourceLabels, taskFilterOptions, taskStatusLabels, taskTone } from "../gradingWorkbench.model";
import type { TaskFilter } from "../gradingWorkbench.types";
import type { useGradingQueue } from "../hooks/useGradingQueue";
import { examSubjectLabel } from "../../../../constants/examStatus";
import "../teacher-product-ux.css";

export interface TaskRailProps {
  scope: Pick<ReturnType<typeof useGradingQueue>, "effectiveExamId" | "subjectFilter" | "questionFilter" | "unassignedOnly" | "subjectOptions" | "examOptions" | "questionOptions" | "matchedAggregate" | "unassignedCount" | "selectingAll" | "selectAllMatchingAssignments">;
  fixedExam: boolean;
  onExamChange: (id: string) => void;
  onSubjectChange: (subject: string) => void;
  onQuestionChange: (id: string) => void;
  onUnassignedChange: (value: boolean) => void;
  canManageTasks: boolean;
  canWork: boolean;
  queueScope: "mine" | "all";
  taskFilter: TaskFilter;
  keyword: string;
  tasks: ReviewTask[];
  filteredTasks: ReviewTask[];
  assignableTasks: ReviewTask[];
  assignmentTaskIds: string[];
  assignmentUserId: string;
  graderOptions: Array<{ value: string; label: string }>;
  graderNames: Record<string, string>;
  gradersError: string | null;
  selectedTaskId: string;
  loadingTasks: boolean;
  taskError: string | null;
  hasMoreTasks: boolean;
  pageError: string | null;
  loadingMoreTasks: boolean;
  actioning: string | null;
  onQueueScopeChange: (scope: "mine" | "all") => void;
  onTaskFilterChange: (filter: TaskFilter) => void;
  onKeywordChange: (keyword: string) => void;
  onAssignmentUserChange: (userId: string) => void;
  onSelectAllAssignments: (selected: boolean) => void;
  onToggleAssignment: (taskId: string, selected: boolean) => void;
  onPreviewAssignments: () => void;
  onSelectTask: (taskId: string) => void;
  onLoadTasks: () => Promise<void>;
  onLoadMoreTasks: () => Promise<ReviewTask[]>;
}

export function TaskRail({
  scope, fixedExam, onExamChange, onSubjectChange, onQuestionChange, onUnassignedChange,
  canManageTasks,
  canWork,
  queueScope,
  taskFilter,
  keyword,
  tasks,
  filteredTasks,
  assignableTasks,
  assignmentTaskIds,
  assignmentUserId,
  graderOptions,
  graderNames,
  gradersError,
  selectedTaskId,
  loadingTasks,
  taskError,
  hasMoreTasks,
  pageError,
  loadingMoreTasks,
  actioning,
  onQueueScopeChange,
  onTaskFilterChange,
  onKeywordChange,
  onAssignmentUserChange,
  onSelectAllAssignments,
  onToggleAssignment,
  onPreviewAssignments,
  onSelectTask,
  onLoadTasks,
  onLoadMoreTasks
}: Readonly<TaskRailProps>) {
  const allAssignableSelected = assignableTasks.length > 0 && assignableTasks.every((task) => assignmentTaskIds.includes(task.id));
  const renderAssignmentBar = () => {
    if (!canManageTasks || queueScope !== "all") return null;
    return (
      <section className="grading-assignment-bar" aria-label="分配阅卷任务">
        <div className="grading-assignment-select-all">
          <Checkbox
            checked={allAssignableSelected}
            indeterminate={assignmentTaskIds.length > 0 && !allAssignableSelected}
            disabled={assignableTasks.length === 0 || loadingTasks || Boolean(actioning) || scope.selectingAll}
            onChange={(event) => onSelectAllAssignments(event.target.checked)}
          >
            {assignmentTaskIds.length ? `已选 ${assignmentTaskIds.length} · 当前已加载` : "选择当前已加载的可分配任务"}
          </Checkbox>
        </div>
        <Button disabled={Boolean(actioning) || loadingTasks} loading={scope.selectingAll} onClick={() => void scope.selectAllMatchingAssignments()}>选择全部匹配的可分配任务</Button>
        <span className="muted">未分配 {scope.unassignedCount ?? "—"} 份；处理中、已提交及双评固定任务不参加改派。</span>
        <Select
          value={assignmentUserId || undefined}
          options={graderOptions}
          placeholder="选择阅卷员"
          showSearch
          optionFilterProp="label"
          status={gradersError ? "warning" : undefined}
          onChange={onAssignmentUserChange}
        />
        <Tooltip title={gradersError ?? "查看按题多人分配计划，确认后才执行"}>
          <Button type="primary" icon={<UserRoundCheck size={15} />} disabled={assignmentTaskIds.length === 0 || loadingTasks || Boolean(gradersError) || scope.selectingAll || Boolean(actioning)} loading={actioning === "assign-tasks"} onClick={onPreviewAssignments}>预览分配</Button>
        </Tooltip>
      </section>
    );
  };

  const renderTask = (task: ReviewTask) => (
    <List.Item className={task.id === selectedTaskId ? "grading-task-item active" : "grading-task-item"}>
      {canManageTasks ? (
        <Checkbox
          checked={assignmentTaskIds.includes(task.id)}
          disabled={["submitted", "completed", "in_progress", "cancelled"].includes(task.status) || task.source === "double_mark_required" || scope.selectingAll || Boolean(actioning)}
          aria-label={`选择 ${task.question_no} ${task.anonymous_code}`}
          onClick={(event) => event.stopPropagation()}
          onChange={(event) => onToggleAssignment(task.id, event.target.checked)}
        />
      ) : null}
      <button type="button" className="grading-task-select" disabled={Boolean(actioning)} aria-pressed={task.id === selectedTaskId} onClick={() => onSelectTask(task.id)}><div className="grading-task-primary">
        <small>{task.exam_name || `考试 ${task.exam_id.slice(0, 8)}`} · {task.subject ? examSubjectLabel(task.subject) : "学科未标注"}</small>
        <strong>{task.anonymous_code || "暂无匿名码"}</strong>
        <span title={sourceLabels[task.source] ? undefined : task.source}>{task.question_no} · 需要人工确认：{sourceLabels[task.source] ?? "其他原因"}{canManageTasks && task.assigned_to ? ` · ${graderNames[task.assigned_to] ?? "已分配"}` : ""}</span>
      </div>
      <div className="grading-task-status">
        <StatusTag tone={taskTone(task.status)}>{taskStatusLabels[task.status] ?? "未知状态"}</StatusTag>
        {task.priority >= 80 ? <StatusTag tone="warning">优先</StatusTag> : null}
      </div>
      </button>
    </List.Item>
  );

  const renderTaskList = () => {
    // 刷新中仍可能保留旧任务；先显示加载或错误，不提前展示旧列表。
    if (loadingTasks) return <LoadingState label="正在读取阅卷任务" />;
    if (taskError) return <ErrorState message={taskError} onRetry={() => void onLoadTasks()} />;
    if (filteredTasks.length === 0) return <EmptyState title="暂无阅卷任务" description="当前筛选下没有需要处理的答卷。" />;
    return (
      <List
        dataSource={filteredTasks}
        loadMore={hasMoreTasks ? <div className="grading-task-load-more">{pageError ? <span role="alert">{pageError}</span> : null}<Button loading={loadingMoreTasks} onClick={() => void onLoadMoreTasks()}>{pageError ? "重试加载更多" : "加载更多"}</Button></div> : null}
        renderItem={renderTask}
      />
    );
  };
  return (
    <aside className="grading-task-rail">
      <section className="grading-taskbar">
        {canManageTasks && canWork ? <Segmented value={queueScope} options={[{ label: "待我处理", value: "mine" }, { label: "全部任务", value: "all" }]} onChange={(value) => onQueueScopeChange(value as "mine" | "all")} /> : null}
        <div className="grading-scope-filters">
          <label>学科<Select allowClear disabled={Boolean(actioning) || fixedExam} value={scope.subjectFilter || undefined} placeholder="全部学科" options={scope.subjectOptions} onChange={(value) => onSubjectChange(value || "")} /></label>
          <label>考试<Select allowClear disabled={Boolean(actioning) || fixedExam} value={scope.effectiveExamId || undefined} placeholder="全部考试" options={scope.examOptions} onChange={(value) => onExamChange(value || "")} /></label>
          <label>题目<Select allowClear disabled={Boolean(actioning)} value={scope.questionFilter || undefined} placeholder="全部题目" options={scope.questionOptions} onChange={(value) => onQuestionChange(value || "")} /></label>
        </div>
        <Select aria-label="任务状态" disabled={Boolean(actioning)} className="toolbar-select" value={taskFilter} options={taskFilterOptions} onChange={onTaskFilterChange} />
        <Input disabled={Boolean(actioning)} prefix={<Search size={16} />} placeholder="搜索考试、题号或密号" value={keyword} onChange={(event) => onKeywordChange(event.target.value)} />
        {canManageTasks && queueScope === "all" ? <Checkbox disabled={Boolean(actioning)} checked={scope.unassignedOnly} onChange={(event) => onUnassignedChange(event.target.checked)}>只看未分配</Checkbox> : null}
        <span className="muted">已加载 {tasks.length} 份 · 当前显示 {filteredTasks.length} 份 · 全部匹配 {scope.matchedAggregate?.total_count ?? "—"} 份</span>
        <small className="muted">考试与题目选项来自已加载的授权任务；也可搜索考试名称。</small>
      </section>
      {renderAssignmentBar()}
      <div className="grading-task-list">
        {renderTaskList()}
      </div>
    </aside>
  );
}
