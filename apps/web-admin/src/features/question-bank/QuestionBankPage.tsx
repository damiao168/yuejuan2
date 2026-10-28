import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	App,
	Empty,
	Form,
} from "antd";
import type {
	CreateQuestionBankItemRequest,
	CreateQuestionBankRequest,
	QuestionBankItem,
	QuestionBankVersion,
	QuestionBankScoring,
	UpdateQuestionBankVersionRequest,
} from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { listSchools } from "../../api/org";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { hasEveryPermission, type SessionUser } from "../../auth/session";
import { ErrorState } from "../../components/PageState";
import { usePageLoading } from "../../components/pageReadiness";
import { useUnsavedChangesGuard } from "../../router/useUnsavedChangesGuard";
import "./question-bank.css";
import { defaults, contentFrom, matchingVersionForItem, metadataSchemaReadState, transitionSuccessMessages } from "./questionBankPageState";
import { QuestionBankHeader } from "./QuestionBankHeader";
import { QuestionBankWorkspace } from "./QuestionBankWorkspace";
import { QuestionBankDialogs } from "./QuestionBankDialogs";
import { QuestionBankConflictDialog } from "./QuestionBankConflictDialog";
import { emptySearch, type SearchFilters } from "./questionBankTypes";
import { listQuestionBankMembers } from "./questionBankMembers";
import { hashQueryParam } from "../../router/query";
import { mergeQuestionBankDraft, type ScoringDraftSnapshot } from "./questionBankDraftMerge";
import { useQuestionBankAdministration } from "./useQuestionBankAdministration";

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
  const [searchContainer, setSearchContainer] = useState<HTMLDivElement | null>(null);
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
  const [restoredScoringDraft, setRestoredScoringDraft] = useState<ScoringDraftSnapshot>();
	const scoringDraft = useRef<unknown>(null);
  const scoringSubmit = useRef<(() => void) | null>(null);
  const registerScoringSubmit = useCallback((submit: (() => void) | null) => { scoringSubmit.current = submit; }, []);
	const [editor] = Form.useForm();
	const [aclForm] = Form.useForm();
	const [schemaForm] = Form.useForm();
	const [schemaFormBankId, setSchemaFormBankId] = useState<string | null>(null);
	const [schemaReadFailureBankId, setSchemaReadFailureBankId] = useState<string | null>(null);
	const schemaContext = useRef({ bankId, readable: false, formReady: false });
	useUnsavedChangesGuard({ dirty: contentDirty || scoringDirty, busy: saving, description: "题目内容或评分标准尚未保存，离开后无法恢复这些修改。" });
	const pending = useRef<{ fingerprint: string; key: string } | null>(null);
	const loadedVersion = useRef<string | null>(null);
  const editorGeneration = useRef(0);
  const displayedEditorGeneration = editorGeneration.current;
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
		queryKey: [...prefix, "items", bankId, kind, query, searchFilters, offset],
		queryFn: async ({ signal }) =>
			kind === "rubric_template"
				? questionBankApi.listQuestionBankRubricTemplates({
						query: { bank_id: bankId, q: query, limit: 20, offset },
						signal,
					})
                : questionBankApi.searchQuestionBankItems({ query: { ...searchFilters, bank_id: bankId, sort: "item_code_asc", limit: 20, offset }, signal }).then(result => ({
              ...result, items: result.items.map(entry => ({ ...entry.item, display_stem: entry.version.stem, display_version_id: entry.version.id, display_status: entry.version.workflow_status }))
            })),
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
	const queriedSelection = matchingVersionForItem(item, version.data?.version);
	// 交接有独立修订：同一内容版本允许更新交接与流程投影，不能让迟到查询倒退人员表单。
	// 内容修订或哈希不同时仍保留编辑快照，不能借后台刷新推进未保存内容的预期修订。
	const selected = queriedSelection && editorVersion?.id === queriedSelection.id && editorVersion.revision === queriedSelection.revision && editorVersion.bundle_hash === queriedSelection.bundle_hash && (editorVersion.handoff?.revision ?? 0) > (queriedSelection.handoff?.revision ?? 0)
		? { ...queriedSelection, handoff: editorVersion.handoff, workflow_status: editorVersion.workflow_status }
		: queriedSelection;
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
		enabled: false,
	});
	const reviews = useQuery({
		queryKey: [...prefix, "reviews", versionId],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBankReviews({ path: { versionId }, signal }),
		enabled: Boolean(versionId),
	});
  useEffect(() => { setOffset(0); }, [searchFilters]);
	const linkedBank = useQuery({queryKey:[...prefix,"linked-bank",bankId],queryFn:()=>questionBankApi.getQuestionBank({path:{bankId}}),enabled:Boolean(bankId && !banks.data?.banks.some(bank=>bank.id===bankId))});
	const linkedItem = useQuery({queryKey:[...prefix,"linked-item",linkedItemId],queryFn:()=>questionBankApi.getQuestionBankItem({path:{itemId:linkedItemId}}),enabled:Boolean(linkedItemId && !linkedSelectionApplied.current)});
	// 题库目录会继续触发题目和版本读取；这些首屏等待只显示在控件内，也须参与切页就绪判断。
	usePageLoading(banks.isLoading || (!banks.isError && (items.isLoading || linkedBank.isLoading || linkedItem.isLoading || versions.isLoading || version.isLoading)));
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
			setRestoredScoringDraft(undefined);
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
	const { saveACL, saveSchema } = useQuestionBankAdministration({
		bankId, bank, actorId: user.id, acl, currentSchema, members: members.data?.members ?? [],
		aclOpen, schemaOpen, aclForm, schemaForm, schemaContext, saving, setSaving, setAclOpen, setSchemaOpen,
		setSchemaFormBankId, setSchemaReadFailureBankId, command, refresh,
	});
	// 未保存的题目编辑只在当前表单里；切换范围前要由用户决定是否丢弃。
	function confirmDiscard(action: () => void, content = "当前题目内容或评分标准尚未保存，切换后无法恢复。") {
		if (!contentDirty && !scoringDirty) return action();
		modal.confirm({
			title: "放弃未保存的修改？",
			content,
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
    editorGeneration.current += 1; setSaving(false);
		schemaContext.current = { bankId: id, readable: false, formReady: false };
		setSchemaFormBankId(null);
		loadedVersion.current = null;
		setBankId(id);
		setItem(null);
		setVersionId("");
		setOffset(0);
		setQuery("");
		setSearchFilters(emptySearch);
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
    editorGeneration.current += 1; setSaving(false);
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
      editorGeneration.current += 1; setSaving(false);
      loadedVersion.current = null;
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
		const reservation = pending.current;
		const result = await execute({ "Idempotency-Key": reservation.key });
		if (pending.current === reservation) pending.current = null;
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
    const generation = editorGeneration.current;
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
			if (loadedVersion.current === result.version.id && generation === editorGeneration.current) {
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
			if (generation !== editorGeneration.current) return;
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(getUserErrorMessage(error, "保存草稿失败"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
  async function saveCombinedScoring(scoring: QuestionBankScoring) {
    if (!editorVersion || saving || conflict) throw new Error("请先核对当前草稿状态");
    const target = editorVersion.id;
    const generation = editorGeneration.current;
    const isCurrent = () => generation === editorGeneration.current && loadedVersion.current === target;
    setSaving(true);
    try {
      const values = await editor.validateFields();
      if (!isCurrent()) throw new Error("题目已切换，请在当前草稿重新保存");
      const body: UpdateQuestionBankVersionRequest = {
        ...values, expected_revision: editorVersion.revision,
        options: values.options?.filter((option: string) => option.trim()) ?? [], scoring
      };
      // 单个命令同时落盘两区；网络重试沿用同一输入与幂等键，不能先保存一半再推进修订。
      const result = await command(`update-version:${target}`, body, headers => questionBankApi.updateQuestionBankVersion({ path: { versionId: target }, body, headers }));
      if (isCurrent()) {
        setContentDirty(false); setScoringDirty(false);
        client.setQueryData([...prefix, "version", target], result);
        editor.setFieldsValue({ ...contentFrom(result.version), expected_revision: result.version.revision });
        setEditorVersion(result.version);
      }
      return result.version;
    } catch (error) {
      if (isCurrent() && error instanceof ApiClientError && error.status === 409) setConflict(true);
      throw error;
    } finally { if (isCurrent()) setSaving(false); }
  }
	async function loadLatest() {
		if (!selected || saving) return;
		const requestVersionId = selected.id;
    const generation = editorGeneration.current;
		setSaving(true);
		try {
			// 预览读取不更新查询缓存或编辑修订号，只有用户明确采用后才替换本地草稿。
			const result = await questionBankApi.getQuestionBankVersion({ path: { versionId: requestVersionId } });
			if (loadedVersion.current === requestVersionId && generation === editorGeneration.current) setLatestPreview(result.version);
		} catch (error) {
			message.error(getUserErrorMessage(error, "加载最新版本失败"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	function acceptLatest() {
		if (latestPreview?.id !== loadedVersion.current) return;
    editorGeneration.current += 1;
		setEditorVersion(latestPreview);
		setRestoredScoringDraft(undefined);
		editor.setFieldsValue({ ...contentFrom(latestPreview), expected_revision: latestPreview.revision });
		client.setQueryData([...prefix, "version", latestPreview.id], { version: latestPreview });
		setContentDirty(false); setScoringDirty(false); setConflict(false); setLatestPreview(null);
		// 即使服务器修订未变化，明确放弃也须重建评分表单，不能只把脏标记清除。
		setScoringResetToken((value) => value + 1);
		message.success("已采用最新版本，本地未保存修改已放弃。");
	}
  function mergeLatest(localGroups: ReadonlySet<string>) {
    if (!canEdit || !editorVersion || !latestPreview || latestPreview.id !== loadedVersion.current || saving) return;
    try {
      const merged = mergeQuestionBankDraft(editorVersion, latestPreview, editor.getFieldsValue(true), scoringDraft.current, localGroups);
      editorGeneration.current += 1;
      // 采用最新修订作为后续保存前提，不向服务器写入；别人再修改时仍会正常触发 409。
      setEditorVersion(latestPreview);
      client.setQueryData([...prefix, "version", latestPreview.id], { version: latestPreview });
      editor.setFieldsValue({ ...merged.content, expected_revision: latestPreview.revision });
      setRestoredScoringDraft(merged.scoring);
      scoringDraft.current = merged.scoring;
      setContentDirty(true); setScoringDirty(true); setConflict(false); setLatestPreview(null);
      setScoringResetToken(value => value + 1);
      message.success("合并草稿已生成，请核对后保存。");
    } catch (error) { message.error(getUserErrorMessage(error, "无法合并当前版本，请重新读取最新草稿")); }
  }
	function downloadLocalDraft() {
		if (!editorVersion) return;
		const blob = new Blob([JSON.stringify({ version_id: editorVersion.id, saved_revision: editorVersion.revision, content: editor.getFieldsValue(true), scoring: scoringDraft.current ?? editorVersion.scoring }, null, 2)], { type: "application/json" });
		const url = URL.createObjectURL(blob);
		const link = document.createElement("a"); link.href = url; link.download = `题目本地修改备份-${item?.item_code ?? "草稿"}.json`; link.click();
		window.setTimeout(() => URL.revokeObjectURL(url), 1000);
	}
	function scoringSaved(v: QuestionBankVersion, generation = editorGeneration.current) {
    // 评分保存有独立的等待状态；即使用户已换题，旧表单的成功回执也不能重建当前内容表单。
    if (loadedVersion.current !== v.id || generation !== editorGeneration.current) return;
		setRestoredScoringDraft(undefined);
		setEditorVersion(v);
		editor.setFieldsValue({ ...contentFrom(v), expected_revision: v.revision });
		void refresh();
	}
	function currentHandoffRevision(v: QuestionBankVersion) {
		return selected?.id === v.id && selected.revision === v.revision && selected.bundle_hash === v.bundle_hash ? selected.handoff?.revision ?? 0 : v.handoff?.revision ?? 0;
	}
	async function transition(
		decision: "submit-review" | "approve" | "return-to-draft" | "publish",
		assignees?: { reviewer_id: string; publisher_id: string },
	) {
		if (!editorVersion || saving) return;
    const generation = editorGeneration.current;
		setSaving(true);
		const body = {
			expected_revision: editorVersion.revision,
			bundle_hash: editorVersion.bundle_hash,
			comment,
			expected_handoff_revision: currentHandoffRevision(editorVersion),
			...assignees,
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
			if (generation === editorGeneration.current) {
        scoringSaved(result.version, generation);
        setComment("");
        message.success(transitionSuccessMessages[decision]);
      }
		} catch (error) {
			if (generation !== editorGeneration.current) return;
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(
				getUserErrorMessage(error, "版本操作失败，请核对授权与评分标准"),
			);
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	async function updateHandoff(action: "assign" | "review" | "publish", assignees?: { reviewer_id: string; publisher_id: string }) {
		if (!editorVersion || saving) return;
		const versionId = editorVersion.id;
		const generation = editorGeneration.current;
		const body = { expected_revision: editorVersion.revision, expected_handoff_revision: currentHandoffRevision(editorVersion) };
		setSaving(true);
		try {
			const result = await command(`handoff:${action}:${versionId}`, { ...body, ...assignees }, headers => {
				if (action === "assign") {
					if (!assignees) throw new Error("请选择审核与发布教师");
					return questionBankApi.assignQuestionBankHandoff({ path: { versionId }, body: { ...body, ...assignees }, headers });
				}
				return action === "review" ? questionBankApi.claimQuestionBankReview({ path: { versionId }, body, headers }) : questionBankApi.claimQuestionBankPublish({ path: { versionId }, body, headers });
			});
			// 同一题离开后再进入也有新世代；旧领取回执不能覆盖新页面的交接状态。
			if (generation === editorGeneration.current) {
				// 交接不编辑题干或评分；成功回执只推进交接投影，不能重填表单而丢掉本地修改。
				setEditorVersion(current => current?.id === result.version.id && current.revision === result.version.revision && current.bundle_hash === result.version.bundle_hash ? { ...current, handoff: result.version.handoff, workflow_status: result.version.workflow_status } : current);
				client.setQueryData([...prefix, "version", versionId], result);
				void refresh();
				message.success(action === "assign" ? "交接人员已更新" : "已领取本题待办");
			}
		} catch (error) {
			if (generation === editorGeneration.current) message.error(getUserErrorMessage(error, "交接未完成，请读取最新版本后重试"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	function derive() {
		if (!selected || saving) return;
		const sourceVersionId = selected.id;
		// 派生复制服务端已保存版本；内容和评分两个草稿都必须先经明确丢弃确认。
		confirmDiscard(() => {
			if (loadedVersion.current === sourceVersionId) void deriveSavedVersion();
		}, "新版本会复制当前已保存的内容和评分标准；未保存的题目或评分修改不会进入新版本，放弃后无法恢复。");
	}
	async function deriveSavedVersion() {
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
				onSearchOpen={() => setSearchOpen(true)} onSearch={(value) => { setQuery(value); setSearchFilters(current => ({ ...current, q: value })); setOffset(0); }}
				onBankPageChange={(page) => confirmDiscard(() => { setBankOffset((page - 1) * 20); applyBankSelection(""); })} />
      <div ref={setSearchContainer} />
			{!banks.isLoading && !banks.data?.total ? (
				<Empty description="尚无获授权的题库，可新建题库开始维护题目。" />
			) : (
				<QuestionBankWorkspace
					bank={bank} kind={kind} onKindChange={(value) => confirmDiscard(() => {
            editorGeneration.current += 1; setSaving(false);
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
					scoringSaved={saved => scoringSaved(saved, displayedEditorGeneration)} setScoringDirty={setScoringDirty} scoringResetToken={scoringResetToken} restoredScoringDraft={restoredScoringDraft} onScoringDraftSnapshot={(draft) => { scoringDraft.current = draft; }} user={user}
					comment={comment} setComment={setComment} transition={(decision, assignees) => void transition(decision, assignees)} handoff={(action, assignees) => void updateHandoff(action, assignees)} reviews={reviews}
					members={members}
          saveAllDraft={() => scoringSubmit.current?.()} saveCombinedScoring={saveCombinedScoring} registerScoringSubmit={registerScoringSubmit}
				/>
			)}
			<QuestionBankConflictDialog original={editorVersion} latest={latestPreview} localContent={editor.getFieldsValue(true)} scoringDraft={scoringDraft.current}
				dirty={contentDirty || scoringDirty} onClose={() => setLatestPreview(null)} onDownload={downloadLocalDraft} onAccept={acceptLatest} onMerge={canEdit ? mergeLatest : undefined} />
			<QuestionBankDialogs
				bankId={bankId} dialog={dialog} setDialog={setDialog} saving={saving} schoolOptions={schoolOptions} schools={schools} createBank={(values) => void createBank(values)}
				aclOpen={aclOpen} setAclOpen={setAclOpen} aclForm={aclForm} saveACL={(values) => void saveACL(values)} acl={acl}
				protectedMemberIds={[user.id, bank?.created_by ?? ""]}
				members={members}
				schemaOpen={schemaOpen} setSchemaOpen={setSchemaOpen} currentSchema={currentSchema} schemaReadable={schemaReadable} schemaFormReady={schemaFormReady} schemaLoadFailed={schemaLoadFailed} schemaForm={schemaForm} saveSchema={(values) => void saveSchema(values)}
				searchOpen={searchOpen} setSearchOpen={setSearchOpen} searchFilters={searchFilters} setSearchFilters={setSearchFilters} setSearchOffset={setSearchOffset}
				showSearchResults={false}
				searchContainer={searchContainer}
					searchOffset={searchOffset} searchResults={searchResults} selectItem={selectItem} kind={kind} createItem={(values) => void createItem(values)} defaults={defaults}
			/>
		</div>
	);
}
