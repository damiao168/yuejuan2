import type {
QuestionBankItem,
QuestionBankVersion
} from "@edugrade/sdk";
import { useQuery,useQueryClient } from "@tanstack/react-query";
import {
App,
Form
} from "antd";
import { useCallback,useEffect,useRef,useState } from "react";
import { listSchools } from "../../api/org";
import { questionBankApi } from "../../api/questionBank";
import { hasEveryPermission,type SessionUser } from "../../auth/session";
import { usePageLoading } from "../../components/pageReadiness";
import { hashQueryParam } from "../../router/query";
import { useUnsavedChangesGuard } from "../../router/useUnsavedChangesGuard";
import "./question-bank.css";
import { type ScoringDraftSnapshot } from "./questionBankDraftMerge";
import { listQuestionBankMembers } from "./questionBankMembers";
import { contentFrom,matchingVersionForItem,metadataSchemaReadState } from "./questionBankPageState";
import { emptySearch,type SearchFilters } from "./questionBankTypes";

// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function useQuestionBankEditorData({ user }: Readonly<{ user: SessionUser }>) {
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
  return { user, message, modal, client, prefix, bankId, setBankId, item, setItem, versionId, setVersionId, kind, setKind, editorVersion, setEditorVersion, aclOpen, setAclOpen, schemaOpen, setSchemaOpen, searchOpen, setSearchOpen, searchContainer, setSearchContainer, searchFilters, setSearchFilters, searchOffset, setSearchOffset, comment, setComment, contentDirty, setContentDirty, scoringDirty, setScoringDirty, setQuery, offset, setOffset, bankOffset, setBankOffset, versionOffset, setVersionOffset, dialog, setDialog, saving, setSaving, conflict, setConflict, latestPreview, setLatestPreview, scoringResetToken, setScoringResetToken, restoredScoringDraft, setRestoredScoringDraft, scoringDraft, scoringSubmit, registerScoringSubmit, editor, aclForm, schemaForm, setSchemaFormBankId, setSchemaReadFailureBankId, schemaContext, pending, loadedVersion, editorGeneration, displayedEditorGeneration, canCreate, canEdit, canManage, canRetire, banks, schools, items, versions, version, versionMismatch, selected, currentSchema, boundSchema, acl, searchResults, reviews, linkedItem, bank, schemaReadable, schemaFormReady, schemaLoadFailed, members };
}
