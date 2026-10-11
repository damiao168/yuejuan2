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
    <p className="muted">学生姓名、班级和原阅卷教师已隐藏。复核建议仅供管理员终审，不会直接修改成绩或关闭申诉；请只依据答卷和评分标准判断。</p>
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
      <small>由管理员选择独立复核教师；原阅卷人不能受派复核同一成绩。</small>
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

type Props = TeacherProps & AdminProps & Readonly<{ mode: ProductExperience; draftTouched: boolean }>;

export function AppealDecisionPanel(props: Props) {
  return <aside className="appeal-decision-panel">
    {!props.terminal ? <output className="appeal-draft-note">{props.draftTouched ? "未提交内容仅在本页，切换申请或刷新会丢失。" : "复核意见和终审结论只有提交后才写入服务端。"}</output> : null}
    {props.mode === "teacher" ? <TeacherDecision {...props} /> : <AdminDecision {...props} />}
  </aside>;
}
