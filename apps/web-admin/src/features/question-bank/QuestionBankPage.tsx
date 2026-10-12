import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	App,
	Button,
	Empty,
	Form,
	Modal,
	Space,
} from "antd";
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
import { useUnsavedChangesGuard } from "../../router/useUnsavedChangesGuard";
import "./question-bank.css";
import { defaults, contentFrom, matchingVersionForItem, metadataSchemaReadState, metadataSchemaUpdate } from "./questionBankPageState";
import { QuestionBankHeader } from "./QuestionBankHeader";
import { QuestionBankWorkspace } from "./QuestionBankWorkspace";
import { QuestionBankDialogs } from "./QuestionBankDialogs";
import { QuestionBankConflictComparison } from "./QuestionBankConflictComparison";
import { emptySearch, isEditableACLRole, type ACLFormValues, type MetadataSchemaFormValues, type SearchFilters } from "./questionBankTypes";
import { listQuestionBankMembers, bankRoleLabels, questionBankMemberAmbiguous, questionBankMemberLabel } from "./questionBankMembers";
import { hashQueryParam } from "../../router/query";

const transitionSuccessMessages = {
	publish: "已发布到学校题库。请在考试的题目页选择此版本；已有考试不受影响。",
	"submit-review": "已提交审核，请通知本题库的审核教师在题库中处理。",
	approve: "审核已通过，下一步由有发布权限的教师发布到题库。",
	"return-to-draft": "已退回草稿，可修改后重新提交审核。",
};

