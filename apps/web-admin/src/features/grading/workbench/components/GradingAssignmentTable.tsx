import { useState } from "react";
import { Button, Checkbox, Drawer, Input, List, Select, Space } from "antd";
import { Eye, RefreshCw, Search, UserRoundCheck } from "lucide-react";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { ErrorState } from "../../../../components/PageState";
import type { ReviewTask } from "../../../../api/review";
import { assignmentGroups } from "../assignmentPlan";
import { taskFilterOptions, taskStatusLabels } from "../gradingWorkbench.model";
import type { TaskRailProps } from "./TaskRail";

function taskPurpose(task: ReviewTask) {
  const reason = (task as ReviewTask & { reason_code?: string }).reason_code;
  if (reason === "ai_suggestion_ready") return "确认 AI 建议分";
  if (reason === "omr_manual_only_profile") return "核对选择题";
  if (reason === "omr_set_quality_insufficient" || task.source === "omr_ambiguous") return "核对填涂结果";
  return "教师评分";
}

const finishedStatuses = new Set(["submitted", "completed", "cancelled"]);
type AssignmentGroup = ReturnType<typeof assignmentGroups>[number];

function assignmentSummary(tasks: ReviewTask[], graderNames: Record<string, string>) {
  const teachers = new Map<string, number>();
  let unassigned = 0;
  for (const task of tasks) {
    if (finishedStatuses.has(task.status)) continue;
    if (!task.assigned_to) unassigned += 1;
    else teachers.set(task.assigned_to, (teachers.get(task.assigned_to) ?? 0) + 1);
  }
  return <div className="grading-assignment-status">
    {unassigned ? <strong>未安排教师 {unassigned} 份</strong> : null}
    {teachers.size ? <span>{[...teachers].map(([id, count]) => `${graderNames[id] ?? "已安排教师"} ${count} 份`).join("、")}</span> : null}
    {!unassigned && !teachers.size ? <span>已处理</span> : null}
  </div>;
}

