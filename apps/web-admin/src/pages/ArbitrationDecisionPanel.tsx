import type { Dispatch, SetStateAction } from "react";
import { Button, Input, InputNumber, Space } from "antd";
import { CheckCircle2, ClipboardCheck, ScrollText, UserCheck } from "lucide-react";
import type { AuditLog } from "../api/audit";
import type { ArbitrationTask, FinalGrade } from "../api/review";
import { StatusTag } from "../components/StatusTag";
import { ArbitrationAuditList } from "./ArbitrationPanels";
import { formatScore } from "./arbitrationPresentation.model";
import type { ArbitrationDecisionDraft, ArbitrationDetailState } from "./arbitrationQueue.model";

const finalGradeSourceLabels: Record<string, string> = {
  ai: "AI 评分", human: "人工评分", arbitration: "仲裁定分", appeal: "申诉改分"
};

function ArbitrationFinalGrade({ grade, task }: Readonly<{ grade: FinalGrade | null; task?: ArbitrationTask }>) {
  if (grade) return <div className="final-grade-result">
    <StatusTag tone="success">最终分已写入</StatusTag>
    <strong>{formatScore(grade.score)} / {formatScore(grade.max_score)}</strong>
    <span>{finalGradeSourceLabels[grade.source] ?? "仲裁定分"} · {grade.locked ? "已锁定" : "未锁定"}</span>
  </div>;
  if (task?.final_score != null) return <div className="final-grade-result">
    <StatusTag tone="success">已提交</StatusTag>
    <strong>{formatScore(task.final_score)}</strong>
    <span>{task.reason || "未填写仲裁说明"}</span>
  </div>;
  return null;
}

interface DecisionPanelProps {
  selectedTask?: ArbitrationTask;
  detail: ArbitrationDetailState | null;
  draft: Readonly<ArbitrationDecisionDraft>;
  draftTouched: boolean;
  finalGrade: FinalGrade | null;
  maxScore: number;
  actioning: string | null;
  editingDisabled: boolean;
  canAssign: boolean;
  canSubmit: boolean;
  auditLoading: boolean;
  auditError: string | null;
  auditLogs: AuditLog[];
  setDraft: Dispatch<SetStateAction<ArbitrationDecisionDraft>>;
  setDraftTouched: Dispatch<SetStateAction<boolean>>;
  onAssign: () => void;
  onSubmit: () => void;
  onAuditRetry: () => void;
}

export function ArbitrationDecisionPanel(props: Readonly<DecisionPanelProps>) {
  return <aside className="arbitration-decision-panel">
    <div className="panel-head"><div><h2>仲裁裁决</h2><p>{props.selectedTask ? `${props.selectedTask.anonymous_code} · ${props.selectedTask.question_no}` : "未选择任务"}</p></div><ClipboardCheck size={18} /></div>
    {props.detail && props.detail.task.status !== "submitted" ? <p className="arbitration-draft-note">
      {/* 状态只包含纯文本，保留原段落样式；不再给父级另加 live 区域。 */}
      <output>{props.draftTouched ? "未提交内容仅在本页；切换任务或刷新会丢失。" : "最终分和说明在提交仲裁后才写入服务端。"}</output>
    </p> : null}
    <div className="score-input-row">
      <InputNumber min={0} max={props.maxScore || undefined} precision={1} value={props.draft.finalScore} placeholder="最终分" disabled={props.editingDisabled}
        onChange={(value) => { props.setDraftTouched(true); props.setDraft((current) => ({ ...current, finalScore: value === null ? null : Number(value) })); }} />
      <span>/ {props.maxScore || "-"}</span>
    </div>
    <Input.TextArea rows={4} placeholder="仲裁说明" value={props.draft.reason} disabled={props.editingDisabled}
      onChange={(event) => { props.setDraftTouched(true); props.setDraft((current) => ({ ...current, reason: event.target.value })); }} />
    <Input.TextArea rows={3} placeholder="学生可见反馈" value={props.draft.studentFeedback} disabled={props.editingDisabled}
      onChange={(event) => { props.setDraftTouched(true); props.setDraft((current) => ({ ...current, studentFeedback: event.target.value })); }} />
    <Space wrap>
      {props.canAssign ? <Button icon={<UserCheck size={16} />} disabled={props.editingDisabled} loading={props.actioning === "assign"} onClick={props.onAssign}>分配给我</Button> : null}
      <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={props.editingDisabled || !props.canSubmit} loading={props.actioning === "submit"} onClick={props.onSubmit}>提交仲裁</Button>
    </Space>
    <ArbitrationFinalGrade grade={props.finalGrade} task={props.detail?.task} />
    <section className="arbitration-audit-panel">
      <div className="panel-head compact"><div><h2>操作记录</h2><p>操作全程留痕</p></div><ScrollText size={18} /></div>
      <ArbitrationAuditList loading={props.auditLoading} error={props.auditError} logs={props.auditLogs} onRetry={props.onAuditRetry} />
    </section>
  </aside>;
}
