import { useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Input, List, Modal, Select, Space, Spin, Tag } from "antd";
import { ArchiveRestore, ExternalLink, SearchCheck } from "lucide-react";
import type {
	QuestionBank,
	QuestionBankBatchImportItem,
	QuestionBankBatchImportRequest,
	QuestionBankBatchImportResultItem,
	QuestionBankImportPreview,
} from "@edugrade/sdk";
import { getExamReadiness, type ExamReadiness } from "../../api/configuration";
import type { Question } from "../../api/papers";
import { getUserErrorMessage } from "../../api/userError";
import { questionBankApi } from "../../api/questionBank";
import { questionTypeOptions } from "../../constants/examCatalog";
import "./history-question-importer.css";

type ImportDraft = {
	preview: QuestionBankImportPreview;
	itemCode: string;
	decision: "new_item" | "new_version";
	existingItemId?: string;
	optionsText: string;
};

const copyrightOptions = [
	{ label: "未知（保存为草稿）", value: "unknown" },
	{ label: "本校拥有", value: "owned" },
	{ label: "已获许可", value: "licensed" },
];

function importErrorMessage(errorCode?: string) {
	switch (errorCode) {
		case "source_snapshot_unavailable": return "该题没有完整冻结来源，暂不能精选";
		case "resource_not_found": return "来源题或目标题库不在当前授权范围";
		case "target_revision_conflict": return "目标题库已更新，请重新预览";
		case "command_payload_conflict": return "同一命令编号已用于不同内容";
		case "target_locked": return "目标题库或题目已锁定";
		default: return "精选失败，请稍后重试";
	}
}
function duplicateLabel(kind: string) {
	return kind === "exact_bundle" ? "完全重复" : kind === "similar_content" ? "相似题干" : "内容相同、评分不同";
}

