import type { QuestionBankScoring, QuestionBankVersion } from "@edugrade/sdk";
import { contentFrom } from "./questionBankPageState";

export type ScoringDraftSnapshot = {
  fields: Record<string, unknown>;
  assets: QuestionBankScoring["assets"];
  answerBaseline?: unknown;
};
type MergeGroup = { key: string; label: string; area: "content" | "scoring" | "assets"; fields: string[] };
export const draftMergeGroups: readonly MergeGroup[] = [
  { key: "stem", label: "题干", area: "content", fields: ["stem"] },
  { key: "type", label: "题型与判分方式", area: "content", fields: ["question_type", "assessment_archetype", "options"] },
  { key: "score", label: "满分", area: "content", fields: ["default_score"] },
  { key: "knowledge", label: "知识点", area: "content", fields: ["knowledge_points"] },
  { key: "metadata", label: "学科、年级与补充信息", area: "content", fields: ["metadata", "custom_metadata"] },
  { key: "answer", label: "答案与误差", area: "scoring", fields: ["answer", "equivalent", "absolute", "relative"] },
  { key: "solution", label: "解析与步骤", area: "scoring", fields: ["solution", "steps"] },
  { key: "rubric", label: "整份评分细则（含评分点身份）", area: "scoring", fields: ["rubricEnabled", "points", "deductions", "examples"] },
  { key: "template", label: "引用的评分模板", area: "scoring", fields: ["template"] },
  { key: "policy", label: "可用范围", area: "scoring", fields: ["use_policy"] },
  { key: "assets", label: "评分附件", area: "assets", fields: [] },
];

export function scoringDraftFrom(scoring: QuestionBankScoring): ScoringDraftSnapshot {
  const answer = scoring.answer?.standard_answer;
  return {
    fields: {
      answer: answer == null ? "" : typeof answer === "string" ? answer : JSON.stringify(answer, null, 2),
      equivalent: JSON.stringify(scoring.answer?.equivalent_answers ?? []),
      absolute: scoring.answer?.tolerance?.absolute, relative: scoring.answer?.tolerance?.relative,
      solution: scoring.solution?.raw_text ?? "", steps: JSON.stringify(scoring.solution?.steps ?? []),
      rubricEnabled: Boolean(scoring.rubric),
      points: scoring.rubric?.points.map(point => ({ ...point, evidence: JSON.stringify(point.evidence_requirements ?? []) })) ?? [],
      deductions: JSON.stringify(scoring.rubric?.deductions ?? []), examples: JSON.stringify(scoring.rubric?.examples ?? []),
      template: scoring.template_version_id ?? undefined, use_policy: scoring.use_policy,
    },
    assets: scoring.assets, answerBaseline: answer,
  };
}

export function mergeQuestionBankDraft(original: QuestionBankVersion, latest: QuestionBankVersion,
  localContent: Record<string, unknown>, localScoring: unknown, localGroups: ReadonlySet<string>) {
  if (latest.id !== original.id || latest.item_id !== original.item_id || latest.workflow_status !== "draft") {
    throw new Error("只能在同一道题的最新草稿上合并修改");
  }
  const local = scoringDraftFrom(original.scoring);
  const snapshot = localScoring as Partial<ScoringDraftSnapshot> | null;
  if (snapshot?.fields) local.fields = { ...local.fields, ...snapshot.fields };
  if (snapshot?.assets) local.assets = snapshot.assets;
  if (snapshot && Object.prototype.hasOwnProperty.call(snapshot, "answerBaseline")) local.answerBaseline = snapshot.answerBaseline;
  const content: Record<string, unknown> = { ...contentFrom(latest) };
  const scoring = scoringDraftFrom(latest.scoring);
  const contentSnapshot = { ...contentFrom(original), ...localContent } as Record<string, unknown>;
  // 关联字段整组采纳，评分点身份不按数组下标拼接；合并只产生本地草稿，保存仍校验最新修订。
  for (const group of draftMergeGroups) {
    if (!localGroups.has(group.key)) continue;
    if (group.area === "assets") scoring.assets = local.assets;
    else for (const field of group.fields) {
      if (group.area === "content") content[field] = contentSnapshot[field];
      else scoring.fields[field] = local.fields[field];
    }
    if (group.key === "answer") scoring.answerBaseline = local.answerBaseline;
  }
  return { content, scoring };
}
