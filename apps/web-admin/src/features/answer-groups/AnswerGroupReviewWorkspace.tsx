import { useRef, useState } from "react";
import { Alert, Button, Select, Space, Tag } from "antd";
import type { AnswerGroup, AnswerGroupMemberContext } from "../../api/answerGroups";
import { TaskScoringReference } from "../../components/TaskScoringReference";
import { AnswerGroupSampleEvidence } from "./AnswerGroupSampleEvidence";
import { AnswerGroupManualHandoff } from "./AnswerGroupManualHandoff";
import { sampleRoleLabels } from "./answerGroupingPresentation";
import { getUserErrorMessage } from "../../api/client";

function frozenReferenceKey(value: unknown): string {
  // 冻结材料相等按字段内容判断，不能因 JSON 键顺序不同重复显示，也不能只凭细则版本号合并不同正文。
  if (Array.isArray(value)) return `[${value.map(frozenReferenceKey).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.entries(value).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([key, item]) => `${JSON.stringify(key)}:${frozenReferenceKey(item)}`).join(",")}}`;
  return JSON.stringify(value) ?? "undefined";
}

export function AnswerGroupReviewWorkspace({ group, canReview, acting, onRead, onReview, canReadReview, onNavigate }: Readonly<{
  group: AnswerGroup; canReview: boolean; acting: boolean;
  onRead: (value: AnswerGroupMemberContext) => void;
  onReview: (segmentId: string, outcome: "accepted" | "rejected") => Promise<boolean>;
  canReadReview: boolean; onNavigate?: (path: string) => void;
}>) {
  const members = [...group.members].sort((left, right) => Number(right.representative) - Number(left.representative) || Number(right.boundary) - Number(left.boundary) || Number(right.outlier) - Number(left.outlier));
  const [selection, setSelection] = useState(() => ({ segmentId: members.find(member => !member.sample_status)?.segment_id ?? members[0]?.segment_id ?? "" }));
  const member = members.find(item => item.segment_id === selection.segmentId) ?? members[0];
  const currentSelection = useRef(selection); currentSelection.current = selection;
  const [receipt, setReceipt] = useState<{ owner: typeof selection; material: AnswerGroupMemberContext } | null>(null);
  const [reference, setReference] = useState<AnswerGroupMemberContext["question"]>();
  const referenceIdentity = useRef("");
  const [referenceError, setReferenceError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [reviewError, setReviewError] = useState("");
  const reviewInFlight = useRef(false);
  const index = members.findIndex(item => item.segment_id === member?.segment_id);
  const displayed = receipt?.owner === selection && receipt.material.member.segment_id === member?.segment_id && receipt.material.member.representation_hash === member?.representation_hash ? receipt.material : undefined;
  const recordContext = (value: AnswerGroupMemberContext) => {
    if (currentSelection.current !== selection || value.group_id !== group.id || value.member.segment_id !== member?.segment_id
      || value.member.representation_hash !== member.representation_hash || value.question.id !== group.question_id || value.question.exam_id !== group.exam_id) return false;
    const identity = frozenReferenceKey(value.question);
    if (referenceIdentity.current && referenceIdentity.current !== identity) { setReferenceError("当前样本冻结评分依据与本组不一致，请刷新分组核对；尚未允许抽检提交。"); return false; }
    if (!referenceIdentity.current) { referenceIdentity.current = identity; setReference(value.question); }
    setReferenceError(""); return true;
  };
  const select = (segmentId: string) => { if (!acting && !submitting) { setReviewError(""); setSelection({ segmentId }); } };
  const reviewAndNext = async (outcome: "accepted" | "rejected") => {
    if (!member || !displayed || referenceError || !canReview || acting || reviewInFlight.current) return;
    const owner = selection; reviewInFlight.current = true; setSubmitting(true); setReviewError("");
    try {
      if (!await onReview(member.segment_id, outcome) || currentSelection.current !== owner) return;
      const next = [...members.slice(index + 1), ...members.slice(0, index)].find(item => !item.sample_status);
      if (next) setSelection({ segmentId: next.segment_id });
    } catch (error) { if (currentSelection.current === owner) setReviewError(getUserErrorMessage(error, "样本抽检未保存，请在当前样本重试。")); }
    finally { reviewInFlight.current = false; setSubmitting(false); }
  };
  if (!member) return <Alert type="info" message="当前组未返回可审阅样本，请刷新后重试。" />;
  return <section aria-label="逐份样本审阅">
    <Space wrap><strong>样本 {index + 1} / {members.length}</strong>
      <Select aria-label="选择抽检样本" value={member.segment_id} disabled={acting || submitting} options={members.map((item, position) => ({ value: item.segment_id, label: `样本 ${position + 1} · ${sampleRoleLabels(item).join(" / ") || "普通样本"} · ${item.sample_status === "accepted" ? "已接受" : item.sample_status === "rejected" ? "已拒绝" : "未抽检"}` }))} onChange={select} />
      <Button disabled={acting || submitting || index <= 0} onClick={() => select(members[index - 1].segment_id)}>上一份</Button>
      <Button disabled={acting || submitting || index >= members.length - 1} onClick={() => select(members[index + 1].segment_id)}>下一份</Button>
    </Space>
    <p>{sampleRoleLabels(member).map(label => <Tag key={label} color={label === "异常样本" ? "red" : label === "边界样本" ? "orange" : "blue"}>{label}</Tag>)}{member.sample_status === "accepted" ? "已接受" : member.sample_status === "rejected" ? "已拒绝" : "未抽检"}</p>
    {reference ? <details><summary>本组共用的题目与冻结评分依据</summary><TaskScoringReference question={reference} rubric={reference.rubric} /></details> : null}
    {referenceError ? <Alert type="error" showIcon message={referenceError} /> : null}
    {reviewError ? <Alert type="error" showIcon message={reviewError} /> : null}
    <AnswerGroupSampleEvidence key={`${group.id}:${member.segment_id}:${member.representation_hash}`} autoOpen showReference={false} groupId={group.id} segmentId={member.segment_id} hash={member.representation_hash}
      onContext={recordContext} onRead={value => { if (recordContext(value)) { setReceipt({ owner: selection, material: value }); onRead(value); } }} />
    {member.sample_status === "rejected" ? <AnswerGroupManualHandoff material={displayed} canReadReview={canReadReview} onNavigate={onNavigate} /> : null}
    {canReview ? <Space wrap><Button type="primary" disabled={acting || submitting || !displayed || Boolean(referenceError)} onClick={() => void reviewAndNext("accepted")}>接受并下一份</Button><Button danger disabled={acting || submitting || !displayed || Boolean(referenceError)} onClick={() => void reviewAndNext("rejected")}>拒绝并下一份</Button></Space> : null}
    <details><summary>当前样本诊断</summary><p>相似度 {(member.similarity * 100).toFixed(1)}% · 异常度 {(member.outlier_score * 100).toFixed(1)}%</p></details>
  </section>;
}
