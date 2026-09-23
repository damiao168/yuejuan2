import { Button, Checkbox, Input, List, Segmented, Select, Tooltip } from "antd";
import { Search, UserRoundCheck } from "lucide-react";
import type { ReviewTask } from "../../../../api/review";
import { EmptyState, ErrorState, LoadingState } from "../../../../components/PageState";
import { StatusTag } from "../../../../components/StatusTag";
import { sourceLabels, taskFilterOptions, taskStatusLabels, taskTone } from "../gradingWorkbench.model";
import type { TaskFilter } from "../gradingWorkbench.types";

export interface TaskRailProps {
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
  onAssignSelected: () => Promise<void>;
  onSelectTask: (taskId: string) => void;
  onLoadTasks: () => Promise<void>;
  onLoadMoreTasks: () => Promise<ReviewTask[]>;
}

export function TaskRail({
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
  onAssignSelected,
  onSelectTask,
  onLoadTasks,
  onLoadMoreTasks
}: TaskRailProps) {
  const allAssignableSelected = assignableTasks.length > 0 && assignableTasks.every((task) => assignmentTaskIds.includes(task.id));
  return (
    <aside className="grading-task-rail">
      <section className="grading-taskbar">
        {canManageTasks && canWork ? <Segmented value={queueScope} options={[{ label: "待我处理", value: "mine" }, { label: "全部任务", value: "all" }]} onChange={(value) => onQueueScopeChange(value as "mine" | "all")} /> : null}
        <Select className="toolbar-select" value={taskFilter} options={taskFilterOptions} onChange={onTaskFilterChange} />
        <Input prefix={<Search size={16} />} placeholder="搜索任务" value={keyword} onChange={(event) => onKeywordChange(event.target.value)} />
        <span className="muted">{filteredTasks.length} / {tasks.length}</span>
      </section>
      {canManageTasks && queueScope === "all" ? (
        <section className="grading-assignment-bar" aria-label="分配阅卷任务">
          <div className="grading-assignment-select-all">
            <Checkbox
              checked={allAssignableSelected}
              indeterminate={assignmentTaskIds.length > 0 && !allAssignableSelected}
              disabled={assignableTasks.length === 0}
              onChange={(event) => onSelectAllAssignments(event.target.checked)}
            >
              {assignmentTaskIds.length ? `已选 ${assignmentTaskIds.length}` : "选择任务"}
            </Checkbox>
          </div>
          <Select
            value={assignmentUserId || undefined}
            options={graderOptions}
            placeholder="选择阅卷员"
            showSearch
            optionFilterProp="label"
            status={gradersError ? "warning" : undefined}
            onChange={onAssignmentUserChange}
          />
          <Tooltip title={gradersError ?? "分配选中的阅卷任务"}>
            <Button type="primary" icon={<UserRoundCheck size={15} />} disabled={!assignmentUserId || assignmentTaskIds.length === 0 || Boolean(gradersError)} loading={actioning === "assign-tasks"} onClick={() => void onAssignSelected()}>分配</Button>
          </Tooltip>
        </section>
      ) : null}
      <div className="grading-task-list">
        {loadingTasks ? (
          <LoadingState label="正在读取阅卷任务" />
        ) : taskError ? (
          <ErrorState message={taskError} onRetry={() => void onLoadTasks()} />
        ) : filteredTasks.length === 0 ? (
          <EmptyState title="暂无阅卷任务" description="当前筛选下没有需要处理的答卷。" />
        ) : (
          <List
            dataSource={filteredTasks}
            loadMore={hasMoreTasks ? <div className="grading-task-load-more">{pageError ? <span role="alert">{pageError}</span> : null}<Button loading={loadingMoreTasks} onClick={() => void onLoadMoreTasks()}>{pageError ? "重试加载更多" : "加载更多"}</Button></div> : null}
            renderItem={(task) => (
              <List.Item className={task.id === selectedTaskId ? "grading-task-item active" : "grading-task-item"} onClick={() => onSelectTask(task.id)}>
                {canManageTasks ? (
                  <Checkbox
                    checked={assignmentTaskIds.includes(task.id)}
                    disabled={["submitted", "completed", "in_progress"].includes(task.status)}
                    aria-label={`选择 ${task.question_no} ${task.anonymous_code}`}
                    onClick={(event) => event.stopPropagation()}
                    onChange={(event) => onToggleAssignment(task.id, event.target.checked)}
                  />
                ) : null}
                <div className="grading-task-primary">
                  <strong>{task.anonymous_code || "暂无匿名码"}</strong>
                  <span title={sourceLabels[task.source] ? undefined : task.source}>{task.question_no} · 需要人工确认：{sourceLabels[task.source] ?? "其他原因"}{canManageTasks && task.assigned_to ? ` · ${graderNames[task.assigned_to] ?? "已分配"}` : ""}</span>
                </div>
                <div className="grading-task-status">
                  <StatusTag tone={taskTone(task.status)}>{taskStatusLabels[task.status] ?? "未知状态"}</StatusTag>
                  {task.priority >= 80 ? <StatusTag tone="warning">优先</StatusTag> : null}
                </div>
              </List.Item>
            )}
          />
        )}
      </div>
    </aside>
  );
}
