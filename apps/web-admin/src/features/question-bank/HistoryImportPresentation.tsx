import { Alert, Button, Input, List, Select, Space, Tag } from "antd";
import { ExternalLink, SearchCheck } from "lucide-react";
import type { QuestionBankBatchImportResultItem } from "@edugrade/sdk";
import type { Question } from "../../api/papers";
import { questionTypeOptions } from "../../constants/examCatalog";
import { MathMarkdown } from "../../components/MathMarkdown";
import { importErrorMessage, type ImportDraft } from "./historyImportCommands";
import { referenceText } from "../grading/workbench/components/FrozenQuestionReference";

function duplicateLabel(kind: string) {
  switch (kind) {
    case "exact_bundle": return "完全重复";
    case "similar_content": return "相似题干";
    default: return "内容相同、评分不同";
  }
}

export function HistoryImportActions({ loading, confirming, previewing, hasPending, canRepreview, frozenUnavailable, bankId, selectedCount, drafts, close, preview, confirm }: Readonly<{
  loading: boolean; confirming: boolean; previewing: boolean; hasPending: boolean; canRepreview: boolean;
  frozenUnavailable: boolean; bankId: string; selectedCount: number; drafts: ImportDraft[];
  close: () => void; preview: () => Promise<void>; confirm: () => Promise<void>;
}>) {
  return <Space wrap>
    <Button onClick={close}>关闭</Button>
    <Button icon={<SearchCheck size={16} />} loading={previewing} disabled={loading || confirming || (hasPending && !canRepreview) || frozenUnavailable || !bankId || selectedCount === 0} onClick={() => void preview()}>{hasPending ? "重新预览未完成题目" : "预览已确认题目"}</Button>
    <Button type="primary" loading={confirming} disabled={!hasPending && (!drafts.length || drafts.some((draft) => !draft.preview.available || (draft.decision === "new_version" && !draft.existingItemId)))} onClick={() => void confirm()}>{hasPending ? "重试未完成导入" : `生成 ${drafts.length || ""} 个草稿`}</Button>
  </Space>;
}

function HistoryImportPreview({ draft, patchDraft }: Readonly<{ draft: ImportDraft; patchDraft: (questionId: string, patch: Partial<ImportDraft>) => void }>) {
  return <List.Item>
    <div className="history-import-preview">
      <div className="history-import-preview-head"><strong>第 {draft.preview.source.question_no} 题</strong><Space wrap><Tag>{questionTypeOptions.find((option) => option.value === draft.preview.content.question_type)?.label ?? "题目"}</Tag><Tag>{draft.preview.content.default_score} 分</Tag><Tag color={draft.preview.available ? "green" : "red"}>{draft.preview.available ? "来源一致" : "来源冲突"}</Tag></Space></div>
      <MathMarkdown promoteInlineMath={false}>{draft.preview.content.stem}</MathMarkdown>
      <div className="history-import-row">
        <label><span>保存方式</span><Select value={draft.decision} options={[{ label: "新建题目", value: "new_item" }, { label: "关联为新版本", value: "new_version", disabled: draft.preview.duplicate_hints.length === 0 }]} onChange={(value) => patchDraft(draft.preview.question_id, { decision: value, existingItemId: undefined })} /></label>
        {draft.decision === "new_item" ? <label><span>题目编号</span><Input value={draft.itemCode} onChange={(event) => patchDraft(draft.preview.question_id, { itemCode: event.target.value })} /></label> : <label><span>更新哪道已有题目</span><Select value={draft.existingItemId} options={[...new Map(draft.preview.duplicate_hints.map((hint) => [hint.item_id, { label: `${hint.item_code} · ${duplicateLabel(hint.kind)}`, value: hint.item_id }])).values()]} onChange={(value) => patchDraft(draft.preview.question_id, { existingItemId: value })} /></label>}
      </div>
      {draft.preview.content.assessment_archetype === "selected_response" && draft.preview.content.question_type !== "true_false" ? <label className="history-import-options"><span>选项（每行一个；历史题未冻结选项时需补齐）</span><Input.TextArea rows={3} value={draft.optionsText} onChange={(event) => patchDraft(draft.preview.question_id, { optionsText: event.target.value })} /></label> : null}
      {draft.preview.issues.length ? <ul className="history-import-issues">{draft.preview.issues.map((issue) => <li key={`${issue.code}-${issue.field ?? ""}`}>{issue.message}</li>)}</ul> : null}
      {draft.preview.duplicate_hints.length ? <Alert type="warning" showIcon message={`${draft.preview.duplicate_hints.length} 个目标题库命中`} description={<><p>请比较已授权题目，选择新建题目或关联为新草稿版本。</p><ul>{draft.preview.duplicate_hints.map((hint) => <li key={hint.version_id}><strong>{hint.item_code} · v{hint.version_no} · {duplicateLabel(hint.kind)}</strong><p>{hint.stem_summary}</p></li>)}</ul></>} /> : null}
      <details><summary>查看原卷答案与评分标准</summary>
        <h4>参考答案</h4><MathMarkdown>{referenceText(draft.preview.scoring.answer) || "原卷未提供参考答案"}</MathMarkdown>
        <h4>参考解答</h4><MathMarkdown>{referenceText(draft.preview.scoring.solution) || "原卷未提供参考解答"}</MathMarkdown>
        <h4>评分标准</h4><MathMarkdown>{referenceText(draft.preview.scoring.rubric) || "原卷未提供评分标准"}</MathMarkdown>
        <p>附件：{draft.preview.scoring.assets.map((asset)=>asset.name).join("、") || "无"}</p><p>来源确认时的原始内容保持不变。保存到题库后由审核教师核对。</p></details>
    </div>
  </List.Item>;
}

export function HistoryImportPreviews({ drafts, patchDraft }: Readonly<{ drafts: ImportDraft[]; patchDraft: (questionId: string, patch: Partial<ImportDraft>) => void }>) {
  if (!drafts.length) return null;
  return <List className="history-import-previews" dataSource={drafts} renderItem={(draft) => <HistoryImportPreview draft={draft} patchDraft={patchDraft} />} />;
}

function receiptLabel(item: QuestionBankBatchImportResultItem) {
  if (item.status === "succeeded") return `已生成第 ${item.result?.version.version_no ?? ""} 版草稿，等待审核与发布`;
  const retry = item.retryable ? "（可重试）" : "";
  return `${importErrorMessage(item.error_code)}${retry}`;
}

export function HistoryImportResults({ results, questions, onOpenBank }: Readonly<{ results: QuestionBankBatchImportResultItem[]; questions: Question[]; onOpenBank?: (path?: string) => void }>) {
  if (!results.length) return null;
  return <Alert type={results.every((item) => item.status === "succeeded") ? "success" : "warning"} showIcon message="保存题库的结果" description={<ul>{results.map(item=><li key={item.command_id}>第 {questions.find(question=>question.id===item.question_id)?.question_no ?? "本场"} 题：{receiptLabel(item)}{item.status==="succeeded" && item.result && onOpenBank ? <Button type="link" icon={<ExternalLink size={14}/>} onClick={()=>onOpenBank(`/question-bank?bank=${encodeURIComponent(item.result!.item.bank_id)}&item=${encodeURIComponent(item.result!.item.id)}&version=${encodeURIComponent(item.result!.version.id)}`)}>打开本题草稿，查看审核人员</Button> : null}</li>)}</ul>} />;
}
