import { useEffect, useState } from "react";
import { Alert, Button, Select, Space } from "antd";
import { useQuery } from "@tanstack/react-query";
import type { QuestionBankVersion } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getUserErrorMessage } from "../../api/client";
import { questionBankMemberAmbiguous, questionBankMemberLabel, type QuestionBankMember } from "./questionBankMembers";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";

export type HandoffSelection = { reviewer_id: string; publisher_id: string };

export function handoffSelectionValid(members: QuestionBankMember[], authorId: string, value: HandoffSelection) {
  return (["review", "publish"] as const).every(action => members.some(person => person.user_id === (action === "review" ? value.reviewer_id : value.publisher_id) && person.actions.includes("read") && person.actions.includes(action) && (action !== "review" || person.user_id !== authorId) && !questionBankMemberAmbiguous(person, members)));
}

export function HandoffAssignees({ members, authorId, value, onChange, disabled }: Readonly<{
  members: QuestionBankMember[]; authorId: string; value: HandoffSelection;
  onChange: (value: HandoffSelection) => void; disabled: boolean;
}>) {
  function options(action: string) {
    return members.filter(person => person.actions.includes("read") && person.actions.includes(action) && (action !== "review" || person.user_id !== authorId))
      .map(person => ({ value: person.user_id, label: questionBankMemberLabel(person), disabled: questionBankMemberAmbiguous(person, members) }));
  }
  const reviewers = options("review"), publishers = options("publish");
  return <Space wrap>
    <label>审核教师<Select aria-label="指定审核教师" style={{ minWidth: 180 }} value={reviewers.some(person => person.value === value.reviewer_id && !person.disabled) ? value.reviewer_id : undefined} disabled={disabled} placeholder="选择独立审核教师" options={reviewers} onChange={id => onChange({ ...value, reviewer_id: id })} /></label>
    <label>发布教师<Select aria-label="指定发布教师" style={{ minWidth: 180 }} value={publishers.some(person => person.value === value.publisher_id && !person.disabled) ? value.publisher_id : undefined} disabled={disabled} placeholder="选择发布教师" options={publishers} onChange={id => onChange({ ...value, publisher_id: id })} /></label>
  </Space>;
}

const eventLabels = { assigned: "提交审核", reassigned: "调整交接", claimed: "领取待办", approved: "审核通过", published: "已发布", returned: "退回草稿" };

export function QuestionBankHandoffPanel(props: QuestionBankWorkspaceProps & { selected: QuestionBankVersion }) {
  const { selected, members, user, handoff, saving, canManage, canEdit } = props;
  const [historyOpen, setHistoryOpen] = useState(false);
  const [selection, setSelection] = useState<HandoffSelection>({ reviewer_id: selected.handoff?.reviewer_id ?? "", publisher_id: selected.handoff?.publisher_id ?? "" });
  const directory = members?.data?.members ?? [];
  const stage = selected.workflow_status === "reviewing" ? "review" : selected.workflow_status === "approved" ? "publish" : null;
  const recipient = stage === "review" ? selected.handoff?.reviewer_id : selected.handoff?.publisher_id;
  const ownMember = directory.find(person => person.user_id === user.id);
  const canClaim = stage && ownMember?.actions.includes(stage) && ownMember.actions.includes("read") && (!recipient || recipient === user.id) && !(stage === "review" && selected.author_id === user.id);
  const history = useQuery({
    queryKey: ["question-bank-handoff-events", user.tenant, user.id, selected.id, selected.handoff?.revision],
    queryFn: ({ signal }) => questionBankApi.listQuestionBankHandoffEvents({ path: { versionId: selected.id }, signal }),
    enabled: historyOpen,
  });
  // 同版本的他人改派也会更新交接修订；折叠表单不能继续保存旧人员选择。
  useEffect(() => {
    setSelection({ reviewer_id: selected.handoff?.reviewer_id ?? "", publisher_id: selected.handoff?.publisher_id ?? "" });
  }, [selected.handoff?.reviewer_id, selected.handoff?.publisher_id]);
  function name(id?: string) {
    if (!id) return "待领取";
    if (id === user.id) return "我";
    const person = directory.find(entry => entry.user_id === id);
    return person ? questionBankMemberLabel(person) : "已指派教师（姓名暂不可读取）";
  }
  return <div>
    {stage ? <>
      <p>{stage === "review" ? "审核教师" : "发布教师"}：{name(recipient)}{selected.handoff?.claimed_by ? ` · ${name(selected.handoff.claimed_by)}已领取` : ""}</p>
      {canClaim && !selected.handoff?.claimed_by && handoff ? <Button disabled={saving || members?.isLoading || members?.isError} onClick={() => handoff(stage)}>领取本题待办</Button> : null}
      {handoff && (canManage || canEdit && selected.author_id === user.id) ? <details><summary>调整交接人员</summary>
        <HandoffAssignees members={directory} authorId={selected.author_id} value={selection} onChange={setSelection} disabled={saving || Boolean(members?.isLoading || members?.isError)} />
        <Button disabled={saving || !handoffSelectionValid(directory, selected.author_id, selection) || Boolean(members?.isLoading || members?.isError)} onClick={() => handoff("assign", selection)}>保存交接人员</Button>
      </details> : null}
    </> : null}
    <details open={historyOpen} onToggle={event => setHistoryOpen(event.currentTarget.open)}><summary>交接记录</summary>
      {history.isLoading ? <p>正在读取交接记录…</p> : history.isError ? <Alert type="error" message={getUserErrorMessage(history.error, "交接记录读取失败")} action={<Button onClick={() => void history.refetch()}>重试</Button>} /> : <ol>{history.data?.events.map(event => <li key={event.id}>{eventLabels[event.kind]} · {name(event.actor_id)}{event.recipient_id ? ` → ${name(event.recipient_id)}` : ""} · {new Date(event.created_at).toLocaleString("zh-CN")}</li>)}</ol>}
      {!history.isLoading && !history.isError && history.data?.events.length === 0 ? <p>暂无交接记录。</p> : null}
    </details>
  </div>;
}
