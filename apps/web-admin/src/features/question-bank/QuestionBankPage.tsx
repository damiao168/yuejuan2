import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	App,
	Button,
	Empty,
	Form,
	Input,
	Pagination,
	Select,
	Space,
} from "antd";
import { Filter, Plus, RefreshCw } from "lucide-react";
import type {
	CreateQuestionBankItemRequest,
	CreateQuestionBankRequest,
	QuestionBankItem,
	QuestionBankVersion,
	UpdateQuestionBankVersionRequest,
	UpdateQuestionBankMetadataSchemaRequest,
} from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { listSchools } from "../../api/org";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { hasEveryPermission, type SessionUser } from "../../auth/session";
import { ErrorState } from "../../components/PageState";
import "./question-bank.css";
import { QuestionBankWorkspace } from "./QuestionBankWorkspace";
import { QuestionBankDialogs } from "./QuestionBankDialogs";
import { emptySearch, type ACLFormValues, type MetadataSchemaFormValues, type SearchFilters } from "./questionBankTypes";

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
	async function saveACL(values: ACLFormValues) {
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
	async function saveSchema(values: MetadataSchemaFormValues) {
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
				<QuestionBankWorkspace
					bank={bank} kind={kind} onKindChange={(value) => {
						setKind(value);
						setItem(null);
						setVersionId("");
						setEditorVersion(null);
						setOffset(0);
						loadedVersion.current = null;
					}}
					canManage={canManage} openSchema={() => setSchemaOpen(true)} openAcl={() => setAclOpen(true)}
					items={items} selectItem={selectItem} offset={offset} setOffset={setOffset}
					item={item} selected={selected} versions={versions} version={version}
					versionOffset={versionOffset} setVersionOffset={setVersionOffset} setVersionId={setVersionId}
					saving={saving} loadLatest={() => void loadLatest()} canEdit={canEdit} derive={() => void derive()}
					canRetire={canRetire} retireItem={() => void retireItem()} boundSchema={boundSchema}
					editor={editor} saveVersion={(values) => void saveVersion(values)} contentDirty={contentDirty} setContentDirty={setContentDirty}
					scoringDirty={scoringDirty} conflict={conflict} editorVersion={editorVersion}
					scoringSaved={scoringSaved} setScoringDirty={setScoringDirty} user={user}
					comment={comment} setComment={setComment} transition={(decision) => void transition(decision)} reviews={reviews}
				/>
			)}
			<QuestionBankDialogs
				dialog={dialog} setDialog={setDialog} saving={saving} schoolOptions={schoolOptions} schools={schools} createBank={(values) => void createBank(values)}
				aclOpen={aclOpen} setAclOpen={setAclOpen} aclForm={aclForm} saveACL={(values) => void saveACL(values)} acl={acl}
				schemaOpen={schemaOpen} setSchemaOpen={setSchemaOpen} currentSchema={currentSchema} schemaForm={schemaForm} saveSchema={(values) => void saveSchema(values)}
				searchOpen={searchOpen} setSearchOpen={setSearchOpen} searchFilters={searchFilters} setSearchFilters={setSearchFilters} setSearchOffset={setSearchOffset}
				searchOffset={searchOffset} searchResults={searchResults} selectItem={selectItem} setVersionId={setVersionId} kind={kind} createItem={(values) => void createItem(values)} defaults={defaults}
			/>
		</div>
	);
}
