import { useMemo, useRef, useState } from "react";
import { InputNumber, Modal, Segmented, Select } from "antd";
import type { ReviewTask } from "../../../../api/review";
import { assignmentGroups, buildAssignmentPlan, buildQuotaAssignmentPlan, type AssignmentBatch } from "../assignmentPlan";
import "../teacher-product-ux.css";

export function AssignmentPreview({ tasks, defaultGrader, options, busy, onCancel, onConfirm }: Readonly<{
  tasks: ReviewTask[]; defaultGrader: string; options: Array<{ value: string; label: string }>;
  unassignedCount: number | null; busy: boolean; onCancel: () => void; onConfirm: (plan: AssignmentBatch[]) => Promise<boolean>;
}>) {
  const groups = useMemo(() => assignmentGroups(tasks), [tasks]);
  const [graders, setGraders] = useState<string[]>(() => defaultGrader && options.some(option => option.value === defaultGrader) ? [defaultGrader] : []);
  const [mode, setMode] = useState<"average" | "custom">("average");
  const [counts, setCounts] = useState<Record<string, number | null>>({});
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const submittingRef = useRef(false);
  const working = busy || submitting;
  const balanced = useMemo(() => buildAssignmentPlan(tasks, {}, graders), [tasks, graders]);
  const result = useMemo<{ plan: AssignmentBatch[]; error: string }>(() => {
    if (!graders.length) return { plan: [], error: "" };
    if (mode === "average") return { plan: balanced, error: "" };
    if (graders.some(grader => counts[grader] === null || counts[grader] === undefined)) return { plan: [], error: "请填写每位教师的分配数量" };
    try {
      return { plan: buildQuotaAssignmentPlan(tasks, graders.map(graderId => ({ graderId, count: counts[graderId]! }))), error: "" };
    } catch (error) {
      return { plan: [], error: error instanceof Error ? error.message : "请检查分配数量" };
    }
  }, [balanced, counts, graders, mode, tasks]);
  const plan = result.plan;
  const selectedCount = plan.reduce((sum, batch) => sum + batch.tasks.length, 0);
  const canConfirm = tasks.length > 0 && selectedCount === tasks.length && !result.error;
  const quotaTotal = graders.reduce((sum, grader) => sum + (counts[grader] ?? 0), 0);
  const cancel = () => { if (!working && !submittingRef.current) onCancel(); };
  const confirm = async () => {
    // 父级 busy 回传前也要锁住同一帧的重复确认和取消，提交始终使用这次预览的完整计划。
    if (working || submittingRef.current || !canConfirm) return;
    submittingRef.current = true;
    setSubmitting(true);
    setSubmitError("");
    try {
      if (await onConfirm(plan)) onCancel();
    } catch {
      setSubmitError("分配失败，请稍后重试");
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  };
  const changeGraders = (value: string[]) => {
    if (working || submittingRef.current) return;
    const next = [...new Set(value)];
    setGraders(next);
    setCounts(current => Object.fromEntries(next.map(grader => [grader, current[grader] ?? 0])));
    setSubmitError("");
  };
  const changeMode = (value: "average" | "custom") => {
    if (working || submittingRef.current) return;
    // 切入自定义时先按当前均分结果填数，管理员只需调整需要变化的教师。
    if (value === "custom") setCounts(Object.fromEntries(graders.map(grader => [grader, balanced.find(batch => batch.graderId === grader)?.tasks.length ?? 0])));
    setMode(value);
    setSubmitError("");
  };
  return <Modal open title="分配阅卷任务" width={640} confirmLoading={working} closable={!working} maskClosable={!working} keyboard={!working} onCancel={cancel}
    okText={`确认分配 ${tasks.length} 份`} okButtonProps={{ disabled: !canConfirm || working }} cancelButtonProps={{ disabled: working }} onOk={confirm}>
    <div className="grading-assignment-preview">
      <p className="grading-assignment-preview-scope">已选 {groups.length} 道题 · {tasks.length} 份答卷</p>
      <div className="grading-assignment-preview-questions" aria-label="本次分配题目">{groups.map(group => <span key={group.key} className="grading-assignment-preview-question" title={group.contextLabel}>{group.shortLabel}<span>{group.tasks.length} 份</span></span>)}</div>
      <div className="grading-assignment-preview-field">
        <label htmlFor="assignment-preview-graders">阅卷教师</label>
        <Select id="assignment-preview-graders" mode="multiple" disabled={working} aria-label="统一选择阅卷教师" placeholder="选择一位或多位教师" options={options} showSearch optionFilterProp="label" value={graders} onChange={changeGraders} />
      </div>
      <Segmented className="grading-assignment-preview-strategy" aria-label="分配方式" value={mode} options={[{ label: "平均分配", value: "average" }, { label: "自定义数量", value: "custom" }]} disabled={working} onChange={value => changeMode(value as "average" | "custom")} />
      <p className="grading-assignment-preview-hint">{mode === "average" ? "一位教师接收全部，多位教师按答卷数量平均分配。" : `各教师数量合计需为 ${tasks.length} 份。`}</p>
      {graders.length ? <div className="grading-assignment-preview-teachers" aria-label="教师分配预览">{graders.map(grader => {
        const name = options.find(option => option.value === grader)?.label || "阅卷教师";
        const count = plan.find(batch => batch.graderId === grader)?.tasks.length ?? 0;
        return <div key={grader} className="grading-assignment-preview-teacher"><span className="grading-assignment-preview-teacher-name">{name}</span>
          {mode === "custom" ? <InputNumber<number> aria-label={`${name}分配数量`} min={0} max={tasks.length} step={1} disabled={working} value={counts[grader] ?? null} addonAfter="份" onChange={value => {
            if (working || submittingRef.current) return;
            setCounts(current => ({ ...current, [grader]: value }));
            setSubmitError("");
          }} /> : <strong>{count} 份</strong>}
        </div>;
      })}</div> : <p className="grading-assignment-preview-empty">请选择阅卷教师</p>}
      {mode === "custom" && graders.length ? <p className="grading-assignment-preview-total">已填写 {Number.isFinite(quotaTotal) ? quotaTotal : "—"} / {tasks.length} 份</p> : null}
      {result.error || submitError ? <p className="grading-assignment-preview-error" role="alert">{result.error || submitError}</p> : null}
    </div>
  </Modal>;
}
