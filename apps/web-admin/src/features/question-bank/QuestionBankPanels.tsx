import { Alert, App, Button, Empty, Form, Input, InputNumber, Pagination, Segmented, Select, Space, Tag } from "antd";
import { ArrowLeft, Copy, Settings2, ShieldCheck } from "lucide-react";
import type { QuestionBankItem, QuestionBankVersion } from "@edugrade/sdk";
import { ErrorState } from "../../components/PageState";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { MathMarkdown } from "../../components/MathMarkdown";
import { getUserErrorMessage } from "../../api/client";
import { hasEveryPermission } from "../../auth/session";
import { ScoringEditor } from "./ScoringEditor";
import { ContentFields, gradeScopeLabel } from "./ContentFields";
import { subjects, types } from "./questionBankOptions";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";
import { defaultExperience, pathForExperience } from "../../router/experience";
import { hasPathAccess } from "../../router/navigation";

const workflowLabels = { draft: "草稿", reviewing: "审核中", approved: "已批准", published: "已发布" };
type SelectedProps = QuestionBankWorkspaceProps & Readonly<{ selected: QuestionBankVersion }>;
type ListProps = Pick<QuestionBankWorkspaceProps, "bank" | "kind" | "onKindChange" | "canManage" | "openSchema" | "openAcl" | "items" | "selectItem" | "offset" | "setOffset" | "item"> & Readonly<{ onReselect: () => void }>;

export function QuestionBankList({ bank, kind, onKindChange, canManage, openSchema, openAcl, items, selectItem, offset, setOffset, item, onReselect }: ListProps) {
  return <section className="question-bank-list" aria-label="题目列表">
    <Space wrap>
      <Segmented value={kind} options={[{ value: "question", label: "题目" }, { value: "rubric_template", label: "评分标准模板" }]} onChange={(value) => onKindChange(value as typeof kind)} />
      {canManage ? <><Button icon={<Settings2 size={14} />} onClick={openSchema} disabled={!bank}>题目分类与知识点</Button><Button icon={<ShieldCheck size={14} />} onClick={openAcl} disabled={!bank}>访问权限</Button></> : null}
    </Space>
    <h2>{bank?.name ?? "题目列表"}<span>{items.data?.total ?? "—"}{" "}{kind === "rubric_template" ? "个模板" : "道题目"}</span></h2>
    {items.isError ? <ErrorState message={getUserErrorMessage(items.error, "题目加载失败")} onRetry={() => void items.refetch()} /> : <ResponsiveTable<QuestionBankItem>
      className="dense-data-table" rowKey="id" loading={items.isLoading} dataSource={items.data?.items ?? []}
      columns={[
        { title: "编号", dataIndex: "item_code", render: (code: string, record) => <Button type="link" onClick={() => { selectItem(record); if (record.id === item?.id) onReselect(); }}>{code}</Button> },
        { title: "学科", dataIndex: "subject_code", render: (code: string) => subjects.find((subject) => subject.value === code)?.label ?? code },
        { title: "年级范围", dataIndex: "grade_scope", render: gradeScopeLabel },
        { title: "状态", render: (_, record) => <Tag>{record.current_published_version_id ? "有已发布版本" : "尚未发布"}</Tag> },
      ]}
      pagination={{ current: offset / 20 + 1, pageSize: 20, total: items.data?.total, showSizeChanger: false, onChange: (page) => setOffset((page - 1) * 20) }}
    />}
  </section>;
}

function QuestionBankVersionPreview({ selected }: Readonly<{ selected: QuestionBankVersion }>) {
  return <>
    {selected.import_provenance ? <Alert type="info" showIcon message={`精选来源：第 ${selected.import_provenance.source.question_no} 题`} description={`保留原考试已确认的题目、答案与评分依据 · ${selected.import_provenance.dedup_decision === "new_version" ? "关联为新版本" : "新建题目"}`} /> : null}
    <div className="question-bank-preview">
      <span>{types.find((type) => type.value === selected.question_type)?.label}{" "}· {selected.default_score} 分</span>
      <MathMarkdown promoteInlineMath={false}>{selected.stem}</MathMarkdown>
      {selected.options.map((option, index) => <MathMarkdown key={`${index}:${option}`} promoteInlineMath={false}>{`${String.fromCodePoint(65 + index)}. ${option}`}</MathMarkdown>)}
    </div>
  </>;
}