export function GradingAssignmentTable(props: Readonly<TaskRailProps>) {
  const { scope, fixedExam, filteredTasks, assignmentTaskIds, assignableTasks, graderNames, actioning } = props;
  const [viewingGroupKey, setViewingGroupKey] = useState("");
  const groups = assignmentGroups(filteredTasks);
  const viewingGroup = groups.find(group => group.key === viewingGroupKey);
  const assignableIds = new Set(assignableTasks.map(task => task.id));
  const selectedIds = new Set(assignmentTaskIds);
  const eligible = (group: AssignmentGroup) => group.tasks.filter(task => assignableIds.has(task.id));
  const selectedGroups = groups.filter(group => eligible(group).some(task => selectedIds.has(task.id)));
  const busy = Boolean(actioning) || props.loadingTasks || scope.selectingAll;
  // 后续页会自动载入；页未齐或失败时不能把当前页数量当作全题数量进行分配。
  const incomplete = props.hasMoreTasks || props.loadingMoreTasks || Boolean(props.pageError);
  const selectionDisabled = busy || incomplete || Boolean(props.taskError);
  const toggleGroup = (group: AssignmentGroup, selected: boolean) => {
    if (selectionDisabled) return;
    for (const task of eligible(group)) {
      if (selectedIds.has(task.id) !== selected) props.onToggleAssignment(task.id, selected);
    }
  };
  const viewGroup = (group: AssignmentGroup) => {
    if (group.tasks.length === 1) props.onSelectTask(group.tasks[0].id);
    else setViewingGroupKey(group.key);
  };
  return <section className="grading-assignment-workspace" aria-label="阅卷任务分配">
    <header><div><h1>安排教师</h1><p>按题选择答卷，再指定教师与分配份数。</p></div><Button icon={<RefreshCw size={15} />} loading={props.loadingTasks} disabled={Boolean(actioning) || scope.selectingAll} onClick={() => void props.onLoadTasks()}>刷新</Button></header>
    <div className="grading-assignment-filters">
      {!fixedExam ? <Select allowClear value={scope.effectiveExamId || undefined} placeholder="选择考试" options={scope.examOptions} onChange={value => props.onExamChange(value || "")} disabled={busy} /> : null}
      <Select aria-label="按题目筛选" allowClear value={scope.questionFilter || undefined} placeholder="全部题目" options={scope.questionOptions} onChange={value => props.onQuestionChange(value || "")} disabled={busy} />
      <Select aria-label="任务状态" value={props.taskFilter} options={taskFilterOptions} onChange={props.onTaskFilterChange} disabled={busy} />
      <Input allowClear prefix={<Search size={15} />} value={props.keyword} placeholder="搜索题号或答卷" onChange={event => props.onKeywordChange(event.target.value)} disabled={busy} />
      <Checkbox checked={scope.unassignedOnly} onChange={event => props.onUnassignedChange(event.target.checked)} disabled={busy}>只看未分配</Checkbox>
    </div>
    {props.gradersError ? <ErrorState message={props.gradersError} /> : null}
    {incomplete && !props.pageError ? <p role="status" className="muted">正在统计各题答卷，完成后可按题分配。</p> : null}
    {props.taskError ? <ErrorState message={props.taskError} onRetry={() => void props.onLoadTasks()} /> : <ResponsiveTable<AssignmentGroup>
      rowKey="key" size="middle" loading={props.loadingTasks} dataSource={groups} pagination={false} scroll={{ x: 760 }}
      rowSelection={{ selectedRowKeys: groups.filter(group => eligible(group).length > 0 && eligible(group).every(task => selectedIds.has(task.id))).map(group => group.key),
        onSelect: toggleGroup,
        onSelectAll: selected => { for (const group of groups) toggleGroup(group, selected); },
        getCheckboxProps: group => ({
          disabled: selectionDisabled || !eligible(group).length,
          indeterminate: eligible(group).some(task => selectedIds.has(task.id)) && !eligible(group).every(task => selectedIds.has(task.id)),
          "aria-label": `选择${group.shortLabel}`
        })
      }}
      columns={[
        { title: "题目 / 剩余答卷", width: 210, render: (_, group) => <div className="grading-assignment-question"><strong>{group.shortLabel}</strong><span>{incomplete ? "统计中" : `剩余 ${group.tasks.filter(task => !finishedStatuses.has(task.status)).length} 份`}</span>{!fixedExam ? <small>{group.contextLabel}</small> : null}</div> },
        { title: "待办事项", render: (_, group) => [...new Set(group.tasks.map(taskPurpose))].join("、") },
        { title: "安排情况", render: (_, group) => incomplete ? "统计中" : assignmentSummary(group.tasks, graderNames) },
        { title: "", width: 132, render: (_, group) => <Button type="link" className="grading-view-answer" icon={<Eye size={14} />} onClick={() => viewGroup(group)} disabled={Boolean(actioning)}>查看答卷</Button> }
      ]} />}
    {props.hasMoreTasks || props.pageError ? <div className="grading-task-load-more">{props.pageError ? <span role="alert">{props.pageError}</span> : null}<Button loading={props.loadingMoreTasks} disabled={busy} onClick={() => void props.onLoadMoreTasks()}>{props.pageError ? "重试加载更多" : "加载更多任务"}</Button></div> : null}
    <div className="grading-assignment-footer">
      <Space wrap><span>{assignmentTaskIds.length ? `已选 ${selectedGroups.length} 道题 · ${assignmentTaskIds.length} 份答卷` : `未分配 ${scope.unassignedCount ?? "—"} 份答卷`}</span>
        <Button type="link" disabled={selectionDisabled} loading={scope.selectingAll} onClick={() => void scope.selectAllMatchingAssignments()}>选择全部可分配题目</Button>
      </Space>
      <Button type="primary" icon={<UserRoundCheck size={15} />} disabled={!assignmentTaskIds.length || selectionDisabled || Boolean(props.gradersError) || Boolean(props.taskError)} onClick={props.onPreviewAssignments}>分配给教师</Button>
    </div>
    <Drawer title={`${viewingGroup?.shortLabel ?? ""} · 答卷`} open={Boolean(viewingGroup)} width={480} onClose={() => setViewingGroupKey("")}>
      {viewingGroup ? <List dataSource={viewingGroup.tasks} pagination={{ pageSize: 20, hideOnSinglePage: true }} renderItem={task => <List.Item>
        <div className="grading-assignment-answer"><strong>{/^[0-9a-f]{8}-[0-9a-f-]{27}$/i.test(task.anonymous_code ?? "") ? "匿名答卷" : task.anonymous_code || "匿名答卷"}</strong><span>{task.assigned_to ? `${graderNames[task.assigned_to] ?? "已安排教师"} · ` : ""}{taskStatusLabels[task.status] ?? "待处理"}</span></div>
        <Button type="link" disabled={Boolean(actioning)} onClick={() => { setViewingGroupKey(""); props.onSelectTask(task.id); }}>查看答卷</Button>
      </List.Item>} /> : null}
    </Drawer>
  </section>;
}
