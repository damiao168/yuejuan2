import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Alert,
	App,
	Button,
	Empty,
	Form,
	Input,
	InputNumber,
	Modal,
	Pagination,
	Segmented,
	Select,
	Space,
	Switch,
	Tag,
} from "antd";
import { Copy, Filter, Plus, RefreshCw, Settings2, ShieldCheck } from "lucide-react";
import type {
	CreateQuestionBankItemRequest,
	CreateQuestionBankRequest,
	QuestionBankItem,
	QuestionBankMetadataSchema,
	QuestionBankSearchItem,
	QuestionBankVersion,
	UpdateQuestionBankVersionRequest,
	UpdateQuestionBankMetadataSchemaRequest,
} from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { listSchools } from "../../api/org";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { hasEveryPermission, type SessionUser } from "../../auth/session";
import { ErrorState } from "../../components/PageState";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import "./question-bank.css";
import { ScoringEditor } from "./ScoringEditor";

const workflowLabels = {
	draft: "草稿",
	reviewing: "审核中",
	approved: "已批准",
	published: "已发布",
};

const subjects = [
	{ value: "chinese", label: "语文" },
	{ value: "mathematics", label: "数学" },
	{ value: "english", label: "英语" },
	{ value: "physics", label: "物理" },
	{ value: "chemistry", label: "化学" },
	{ value: "biology", label: "生物" },
	{ value: "history", label: "历史" },
	{ value: "geography", label: "地理" },
	{ value: "ethics_politics", label: "道德与法治 / 思想政治" },
];
const types = [
	{ value: "single_choice", label: "单选题" },
	{ value: "multiple_choice", label: "多选题" },
	{ value: "true_false", label: "判断题" },
	{ value: "fill_blank", label: "填空题" },
	{ value: "numeric", label: "数值题" },
	{ value: "formula", label: "公式题" },
	{ value: "short_answer", label: "简答题" },
	{ value: "calculation", label: "计算题" },
	{ value: "essay", label: "作文" },
	{ value: "discussion", label: "论述题" },
	{ value: "coding", label: "编程题" },
];
const archetypes = [
	{ value: "selected_response", label: "选择作答" },
	{ value: "exact_text", label: "精确文本" },
	{ value: "numeric_expression", label: "数值与表达式" },
	{ value: "structured_steps", label: "分步骤作答" },
	{ value: "short_constructed", label: "短建构作答" },
	{ value: "extended_response", label: "长作答" },
	{ value: "diagram_graph", label: "图形与图表" },
	{ value: "table_experiment", label: "表格与实验" },
];
const defaults: CreateQuestionBankItemRequest = {
	item_code: "",
	question_type: "short_answer",
	assessment_archetype: "short_constructed",
	stem: "",
	default_score: 5,
	options: [],
	knowledge_points: [],
	metadata: {
		subject_code: "mathematics",
		education_stage: "junior",
		grade_scope: "grade_8",
		difficulty_band: "unclassified",
		cognitive_level: "unclassified",
		copyright: "unknown",
		language: "zh-CN",
		intended_use: "practice",
	},
};
function contentFrom(
	v: QuestionBankVersion,
): Omit<UpdateQuestionBankVersionRequest, "expected_revision"> {
	return {
		question_type: v.question_type,
		assessment_archetype: v.assessment_archetype,
		stem: v.stem,
		default_score: v.default_score,
		options: v.options,
		knowledge_points: v.knowledge_points,
		metadata: v.metadata,
		custom_metadata: v.custom_metadata,
	};
}

type SearchFilters = {
	q: string;
	subject_code?: CreateQuestionBankItemRequest["metadata"]["subject_code"];
	knowledge_point?: string;
	question_type?: string;
	workflow_status?: "draft" | "reviewing" | "approved" | "published";
	difficulty_band?: string;
	intended_use?: string;
	mode: "default" | "published" | "my_drafts" | "all";
};

const emptySearch: SearchFilters = { q: "", mode: "default" };

