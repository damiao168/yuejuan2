import type { ScoringRunItem } from "../../../api/review";

export type ScoringReviewFilter = "all" | "choice" | "suggestion" | "recognition" | "manual";
type ScoringReviewCategory = Exclude<ScoringReviewFilter, "all">;

const choiceTypes = new Set(["single_choice", "multiple_choice", "true_false"]);
const reviewDecisions = new Set(["suggestion_ready", "review_needed", "manual_only"]);
const excludedStates = new Set(["confirmed", "auto_confirmed", "human_confirmed", "failed", "pending", "queued", "processing", "cancelled", "cancelling"]);
const unfinishedProcessing = new Set(["waiting_dependency", "queued", "running", "retry_wait", "failed", "cancelled"]);
const recognitionReasons = new Set([
  "answer_observation_uncertain", "observation_unreadable", "observation_uncertain",
  "answer_association_uncertain", "answer_low_confidence", "answer_confidence_missing",
  "recognition_low_confidence", "ocr_low_confidence", "low_ocr_confidence"
]);

/** 仅将待复核结果归入教师待办；失败、处理中和已确认结果不能因残留原因码重新出现。 */
export function scoringReviewCategory(item: ScoringRunItem): ScoringReviewCategory | undefined {
  if (excludedStates.has(item.state) || unfinishedProcessing.has(item.processing_status ?? "") || ["auto_confirmed", "human_confirmed"].includes(item.decision_status ?? "")) return undefined;
  if (item.state !== "review" && !reviewDecisions.has(item.decision_status ?? "")) return undefined;
  if (choiceTypes.has(item.question_type)) return "choice";
  if (item.decision_status === "suggestion_ready" || item.reason_code === "ai_suggestion_ready") return "suggestion";
  // 建议分把握不足与作答识别不确定是两件事，只认明确的识别原因，不从题型或分值反推。
  if (recognitionReasons.has(item.reason_code ?? "")) return "recognition";
  return "manual";
}

export function scoringReviewCounts(items: ScoringRunItem[]): Record<ScoringReviewCategory, number> {
  const counts = { choice: 0, suggestion: 0, recognition: 0, manual: 0 };
  for (const item of items) {
    const category = scoringReviewCategory(item);
    if (category) counts[category] += 1;
  }
  return counts;
}

export function matchesScoringReviewFilter(item: ScoringRunItem, filter: ScoringReviewFilter): boolean {
  return filter === "all" || scoringReviewCategory(item) === filter;
}
