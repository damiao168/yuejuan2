import { Alert, Button, Empty, Form, Input, InputNumber, Pagination, Segmented, Select, Space, Tag } from "antd";
import type { FormInstance } from "antd";
import type { UseQueryResult } from "@tanstack/react-query";
import { Copy, Settings2, ShieldCheck } from "lucide-react";
import type {
  QuestionBank,
  QuestionBankItem,
  QuestionBankItemPage,
  QuestionBankMetadataSchemaResponse,
  QuestionBankReviewsResponse,
  QuestionBankVersion,
  QuestionBankVersionPage,
  QuestionBankVersionResponse,
  UpdateQuestionBankVersionRequest,
} from "@edugrade/sdk";
import { ErrorState } from "../../components/PageState";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { getUserErrorMessage } from "../../api/client";
import { hasEveryPermission, type SessionUser } from "../../auth/session";
import { ScoringEditor } from "./ScoringEditor";
import { ContentFields } from "./ContentFields";
import { subjects, types } from "./questionBankOptions";

const workflowLabels = {
  draft: "草稿",
  reviewing: "审核中",
  approved: "已批准",
  published: "已发布",
};

type QueryView<T> = Pick<UseQueryResult<T, Error>, "data" | "isError" | "isLoading" | "error" | "refetch">;

type QuestionBankWorkspaceProps = {
  bank?: QuestionBank;
  kind: "question" | "rubric_template";
  onKindChange: (kind: "question" | "rubric_template") => void;
  canManage: boolean;
  openSchema: () => void;
  openAcl: () => void;
  items: QueryView<QuestionBankItemPage>;
  selectItem: (item: QuestionBankItem) => void;
  offset: number;
  setOffset: (offset: number) => void;
  item: QuestionBankItem | null;
  selected?: QuestionBankVersion;
  versions: QueryView<QuestionBankVersionPage>;
  version: QueryView<QuestionBankVersionResponse>;
  versionOffset: number;
  setVersionOffset: (offset: number) => void;
  setVersionId: (id: string) => void;
  saving: boolean;
  loadLatest: () => void;
  canEdit: boolean;
  derive: () => void;
  canRetire: boolean;
  retireItem: () => void;
  boundSchema: QueryView<QuestionBankMetadataSchemaResponse>;
  editor: FormInstance;
  saveVersion: (values: UpdateQuestionBankVersionRequest) => void;
  contentDirty: boolean;
  setContentDirty: (dirty: boolean) => void;
  scoringDirty: boolean;
  conflict: boolean;
  editorVersion: QuestionBankVersion | null;
  scoringSaved: (version: QuestionBankVersion) => void;
  setScoringDirty: (dirty: boolean) => void;
  user: SessionUser;
  comment: string;
  setComment: (comment: string) => void;
  transition: (decision: "submit-review" | "approve" | "return-to-draft" | "publish") => void;
  reviews: QueryView<QuestionBankReviewsResponse>;
};