function QuestionBankVersionEditor(props: SelectedProps) {
  const { selected, conflict, loadLatest, saving, editor, saveVersion, setContentDirty, canEdit, scoringDirty, bank, boundSchema, contentDirty, editorVersion, scoringSaved, setScoringDirty, user } = props;
  // 两个编辑区域共用版本修订号；另一侧有未保存内容时保持互锁，不因拆分组件提前放行。
  return <>
    {conflict && <Alert type="warning" showIcon message="草稿已被其他人修改" description="本地修改已保留。先查看并比较最新版本，可下载本地备份，再决定是否放弃修改。" action={<Button loading={saving} onClick={() => { loadLatest(); }}>查看最新版本</Button>} />}
    <Form form={editor} layout="vertical" onFinish={saveVersion} onValuesChange={() => setContentDirty(true)} disabled={!canEdit || scoringDirty || selected.workflow_status !== "draft" || bank?.status !== "active" || saving || conflict}>
      <Form.Item name="expected_revision" hidden><InputNumber /></Form.Item>
      <ContentFields schema={boundSchema.data?.schema} />
      <Button type="primary" htmlType="submit" loading={saving} disabled={scoringDirty || conflict || !canEdit || selected.workflow_status !== "draft" || bank?.status !== "active"}>保存草稿</Button>
    </Form>
    {contentDirty || scoringDirty ? <Alert type="info" message="请先保存当前编辑区域，再编辑另一部分或提交审核。" /> : null}
    {editorVersion?.id === selected.id ? <ScoringEditor key={`${selected.id}:${props.scoringResetToken ?? 0}`} version={editorVersion} disabled={contentDirty || !canEdit || selected.workflow_status !== "draft" || bank?.status !== "active" || saving || conflict} onSaved={scoringSaved} onDirty={setScoringDirty} onDraftSnapshot={props.onScoringDraftSnapshot} canManageFiles={hasEveryPermission(user, ["file:manage"])} actorId={user.id} /> : null}
  </>;
}

function reviewDecisionLabel(decision: string) {
  switch (decision) {
    case "approve": return "批准";
    case "publish": return "发布";
    case "submit-review": return "提交审核";
    default: return "退回草稿";
  }
}

type ReviewActionProps = Pick<SelectedProps, "selected" | "user" | "transition" | "contentDirty" | "scoringDirty" | "conflict" | "saving" | "canEdit" | "members"> & Readonly<{
  contactsUnavailable: boolean;
  canReview: boolean;
  canPublish: boolean;
}>;

function QuestionBankReviewActions({ selected, user, transition, contentDirty, scoringDirty, conflict, saving, canEdit, members, contactsUnavailable, canReview, canPublish }: ReviewActionProps) {
  return <Space wrap style={{ marginTop: 12 }}>
    {selected.workflow_status === "draft" && canEdit ? <Button disabled={contentDirty || scoringDirty || conflict || saving || contactsUnavailable} onClick={() => { transition("submit-review"); }}>提交审核</Button> : null}
    {selected.workflow_status === "reviewing" && hasEveryPermission(user, ["question_bank:review"]) && selected.author_id !== user.id && canReview ? <Button loading={saving} onClick={() => { transition("approve"); }}>批准版本</Button> : null}
    {["reviewing", "approved"].includes(selected.workflow_status) && (!members || (selected.author_id === user.id && canEdit) || canReview) ? <Button loading={saving} onClick={() => { transition("return-to-draft"); }}>退回草稿</Button> : null}
    {selected.workflow_status === "approved" && hasEveryPermission(user, ["question_bank:publish"]) && canPublish ? <Button type="primary" loading={saving} onClick={() => { transition("publish"); }}>发布版本</Button> : null}
  </Space>;
}