export function QuestionBankPage({ user }: { user: SessionUser }) {
	const { message } = App.useApp();
	const client = useQueryClient();
	const prefix = ["question-bank", user.tenant, user.id];
	const [bankId, setBankId] = useState("");
	const [item, setItem] = useState<QuestionBankItem | null>(null);
	const [versionId, setVersionId] = useState("");
	const [kind, setKind] = useState<"question" | "rubric_template">("question");
	const [editorVersion, setEditorVersion] =
		useState<QuestionBankVersion | null>(null);
	const [aclOpen, setAclOpen] = useState(false);
	const [schemaOpen, setSchemaOpen] = useState(false);
	const [searchOpen, setSearchOpen] = useState(false);
	const [searchFilters, setSearchFilters] = useState<SearchFilters>(emptySearch);
	const [searchOffset, setSearchOffset] = useState(0);
	const [comment, setComment] = useState("");
	const [contentDirty, setContentDirty] = useState(false);
	const [scoringDirty, setScoringDirty] = useState(false);
	const [query, setQuery] = useState("");
	const [offset, setOffset] = useState(0);
	const [bankOffset, setBankOffset] = useState(0);
	const [versionOffset, setVersionOffset] = useState(0);
	const [dialog, setDialog] = useState<"bank" | "item" | null>(null);
	const [saving, setSaving] = useState(false);
	const [conflict, setConflict] = useState(false);
	const [editor] = Form.useForm();
	const [aclForm] = Form.useForm();
	const [schemaForm] = Form.useForm();
	const pending = useRef<{ fingerprint: string; key: string } | null>(null);
	const loadedVersion = useRef<string | null>(null);
	const canCreate = hasEveryPermission(user, ["question_bank:create"]);
	const canEdit = hasEveryPermission(user, ["question_bank:edit"]);
	const canManage = hasEveryPermission(user, ["question_bank:manage"]);
	const canRetire = hasEveryPermission(user, ["question_bank:retire"]);
	const banks = useQuery({
		queryKey: [...prefix, "banks", bankOffset],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBanks({
				query: { limit: 20, offset: bankOffset },
				signal,
			}),
	});
	const schools = useQuery({
		queryKey: [...prefix, "schools"],
		queryFn: listSchools,
		enabled: canCreate && hasEveryPermission(user, ["org:manage"]),
	});
	const items = useQuery({
		queryKey: [...prefix, "items", bankId, kind, query, offset],
		queryFn: ({ signal }) =>
			kind === "rubric_template"
				? questionBankApi.listQuestionBankRubricTemplates({
						query: { bank_id: bankId, q: query, limit: 20, offset },
						signal,
					})
				: questionBankApi.listQuestionBankItems({
						path: { bankId },
						query: { q: query, limit: 20, offset },
						signal,
					}),
		enabled: Boolean(bankId),
	});
	const versions = useQuery({
		queryKey: [...prefix, "versions", item?.id, versionOffset],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBankVersions({
				path: { itemId: item?.id ?? "" },
				query: { limit: 20, offset: versionOffset },
				signal,
			}),
		enabled: Boolean(item),
	});
	const version = useQuery({
		queryKey: [...prefix, "version", versionId],
		queryFn: ({ signal }) =>
			questionBankApi.getQuestionBankVersion({ path: { versionId }, signal }),
		enabled: Boolean(versionId),
	});
	const selected = version.data?.version;
	const currentSchema = useQuery({
		queryKey: [...prefix, "metadata-schema", bankId, "current"],
		queryFn: ({ signal }) =>
			questionBankApi.getQuestionBankMetadataSchema({
				path: { bankId },
				signal,
			}),
		enabled: Boolean(bankId),
	});
	const boundSchema = useQuery({
		queryKey: [...prefix, "metadata-schema", bankId, selected?.schema_version],
		queryFn: ({ signal }) =>
			questionBankApi.getQuestionBankMetadataSchema({
				path: { bankId },
				query: { version: selected?.schema_version },
				signal,
			}),
		enabled: Boolean(bankId && selected?.schema_version),
	});
	const acl = useQuery({
		queryKey: [...prefix, "acl", bankId],
		queryFn: ({ signal }) =>
			questionBankApi.getQuestionBankACL({ path: { bankId }, signal }),
		enabled: Boolean(bankId && aclOpen && canManage),
	});
	const searchResults = useQuery({
		queryKey: [...prefix, "search", bankId, searchFilters, searchOffset],
		queryFn: ({ signal }) =>
			questionBankApi.searchQuestionBankItems({
				query: {
					...searchFilters,
					bank_id: bankId,
					sort: "item_code_asc",
					limit: 20,
					offset: searchOffset,
				},
				signal,
			}),
		enabled: Boolean(bankId && searchOpen),
	});
	const reviews = useQuery({
		queryKey: [...prefix, "reviews", versionId],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBankReviews({ path: { versionId }, signal }),
		enabled: Boolean(versionId),
	});
	const bank = banks.data?.banks.find((b) => b.id === bankId);
	useEffect(() => {
		if (!bankId && banks.data?.banks[0]) setBankId(banks.data.banks[0].id);
	}, [bankId, banks.data]);
	useEffect(() => {
		if (!versionId && versions.data?.versions[0])
			setVersionId(versions.data.versions[0].id);
	}, [versionId, versions.data]);
	useEffect(() => {
		// Background refetches must not replace unsaved edits or advance their
		// expected revision. Load only on explicit version selection/save/reload.
		if (selected && loadedVersion.current !== selected.id) {
			editor.setFieldsValue({
				...contentFrom(selected),
				expected_revision: selected.revision,
			});
			loadedVersion.current = selected.id;
			setEditorVersion(selected);
			setContentDirty(false);
			setScoringDirty(false);
			setConflict(false);
		}
	}, [selected, editor]);
	useEffect(() => {
		if (!aclOpen || !acl.data?.acl) return;
		aclForm.setFieldsValue({
			bindings: acl.data.acl.bindings
				.filter((binding) => binding.preset !== "Custom")
				.map(({ user_id, preset }) => ({ user_id, preset })),
			groups: acl.data.acl.groups.map((group) => ({
				id: group.id,
				name: group.name,
				preset: group.preset,
				members: group.member_user_ids.join("\n"),
			})),
		});
	}, [acl.data, aclForm, aclOpen]);
	useEffect(() => {
		if (!schemaOpen || !currentSchema.data?.schema) return;
		schemaForm.setFieldsValue({
			fields: currentSchema.data.schema.fields.map((field) => ({
				...field,
				options_text: field.options
					?.map((option) => `${option.value}|${option.label}`)
					.join("\n"),
			})),
			taxonomies: currentSchema.data.schema.taxonomies.map((taxonomy) => ({
				id: taxonomy.id,
				label: taxonomy.label,
				terms_text: taxonomy.terms
					.map((term) => `${term.id}|${term.label}`)
					.join("\n"),
			})),
		});
	}, [currentSchema.data, schemaForm, schemaOpen]);
	function selectBank(id: string) {
		loadedVersion.current = null;
		setBankId(id);
		setItem(null);
		setVersionId("");
		setOffset(0);
		setQuery("");
		setConflict(false);
	}
	function selectItem(next: QuestionBankItem) {
		loadedVersion.current = null;
		setItem(next);
		setVersionId("");
		setVersionOffset(0);
		setConflict(false);
	}
	async function command<T>(
		operation: string,
		payload: unknown,
		execute: (headers: { "Idempotency-Key": string }) => Promise<T>,
	): Promise<T> {
		const fingerprint = JSON.stringify({ operation, payload });
		if (pending.current?.fingerprint !== fingerprint)
			pending.current = { fingerprint, key: crypto.randomUUID() };
		const result = await execute({ "Idempotency-Key": pending.current.key });
		pending.current = null;
		return result;
	}
	async function refresh() {
		await client.invalidateQueries({ queryKey: prefix });
	}
	async function createBank(values: CreateQuestionBankRequest) {
		setSaving(true);
		try {
			const result = await command("create-bank", values, (headers) =>
				questionBankApi.createQuestionBank({ body: values, headers }),
			);
			setDialog(null);
			setBankOffset(0);
			selectBank(result.bank.id);
			await refresh();
			message.success("题库已创建");
		} catch (error) {
			message.error(getUserErrorMessage(error, "创建题库失败"));
		} finally {
			setSaving(false);
		}
	}
	async function createItem(values: CreateQuestionBankItemRequest) {
		values = {
			...values,
			options: values.options?.filter((option) => option.trim()) ?? [],
		};
		setSaving(true);
		try {
			const result = await command(
				`create-item:${bankId}`,
				values,
				(headers) =>
					kind === "rubric_template"
						? questionBankApi.createQuestionBankRubricTemplate({
								body: { ...values, bank_id: bankId },
								headers,
							})
						: questionBankApi.createQuestionBankItem({
								path: { bankId },
								body: values,
								headers,
							}),
			);
			setDialog(null);
			setOffset(0);
			selectItem(result.item);
			await refresh();
			message.success(
				kind === "rubric_template" ? "模板草稿已创建" : "题目草稿已创建",
			);
		} catch (error) {
			message.error(
				getUserErrorMessage(
					error,
					kind === "rubric_template" ? "创建模板失败" : "创建题目失败",
				),
			);
		} finally {
			setSaving(false);
		}
	}
	async function saveVersion(values: UpdateQuestionBankVersionRequest) {
		if (!selected) return;
		values = {
			...values,
			options: values.options?.filter((option) => option.trim()) ?? [],
		};
		setSaving(true);
		try {
			const result = await command(
				`update-version:${selected.id}`,
				values,
				(headers) =>
					questionBankApi.updateQuestionBankVersion({
						path: { versionId: selected.id },
						body: values,
						headers,
					}),
			);
			if (loadedVersion.current === result.version.id) {
				setContentDirty(false);
				setEditorVersion(result.version);
				editor.setFieldsValue({
					...contentFrom(result.version),
					expected_revision: result.version.revision,
				});
			}
			await refresh();
			message.success("草稿已保存");
		} catch (error) {
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(getUserErrorMessage(error, "保存草稿失败"));
		} finally {
			setSaving(false);
		}
	}
	async function loadLatest() {
		if (!selected) return;
		setSaving(true);
		try {
			const result = await version.refetch();
			if (result.error) throw result.error;
			const latest = result.data?.version;
			if (latest) {
				setEditorVersion(latest);
				setContentDirty(false);
				setScoringDirty(false);
			}
			if (latest && loadedVersion.current === latest.id) {
				editor.setFieldsValue({
					...contentFrom(latest),
					expected_revision: latest.revision,
				});
				setConflict(false);
			}
		} catch (error) {
			message.error(getUserErrorMessage(error, "加载最新版本失败"));
		} finally {
			setSaving(false);
		}
	}
	function scoringSaved(v: QuestionBankVersion) {
		setEditorVersion(v);
		editor.setFieldsValue({ ...contentFrom(v), expected_revision: v.revision });
		void refresh();
	}
	async function transition(
		decision: "submit-review" | "approve" | "return-to-draft" | "publish",
	) {
		if (!editorVersion) return;
		setSaving(true);
		const body = {
			expected_revision: editorVersion.revision,
			bundle_hash: editorVersion.bundle_hash,
			comment,
		};
		try {
			const methods = {
				"submit-review":
					questionBankApi.submitQuestionBankReview.bind(questionBankApi),
				approve:
					questionBankApi.approveQuestionBankVersion.bind(questionBankApi),
				"return-to-draft":
					questionBankApi.returnQuestionBankVersionToDraft.bind(
						questionBankApi,
					),
				publish:
					questionBankApi.publishQuestionBankVersion.bind(questionBankApi),
			};
			const result = await command(
				`${decision}:${editorVersion.id}`,
				body,
				(headers) =>
					methods[decision]({
						path: { versionId: editorVersion.id },
						body,
						headers,
					}),
			);
			scoringSaved(result.version);
			setComment("");
			message.success("版本状态已更新");
		} catch (error) {
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(
				getUserErrorMessage(error, "版本操作失败，请核对授权与评分标准"),
			);
		} finally {
			setSaving(false);
		}
	}
	async function derive() {
		if (!item || !selected) return;
		setSaving(true);
		try {
			const result = await command(
				`derive:${item.id}`,
				{ source_version_id: selected.id },
				(headers) =>
					questionBankApi.createQuestionBankVersion({
						path: { itemId: item.id },
						body: { source_version_id: selected.id },
						headers,
					}),
			);
			setVersionOffset(0);
			setVersionId(result.version.id);
			await refresh();
			message.success("新版本草稿已创建");
		} catch (error) {
			message.error(getUserErrorMessage(error, "创建新版本失败"));
		} finally {
			setSaving(false);
		}
	}
	async function retireItem() {
		if (!item) return;
		setSaving(true);
		try {
			await command(`retire:${item.id}`, { expected_revision: item.revision }, (headers) =>
				questionBankApi.retireQuestionBankItem({
					path: { itemId: item.id },
					body: { expected_revision: item.revision },
					headers,
				}),
			);
			setItem(null);
			setVersionId("");
			await refresh();
			message.success("题目已退役");
		} catch (error) {
			message.error(getUserErrorMessage(error, "退役题目失败"));
		} finally {
			setSaving(false);
		}
	}
	async function saveACL(values: {
		bindings?: Array<{ user_id: string; preset: "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager" }>;
		groups?: Array<{ id?: string; name: string; preset: "Viewer" | "Author" | "Reviewer" | "Publisher" | "Manager"; members?: string }>;
	}) {
		if (!acl.data?.acl) return;
		const body = {
			expected_revision: acl.data.acl.revision,
			bindings: values.bindings ?? [],
			groups: (values.groups ?? []).map(({ members, ...group }) => ({
				...group,
				member_user_ids: [...new Set((members ?? "").split(/\s+/).map((id) => id.trim()).filter(Boolean))],
			})),
		};
		setSaving(true);
		try {
			await command(`acl:${bankId}`, body, (headers) =>
				questionBankApi.updateQuestionBankACL({ path: { bankId }, body, headers }),
			);
			setAclOpen(false);
			await refresh();
			message.success("题库权限已更新");
		} catch (error) {
			message.error(getUserErrorMessage(error, "权限更新失败"));
		} finally {
			setSaving(false);
		}
	}
	async function saveSchema(values: {
		fields?: Array<Record<string, unknown> & { options_text?: string }>;
		taxonomies?: Array<{ id: string; label: string; terms_text?: string }>;
	}) {
		if (!bank) return;
		const fields = (values.fields ?? []).map(({ options_text, ...field }) => ({
			...field,
			options:
				field.type === "enum"
					? (options_text ?? "").split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
							const [value, label = value] = line.split("|");
							return { value: value.trim(), label: label.trim(), active: true };
						})
					: undefined,
		}));
		const taxonomies = (values.taxonomies ?? []).map(({ terms_text, ...taxonomy }) => ({
			...taxonomy,
			terms: (terms_text ?? "").split("\n").map((line) => line.trim()).filter(Boolean).map((line) => {
				const [id, label = id] = line.split("|");
				return { id: id.trim(), label: label.trim(), active: true };
			}),
		}));
		const body = { expected_revision: bank.revision, fields, taxonomies } as UpdateQuestionBankMetadataSchemaRequest;
		setSaving(true);
		try {
			await command(`schema:${bankId}`, body, (headers) =>
				questionBankApi.updateQuestionBankMetadataSchema({ path: { bankId }, body, headers }),
			);
			setSchemaOpen(false);
			await refresh();
			message.success("Metadata schema 已发布新版本");
		} catch (error) {
			message.error(getUserErrorMessage(error, "Metadata schema 更新失败"));
		} finally {
			setSaving(false);
		}
	}
	const schoolOptions =
		schools.data?.schools
			.filter((s) => s.status === "active")
			.map((s) => ({ value: s.id, label: s.name })) ??
		user.organizationScope.schoolIds.map((id) => ({
			value: id,
			label: user.school || "所属学校",
		}));
	if (banks.isError)
		return (
			<ErrorState
				message={getUserErrorMessage(banks.error, "题库加载失败")}
				onRetry={() => void banks.refetch()}
			/>
		);
	return (
		<div className="question-bank-page">
			<section className="question-bank-heading">
				<div>
					<h1>题库</h1>
					<p>维护题目、答案和评分标准版本，审核通过后发布到考试。</p>
				</div>
				<Space>
					<Button icon={<RefreshCw size={15} />} onClick={() => void refresh()}>
						刷新
					</Button>
					{canCreate && (
						<Button icon={<Plus size={15} />} onClick={() => setDialog("bank")}>
							新建题库
						</Button>
					)}
				</Space>
			</section>
			<div className="question-bank-toolbar">
				<Select
					aria-label="选择题库"
					placeholder={banks.isLoading ? "正在加载题库" : "选择题库"}
					value={bankId || undefined}
					onChange={selectBank}
					options={banks.data?.banks.map((b) => ({
						value: b.id,
						label: `${b.name}${b.status === "archived" ? "（已归档）" : ""}`,
					}))}
				/>
				<Input.Search
					aria-label="搜索题目编号"
					placeholder="搜索题目编号"
					onSearch={(value) => {
						setQuery(value);
						setOffset(0);
					}}
					allowClear
					disabled={!bankId}
				/>
				<Button icon={<Filter size={15} />} disabled={!bankId} onClick={() => setSearchOpen(true)}>
					组合检索
				</Button>
				{canCreate && (
					<Button
						type="primary"
						icon={<Plus size={15} />}
						disabled={!bankId || bank?.status !== "active"}
						onClick={() => setDialog("item")}
					>
						{kind === "rubric_template" ? "新建 Rubric 模板" : "新建题目"}
					</Button>
				)}
			</div>
			{(banks.data?.total ?? 0) > 20 && (
				<Pagination
					size="small"
					current={bankOffset / 20 + 1}
					pageSize={20}
					total={banks.data?.total}
					showSizeChanger={false}
					onChange={(page) => {
						setBankOffset((page - 1) * 20);
						selectBank("");
					}}
				/>
			)}
			{!banks.isLoading && !banks.data?.total ? (
				<Empty description="尚无获授权的题库，可新建题库开始维护题目。" />
			) : (
				<div className="question-bank-workspace">
					<section className="question-bank-list" aria-label="题目列表">
						<Space wrap>
							<Segmented
								value={kind}
								options={[
									{ value: "question", label: "题目" },
									{ value: "rubric_template", label: "Rubric 模板" },
								]}
								onChange={(value) => {
									setKind(value as typeof kind);
									setItem(null);
									setVersionId("");
									setEditorVersion(null);
									setOffset(0);
									loadedVersion.current = null;
								}}
							/>
							{canManage ? (
								<>
									<Button icon={<Settings2 size={14} />} onClick={() => setSchemaOpen(true)} disabled={!bank}>
										Metadata 规则
									</Button>
									<Button icon={<ShieldCheck size={14} />} onClick={() => setAclOpen(true)} disabled={!bank}>
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
			)}
			<Modal
				title="新建题库"
				open={dialog === "bank"}
				onCancel={() => !saving && setDialog(null)}
				footer={null}
				destroyOnHidden
			>
				<Form
					layout="vertical"
					onFinish={createBank}
					initialValues={{
						school_id: schoolOptions[0]?.value,
						description: "",
					}}
				>
					<Form.Item
						name="school_id"
						label="所属学校"
						rules={[{ required: true }]}
					>
						<Select options={schoolOptions} loading={schools.isLoading} />
					</Form.Item>
					{schools.isError && <Alert type="error" message="学校列表加载失败" />}
					<Form.Item
						name="name"
						label="题库名称"
						rules={[{ required: true, whitespace: true, max: 160 }]}
					>
						<Input maxLength={160} />
					</Form.Item>
					<Form.Item name="description" label="说明">
						<Input.TextArea maxLength={2000} rows={3} />
					</Form.Item>
					<Button type="primary" htmlType="submit" loading={saving}>
						创建题库
					</Button>
				</Form>
			</Modal>
			<Modal title="访问权限" open={aclOpen} onCancel={() => setAclOpen(false)} footer={null} width={820} destroyOnHidden>
				<Alert type="info" showIcon message="Manager 只管理结构与授权，不自动获得题干浏览权限。用户和组预设由后端展开，每次请求重新解析。" />
				<Form form={aclForm} layout="vertical" onFinish={saveACL} disabled={saving || acl.isLoading}>
					<h3>用户授权</h3>
					<Form.List name="bindings">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-rule-row" key={key}>
								<Form.Item {...field} name={[field.name,"user_id"]} rules={[{required:true}]}><Input placeholder="用户 UUID" /></Form.Item>
								<Form.Item {...field} name={[field.name,"preset"]} rules={[{required:true}]}><Select options={["Viewer","Author","Reviewer","Publisher","Manager"].map(value=>({value,label:value}))} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除</Button>
							</div>)}
							<Button onClick={() => add({preset:"Viewer"})}>添加用户</Button>
						</>}
					</Form.List>
					<h3>题库组</h3>
					<Form.List name="groups">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-group-row" key={key}>
								<Form.Item {...field} name={[field.name,"id"]} hidden><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"name"]} label="组名" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"preset"]} label="预设" rules={[{required:true}]}><Select options={["Viewer","Author","Reviewer","Publisher","Manager"].map(value=>({value,label:value}))} /></Form.Item>
								<Form.Item {...field} name={[field.name,"members"]} label="成员 UUID（每行一个）"><Input.TextArea rows={3} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除组</Button>
							</div>)}
							<Button onClick={() => add({preset:"Author"})}>添加组</Button>
						</>}
					</Form.List>
					<Button type="primary" htmlType="submit" loading={saving}>保存完整 ACL</Button>
				</Form>
			</Modal>
			<Modal title={`Metadata 规则 · v${currentSchema.data?.schema.version ?? "—"}`} open={schemaOpen} onCancel={() => setSchemaOpen(false)} footer={null} width={900} destroyOnHidden>
				<Alert type="info" showIcon message="保存会创建新 schema 版本；旧题目继续按原版本解释，稳定 option 与 taxonomy ID 会保留在历史定义中。" />
				<Form form={schemaForm} layout="vertical" onFinish={saveSchema} disabled={saving || currentSchema.isLoading}>
					<Form.List name="fields">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-schema-row" key={key}>
								<Form.Item {...field} name={[field.name,"key"]} label="字段 key" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"label"]} label="显示名" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"type"]} label="类型" rules={[{required:true}]}><Select options={["enum","string","number","boolean","taxonomy"].map(value=>({value,label:value}))} /></Form.Item>
								<Form.Item {...field} name={[field.name,"required"]} label="必填" valuePropName="checked"><Switch /></Form.Item>
								<Form.Item {...field} name={[field.name,"min"]} label="最小值"><InputNumber /></Form.Item>
								<Form.Item {...field} name={[field.name,"max"]} label="最大值"><InputNumber /></Form.Item>
								<Form.Item {...field} name={[field.name,"max_length"]} label="最大长度"><InputNumber min={1} max={2000} /></Form.Item>
								<Form.Item {...field} name={[field.name,"taxonomy_id"]} label="Taxonomy ID"><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"options_text"]} label="枚举项 value|标签（每行一个）"><Input.TextArea rows={3} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除字段</Button>
							</div>)}
							<Button onClick={() => add({type:"string",required:false,max_length:120})}>添加字段</Button>
						</>}
					</Form.List>
					<h3>Taxonomies</h3>
					<Form.List name="taxonomies">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-rule-row" key={key}>
								<Form.Item {...field} name={[field.name,"id"]} rules={[{required:true}]}><Input placeholder="taxonomy_id" /></Form.Item>
								<Form.Item {...field} name={[field.name,"label"]} rules={[{required:true}]}><Input placeholder="显示名" /></Form.Item>
								<Form.Item {...field} name={[field.name,"terms_text"]} rules={[{required:true}]}><Input.TextArea rows={3} placeholder="term_id|标签，每行一个" /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除</Button>
							</div>)}
							<Button onClick={() => add()}>添加 taxonomy</Button>
						</>}
					</Form.List>
					<Button type="primary" htmlType="submit" loading={saving}>发布新 schema 版本</Button>
				</Form>
			</Modal>
			<Modal title="组合检索" open={searchOpen} onCancel={() => setSearchOpen(false)} footer={null} width={980} destroyOnHidden>
				<div className="question-bank-filter-grid">
					<Input.Search value={searchFilters.q} placeholder="题干或题目编号" onChange={event=>setSearchFilters(current=>({...current,q:event.target.value}))} onSearch={()=>{setSearchOffset(0);void searchResults.refetch();}} />
					<Select allowClear placeholder="学科" value={searchFilters.subject_code} options={subjects} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,subject_code:value}))}} />
					<Select allowClear placeholder="题型" value={searchFilters.question_type} options={types} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,question_type:value}))}} />
					<Select allowClear placeholder="版本状态" value={searchFilters.workflow_status} options={Object.entries(workflowLabels).map(([value,label])=>({value,label}))} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,workflow_status:value}))}} />
					<Select allowClear placeholder="难度标签" value={searchFilters.difficulty_band} options={["easy","medium","hard"].map(value=>({value,label:value}))} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,difficulty_band:value}))}} />
					<Input placeholder="知识点 ID" value={searchFilters.knowledge_point} onChange={event=>{setSearchOffset(0);setSearchFilters(current=>({...current,knowledge_point:event.target.value}))}} />
					<Select value={searchFilters.mode} options={[{value:"default",label:"已发布 + 我的草稿"},{value:"published",label:"当前发布版"},{value:"my_drafts",label:"我的草稿"},{value:"all",label:"全部可见版本"}]} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,mode:value}))}} />
				</div>
				<ResponsiveTable<QuestionBankSearchItem> className="dense-data-table" rowKey={record=>record.version.id} loading={searchResults.isLoading} dataSource={searchResults.data?.items ?? []} columns={[
					{title:"编号",render:(_,record)=><Button type="link" onClick={()=>{selectItem(record.item);setVersionId(record.version.id);setSearchOpen(false)}}>{record.item.item_code}</Button>},
					{title:"题干",dataIndex:["version","stem"],ellipsis:true},
					{title:"版本",render:(_,record)=>`v${record.version.version_no} · ${workflowLabels[record.version.workflow_status]}`},
					{title:"难度",render:(_,record)=>record.version.metadata.difficulty_band},
					{title:"统计",render:(_,record)=>record.statistics_available?"已有观测":"尚无观测"},
				]} pagination={{current:searchOffset/20+1,pageSize:20,total:searchResults.data?.total,showSizeChanger:false,onChange:page=>setSearchOffset((page-1)*20)}} />
			</Modal>
			<Modal
				title={
					kind === "rubric_template" ? "新建 Rubric 模板草稿" : "新建题目草稿"
				}
				open={dialog === "item"}
				onCancel={() => !saving && setDialog(null)}
				footer={null}
				width={680}
				destroyOnHidden
			>
				<Form
					layout="vertical"
					onFinish={createItem}
					initialValues={defaults}
					disabled={saving}
				>
					<Form.Item
						name="item_code"
						label="题目编号"
						rules={[
							{
								required: true,
								pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/,
								message: "使用字母、数字、点、下划线或短横线，最多64个字符",
							},
						]}
					>
						<Input />
					</Form.Item>
					<ContentFields schema={currentSchema.data?.schema} />
					<Button type="primary" htmlType="submit" loading={saving}>
						{kind === "rubric_template" ? "创建模板草稿" : "创建题目草稿"}
					</Button>
				</Form>
			</Modal>
		</div>
	);
}

