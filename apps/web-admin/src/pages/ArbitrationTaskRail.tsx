import { Button, Input, Select } from "antd";
import { Search } from "lucide-react";
import type { ArbitrationTask } from "../api/review";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { formatScore, statusLabels, taskTone } from "./arbitrationPresentation.model";
import { arbitrationScopeOptions, arbitrationStatusOptions, type ArbitrationScopeFilter, type ArbitrationStatusFilter } from "./arbitrationQueue.model";

interface TaskQueueProps {
  loading: boolean;
  error: string | null;
  tasks: readonly ArbitrationTask[];
  selectedTaskId: string;
  examNames: Readonly<Record<string, string>>;
  canReadExams: boolean;
  actioning: boolean;
  hasMoreTasks: boolean;
  loadingMoreTasks: boolean;
  onRetry: () => void;
  onSelect: (taskId: string) => void;
  onLoadMore: () => void;
}

function ArbitrationTaskQueue(props: Readonly<TaskQueueProps>) {
  if (props.loading) return <LoadingState label="正在读取仲裁任务" />;
  if (props.error) return <ErrorState message={props.error} onRetry={props.onRetry} />;
  return <>
    <div className="arbitration-queue-head"><h2>待处理任务</h2><span>{props.tasks.length} 条已加载</span></div>
    {props.tasks.length ? <div className="arbitration-task-list">
      {/* 保留滚动容器；无圆点列表仍显式标为列表，兼顾 Safari 的可访问性树。 */}
      <ul role="list" aria-label="仲裁任务" style={{ margin: 0, padding: 0, listStyle: "none" }}>
        {props.tasks.map((task, index) => <li key={task.id} style={{ borderTop: index > 0 ? "1px solid var(--border)" : undefined }}>
          <button type="button" disabled={props.actioning} className={`arbitration-task-item${task.id === props.selectedTaskId ? " active" : ""}`} aria-current={task.id === props.selectedTaskId ? "true" : undefined} onClick={() => props.onSelect(task.id)}>
            <span className="arbitration-task-first"><strong>{props.examNames[task.exam_id] ?? (props.canReadExams ? "未匹配到考试" : "本场考试")}</strong><StatusTag tone={taskTone(task.status)}>{statusLabels[task.status] ?? "未知状态"}</StatusTag></span>
            <span className="arbitration-task-meta"><span>{task.question_no} · {task.anonymous_code}</span><span>A {formatScore(task.first_score)} / B {formatScore(task.second_score)}</span></span>
            <span className="arbitration-task-diff">分差 {formatScore(task.score_difference)}</span>
          </button>
        </li>)}
      </ul>
    </div> : <EmptyState title="暂无仲裁任务" description="当前筛选条件下没有需要处理的评分差异。" />}
    {props.hasMoreTasks ? <Button block loading={props.loadingMoreTasks} onClick={props.onLoadMore}>加载更多仲裁任务</Button> : null}
  </>;
}

interface TaskRailProps extends TaskQueueProps {
  personalScope: boolean;
  scope: ArbitrationScopeFilter;
  statusFilter: ArbitrationStatusFilter;
  keyword: string;
  totalTasks: number;
  onScopeChange: (value: ArbitrationScopeFilter) => void;
  onStatusChange: (value: ArbitrationStatusFilter) => void;
  onKeywordChange: (value: string) => void;
}

export function ArbitrationTaskRail(props: Readonly<TaskRailProps>) {
  return <div className="arbitration-task-rail">
    <section className="arbitration-filterbar">
      {props.personalScope ? <span className="scope-fixed-label">仅显示分配给我的任务</span> : <Select disabled={props.actioning} className="toolbar-select" value={props.scope} options={arbitrationScopeOptions} onChange={props.onScopeChange} />}
      <Select disabled={props.actioning} className="toolbar-select" value={props.statusFilter} options={arbitrationStatusOptions} onChange={props.onStatusChange} />
      <Input disabled={props.actioning} prefix={<Search size={16} />} placeholder="搜索考试、匿名码或题号" value={props.keyword} onChange={(event) => props.onKeywordChange(event.target.value)} />
      <span className="muted arbitration-filter-count">{props.tasks.length} / {props.totalTasks} 个仲裁任务</span>
    </section>
    <section className="arbitration-queue-panel"><ArbitrationTaskQueue {...props} /></section>
  </div>;
}