export function QuestionBankPage({ user }: Readonly<{ user: SessionUser }>) {
	const { message, modal } = App.useApp();
	const client = useQueryClient();
	const prefix = ["question-bank", user.tenant, user.id];
	const [bankId, setBankId] = useState(() => hashQueryParam("bank"));
	const [item, setItem] = useState<QuestionBankItem | null>(null);
	const [versionId, setVersionId] = useState(() => hashQueryParam("version"));
	const [linkedItemId] = useState(() => hashQueryParam("item"));
	const linkedSelectionApplied = useRef(false);
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
	const [latestPreview, setLatestPreview] = useState<QuestionBankVersion | null>(null);
	const [scoringResetToken, setScoringResetToken] = useState(0);
	const scoringDraft = useRef<unknown>(null);
	const [editor] = Form.useForm();
	const [aclForm] = Form.useForm();
	const [schemaForm] = Form.useForm();
	const [schemaFormBankId, setSchemaFormBankId] = useState<string | null>(null);
	const [schemaReadFailureBankId, setSchemaReadFailureBankId] = useState<string | null>(null);
	const schemaContext = useRef({ bankId, readable: false, formReady: false });
	useUnsavedChangesGuard({ dirty: contentDirty || scoringDirty, busy: saving, description: "题目内容或评分标准尚未保存，离开后无法恢复这些修改。" });
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
	const versionMismatch = Boolean(item && version.data?.version && version.data.version.item_id !== item.id);
	const selected = matchingVersionForItem(item, version.data?.version);
	const currentSchema = useQuery({
		queryKey: [...prefix, "metadata-schema", bankId, "current"],
		queryFn: ({ signal }) =>
			questionBankApi.getQuestionBankMetadataSchema({
				path: { bankId },
				signal,
			}),
		enabled: Boolean(bankId),
	});
	// 版本内容按创建时绑定的 schema 解析；题库后来升级规则时，旧版本仍必须按原 schema 读取。
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
	const linkedBank = useQuery({queryKey:[...prefix,"linked-bank",bankId],queryFn:()=>questionBankApi.getQuestionBank({path:{bankId}}),enabled:Boolean(bankId && !banks.data?.banks.some(bank=>bank.id===bankId))});
	const linkedItem = useQuery({queryKey:[...prefix,"linked-item",linkedItemId],queryFn:()=>questionBankApi.getQuestionBankItem({path:{itemId:linkedItemId}}),enabled:Boolean(linkedItemId && !linkedSelectionApplied.current)});
	useEffect(()=>{ if (linkedItem.data?.item && !linkedSelectionApplied.current) {linkedSelectionApplied.current=true;setItem(linkedItem.data.item);setBankId(linkedItem.data.item.bank_id);} },[linkedItem.data]);
	const bank = banks.data?.banks.find((b) => b.id === bankId) ?? linkedBank.data?.bank;
	const { schemaReadable, schemaFormReady, schemaLoadFailed } = metadataSchemaReadState({
		bankId, loadedBankId: bank?.id, query: currentSchema,
		formBankId: schemaFormBankId, failureBankId: schemaReadFailureBankId,
	});
	// 确认弹窗的回调可能来自前一题库；执行时再核对当前范围和读取状态，不能沿用旧表单提交。
	schemaContext.current = { bankId, readable: schemaReadable, formReady: schemaFormReady };
	const members = useQuery({queryKey:[...prefix,"members",bankId],queryFn:()=>listQuestionBankMembers(bankId),enabled:Boolean(bankId)});
	useEffect(() => {
		if (!bankId && banks.data?.banks[0]) setBankId(banks.data.banks[0].id);
	}, [bankId, banks.data]);
	useEffect(() => {
		if (!versionId && versions.data?.versions[0])
			setVersionId(versions.data.versions[0].id);
	}, [versionId, versions.data]);
	useEffect(() => {
		// 后台重新获取数据不能覆盖未保存的编辑，也不能推进其预期修订号；
		// 只在明确选择版本、保存或重新加载时读取。
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
				.filter((binding) => binding.preset !== "Custom" && binding.user_id !== bank?.created_by && binding.user_id !== user.id)
				.map(({ user_id, preset }) => ({ user_id, preset })),
			groups: acl.data.acl.groups.flatMap((group) => isEditableACLRole(group.preset) ? [{
				id: group.id,
				name: group.name,
				preset: group.preset,
				members: group.member_user_ids,
			}] : []),
		});
	}, [acl.data, aclForm, aclOpen, bank?.created_by, user.id]);
	useEffect(() => {
		// 打开或切换题库时先清除旧表单。404、断网等读取失败都不能被解释成新库没有配置。
		if (schemaOpen) schemaForm.setFieldsValue({ fields: [], taxonomies: [] });
		setSchemaFormBankId(null);
	}, [bankId, schemaForm, schemaOpen]);
	useEffect(() => {
		// 重试可能暂时恢复加载状态，失败说明要保留到成功返回，不能提前表示已恢复。
		if (currentSchema.isError) setSchemaReadFailureBankId(bankId);
		else if (currentSchema.isSuccess) setSchemaReadFailureBankId(null);
	}, [bankId, currentSchema.isError, currentSchema.isSuccess]);
	useEffect(() => {
		if (!schemaOpen || !currentSchema.isSuccess || !currentSchema.data?.schema) return;
		schemaForm.setFieldsValue({
			fields: currentSchema.data.schema.fields.map((field) => ({
				...field,
				options_text: field.options
					?.map((option) => `${option.value}|${option.label}`)
					.join("\n"),
			})),
			taxonomies: currentSchema.data.schema.taxonomies.map((taxonomy) => ({
				terms: taxonomy.terms,
				id: taxonomy.id,
				label: taxonomy.label,
				terms_text: taxonomy.terms
					.map((term) => `${term.id}|${term.label}`)
					.join("\n"),
			})),
		});
		setSchemaFormBankId(bankId);
	}, [bankId, currentSchema.data, currentSchema.isSuccess, schemaForm, schemaOpen]);
	// 未保存的题目编辑只在当前表单里；切换范围前要由用户决定是否丢弃。
	function confirmDiscard(action: () => void) {
		if (!contentDirty && !scoringDirty) return action();
		modal.confirm({
			title: "放弃未保存的修改？",
			content: "当前题目内容或评分标准尚未保存，切换后无法恢复。",
			okText: "放弃并切换",
			okButtonProps: { danger: true },
			cancelText: "继续编辑",
			onOk: action,
		});
	}
	function selectBank(id: string) {
		if (id === bankId) return;
		confirmDiscard(() => applyBankSelection(id));
	}
	function applyBankSelection(id: string) {
		schemaContext.current = { bankId: id, readable: false, formReady: false };
		setSchemaFormBankId(null);
		loadedVersion.current = null;
		setBankId(id);
		setItem(null);
		setVersionId("");
		setOffset(0);
		setQuery("");
		setConflict(false);
	}
	function selectItem(next: QuestionBankItem, nextVersionId = "", onSelected?: () => void) {
		if (next.id === item?.id && (!nextVersionId || nextVersionId === versionId)) {
			onSelected?.();
			return;
		}
		confirmDiscard(() => {
			applyItemSelection(next, nextVersionId);
			onSelected?.();
		});
	}
	function applyItemSelection(next: QuestionBankItem, nextVersionId = "") {
		loadedVersion.current = null;
		setItem(next);
		setVersionId(nextVersionId);
		setVersionOffset(0);
		setConflict(false);
	}
	function selectVersion(id: string, nextOffset?: number) {
		if (id === versionId && nextOffset === undefined) return;
		confirmDiscard(() => {
			if (nextOffset !== undefined) setVersionOffset(nextOffset);
			setVersionId(id);
		});
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
		if (saving || schemaContext.current.bankId !== bankId || !schemaContext.current.readable) {
			message.warning("请先成功读取当前题库的分类规则，再创建题目草稿。");
			return;
		}
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
		if (!selected || saving) return;
		const requestVersionId = selected.id;
		setSaving(true);
		try {
			// 预览读取不更新查询缓存或编辑修订号，只有用户明确采用后才替换本地草稿。
			const result = await questionBankApi.getQuestionBankVersion({ path: { versionId: requestVersionId } });
			if (loadedVersion.current === requestVersionId) setLatestPreview(result.version);
		} catch (error) {
			message.error(getUserErrorMessage(error, "加载最新版本失败"));
		} finally {
			setSaving(false);
		}
	}
	function acceptLatest() {
		if (latestPreview?.id !== loadedVersion.current) return;
		setEditorVersion(latestPreview);
		editor.setFieldsValue({ ...contentFrom(latestPreview), expected_revision: latestPreview.revision });
		client.setQueryData([...prefix, "version", latestPreview.id], { version: latestPreview });
		setContentDirty(false); setScoringDirty(false); setConflict(false); setLatestPreview(null);
		// 即使服务器修订未变化，明确放弃也须重建评分表单，不能只把脏标记清除。
		setScoringResetToken((value) => value + 1);
		message.success("已采用最新版本，本地未保存修改已放弃。");
	}
	function downloadLocalDraft() {
		if (!editorVersion) return;
		const blob = new Blob([JSON.stringify({ version_id: editorVersion.id, saved_revision: editorVersion.revision, content: editor.getFieldsValue(true), scoring: scoringDraft.current ?? editorVersion.scoring }, null, 2)], { type: "application/json" });
		const url = URL.createObjectURL(blob);
		const link = document.createElement("a"); link.href = url; link.download = `题目本地修改备份-${item?.item_code ?? "草稿"}.json`; link.click();
		window.setTimeout(() => URL.revokeObjectURL(url), 1000);
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
			message.success(transitionSuccessMessages[decision]);
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
	function confirmRetireItem() {
		if (!item || saving) return;
		modal.confirm({title:`停止选用题目 ${item.item_code}？`,content:"此题将从新考试的候选题中移除，不能再次选入考试。已有考试保存的题目版本和历史成绩继续保留。当前不支持恢复选用；如需再用请新建题目。",okText:"确认停止选用",cancelText:"继续保留",okButtonProps:{danger:true},onOk:retireItem});
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
			message.success("已停止选用；已有考试和历史成绩保留");
		} catch (error) {
			message.error(getUserErrorMessage(error, "停止选用失败，题目仍保留"));
		} finally {
			setSaving(false);
		}
	}
	async function saveACL(values: ACLFormValues) {
		if (!acl.data?.acl || acl.isError || acl.isLoading || saving) return;
		// 表单仅提交预设职责；组合授权由服务端在同一修订锁内保留，不能把旧动作数组作为新授权提交。
		const retainedBindings = acl.data.acl.bindings.filter(binding => binding.preset === "Custom" || binding.user_id === bank?.created_by || binding.user_id === user.id);
		const retainedGroups = acl.data.acl.groups.filter(group => String(group.preset) === "Custom");
		const body = {
			expected_revision: acl.data.acl.revision,
			bindings: values.bindings ?? [],
			groups: (values.groups ?? []).map(({ members, ...group }) => ({
				...group,
				member_user_ids: [...new Set((Array.isArray(members) ? members : (members ?? "").split(/\s+/)).map((id) => id.trim()).filter(Boolean))],
			})),
		};
		if (body.bindings.some(binding => retainedBindings.some(old => old.user_id === binding.user_id)) || body.groups.some(group => retainedGroups.some(old => old.id === group.id || old.name.trim() === group.name.trim()))) {
			message.warning("只读保留的授权不能作为新职责覆盖，请选其他人员或使用不同组名。"); return;
		}
		const directory = members.data?.members ?? [];
		const selectedIDs = new Set([...body.bindings.map(binding => binding.user_id), ...body.groups.flatMap(group => group.member_user_ids)]);
		if (directory.some(person => selectedIDs.has(person.user_id) && questionBankMemberAmbiguous(person, directory))) {
			message.warning("选中的同名人员缺少可区分的账号，请重新读取人员列表并核对身份后再更新授权。"); return;
		}
		const memberLabel = (id: string) => questionBankMemberLabel(directory.find(person => person.user_id === id));
		// 确认摘要一次生成；行编号只用于显示，不进入授权请求或幂等指纹。
		const summary = body.bindings.map(binding => `${memberLabel(binding.user_id)}：${bankRoleLabels[binding.preset]}`).concat(body.groups.map(group=>`${group.name}：${group.member_user_ids.map(memberLabel).join("、") || "暂无人员"} · ${bankRoleLabels[group.preset]}`)).map(text => ({ key: crypto.randomUUID(), text }));
		modal.confirm({title:"确认更新题库授权？",content:<><p>将保存以下人员和分组的权限。管理结构与授权本身不包含浏览题目权限。</p><ul>{summary.map(line=><li key={line.key}>{line.text}</li>)}</ul><p>另有 {retainedBindings.length} 条用户授权、{retainedGroups.length} 个历史组合权限组原样保留。创建者、当前操作者和历史组合授权不会被本次表单删除或改写。</p></>,okText:"确认更新授权",cancelText:"返回核对",onOk:async()=>{
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
		}});
	}
	async function saveSchema(values: MetadataSchemaFormValues) {
		if (!bank || saving || schemaContext.current.bankId !== bankId || !schemaContext.current.formReady) {
			message.warning("请先成功读取当前题库的分类规则，再保存修改。");
			return;
		}
		const body = metadataSchemaUpdate(values, bank.revision);
		const { fields, taxonomies } = body;
		// 空规则是有效配置，但从已有配置清空会改变新题目的填写和目录检索，须先确认影响。
		const existingSchema = currentSchema.data!.schema;
		if (!fields.length && !taxonomies.length && (existingSchema.fields.length || existingSchema.taxonomies.length)) {
			modal.confirm({
				title: "清空当前题库的分类规则？",
				content: `将移除 ${existingSchema.fields.length} 个补充字段和 ${existingSchema.taxonomies.length} 个知识点或分类目录。新建题目将不再填写这些内容，组合检索也将没有当前知识点目录可选。已有题目版本继续保留原规则和记录，已有考试不受影响。`,
				okText: "确认清空并保存",
				okButtonProps: { danger: true },
				cancelText: "返回编辑",
				onOk: () => persistSchema(body),
			});
			return;
		}
		await persistSchema(body);
	}
	async function persistSchema(body: UpdateQuestionBankMetadataSchemaRequest) {
		if (saving || schemaContext.current.bankId !== bankId || !schemaContext.current.formReady) {
			message.warning("当前题库或分类规则读取状态已变化，请重新核对后保存。");
			return;
		}
		setSaving(true);
		try {
			await command(`schema:${bankId}`, body, (headers) =>
				questionBankApi.updateQuestionBankMetadataSchema({ path: { bankId }, body, headers }),
			);
			setSchemaOpen(false);
			await refresh();
			message.success("题目分类规则已更新，旧题目继续使用原规则");
		} catch (error) {
			message.error(getUserErrorMessage(error, "分类规则更新失败，请核对字段和目录条目"));
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
			{linkedItem.isError ? <ErrorState message="这道题目暂不可打开。请核对题库授权，或联系题库负责人；其他题库仍可浏览。" onRetry={()=>void linkedItem.refetch()} /> : null}
            {versionMismatch ? <ErrorState message="链接中的版本不属于当前题目，已停止显示和编辑。请打开本题最新版本。" onRetry={()=>setVersionId(versions.data?.versions[0]?.id ?? "")} /> : null}
			<QuestionBankHeader canCreate={canCreate} loading={banks.isLoading} banks={banks.data?.banks} bankId={bankId}
				selectBank={selectBank} bankStatus={bank?.status} kind={kind} bankOffset={bankOffset} total={banks.data?.total ?? 0}
				onRefresh={() => void refresh()} onCreateBank={() => setDialog("bank")} onCreateItem={() => setDialog("item")}
				onSearchOpen={() => setSearchOpen(true)} onSearch={(value) => { setQuery(value); setOffset(0); }}
				onBankPageChange={(page) => confirmDiscard(() => { setBankOffset((page - 1) * 20); applyBankSelection(""); })} />
			{!banks.isLoading && !banks.data?.total ? (
				<Empty description="尚无获授权的题库，可新建题库开始维护题目。" />
			) : (
				<QuestionBankWorkspace
					bank={bank} kind={kind} onKindChange={(value) => confirmDiscard(() => {
						setKind(value);
						setItem(null);
						setVersionId("");
						setEditorVersion(null);
						setOffset(0);
						loadedVersion.current = null;
					})}
					canManage={canManage} openSchema={() => setSchemaOpen(true)} openAcl={() => setAclOpen(true)}
					items={items} selectItem={selectItem} offset={offset} setOffset={setOffset}
					item={item} selected={selected} versions={versions} version={version}
					versionOffset={versionOffset} setVersionId={selectVersion}
					saving={saving} loadLatest={() => void loadLatest()} canEdit={canEdit} derive={() => void derive()}
					canRetire={canRetire} retireItem={confirmRetireItem} boundSchema={boundSchema}
					editor={editor} saveVersion={(values) => void saveVersion(values)} contentDirty={contentDirty} setContentDirty={setContentDirty}
					scoringDirty={scoringDirty} conflict={conflict} editorVersion={editorVersion}
					scoringSaved={scoringSaved} setScoringDirty={setScoringDirty} scoringResetToken={scoringResetToken} onScoringDraftSnapshot={(draft) => { scoringDraft.current = draft; }} user={user}
					comment={comment} setComment={setComment} transition={(decision) => void transition(decision)} reviews={reviews}
					members={members}
				/>
			)}
			<Modal title="比较本地修改与最新版本" open={Boolean(latestPreview)} width={900} onCancel={() => setLatestPreview(null)} footer={<Space wrap>
				<Button onClick={downloadLocalDraft}>下载本地修改备份</Button><Button onClick={() => setLatestPreview(null)}>保留本地修改</Button>
				<Button danger type="primary" onClick={() => {
					if (!contentDirty && !scoringDirty) { acceptLatest(); return; }
					modal.confirm({ title: "采用最新版本并放弃本地修改？", content: "题目内容和评分标准的未保存修改都会被替换。请先下载备份，或返回保留本地修改。", okText: "放弃本地修改并采用", okButtonProps: { danger: true }, cancelText: "保留本地修改", onOk: acceptLatest });
				}}>采用最新版本</Button>
			</Space>}>
				<p>查看最新版本不会覆盖本地修改。采用前请核对差异，下载的备份包含当前题目内容、答案、评分编辑和附件信息。</p>
				{latestPreview && <QuestionBankConflictComparison original={editorVersion} latest={latestPreview} localContent={editor.getFieldsValue(true)} scoringDraft={scoringDraft.current} />}
			</Modal>
			<QuestionBankDialogs
				bankId={bankId} dialog={dialog} setDialog={setDialog} saving={saving} schoolOptions={schoolOptions} schools={schools} createBank={(values) => void createBank(values)}
				aclOpen={aclOpen} setAclOpen={setAclOpen} aclForm={aclForm} saveACL={(values) => void saveACL(values)} acl={acl}
				protectedMemberIds={[user.id, bank?.created_by ?? ""]}
				members={members}
				schemaOpen={schemaOpen} setSchemaOpen={setSchemaOpen} currentSchema={currentSchema} schemaReadable={schemaReadable} schemaFormReady={schemaFormReady} schemaLoadFailed={schemaLoadFailed} schemaForm={schemaForm} saveSchema={(values) => void saveSchema(values)}
				searchOpen={searchOpen} setSearchOpen={setSearchOpen} searchFilters={searchFilters} setSearchFilters={setSearchFilters} setSearchOffset={setSearchOffset}
					searchOffset={searchOffset} searchResults={searchResults} selectItem={selectItem} kind={kind} createItem={(values) => void createItem(values)} defaults={defaults}
			/>
		</div>
	);
}
