import type { TemplateAnalysisCandidate } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { containsObjectiveBox, isObjectiveQuestion, objectiveBoxesOverlap, validObjectiveBox } from "./objectiveGridGeometry";

const normalizeLabel = (label: string) => label.replace(/\s+/g, "").toLowerCase();

/** 只接纳模型实际读到的题号与选项框；任何缺项、重复或越界都保留大框供纠错，不按位置补造。 */
export function buildMultimodalObjectiveRegions(candidate: TemplateAnalysisCandidate, questions: Question[], candidates: TemplateAnalysisCandidate[]): LayoutRegion[] | undefined {
  const items = candidate.objective_items;
  const block = candidate.proposed_bbox;
  if (!block || !validObjectiveBox(block) || !Array.isArray(items) || !items.length || items.length > 256) return;
  const used = new Set<string>();
  const regions: LayoutRegion[] = [];
  const allItems = candidates.filter(row => row.role === "objective_group" && row.semantic_check === "supported")
    .flatMap(row => Array.isArray(row.objective_items) ? row.objective_items : []);
  for (const item of items) {
    if (!item || typeof item.printed_question_label !== "string" || !validObjectiveBox(item.bbox)
      || !containsObjectiveBox(block, item.bbox) || !Array.isArray(item.options) || item.options.length < 2 || item.options.length > 12) return;
    const label = normalizeLabel(item.printed_question_label);
    const matching = questions.filter(question => normalizeLabel(question.question_no) === label);
    if (matching.length !== 1 || !isObjectiveQuestion(matching[0]) || used.has(matching[0].id)
      || allItems.filter(other => other && typeof other.printed_question_label === "string" && normalizeLabel(other.printed_question_label) === label).length !== 1) return;
    const question = matching[0];
    if (question.options?.length && question.options.length !== item.options.length
      || question.question_type === "true_false" && item.options.length !== 2) return;
    if (item.options.some(option => !option || typeof option.label !== "string")) return;
    const labels = new Set(item.options.map(option => option.label));
    if (labels.size !== item.options.length || item.options.some(option => !/^[A-L]$/.test(option.label)
      || !validObjectiveBox(option.bbox) || !containsObjectiveBox(item.bbox, option.bbox))
      || item.options.some((option, index) => item.options.slice(index + 1).some(other => objectiveBoxesOverlap(option.bbox, other.bbox)))) return;
    // 完整选择题必须有从 A 开始的全部选项；顺序保留模型标签，不能按几何位置重新命名。
    if (Array.from({ length: item.options.length }, (_, index) => String.fromCharCode(65 + index)).some(label => !labels.has(label))) return;
    if (allItems.some(other => other && other !== item && validObjectiveBox(other.bbox) && objectiveBoxesOverlap(item.bbox, other.bbox))) return;
    used.add(question.id);
    const coordinate = (value: number) => Number(Math.max(0, Math.min(1, value)).toFixed(8));
    regions.push({ id: crypto.randomUUID(), question_id: question.id, label: question.question_no, ...item.bbox,
      option_coordinate_space: "question_relative", suggestion_source: "multimodal_layout",
      option_regions: item.options.map(option => ({ id: crypto.randomUUID(), label: option.label,
        x: coordinate((option.bbox.x - item.bbox.x) / item.bbox.width), y: coordinate((option.bbox.y - item.bbox.y) / item.bbox.height),
        width: coordinate(option.bbox.width / item.bbox.width), height: coordinate(option.bbox.height / item.bbox.height) })) });
  }
  return regions;
}
