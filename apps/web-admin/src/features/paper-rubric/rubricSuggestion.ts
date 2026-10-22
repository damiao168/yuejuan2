import type { Question, SuggestedRubricCandidate } from "../../api/papers";
import type { RubricSolutionLine } from "./rubricSolutionLines";
import type { SuggestedPointDraft } from "./rubricAssistantDraft";
import { newRubricPointId } from "./rubricPointIdentity";

export function readRubricSuggestion(suggestion: SuggestedRubricCandidate | undefined, question: Question, lines: RubricSolutionLine[]): SuggestedPointDraft[] {
  if (!suggestion || suggestion.candidate_id !== question.id || suggestion.origin !== "ai_suggestion_from_solution" || suggestion.status !== "review_required"
    || suggestion.max_score !== question.score || !suggestion.points?.length) throw new Error("建议与当前题目不一致，请重新生成");
  return suggestion.points.map(point => {
    const steps = point.evidence_step_ids.map(id => { const match = /^step-([1-9]\d*)$/.exec(id); return match ? lines[Number(match[1]) - 1] : undefined; });
    if (!point.description.trim() || !steps.length || steps.some(step => !step)
      || (point.suggested_score !== null && (!Number.isFinite(point.suggested_score) || point.suggested_score < 0))) throw new Error("建议缺少有效的解答步骤或分值，请重新生成");
    return { id: newRubricPointId(), description: point.description, score: point.suggested_score, steps: steps as RubricSolutionLine[], note: point.review_note };
  });
}
