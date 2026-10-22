import { Button, Checkbox, Input, Select, Space } from "antd";
import { Eye, RefreshCw, Search, UserRoundCheck } from "lucide-react";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { ErrorState } from "../../../../components/PageState";
import type { ReviewTask } from "../../../../api/review";
import { taskFilterOptions, taskStatusLabels } from "../gradingWorkbench.model";
import type { TaskRailProps } from "./TaskRail";

function taskPurpose(task: ReviewTask) {
  const reason = (task as ReviewTask & { reason_code?: string }).reason_code;
  if (reason === "ai_suggestion_ready") return "确认 AI 建议分";
  if (reason === "omr_manual_only_profile") return "核对选择题";
  if (reason === "omr_set_quality_insufficient" || task.source === "omr_ambiguous") return "核对填涂结果";
  return "教师评分";
}

function assignmentStatus(task: ReviewTask, graderNames: Record<string, string>) {
  // 分配与评分状态一起解释；未安排教师时只提示管理员的下一步，不重复显示“待分配”。
  if (!task.assigned_to && task.status === "pending") return <div className="grading-assignment-status"><strong>未安排教师</strong><span>等待管理员选择教师并分配</span></div>;
  const teacher = task.assigned_to ? graderNames[task.assigned_to] ?? "已安排教师" : "";
  const status = ["pending", "assigned"].includes(task.status) ? "待评分" : taskStatusLabels[task.status] ?? "待处理";
  const nextStep = task.status === "in_progress" ? "等待教师完成评分并提交" : ["pending", "assigned", "returned"].includes(task.status) ? "等待教师处理答卷" : task.status === "needs_arbitration" ? "等待仲裁教师处理" : "";
  return <div className="grading-assignment-status"><strong>{teacher ? `${teacher} · ${status}` : status}</strong>{nextStep ? <span>{nextStep}</span> : null}</div>;
}

export function GradingAssignmentTable(props: Readonly<TaskRailProps>) {
  const { scope, fixedExam, filteredTasks, assignmentTaskIds, assignableTasks, graderNames, actioning } = props;
  const busy = Boolean(actioning) || props.loadingTasks || scope.selectingAll;
  return <section className="grading-assignment-workspace" aria-label="阅卷任务分配">
    <header><div><h1>安排教师</h1><p>选择待处理答卷并分配给教师；使用“查看答卷”可先检查作答。</p></div><Button icon={<RefreshCw size={15} />} loading={props.loadingTasks} onClick={() => void props.onLoadTasks()}>刷新</Button></header>
    <div className="grading-assignment-filters">
      {!fixedExam ? <Select allowClear value={scope.effectiveExamId || undefined} placeholder="选择考试" options={scope.examOptions} onChange={value => props.onExamChange(value || "")} /> : null}
      <Select aria-label="按题目筛选" allowClear value={scope.questionFilter || undefined} placeholder="全部题目" options={scope.questionOptions} onChange={value => props.onQuestionChange(value || "")} disabled={busy} />
      <Select aria-label="任务状态" value={props.taskFilter} options={taskFilterOptions} onChange={props.onTaskFilterChange} disabled={busy} />
      <Input allowClear prefix={<Search size={15} />} value={props.keyword} placeholder="搜索题号或答卷" onChange={event => props.onKeywordChange(event.target.value)} disabled={Boolean(actioning)} />
      <Checkbox checked={scope.unassignedOnly} onChange={event => props.onUnassignedChange(event.target.checked)} disabled={busy}>只看未分配</Checkbox>
    </div>
    {props.gradersError ? <ErrorState message={props.gradersError} /> : null}
    {props.taskError ? <ErrorState message={props.taskError} onRetry={() => void props.onLoadTasks()} /> : <ResponsiveTable<ReviewTask>
      rowKey="id" size="middle" loading={props.loadingTasks} dataSource={filteredTasks} pagination={false}
      rowSelection={{ selectedRowKeys: assignmentTaskIds, preserveSelectedRowKeys: true,
        onChange: keys => {
          const next = new Set(keys.map(String));
          for (const task of assignableTasks) {
            if (next.has(task.id) !== assignmentTaskIds.includes(task.id)) props.onToggleAssignment(task.id, next.has(task.id));
          }
        },
        getCheckboxProps: task => ({ disabled: busy || !assignableTasks.some(candidate => candidate.id === task.id) })
      }}
      columns={[
        { title: "题目", width: 110, render: (_, task) => <strong>第 {task.question_no} 题</strong> },
        { title: "答卷", render: (_, task) => /^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(task.anonymous_code ?? "") ? "匿名答卷" : task.anonymous_code || "匿名答卷" },
        { title: "待办事项", render: (_, task) => taskPurpose(task) },
        { title: "安排状态", width: 270, render: (_, task) => assignmentStatus(task, graderNames) },
        { title: "", width: 132, render: (_, task) => <Button type="link" className="grading-view-answer" icon={<Eye size={14} />} onClick={() => props.onSelectTask(task.id)} disabled={Boolean(actioning)}>查看答卷</Button> }
      ]} />}
    {props.hasMoreTasks ? <div className="grading-task-load-more">{props.pageError ? <span role="alert">{props.pageError}</span> : null}<Button loading={props.loadingMoreTasks} onClick={() => void props.onLoadMoreTasks()}>加载更多任务</Button></div> : null}
    <div className="grading-assignment-footer">
      <Space wrap><span>{assignmentTaskIds.length ? `已选择 ${assignmentTaskIds.length} 项` : `未分配 ${scope.unassignedCount ?? "—"} 项`}</span>
        <Button type="link" disabled={busy} loading={scope.selectingAll} onClick={() => void scope.selectAllMatchingAssignments()}>选择筛选结果中的全部待分配项</Button>
      </Space>
      <Space wrap><Select aria-label="选择阅卷教师" showSearch optionFilterProp="label" placeholder="选择阅卷教师" value={props.assignmentUserId || undefined} options={props.graderOptions} onChange={props.onAssignmentUserChange} disabled={busy} />
        <Button type="primary" icon={<UserRoundCheck size={15} />} disabled={!assignmentTaskIds.length || busy || Boolean(props.gradersError)} onClick={props.onPreviewAssignments}>分配给教师</Button>
      </Space>
    </div>
  </section>;
}
