import { useMemo, useState } from "react";
import { Alert, Modal, Select } from "antd";
import type { ReviewTask } from "../../../../api/review";
import { assignmentGroups, buildAssignmentPlan, type AssignmentBatch } from "../assignmentPlan";
import "../teacher-product-ux.css";

export function AssignmentPreview({ tasks, defaultGrader, options, unassignedCount, busy, onCancel, onConfirm }: Readonly<{
  tasks: ReviewTask[]; defaultGrader: string; options: Array<{ value: string; label: string }>;
  unassignedCount: number | null; busy: boolean; onCancel: () => void; onConfirm: (plan: AssignmentBatch[]) => Promise<boolean>;
}>) {
  const groups = useMemo(() => assignmentGroups(tasks), [tasks]);
  const [targets, setTargets] = useState<Record<string, string[]>>(() => Object.fromEntries(groups.map((group) => [group.key, defaultGrader ? [defaultGrader] : []])));
  const plan = useMemo(() => buildAssignmentPlan(tasks, targets), [tasks, targets]);
  const selectedCount = plan.reduce((sum, batch) => sum + batch.tasks.length, 0);
  const newlyAssigned = plan.flatMap((batch) => batch.tasks).filter((task) => !task.assigned_to).length;
  return <Modal open title="按题分配阅卷任务" width={760} confirmLoading={busy} closable={!busy} maskClosable={!busy} keyboard={!busy} onCancel={onCancel}
    okText={`确认分配 ${selectedCount} 份`} okButtonProps={{ disabled: !selectedCount || busy }} onOk={async () => { if (await onConfirm(plan)) onCancel(); }}>
    <Alert type="info" showIcon message={`预览固定了当前选择的 ${tasks.length} 份任务`} description="每题可选择多名阅卷员，按任务数平均分配。服务端仍校验资格、任务状态和修订号；双评固定阅卷员的任务不能在此改派。" />
    <div className="grading-assignment-preview">
      {groups.map((group) => <div key={group.key} className="grading-assignment-preview-row"><strong>{group.label} · {group.tasks.length} 份</strong>
        <Select mode="multiple" disabled={busy} aria-label={`${group.label}阅卷员`} placeholder="为此题选择阅卷员" options={options} showSearch optionFilterProp="label" value={targets[group.key] ?? []} onChange={(value) => setTargets((current) => ({ ...current, [group.key]: value }))} />
        <span className="muted">{(targets[group.key] ?? []).length ? plan.filter((batch) => batch.tasks.some((task) => group.tasks.some((candidate) => candidate.id === task.id))).map((batch) => `${options.find((option) => option.value === batch.graderId)?.label || "阅卷员"} ${batch.tasks.filter((task) => group.tasks.some((candidate) => candidate.id === task.id)).length} 份`).join("；") : "暂不分配本题"}</span>
      </div>)}
    </div>
    <p>本次分配 {selectedCount} 份；已选中但暂不分配 {tasks.length - selectedCount} 份。{unassignedCount === null ? "未分配总数暂不可用。" : `当前筛选下未分配 ${unassignedCount} 份；本次预计新增分配 ${newlyAssigned} 份，余 ${Math.max(0, unassignedCount - newlyAssigned)} 份。`}</p>
    <p className="muted">各阅卷员分批提交；若某批失败，之前成功的分配保留，剩余任务可刷新后重试。</p>
  </Modal>;
}
