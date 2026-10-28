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
import { InternalRouteButton } from "../../components/InternalRouteButton";
import { referenceText } from "../grading/workbench/components/FrozenQuestionReference";
import { QuestionBankReferenceImages } from "./QuestionBankReferenceImages";
import { useState } from "react";
import { HandoffAssignees, handoffSelectionValid, QuestionBankHandoffPanel, type HandoffSelection } from "./QuestionBankHandoffPanel";

const workflowLabels = { draft: "草稿", reviewing: "审核中", approved: "已批准", published: "已发布" };
type SelectedProps = QuestionBankWorkspaceProps & Readonly<{ selected: QuestionBankVersion }>;
type ListProps = Pick<QuestionBankWorkspaceProps, "bank" | "kind" | "onKindChange" | "canManage" | "openSchema" | "openAcl" | "items" | "selectItem" | "offset" | "setOffset" | "item"> & Readonly<{ onReselect: () => void }>;
type ListItem = QuestionBankItem & { display_stem?: string; display_version_id?: string; display_status?: keyof typeof workflowLabels };

export function QuestionBankList({ bank, kind, onKindChange, canManage, openSchema, openAcl, items, selectItem, offset, setOffset, item, onReselect }: ListProps) {
  return <section className="question-bank-list" aria-label="题目列表">
    <Space wrap>
      <Segmented value={kind} options={[{ value: "question", label: "题目" }, { value: "rubric_template", label: "评分标准模板" }]} onChange={(value) => onKindChange(value as typeof kind)} />
      {canManage ? <><Button icon={<Settings2 size={14} />} onClick={openSchema} disabled={!bank}>题目分类与知识点</Button><Button icon={<ShieldCheck size={14} />} onClick={openAcl} disabled={!bank}>访问权限</Button></> : null}
    </Space>
    <h2>{bank?.name ?? "题目列表"}<span>{items.data?.total ?? "—"}{" "}{kind === "rubric_template" ? "个模板" : "道题目"}</span></h2>
    {items.isError ? <ErrorState message={getUserErrorMessage(items.error, "题目加载失败")} onRetry={() => void items.refetch()} /> : <ResponsiveTable<ListItem>
      className="dense-data-table" rowKey={record => record.display_version_id ?? record.id} loading={items.isLoading} dataSource={items.data?.items ?? []}
      columns={[
        { title: "题目", render: (_, record) => <div><Button type="link" onClick={() => { if (record.display_version_id) selectItem(record, record.display_version_id); else selectItem(record); if (record.id === item?.id) onReselect(); }}>{record.display_stem?.slice(0, 140) || record.item_code}</Button>{record.display_stem ? <div className="muted">{record.item_code}</div> : null}</div> },
        { title: "学科", dataIndex: "subject_code", render: (code: string) => subjects.find((subject) => subject.value === code)?.label ?? code },
        { title: "年级范围", dataIndex: "grade_scope", render: gradeScopeLabel },
        { title: "状态", render: (_, record) => <Tag>{record.display_status ? workflowLabels[record.display_status] : record.current_published_version_id ? "有已发布版本" : "尚未发布"}</Tag> },
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

function QuestionBankReadOnlyScoring({ selected }: Readonly<{ selected: QuestionBankVersion }>) {
  const scoring = selected.scoring;
  return <section aria-label="本版本答案与评分标准">
    {scoring.answer ? <><h3>参考答案</h3><MathMarkdown>{referenceText(scoring.answer.standard_answer)}</MathMarkdown></> : null}
    {scoring.solution ? <><h3>解析</h3><MathMarkdown>{referenceText(scoring.solution)}</MathMarkdown></> : null}
    {scoring.rubric ? <><h3>评分细则</h3><MathMarkdown>{referenceText(scoring.rubric)}</MathMarkdown></> : null}
    <QuestionBankReferenceImages materials={scoring.reference_materials} versionId={selected.id} />
    {scoring.assets.length ? <><h3>附件</h3><ul>{scoring.assets.map(asset => <li key={asset.file_asset_id}>{asset.name}</li>)}</ul></> : null}
    <p>{scoring.use_policy === "exam_allowed" ? "可用于考试" : "仅用于练习"}</p>
  </section>;
}

function QuestionBankVersionEditor(props: SelectedProps) {
  const { selected, conflict, loadLatest, saving, editor, saveVersion, setContentDirty, canEdit, scoringDirty, bank, boundSchema, contentDirty, editorVersion, scoringSaved, setScoringDirty, user } = props;
  const combined = Boolean(props.saveCombinedScoring);
  const draftScore = Form.useWatch("default_score", editor);
  const draftType = Form.useWatch("question_type", editor);
  const draftOptions = Form.useWatch("options", editor);
  const draftArchetype = Form.useWatch("assessment_archetype", editor);
  // 整题保存使用同一修订提交内容和评分；旧独立调用方仍保留互锁，避免两次写入各自覆盖。
  return <>
    {conflict && <Alert type="warning" showIcon message="草稿已被其他人修改" description="你在本页的修改尚未被覆盖，请先比较版本。" action={<Button loading={saving} onClick={() => { loadLatest(); }}>查看最新版本</Button>} />}
    <Form form={editor} layout="vertical" onFinish={combined ? props.saveAllDraft : saveVersion} onValuesChange={() => setContentDirty(true)} disabled={!canEdit || (!combined && scoringDirty) || selected.workflow_status !== "draft" || bank?.status !== "active" || saving || conflict}>
      <Form.Item name="expected_revision" hidden><InputNumber /></Form.Item>
      <ContentFields schema={boundSchema.data?.schema} />
      {!combined ? <Button type="primary" htmlType="submit" loading={saving} disabled={scoringDirty || conflict || !canEdit || selected.workflow_status !== "draft" || bank?.status !== "active"}>保存草稿</Button> : null}
    </Form>
    {!combined && (contentDirty || scoringDirty) ? <Alert type="info" message={contentDirty ? "先保存题目内容，再编辑评分标准或提交审核。" : "先保存评分标准，再编辑题目内容或提交审核。"} /> : null}
    {editorVersion?.id === selected.id ? <ScoringEditor key={`${selected.id}:${props.scoringResetToken ?? 0}`} version={editorVersion} disabled={(!combined && contentDirty) || !canEdit || selected.workflow_status !== "draft" || bank?.status !== "active" || saving || conflict} onSaved={scoringSaved} onDirty={setScoringDirty} onDraftSnapshot={props.onScoringDraftSnapshot} canManageFiles={hasEveryPermission(user, ["file:manage"])} actorId={user.id} onSaveCombined={props.saveCombinedScoring} registerSubmit={props.registerScoringSubmit} draftScore={draftScore} restoredDraft={props.restoredScoringDraft} draftContent={combined ? { question_type: draftType ?? editorVersion.question_type, options: draftOptions ?? editorVersion.options, assessment_archetype: draftArchetype ?? editorVersion.assessment_archetype } : undefined} /> : null}
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
  assignees: HandoffSelection;
}>;

function QuestionBankReviewActions({ selected, user, transition, contentDirty, scoringDirty, conflict, saving, canEdit, members, contactsUnavailable, canReview, canPublish, assignees }: ReviewActionProps) {
  return <Space wrap style={{ marginTop: 12 }}>
    {selected.workflow_status === "draft" && canEdit ? <Button disabled={contentDirty || scoringDirty || conflict || saving || contactsUnavailable || Boolean(members && (!assignees.reviewer_id || !assignees.publisher_id))} onClick={() => { if (members) transition("submit-review", assignees); else transition("submit-review"); }}>提交审核</Button> : null}
    {selected.workflow_status === "reviewing" && hasEveryPermission(user, ["question_bank:review"]) && selected.author_id !== user.id && canReview ? <Button loading={saving} onClick={() => { transition("approve"); }}>批准版本</Button> : null}
    {["reviewing", "approved"].includes(selected.workflow_status) && (!members || (selected.author_id === user.id && canEdit) || canReview) ? <Button loading={saving} onClick={() => { transition("return-to-draft"); }}>退回草稿</Button> : null}
    {selected.workflow_status === "approved" && hasEveryPermission(user, ["question_bank:publish"]) && canPublish ? <Button type="primary" loading={saving} onClick={() => { transition("publish"); }}>发布版本</Button> : null}
  </Space>;
}

function QuestionBankReview(props: SelectedProps) {
  const { selected, bank, user, comment, setComment, reviews, members } = props;
  const [assignees, setAssignees] = useState<HandoffSelection>({ reviewer_id: selected.handoff?.reviewer_id ?? "", publisher_id: selected.handoff?.publisher_id ?? "" });
  const { message } = App.useApp();
  const reviewers = members?.data?.members.filter(person=>person.user_id !== selected.author_id && person.actions.includes("review") && person.actions.includes("read")) ?? [];
  const publishers = members?.data?.members.filter(person=>person.actions.includes("publish") && person.actions.includes("read")) ?? [];
  const contactsUnavailable = Boolean(members && (members.isLoading || members.isError || !handoffSelectionValid(members.data?.members ?? [], selected.author_id, assignees)));
  const canReview = (!members || reviewers.some(person => person.user_id === user.id)) && (!selected.handoff?.reviewer_id || selected.handoff.reviewer_id === user.id);
  const canPublish = (!members || publishers.some(person => person.user_id === user.id)) && (!selected.handoff?.publisher_id || selected.handoff.publisher_id === user.id);
  // 审核人可能使用不同角色入口；公共业务路径由接收人的登录身份选择对应端。
  const reviewPath = `/question-bank?bank=${encodeURIComponent(bank?.id ?? "")}&item=${encodeURIComponent(selected.item_id)}&version=${encodeURIComponent(selected.id)}`;
  const reviewLink = `${window.location.origin}${reviewPath}`;
  const examPath = pathForExperience("/exams", defaultExperience(user));
  async function copyReviewLink() {
    const contact = selected.workflow_status === "approved" ? "发布教师" : "审核教师";
    try { await navigator.clipboard.writeText(reviewLink); message.success(`链接已复制，请发给${contact}。`); }
    catch { message.warning(`复制失败，请手动复制下方链接，并联系${contact}。`); }
  }
  return <section className="question-bank-review">
    <h3>审核与发布</h3>
    {selected.workflow_status === "draft" ? <p>提交后锁定当前内容，需由其他教师审核。</p> : null}
    {selected.workflow_status === "reviewing" ? <p>审核中，当前内容已锁定。</p> : null}
    {selected.workflow_status === "approved" ? <p>审核通过，等待有发布权限的教师发布。</p> : null}
    <details><summary>查看审核与发布流程</summary><p>草稿 → 审核 → 发布到题库 → 选入考试。</p></details>
    {selected.workflow_status !== "published" && members?.isError ? <Alert type="warning" message="审核人员暂不可读取，请联系题库负责人" action={<Button onClick={()=>void members.refetch()}>重试人员列表</Button>} /> : null}
    {selected.workflow_status === "draft" && members && props.canEdit ? <HandoffAssignees members={members.data?.members ?? []} authorId={selected.author_id} value={assignees} onChange={setAssignees} disabled={props.saving || members.isError || members.isLoading} /> : null}
    {selected.workflow_status === "draft" && members && !members.isLoading && !members.isError && (!reviewers.length || !publishers.length) ? <p>请联系题库负责人配置独立审核教师和发布教师。</p> : null}
    {props.handoff ? <QuestionBankHandoffPanel {...props} /> : null}
    {["reviewing", "approved"].includes(selected.workflow_status) ? <details><summary>分享本题链接</summary><label>本题审核链接<Input aria-label="本题审核链接" value={reviewLink} readOnly /></label><Button onClick={()=>void copyReviewLink()}>复制本题审核链接</Button></details> : null}
    {selected.workflow_status === "published" ? <Alert type="success" message="已发布到学校题库" description="在考试的“小题与分值”中选入此版本。已有考试仍使用原版本。" action={hasPathAccess(user,examPath) ? <InternalRouteButton href={examPath}>去考试中选题</InternalRouteButton> : undefined} /> : null}
    <Input.TextArea aria-label="审核意见" value={comment} maxLength={2000} onChange={(event) => setComment(event.target.value)} placeholder="审核意见或退回原因" />
    <QuestionBankReviewActions {...props} assignees={assignees} contactsUnavailable={contactsUnavailable} canReview={canReview} canPublish={canPublish} />
    {reviews.isError ? <Alert type="error" message="审核记录加载失败" /> : <ol>{reviews.data?.reviews.map((review) => <li key={review.id}>{reviewDecisionLabel(review.decision)}{" "}· 修订 {review.content_revision} ·{" "}{new Date(review.created_at).toLocaleString("zh-CN")}{review.comment ? <p>{review.comment}</p> : null}</li>)}</ol>}
  </section>;
}

export function QuestionBankInspector(props: QuestionBankWorkspaceProps & Readonly<{ onBack: () => void }>) {
  const { item, onBack, selected, saving, loadLatest, canEdit, bank, derive, canRetire, retireItem, versions, version, setVersionId, versionOffset } = props;
  const [editingVersion, setEditingVersion] = useState("");
  const editing = Boolean(selected && (selected.id === editingVersion || props.contentDirty || props.scoringDirty) && selected.workflow_status === "draft" && canEdit);
  return <section className="question-bank-inspector" aria-label="题目详情">
    {item ? <Button className="question-bank-back" type="link" icon={<ArrowLeft size={16} />} onClick={onBack}>返回题目列表</Button> : null}
    {!item ? <Empty description="选择题目，查看预览与版本历史。" /> : <>
      <div className="question-bank-version-heading">
        <h2>{item.item_code}</h2>
        {selected && <Tag color={selected.workflow_status === "published" ? "green" : "gold"}>{workflowLabels[selected.workflow_status]} v{selected.version_no}</Tag>}
        {selected ? <Button disabled={saving} onClick={() => { loadLatest(); }}>查看最新版本</Button> : null}
        {selected?.workflow_status === "draft" && canEdit ? <Button disabled={saving || editing && (props.contentDirty || props.scoringDirty)} onClick={() => setEditingVersion(editing ? "" : selected.id)}>{editing ? "完成编辑" : "编辑本题"}</Button> : null}
        {canEdit && <Button icon={<Copy size={14} />} disabled={!selected || bank?.status !== "active"} loading={saving} onClick={() => { derive(); }}>创建新版本</Button>}
        {canRetire && item.status === "active" ? <Button danger disabled={saving} onClick={() => { retireItem(); }}>停止选用</Button> : null}
      </div>
      {versions.isError || version.isError ? <ErrorState message={getUserErrorMessage(versions.error ?? version.error, "版本加载失败")} onRetry={() => { void versions.refetch(); void version.refetch(); }} /> : <>
        <Select aria-label="版本历史" loading={versions.isLoading} value={selected?.id} onChange={(id) => setVersionId(id)} options={versions.data?.versions.map((entry) => ({ value: entry.id, label: `v${entry.version_no} · ${workflowLabels[entry.workflow_status]} · ${new Date(entry.created_at).toLocaleString("zh-CN")}` }))} />
        {(versions.data?.total ?? 0) > 20 && <Pagination size="small" current={versionOffset / 20 + 1} pageSize={20} total={versions.data?.total} showSizeChanger={false} onChange={(page) => setVersionId("", (page - 1) * 20)} />}
        {selected && <>{editing ? <QuestionBankVersionEditor {...props} selected={selected} /> : <><QuestionBankVersionPreview selected={selected} /><QuestionBankReadOnlyScoring selected={selected} /></>}<QuestionBankReview key={`${selected.id}:${selected.handoff?.revision ?? 0}`} {...props} selected={selected} /></>}
      </>}
    </>}
  </section>;
}