function QuestionBankReview(props: SelectedProps) {
  const { selected, bank, user, comment, setComment, reviews, members } = props;
  const { message } = App.useApp();
  const reviewers = members?.data?.members.filter(person=>person.user_id !== selected.author_id && person.actions.includes("review") && person.actions.includes("read")) ?? [];
  const publishers = members?.data?.members.filter(person=>person.actions.includes("publish") && person.actions.includes("read")) ?? [];
  const contactsUnavailable = Boolean(members && (members.isLoading || members.isError || !reviewers.length));
  const canReview = !members || reviewers.some(person => person.user_id === user.id);
  const canPublish = !members || publishers.some(person => person.user_id === user.id);
  const reviewLink = `${window.location.href.split("#")[0]}#/question-bank?bank=${encodeURIComponent(bank?.id ?? "")}&item=${encodeURIComponent(selected.item_id)}&version=${encodeURIComponent(selected.id)}`;
  const examPath = pathForExperience("/exams", defaultExperience(user));
  async function copyReviewLink() {
    try { await navigator.clipboard.writeText(reviewLink); message.success("审核链接已复制，请发送给上方有权限的审核教师；系统不会自动通知。"); }
    catch { message.warning("暂不能自动复制，请选择下方审核链接后复制，并联系审核教师。"); }
  }
  return <section className="question-bank-review">
    <h3>审核与发布</h3>
    <p>草稿 → 审核 → 发布到学校题库 → 在考试中选入题目。提交后锁定当前内容，作者不能审核自己的版本。</p>
    {members?.isError ? <Alert type="warning" message="审核人员暂不可读取，请联系题库负责人" action={<Button onClick={()=>void members.refetch()}>重试人员列表</Button>} /> : <>
      <p>请联系审核教师：{members?.isLoading ? "正在读取…" : reviewers.map(person=>person.display_name).join("、") || "尚未配置，请联系题库负责人授权独立审核教师"}。</p>
      <p>审核通过后由以下人员发布：{publishers.map(person=>person.display_name).join("、") || "尚未配置，请联系题库负责人"}。此处列出实际有权限的人员，提交不会自动发送通知。</p>
    </>}
    {["reviewing", "approved"].includes(selected.workflow_status) ? <div><label>本题审核链接<Input aria-label="本题审核链接" value={reviewLink} readOnly /></label><Button onClick={()=>void copyReviewLink()}>复制本题审核链接</Button></div> : null}
    {selected.workflow_status === "published" ? <Alert type="success" message="已发布到学校题库" description="下一步在考试的“小题与分值”页点击“从题库选题”。已有考试保留原版本；新版本不会自动替换考试中的题目。" action={hasPathAccess(user,examPath) ? <Button href={`#${examPath}`}>去考试中选题</Button> : undefined} /> : null}
    <Input.TextArea aria-label="审核意见" value={comment} maxLength={2000} onChange={(event) => setComment(event.target.value)} placeholder="审核意见或退回原因" />
    <QuestionBankReviewActions {...props} contactsUnavailable={contactsUnavailable} canReview={canReview} canPublish={canPublish} />
    {reviews.isError ? <Alert type="error" message="审核记录加载失败" /> : <ol>{reviews.data?.reviews.map((review) => <li key={review.id}>{reviewDecisionLabel(review.decision)}{" "}· 修订 {review.content_revision} ·{" "}{new Date(review.created_at).toLocaleString("zh-CN")}<p>{review.comment || "无补充意见"}</p></li>)}</ol>}
  </section>;
}

export function QuestionBankInspector(props: QuestionBankWorkspaceProps & Readonly<{ onBack: () => void }>) {
  const { item, onBack, selected, saving, loadLatest, canEdit, bank, derive, canRetire, retireItem, versions, version, setVersionId, versionOffset } = props;
  return <section className="question-bank-inspector" aria-label="题目详情">
    {item ? <Button className="question-bank-back" type="link" icon={<ArrowLeft size={16} />} onClick={onBack}>返回题目列表</Button> : null}
    {!item ? <Empty description="选择题目，查看预览与版本历史。" /> : <>
      <div className="question-bank-version-heading">
        <h2>{item.item_code}</h2>
        {selected && <Tag color={selected.workflow_status === "published" ? "green" : "gold"}>{workflowLabels[selected.workflow_status]} v{selected.version_no}</Tag>}
        {selected ? <Button disabled={saving} onClick={() => { loadLatest(); }}>查看最新版本</Button> : null}
        {canEdit && <Button icon={<Copy size={14} />} disabled={!selected || bank?.status !== "active"} loading={saving} onClick={() => { derive(); }}>创建新版本</Button>}
        {canRetire && item.status === "active" ? <Button danger disabled={saving} onClick={() => { retireItem(); }}>停止选用</Button> : null}
      </div>
      {versions.isError || version.isError ? <ErrorState message={getUserErrorMessage(versions.error ?? version.error, "版本加载失败")} onRetry={() => { void versions.refetch(); void version.refetch(); }} /> : <>
        <Select aria-label="版本历史" loading={versions.isLoading} value={selected?.id} onChange={(id) => setVersionId(id)} options={versions.data?.versions.map((entry) => ({ value: entry.id, label: `v${entry.version_no} · ${workflowLabels[entry.workflow_status]} · ${new Date(entry.created_at).toLocaleString("zh-CN")}` }))} />
        {(versions.data?.total ?? 0) > 20 && <Pagination size="small" current={versionOffset / 20 + 1} pageSize={20} total={versions.data?.total} showSizeChanger={false} onChange={(page) => setVersionId("", (page - 1) * 20)} />}
        {selected && <><QuestionBankVersionPreview selected={selected} /><QuestionBankVersionEditor {...props} selected={selected} /><QuestionBankReview {...props} selected={selected} /></>}
      </>}
    </>}
  </section>;
}
