import { Button, Input, InputNumber, Select } from "antd";
import { CheckCircle2, Send, UserRoundCheck } from "lucide-react";
import type { Appeal, SubmitAppealRecommendationPayload } from "../../api/appeals";
import type { ProductExperience } from "../../router/experience";
import { recommendationOptions } from "./appealPresentation";

type CommonProps = Readonly<{
  actioning: string;
  score: number | null;
  maxScore?: number;
  reason: string;
  onScoreChange: (value: number | null) => void;
  onReasonChange: (value: string) => void;
}>;
type TeacherProps = CommonProps & Readonly<{
  canSubmit: boolean;
  recommendation: SubmitAppealRecommendationPayload["recommendation"];
  onRecommendationChange: (value: SubmitAppealRecommendationPayload["recommendation"]) => void;
  onSubmit: () => void;
}>;
type AdminProps = CommonProps & Readonly<{
  selected: Appeal;
  workerNames: Record<string, string>;
  workerOptions: { value: string; label: string }[];
  assignedTo: string;
  decision: string;
  canManage: boolean;
  terminal: boolean;
  onAssignedToChange: (value: string) => void;
  onDecisionChange: (value: string) => void;
  onReassign: () => void;
  onAdminSubmit: () => void;
}>;

function TeacherDecision(props: TeacherProps) {
  const { actioning, canSubmit, recommendation, score, maxScore, reason } = props;
  return <>
    <h3>提交独立复核</h3>
    <p className="muted">请独立核对答卷和评分标准。学生身份及原阅卷人已隐藏；意见由管理员终审，不直接改分或结案。</p>
    <Select value={recommendation} options={recommendationOptions} onChange={props.onRecommendationChange} disabled={!canSubmit || Boolean(actioning)} />
    {recommendation === "adjust_score" ? <InputNumber disabled={Boolean(actioning)} min={0} max={maxScore} precision={2} value={score} onChange={props.onScoreChange} placeholder="建议分数" /> : null}
    <Input.TextArea disabled={Boolean(actioning)} rows={5} value={reason} onChange={(event) => props.onReasonChange(event.target.value)} maxLength={500} placeholder="写明对应评分点、答卷证据和结论（不少于10字）" />
    <Button type="primary" icon={<Send size={16} />} disabled={!canSubmit || Boolean(actioning)} loading={actioning === "recommend"} onClick={props.onSubmit}>提交复核意见</Button>
  </>;
}

function AdminDecision(props: AdminProps) {
  const { actioning, selected, workerNames, workerOptions, assignedTo, decision, canManage, terminal, maxScore, score, reason } = props;
  return <>
    <h3>复核进度与终审</h3>
    <div className="appeal-routing">
      <span>当前分派</span><strong>{selected.assigned_to ? workerNames[selected.assigned_to] ?? "已分派教师" : "尚未分派"}</strong>
      <small>由管理员选择任教本场学科的独立复核教师；原阅卷人不能受派复核同一成绩。</small>
    </div>
    {selected.assigned_to ? <details className="appeal-reassign">
      <summary>更换复核教师</summary>
      <Select disabled={!canManage || terminal || Boolean(actioning)} showSearch optionFilterProp="label" value={assignedTo || undefined} options={workerOptions} onChange={props.onAssignedToChange} placeholder="选择其他复核教师" />
      <Button icon={<UserRoundCheck size={16} />} disabled={!canManage || terminal || !assignedTo || Boolean(actioning)} loading={actioning === "assign"} onClick={props.onReassign}>确认更换</Button>
    </details> : <div className="appeal-reassign">
      <h4>分派独立复核</h4>
      <Select disabled={!canManage || terminal || Boolean(actioning)} showSearch optionFilterProp="label" value={assignedTo || undefined} options={workerOptions} onChange={props.onAssignedToChange} placeholder="选择复核教师" />
      <Button icon={<UserRoundCheck size={16} />} disabled={!canManage || terminal || !assignedTo || Boolean(actioning)} loading={actioning === "assign"} onClick={props.onReassign}>分派复核</Button>
    </div>}
    {!workerOptions.length && canManage && !terminal ? <p className="muted">暂无符合本场学科的复核教师，请核对教师岗位、任教学科和账号状态。</p> : null}
    <div className="appeal-decision-divider" />
    <Select value={decision} onChange={props.onDecisionChange} disabled={!canManage || terminal || Boolean(actioning)} options={[
      { value: "accepted", label: "申诉成立，维持当前分数" }, { value: "rejected", label: "申诉不成立，维持原分" },
      { value: "score_adjusted", label: "申诉成立，调整分数" }, { value: "need_more_info", label: "争议较大，继续复核" }
    ]} />
    {decision === "score_adjusted" ? <InputNumber disabled={Boolean(actioning)} min={0} max={maxScore} precision={2} value={score} onChange={props.onScoreChange} placeholder="调整后分数" /> : null}
    <Input.TextArea disabled={Boolean(actioning)} rows={5} value={reason} onChange={(event) => props.onReasonChange(event.target.value)} maxLength={500} placeholder="填写学生可见的最终处理说明" />
    <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={!canManage || terminal || Boolean(actioning)} loading={actioning === "decision"} onClick={props.onAdminSubmit}>提交最终结论</Button>
  </>;
}

type Props = TeacherProps & AdminProps & Readonly<{ mode: ProductExperience; draftTouched: boolean; memorySaved?: boolean }>;

export function AppealDecisionPanel(props: Props) {
  return <aside className="appeal-decision-panel">
    {props.mode === "teacher" ? <TeacherDecision {...props} /> : <AdminDecision {...props} />}
    {!props.terminal && (props.mode === "teacher" ? props.canSubmit : props.canManage) ? <output className="appeal-draft-note" role="status" title="自动暂存只在本次会话；服务端私有草稿需单独保存，7天后不可恢复。">{props.draftTouched ? props.memorySaved ? "未提交；本次会话已暂存，保存私有草稿后可刷新恢复。" : "有未提交内容；离开后无法恢复。" : "提交后才会正式保存。"}</output> : null}
  </aside>;
}