export function QuestionBankWorkspace(props: QuestionBankWorkspaceProps) {
  const {
    bank, kind, onKindChange, canManage, openSchema, openAcl, items, selectItem,
    offset, setOffset, item, selected, versions, version, versionOffset,
    setVersionOffset, setVersionId, saving, loadLatest, canEdit, derive,
    canRetire, retireItem, boundSchema, editor, saveVersion, contentDirty, setContentDirty,
    scoringDirty, conflict, editorVersion, scoringSaved, setScoringDirty,
    user, comment, setComment, transition, reviews,
  } = props;
  return (				<div className="question-bank-workspace">
					<section className="question-bank-list" aria-label="题目列表">
						<Space wrap>
							<Segmented
								value={kind}
								options={[
									{ value: "question", label: "题目" },
									{ value: "rubric_template", label: "Rubric 模板" },
								]}
								onChange={(value) => {
onKindChange(value as typeof kind);
								}}
							/>
							{canManage ? (
								<>
								<Button icon={<Settings2 size={14} />} onClick={openSchema} disabled={!bank}>
										Metadata 规则
									</Button>
								<Button icon={<ShieldCheck size={14} />} onClick={openAcl} disabled={!bank}>
										访问权限
									</Button>
								</>
							) : null}
						</Space>
						<h2>
							{bank?.name ?? "题目列表"}
							<span>
								{items.data?.total ?? "—"}{" "}
								{kind === "rubric_template" ? "个模板" : "道题目"}
							</span>
						</h2>
						{items.isError ? (
							<ErrorState
								message={getUserErrorMessage(items.error, "题目加载失败")}
								onRetry={() => void items.refetch()}
							/>
						) : (
							<ResponsiveTable<QuestionBankItem>
								className="dense-data-table"
								rowKey="id"
								loading={items.isLoading}
								dataSource={items.data?.items ?? []}
								columns={[
									{
										title: "编号",
										dataIndex: "item_code",
										render: (code: string, record) => (
											<Button type="link" onClick={() => selectItem(record)}>
												{code}
											</Button>
										),
									},
									{
										title: "学科",
										dataIndex: "subject_code",
										render: (code: string) =>
											subjects.find((s) => s.value === code)?.label ?? code,
									},
									{ title: "年级范围", dataIndex: "grade_scope" },
									{
										title: "状态",
										render: (_, record) => (
											<Tag>
												{record.current_published_version_id
													? "有已发布版本"
													: "尚未发布"}
											</Tag>
										),
									},
								]}
								pagination={{
									current: offset / 20 + 1,
									pageSize: 20,
									total: items.data?.total,
									showSizeChanger: false,
									onChange: (page) => setOffset((page - 1) * 20),
								}}
							/>
						)}
					</section>
					<section className="question-bank-inspector" aria-label="题目详情">
						{!item ? (
							<Empty description="选择题目，查看预览与版本历史。" />
						) : (
							<>
								<div className="question-bank-version-heading">
									<h2>{item.item_code}</h2>
									{selected && (
										<Tag
											color={
												selected.workflow_status === "published"
													? "green"
													: "gold"
											}
										>
											{workflowLabels[selected.workflow_status]} v
											{selected.version_no}
										</Tag>
									)}
									{selected ? (
										<Button disabled={saving} onClick={() => void loadLatest()}>
											加载最新版本
										</Button>
									) : null}
									{canEdit && (
										<Button
											icon={<Copy size={14} />}
											disabled={!selected || bank?.status !== "active"}
											loading={saving}
											onClick={() => void derive()}
										>
											创建新版本
										</Button>
									)}
									{canRetire && item.status === "active" ? (
										<Button danger disabled={saving} onClick={() => void retireItem()}>
											退役题目
										</Button>
									) : null}
								</div>
								{versions.isError || version.isError ? (
									<ErrorState
										message={getUserErrorMessage(
											versions.error ?? version.error,
											"版本加载失败",
										)}
										onRetry={() => {
											void versions.refetch();
											void version.refetch();
										}}
									/>
								) : (
									<>
										<Select
											aria-label="版本历史"
											loading={versions.isLoading}
											value={selected?.id}
											onChange={setVersionId}
											options={versions.data?.versions.map((v) => ({
												value: v.id,
												label: `v${v.version_no} · ${workflowLabels[v.workflow_status]} · ${new Date(v.created_at).toLocaleString("zh-CN")}`,
											}))}
										/>
										{(versions.data?.total ?? 0) > 20 && (
											<Pagination
												size="small"
												current={versionOffset / 20 + 1}
												pageSize={20}
												total={versions.data?.total}
												showSizeChanger={false}
												onChange={(page) => {
													setVersionOffset((page - 1) * 20);
													setVersionId("");
												}}
											/>
										)}
										{selected && (
											<>
												{selected.import_provenance ? <Alert type="info" showIcon message={`精选来源：第 ${selected.import_provenance.source.question_no} 题`} description={`readiness ${selected.import_provenance.source.readiness_snapshot_id} · Assessment ${selected.import_provenance.source.assessment_snapshot_id} · ${selected.import_provenance.dedup_decision === "new_version" ? "关联为新版本" : "新建题目"}`} /> : null}
												<div className="question-bank-preview">
													<span>
														{
															types.find(
																(t) => t.value === selected.question_type,
															)?.label
														}{" "}
														· {selected.default_score} 分
													</span>
													<p>{selected.stem}</p>
													{selected.options.map((option, i) => (
														<p key={`${i}:${option}`}>
															{String.fromCharCode(65 + i)}. {option}
														</p>
													))}
												</div>
												{conflict && (
													<Alert
														type="warning"
														showIcon
														message="草稿已被其他人修改"
														description="当前编辑内容已保留，请先重新加载最新版本，再核对修改。"
														action={
															<Button
																loading={saving}
																onClick={() => void loadLatest()}
															>
																加载最新版本
															</Button>
														}
													/>
												)}
												<Form
													form={editor}
													layout="vertical"
													onFinish={saveVersion}
													onValuesChange={() => setContentDirty(true)}
													disabled={
														!canEdit ||
														scoringDirty ||
														selected.workflow_status !== "draft" ||
														bank?.status !== "active" ||
														saving ||
														conflict
													}
												>
													<Form.Item name="expected_revision" hidden>
														<InputNumber />
													</Form.Item>
											<ContentFields schema={boundSchema.data?.schema} />
													<Button
														type="primary"
														htmlType="submit"
														loading={saving}
														disabled={
															scoringDirty ||
															conflict ||
															!canEdit ||
															selected.workflow_status !== "draft" ||
															bank?.status !== "active"
														}
													>
														保存草稿
													</Button>
												</Form>
												{contentDirty || scoringDirty ? (
													<Alert
														type="info"
														message="请先保存当前编辑区域，再编辑另一部分或提交审核。"
													/>
												) : null}
												{editorVersion?.id === selected.id ? (
													<ScoringEditor
														key={selected.id}
														version={editorVersion}
														disabled={
															contentDirty ||
															!canEdit ||
															selected.workflow_status !== "draft" ||
															bank?.status !== "active" ||
															saving ||
															conflict
														}
														onSaved={scoringSaved}
														onDirty={setScoringDirty}
														canManageFiles={hasEveryPermission(user, [
															"file:manage",
														])}
														actorId={user.id}
													/>
												) : null}
												<section className="question-bank-review">
													<h3>审核与发布</h3>
													<p>
														提交后锁定题干、答案和评分标准。作者不能批准自己的版本。
													</p>
													<Input.TextArea
														aria-label="审核意见"
														value={comment}
														maxLength={2000}
														onChange={(e) => setComment(e.target.value)}
														placeholder="审核意见或退回原因"
													/>
													<Space wrap style={{ marginTop: 12 }}>
														{selected.workflow_status === "draft" && canEdit ? (
															<Button
																disabled={
																	contentDirty ||
																	scoringDirty ||
																	conflict ||
																	saving
																}
																onClick={() => void transition("submit-review")}
															>
																提交审核
															</Button>
														) : null}
														{selected.workflow_status === "reviewing" &&
														hasEveryPermission(user, [
															"question_bank:review",
														]) &&
														selected.author_id !== user.id ? (
															<Button
																loading={saving}
																onClick={() => void transition("approve")}
															>
																批准版本
															</Button>
														) : null}
														{["reviewing", "approved"].includes(
															selected.workflow_status,
														) ? (
															<Button
																loading={saving}
																onClick={() =>
																	void transition("return-to-draft")
																}
															>
																退回草稿
															</Button>
														) : null}
														{selected.workflow_status === "approved" &&
														hasEveryPermission(user, [
															"question_bank:publish",
														]) ? (
															<Button
																type="primary"
																loading={saving}
																onClick={() => void transition("publish")}
															>
																发布版本
															</Button>
														) : null}
													</Space>
													{reviews.isError ? (
														<Alert type="error" message="审核记录加载失败" />
													) : (
														<ol>
															{reviews.data?.reviews.map((r) => (
																<li key={r.id}>
																	{r.decision === "approve"
																		? "批准"
																		: r.decision === "publish"
																			? "发布"
																			: r.decision === "submit-review"
																				? "提交审核"
																				: "退回草稿"}{" "}
																	· 修订 {r.content_revision} ·{" "}
																	{new Date(r.created_at).toLocaleString(
																		"zh-CN",
																	)}
																	<p>{r.comment || "无补充意见"}</p>
																</li>
															))}
														</ol>
													)}
												</section>
											</>
										)}
									</>
								)}
							</>
						)}
					</section>
				</div>
  );
}

