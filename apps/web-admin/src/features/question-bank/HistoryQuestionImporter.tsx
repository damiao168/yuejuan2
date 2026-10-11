import { useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Modal, Select, Spin } from "antd";
import { ArchiveRestore } from "lucide-react";
import type {
	QuestionBank,
	QuestionBankBatchImportRequest,
	QuestionBankBatchImportResultItem,
} from "@edugrade/sdk";
import { getExamReadiness, type ExamReadiness } from "../../api/configuration";
import type { Question } from "../../api/papers";
import { getUserErrorMessage } from "../../api/userError";
import { questionBankApi } from "../../api/questionBank";
import { buildHistoryImportRequest, importErrorMessage, type ImportDraft } from "./historyImportCommands";
import { HistoryImportActions, HistoryImportPreviews, HistoryImportResults } from "./HistoryImportPresentation";
import "./history-question-importer.css";

const copyrightOptions = [
	{ label: "未知（保存为草稿）", value: "unknown" },
	{ label: "本校拥有", value: "owned" },
	{ label: "已获许可", value: "licensed" },
];

export function HistoryQuestionImporter({
	examId,
	questions,
	onImported,
	onOpenBank,
	scopeKey,
}: Readonly<{
	examId: string;
	questions: Question[];
	onImported: () => Promise<void> | void;
	onOpenBank?: (path?: string) => void;
	scopeKey: string;
}>) {
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
				const request = buildHistoryImportRequest(drafts, selectedBank, gradeScope, copyright);
				// 逐题命令先落到本地再发送；响应丢失时重放同一命令编号，服务端才能识别为同一次导入。
				pending.current = request instanceof Promise ? await request : request;
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
			footer={<HistoryImportActions loading={loading} confirming={confirming} previewing={previewing} hasPending={hasPending} canRepreview={canRepreview} frozenUnavailable={Boolean(frozenUnavailable)} bankId={bankId} selectedCount={selectedIds.length} drafts={drafts} close={() => setOpen(false)} preview={preview} confirm={confirm} />}
		>
			{loading ? <div className="history-import-loading"><Spin /> 正在读取冻结来源与已授权题库…</div> : null}
			{hasPending ? <Alert type="warning" showIcon message="未完成的导入已保留" description={canRepreview ? "成功题目已移出列表。可重试未完成题目，或重新预览后调整。" : "结果尚未确认，请先重试本次导入。系统会核对原请求，避免重复生成草稿。"} /> : null}
			{frozenUnavailable ? <Alert type="warning" showIcon message="没有可保存的已确认试卷内容" description="当前试卷尚未确认完整内容，或历史记录没有保存完整资料。请联系考试负责人核对；已结束的考试不能重新冻结准备配置。" /> : null}
			{readiness?.snapshot_id && readiness.import_snapshot_available ? <Alert type="info" showIcon message="从已确认的试卷保存题目" description="只保存确认时的题目、答案和评分标准，不包含学生作答。保存后为题库草稿，仍需审核和发布才能选入其他考试。" /> : null}
			<div className="history-import-controls">
				<label><span>目标题库</span><Select value={bankId || undefined} placeholder="选择已授权题库" options={banks.map((bank) => ({ label: bank.name, value: bank.id }))} onChange={(value) => { setBankId(value); setDrafts([]); setResults([]); }} /></label>
				<label><span>适用年级</span><Select allowClear value={gradeScope || undefined} placeholder="选择年级，可留空后补齐" options={[{value:"grade_7",label:"七年级"},{value:"grade_8",label:"八年级"},{value:"grade_9",label:"九年级"},{value:"grade_10",label:"高一"},{value:"grade_11",label:"高二"},{value:"grade_12",label:"高三"}]} onChange={value=>{setGradeScope(value ?? "");setDrafts([]);}} /></label>
				<label><span>版权</span><Select value={copyright} options={copyrightOptions} onChange={(value) => { setCopyright(value); setDrafts([]); }} /></label>
			</div>
			<div className="history-import-selector">
				<strong>选择题目（最多 50 道）</strong>
				<Checkbox.Group value={selectedIds} onChange={(values) => { setSelectedIds(values.slice(0, 50) as string[]); setDrafts([]); setResults([]); }}>
					{questions.map((question) => <Checkbox key={question.id} value={question.id}>第 {question.question_no} 题 · {question.score} 分 · {question.stem || "未命名题目"}</Checkbox>)}
				</Checkbox.Group>
			</div>
			{Object.keys(previewFailures).length ? <Alert type="warning" showIcon message="部分题目不可预览" description={<ul>{Object.entries(previewFailures).map(([id, reason]) => <li key={id}>{questions.find((question) => question.id === id)?.question_no ?? id}: {reason}</li>)}</ul>} /> : null}
			<HistoryImportPreviews drafts={drafts} patchDraft={patchDraft} />
			<HistoryImportResults results={results} questions={questions} onOpenBank={onOpenBank} />
		</Modal>
	</>;
}