function ContentFields({ schema }: { schema?: QuestionBankMetadataSchema }) {
	const knowledgePointTaxonomy = schema?.taxonomies.find(
		(taxonomy) => taxonomy.id === "knowledge_points",
	);
	return (
		<>
			<div className="question-bank-form-pair">
				<Form.Item
					name={["metadata", "subject_code"]}
					label="学科"
					rules={[{ required: true }]}
				>
					<Select options={subjects} />
				</Form.Item>
				<Form.Item
					name={["metadata", "education_stage"]}
					label="学段"
					rules={[{ required: true }]}
				>
					<Select
						options={[
							{ value: "junior", label: "初中" },
							{ value: "senior", label: "高中" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item
					name={["metadata", "grade_scope"]}
					label="年级范围"
					rules={[{ required: true, whitespace: true, max: 80 }]}
				>
					<Input maxLength={80} placeholder="如 grade_8" />
				</Form.Item>
				<Form.Item
					name="default_score"
					label="默认分值"
					rules={[{ required: true }]}
				>
					<InputNumber min={0.01} max={100000} precision={2} />
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item
					name="question_type"
					label="题型"
					rules={[{ required: true }]}
				>
					<Select options={types} />
				</Form.Item>
				<Form.Item
					name="assessment_archetype"
					label="作答方式"
					rules={[{ required: true }]}
				>
					<Select options={archetypes} />
				</Form.Item>
			</div>
			<Form.Item
				name="stem"
				label="题干"
				rules={[{ required: true, whitespace: true, max: 50000 }]}
			>
				<Input.TextArea rows={5} maxLength={50000} />
			</Form.Item>
			<Form.Item
				name="options"
				label="选项（每行一个，按题目顺序）"
				getValueProps={(value: string[] = []) => ({ value: value.join("\n") })}
				getValueFromEvent={(event: { target: { value: string } }) =>
					event.target.value === "" ? [] : event.target.value.split("\n")
				}
			>
				<Input.TextArea rows={3} />
			</Form.Item>
			<Form.Item
				name="knowledge_points"
				label="知识点"
				normalize={(value: string[]) => [
					...new Set(value.map((v) => v.trim()).filter(Boolean)),
				]}
			>
				<Select
					mode="multiple"
					tokenSeparators={[",", "，"]}
					maxCount={64}
					options={knowledgePointTaxonomy?.terms
						.filter((term) => term.active)
						.map((term) => ({ value: term.id, label: term.label }))}
						showSearch
					placeholder={
						knowledgePointTaxonomy
							? "选择受控知识点"
							: "当前 schema 未配置 knowledge_points taxonomy"
					}
					disabled={!knowledgePointTaxonomy}
				/>
			</Form.Item>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "difficulty_band"]} label="难度标签">
					<Select
						options={[
							{ value: "unclassified", label: "未分类" },
							{ value: "easy", label: "容易" },
							{ value: "medium", label: "中等" },
							{ value: "hard", label: "困难" },
						]}
					/>
				</Form.Item>
				<Form.Item name={["metadata", "copyright"]} label="版权状态">
					<Select
						options={[
							{ value: "unknown", label: "待确认" },
							{ value: "owned", label: "自有" },
							{ value: "licensed", label: "已授权" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "cognitive_level"]} label="认知层级">
					<Select
						options={[
							{ value: "unclassified", label: "未分类" },
							{ value: "remember", label: "记忆" },
							{ value: "understand", label: "理解" },
							{ value: "apply", label: "应用" },
							{ value: "analyze", label: "分析" },
							{ value: "evaluate", label: "评价" },
							{ value: "create", label: "创造" },
						]}
					/>
				</Form.Item>
				<Form.Item name={["metadata", "language"]} label="语言">
					<Select
						options={[
							{ value: "zh-CN", label: "中文" },
							{ value: "en", label: "英语" },
						]}
					/>
				</Form.Item>
			</div>
			<div className="question-bank-form-pair">
				<Form.Item name={["metadata", "intended_use"]} label="建议用途">
					<Select options={[
						{value:"practice",label:"练习"},{value:"homework",label:"作业"},{value:"quiz",label:"测验"},{value:"exam",label:"考试"},{value:"mock_exam",label:"模拟考试"},
					]} />
				</Form.Item>
				<Form.Item name={["metadata", "suggested_time_minutes"]} label="建议用时（分钟）">
					<InputNumber min={1} max={1440} />
				</Form.Item>
			</div>
			<Form.Item name={["metadata", "source_year"]} label="来源年份">
				<InputNumber min={1900} max={2200} />
			</Form.Item>
			{schema?.fields.length ? <section className="question-bank-custom-fields">
				<h3>受控自定义 Metadata · schema v{schema.version}</h3>
				<div className="question-bank-form-pair">
					{schema.fields.map((field) => {
						const rules = [{ required: field.required, message: `${field.label}为必填字段` }];
						let control = <Input maxLength={field.max_length ?? undefined} />;
						if (field.type === "number") control = <InputNumber min={field.min ?? undefined} max={field.max ?? undefined} />;
						if (field.type === "boolean") control = <Switch />;
						if (field.type === "enum") control = <Select options={(field.options ?? []).filter(option=>option.active).map(option=>({value:option.value,label:option.label}))} />;
						if (field.type === "taxonomy") {
							const taxonomy = schema.taxonomies.find(value=>value.id===field.taxonomy_id);
							control = <Select showSearch options={(taxonomy?.terms ?? []).filter(term=>term.active).map(term=>({value:term.id,label:term.label}))} />;
						}
						return <Form.Item key={field.key} name={["custom_metadata",field.key]} label={field.label} rules={rules} valuePropName={field.type === "boolean" ? "checked" : "value"}>{control}</Form.Item>;
					})}
				</div>
			</section> : null}
		</>
	);
}
