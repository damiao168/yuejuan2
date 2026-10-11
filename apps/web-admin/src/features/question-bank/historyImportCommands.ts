import type { QuestionBank, QuestionBankBatchImportItem, QuestionBankBatchImportRequest, QuestionBankImportPreview } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";

export type ImportDraft = {
  preview: QuestionBankImportPreview;
  itemCode: string;
  decision: "new_item" | "new_version";
  existingItemId?: string;
  optionsText: string;
};
export type ImportCopyright = "unknown" | "owned" | "licensed";

export function importErrorMessage(errorCode?: string) {
  switch (errorCode) {
    case "source_snapshot_unavailable": return "该题没有完整冻结来源，暂不能精选";
    case "resource_not_found": return "来源题或目标题库不在当前授权范围";
    case "target_revision_conflict": return "目标题库已更新，请重新预览";
    case "command_payload_conflict": return "同一命令编号已用于不同内容";
    case "target_locked": return "目标题库或题目已锁定";
    default: return "精选失败，请稍后重试";
  }
}

function draftRequest(draft: ImportDraft, bank: QuestionBank, revision: number, gradeScope: string, copyright: ImportCopyright): QuestionBankBatchImportItem {
  return {
    question_id: draft.preview.question_id,
    target_bank_id: bank.id,
    source_snapshot_id: draft.preview.source.readiness_snapshot_id,
    assessment_snapshot_id: draft.preview.source.assessment_snapshot_id,
    scoring_source: "original_exam",
    dedup_decision: draft.decision,
    ...(draft.decision === "new_version" ? { existing_item_id: draft.existingItemId } : {}),
    expected_target_revision: revision,
    expected_target_schema_version: bank.metadata_schema_version,
    mapping: {
      ...(draft.decision === "new_item" ? { item_code: draft.itemCode.trim() } : {}),
      ...(gradeScope.trim() ? { grade_scope: gradeScope.trim() } : {}),
      copyright,
      intended_use: "exam",
      options: draft.optionsText.split("\n").map((value) => value.trim()).filter(Boolean),
    },
    command_id: crypto.randomUUID(),
  };
}

async function buildLinkedRequests(drafts: ImportDraft[], bank: QuestionBank, gradeScope: string, copyright: ImportCopyright): Promise<QuestionBankBatchImportRequest> {
  const items: QuestionBankBatchImportItem[] = [];
  // 关联版本逐题读取 Item 修订号；任何一题读取失败都会中止整批组装，未落盘前不发送导入命令。
  for (const draft of drafts) {
    let revision = bank.revision;
    if (draft.decision === "new_version" && draft.existingItemId) {
      const existing = await questionBankApi.getQuestionBankItem({ path: { itemId: draft.existingItemId } });
      revision = existing.item.revision;
    }
    items.push(draftRequest(draft, bank, revision, gradeScope, copyright));
  }
  return { items };
}

export function buildHistoryImportRequest(drafts: ImportDraft[], bank: QuestionBank, gradeScope: string, copyright: ImportCopyright): QuestionBankBatchImportRequest | Promise<QuestionBankBatchImportRequest> {
  // 纯新建批次原本在首次 await 前就写入 pending；保留同步路径，连续确认不会另造一组命令编号。
  if (!drafts.some((draft) => draft.decision === "new_version" && draft.existingItemId)) {
    return { items: drafts.map((draft) => draftRequest(draft, bank, bank.revision, gradeScope, copyright)) };
  }
  return buildLinkedRequests(drafts, bank, gradeScope, copyright);
}