export function HistoryQuestionImporter({
	examId,
	questions,
	onImported,
	onOpenBank,
	scopeKey,
}: {
	examId: string;
	questions: Question[];
	onImported: () => Promise<void> | void;
	onOpenBank?: () => void;
	scopeKey: string;
}) {
	const { message } = App.useApp();
	const [open, setOpen] = useState(false);
	const [loading, setLoading] = useState(false);
	const [previewing, setPreviewing] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [readiness, setReadiness] = useState<ExamReadiness>();
	const [banks, setBanks] = useState<QuestionBank[]>([]);
	const [bankId, setBankId] = useState("");
	const [selectedIds, setSelectedIds] = useState<string[]>([]);
	const [gradeScope, setGradeScope] = useState("");
	const [copyright, setCopyright] = useState<"unknown" | "owned" | "licensed">("unknown");
	const [drafts, setDrafts] = useState<ImportDraft[]>([]);
	const [previewFailures, setPreviewFailures] = useState<Record<string, string>>({});
	const [results, setResults] = useState<QuestionBankBatchImportResultItem[]>([]);
	const storageKey = `question-bank-history-import:${scopeKey}:${examId}`;
	const pending = useRef<QuestionBankBatchImportRequest | undefined>(undefined);
	const [hasPending, setHasPending] = useState(false);
	const [canRepreview, setCanRepreview] = useState(false);
	const selectedBank = useMemo(() => banks.find((bank) => bank.id === bankId), [bankId, banks]);

	async function showImporter() {
		setOpen(true);
		setLoading(true);
		setDrafts([]);
		setResults([]);
		setCanRepreview(false);
		try {
			const saved = localStorage.getItem(storageKey);
			pending.current = saved ? JSON.parse(saved) as QuestionBankBatchImportRequest : undefined;
			setHasPending(Boolean(pending.current));
		} catch { pending.current = undefined; setHasPending(false); }
		try {
			const [readinessResult, bankResult] = await Promise.all([
				getExamReadiness(examId),
				questionBankApi.listQuestionBanks({ query: { limit: 100 } }),
			]);
			setReadiness(readinessResult.readiness);
			setBanks(bankResult.banks);
			setBankId((current) => bankResult.banks.some((bank) => bank.id === current) ? current : bankResult.banks[0]?.id ?? "");
			setSelectedIds((current) => current.filter((id) => questions.some((question) => question.id === id)));
		} catch (error) {
			message.error(getUserErrorMessage(error, "读取冻结来源或题库失败"));
		} finally {
			setLoading(false);
		}
	}

	async function preview() {
		if (hasPending && !canRepreview) return;
		if (!readiness?.snapshot_id || !readiness.import_snapshot_available || !bankId || selectedIds.length === 0) return;
		setPreviewing(true);
		setResults([]);
		pending.current = undefined;
		localStorage.removeItem(storageKey);
		setHasPending(false);
		setCanRepreview(false);
		try {
			const response = await questionBankApi.previewQuestionBankImports({
				body: {
					selections: selectedIds.map((questionId) => ({
						question_id: questionId,
						target_bank_id: bankId,
						source_snapshot_id: readiness.snapshot_id as string,
						mapping: {
							...(gradeScope.trim() ? { grade_scope: gradeScope.trim() } : {}),
							copyright,
							intended_use: "exam" as const,
						},
					})),
				}
			});
			const failures: Record<string, string> = {};
			const nextDrafts: ImportDraft[] = [];
			for (const item of response.items) {
				if (!item.preview) {
					failures[item.question_id] = importErrorMessage(item.error_code);
					continue;
				}
				nextDrafts.push({
					preview: item.preview,
					itemCode: item.preview.suggested_item_code,
					decision: "new_item",
					optionsText: item.preview.content.options.join("\n"),
				});
			}
			setPreviewFailures(failures);
			setDrafts(nextDrafts);
			if (nextDrafts.length) message.success(`已读取 ${nextDrafts.length} 道冻结题目`);
		} catch (error) {
			message.error(getUserErrorMessage(error, "冻结来源预览失败"));
		} finally {
			setPreviewing(false);
		}
	}

	function patchDraft(questionId: string, patch: Partial<ImportDraft>) {
		setDrafts((current) => current.map((draft) => draft.preview.question_id === questionId ? { ...draft, ...patch } : draft));
	}

	async function confirm() {
		if (!pending.current && (!selectedBank || drafts.length === 0)) return;
		setConfirming(true);
		try {
			if (!pending.current && selectedBank) {
				const requests: QuestionBankBatchImportItem[] = [];
				for (const draft of drafts) {
					let expectedRevision = selectedBank.revision;
					if (draft.decision === "new_version" && draft.existingItemId) {
						const existing = await questionBankApi.getQuestionBankItem({ path: { itemId: draft.existingItemId } });
						expectedRevision = existing.item.revision;
					}
					const options = draft.optionsText.split("\n").map((value) => value.trim()).filter(Boolean);
					requests.push({
						question_id: draft.preview.question_id,
						target_bank_id: selectedBank.id,
						source_snapshot_id: draft.preview.source.readiness_snapshot_id,
						assessment_snapshot_id: draft.preview.source.assessment_snapshot_id,
						scoring_source: "original_exam",
						dedup_decision: draft.decision,
						...(draft.decision === "new_version" ? { existing_item_id: draft.existingItemId } : {}),
						expected_target_revision: expectedRevision,
						expected_target_schema_version: selectedBank.metadata_schema_version,
						mapping: {
							...(draft.decision === "new_item" ? { item_code: draft.itemCode.trim() } : {}),
							...(gradeScope.trim() ? { grade_scope: gradeScope.trim() } : {}),
							copyright,
							intended_use: "exam",
							options,
						},
						command_id: crypto.randomUUID(),
					} as QuestionBankBatchImportItem);
				}
				// 逐题命令先落到本地再发送；响应丢失时重放同一命令编号，服务端才能识别为同一次导入。
				pending.current = { items: requests };
				localStorage.setItem(storageKey, JSON.stringify(pending.current));
				setHasPending(true);
			}
			if (!pending.current) return;
			const response = await questionBankApi.confirmQuestionBankImportBatch({ body: pending.current });
			setResults(response.items);
			const succeeded = response.items.filter((item) => item.status === "succeeded").length;
			if (succeeded !== response.items.length) message.warning("部分题目未导入，可重试失败项或重新预览调整");
			if (succeeded === response.items.length) {
				pending.current = undefined;
				localStorage.removeItem(storageKey);
				setHasPending(false);
				setDrafts([]);
			} else {
				// 只有逐题回执确认成功后才移除该项；失败项继续保留，方便按原命令重试或重新预览。
				const failedIDs = new Set(response.items.filter((item) => item.status !== "succeeded").map((item) => item.command_id));
				pending.current = { items: pending.current.items.filter((item) => failedIDs.has(item.command_id)) };
				localStorage.setItem(storageKey, JSON.stringify(pending.current));
				setSelectedIds(pending.current.items.map((item) => item.question_id));
				setDrafts((current) => current.filter((draft) => pending.current?.items.some((item) => item.question_id === draft.preview.question_id)));
				setCanRepreview(true);
			}
			if (succeeded) {
				message.success(`${succeeded} 道题已生成题库草稿`);
				try { await onImported(); } catch { message.warning("草稿已保存，列表刷新失败，请手动刷新"); }
			}
		} catch (error) {
			setCanRepreview(false);
			message.error(getUserErrorMessage(error, "精选入题库失败"));
		} finally {
			setConfirming(false);
		}
	}

	const frozenUnavailable = readiness && (!readiness.snapshot_id || !readiness.import_snapshot_available);
	return <>
		<Button icon={<ArchiveRestore size={16} />} onClick={() => void showImporter()}>精选入题库</Button>
		<Modal
			open={open}
			title="从本次考试精选题目"
			width={920}
			styles={{ body: { maxHeight: "min(65vh, 720px)", overflowY: "auto", paddingRight: 4 } }}
			onCancel={() => setOpen(false)}
			footer={<Space wrap>
				<Button onClick={() => setOpen(false)}>关闭</Button>
				<Button icon={<SearchCheck size={16} />} loading={previewing} disabled={loading || confirming || (hasPending && !canRepreview) || Boolean(frozenUnavailable) || !bankId || selectedIds.length === 0} onClick={() => void preview()}>{hasPending ? "重新预览失败项" : "预览冻结内容"}</Button>
				<Button type="primary" loading={confirming} disabled={!hasPending && (!drafts.length || drafts.some((draft) => !draft.preview.available || (draft.decision === "new_version" && !draft.existingItemId)))} onClick={() => void confirm()}>{hasPending ? "重试原逐题命令" : `生成 ${drafts.length || ""} 个草稿`}</Button>
			</Space>}
		>
			{loading ? <div className="history-import-loading"><Spin /> 正在读取冻结来源与已授权题库…</div> : null}
			{hasPending ? <Alert type="warning" showIcon message="已保留原导入请求" description={canRepreview ? "成功题目已移出待处理列表。重试将复用失败题目的原请求；也可重新预览失败项后调整。" : "导入结果尚未确认。请重试原逐题命令核对结果，再调整选择，以免重复生成草稿。"} /> : null}
			{frozenUnavailable ? <Alert type="warning" showIcon message="没有可导入的冻结来源" description="原记录没有保存完整题目内容，或当前配置尚未确认。请在考试准备中重新确认后再精选。" /> : null}
			{readiness?.snapshot_id && readiness.import_snapshot_available ? <Alert type="info" showIcon message="已锁定来源快照" description="读取考试确认时保存的内容，不包含学生作答与原答题区坐标。" /> : null}
			<div className="history-import-controls">
				<label><span>目标题库</span><Select value={bankId || undefined} placeholder="选择已授权题库" options={banks.map((bank) => ({ label: bank.name, value: bank.id }))} onChange={(value) => { setBankId(value); setDrafts([]); setResults([]); }} /></label>
				<label><span>目标年级范围</span><Input value={gradeScope} placeholder="例如 grade_8；可留空后在草稿补齐" onChange={(event) => { setGradeScope(event.target.value); setDrafts([]); }} /></label>
				<label><span>版权</span><Select value={copyright} options={copyrightOptions} onChange={(value) => { setCopyright(value); setDrafts([]); }} /></label>
			</div>
			<div className="history-import-selector">
				<strong>选择题目（最多 50 道）</strong>
				<Checkbox.Group value={selectedIds} onChange={(values) => { setSelectedIds(values.slice(0, 50) as string[]); setDrafts([]); setResults([]); }}>
					{questions.map((question) => <Checkbox key={question.id} value={question.id}>第 {question.question_no} 题 · {question.score} 分 · {question.stem || "未命名题目"}</Checkbox>)}
				</Checkbox.Group>
			</div>
			{Object.keys(previewFailures).length ? <Alert type="warning" showIcon message="部分题目不可预览" description={<ul>{Object.entries(previewFailures).map(([id, reason]) => <li key={id}>{questions.find((question) => question.id === id)?.question_no ?? id}: {reason}</li>)}</ul>} /> : null}
			{drafts.length ? <List
				className="history-import-previews"
				dataSource={drafts}
				renderItem={(draft) => <List.Item>
					<div className="history-import-preview">
						<div className="history-import-preview-head"><strong>第 {draft.preview.source.question_no} 题</strong><Space wrap><Tag>{questionTypeOptions.find((option) => option.value === draft.preview.content.question_type)?.label ?? "题目"}</Tag><Tag>{draft.preview.content.default_score} 分</Tag><Tag color={draft.preview.available ? "green" : "red"}>{draft.preview.available ? "来源一致" : "来源冲突"}</Tag></Space></div>
						<p>{draft.preview.content.stem}</p>
						<div className="history-import-row">
							<label><span>保存方式</span><Select value={draft.decision} options={[{ label: "新建题目", value: "new_item" }, { label: "关联为新版本", value: "new_version", disabled: draft.preview.duplicate_hints.length === 0 }]} onChange={(value) => patchDraft(draft.preview.question_id, { decision: value, existingItemId: undefined })} /></label>
							{draft.decision === "new_item" ? <label><span>题目编码</span><Input value={draft.itemCode} onChange={(event) => patchDraft(draft.preview.question_id, { itemCode: event.target.value })} /></label> : <label><span>关联 Item</span><Select value={draft.existingItemId} options={[...new Map(draft.preview.duplicate_hints.map((hint) => [hint.item_id, { label: `${hint.item_code} · ${duplicateLabel(hint.kind)}`, value: hint.item_id }])).values()]} onChange={(value) => patchDraft(draft.preview.question_id, { existingItemId: value })} /></label>}
						</div>
						{draft.preview.content.assessment_archetype === "selected_response" && draft.preview.content.question_type !== "true_false" ? <label className="history-import-options"><span>选项（每行一个；历史题未冻结选项时需补齐）</span><Input.TextArea rows={3} value={draft.optionsText} onChange={(event) => patchDraft(draft.preview.question_id, { optionsText: event.target.value })} /></label> : null}
						{draft.preview.issues.length ? <ul className="history-import-issues">{draft.preview.issues.map((issue) => <li key={`${issue.code}-${issue.field ?? ""}`}>{issue.message}</li>)}</ul> : null}
						{draft.preview.duplicate_hints.length ? <Alert type="warning" showIcon message={`${draft.preview.duplicate_hints.length} 个目标题库命中`} description={<><p>请比较已授权题目，选择新建题目或关联为新草稿版本。</p><ul>{draft.preview.duplicate_hints.map((hint) => <li key={hint.version_id}><strong>{hint.item_code} · v{hint.version_no} · {duplicateLabel(hint.kind)}</strong><p>{hint.stem_summary}</p></li>)}</ul></>} /> : null}
						<details><summary>冻结来源与评分事实</summary><div className="history-import-facts"><span>readiness hash：{draft.preview.source.snapshot_hash}</span><span>Assessment Snapshot：{draft.preview.source.assessment_snapshot_id}</span><span>Assessment hash：{draft.preview.source.assessment_snapshot_hash}</span><span>Profile：{JSON.stringify(draft.preview.source.profile_snapshot)}</span><span>Archetype：{JSON.stringify(draft.preview.source.archetype_snapshot)}</span><span>答案：{JSON.stringify(draft.preview.scoring.answer)}</span><span>解析：{JSON.stringify(draft.preview.scoring.solution)}</span><span>Rubric：{JSON.stringify(draft.preview.scoring.rubric)}</span><span>附件：{draft.preview.scoring.assets.map((asset) => asset.name).join("、") || "无"}</span></div></details>
					</div>
				</List.Item>}
			/> : null}
			{results.length ? <Alert type={results.every((item) => item.status === "succeeded") ? "success" : "warning"} showIcon message="逐题导入回执" description={<><ul>{results.map((item) => <li key={item.command_id}>第 {questions.find((question) => question.id === item.question_id)?.question_no ?? item.question_id} 题：{item.status === "succeeded" ? `已生成 Draft ${item.result?.version.id.slice(0, 8)}` : `${importErrorMessage(item.error_code)}${item.retryable ? "（可重试）" : ""}`}</li>)}</ul>{results.some((item) => item.status === "succeeded") && onOpenBank ? <Button type="link" icon={<ExternalLink size={14} />} onClick={onOpenBank}>打开题库审核草稿</Button> : null}</>} /> : null}
		</Modal>
	</>;
}
