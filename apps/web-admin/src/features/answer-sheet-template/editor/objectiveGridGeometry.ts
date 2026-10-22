import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import type { ObjectiveGridGroup, ObjectiveOptionOrientation } from "../../../api/answerSheetObjectiveGrid";
import type { LayoutRegion } from "../../../api/configuration";
import type { Question } from "../../../api/papers";

export type ObjectiveReadOrder = "row_first" | "column_first";
export const isObjectiveQuestion = (question: Question) => ["single_choice", "multiple_choice", "true_false"].includes(question.question_type);

export function validObjectiveBox(box: AnalysisBox | undefined): box is AnalysisBox {
  return Boolean(box && [box.x, box.y, box.width, box.height].every(Number.isFinite)
    && box.x >= 0 && box.y >= 0 && box.width > 0 && box.height > 0
    && box.x + box.width <= 1.000001 && box.y + box.height <= 1.000001);
}

export function containsObjectiveBox(outer: AnalysisBox, inner: AnalysisBox) {
  return inner.x >= outer.x - .000001 && inner.y >= outer.y - .000001
    && inner.x + inner.width <= outer.x + outer.width + .000001
    && inner.y + inner.height <= outer.y + outer.height + .000001;
}

export function objectiveBoxesOverlap(a: AnalysisBox, b: AnalysisBox) {
  return Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x) > .000001
    && Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y) > .000001;
}

/** 阅读顺序只排列实测组；同行/同列用框中心聚类，不根据题数补造任何格子。 */
export function orderObjectiveGroups(groups: ObjectiveGridGroup[], order: ObjectiveReadOrder) {
  const primary = order === "row_first" ? "y" : "x";
  const secondary = primary === "y" ? "x" : "y";
  const extent = primary === "y" ? "height" : "width";
  const bands: ObjectiveGridGroup[][] = [];
  for (const group of [...groups].sort((a, b) => a.bbox[primary] - b.bbox[primary] || a.bbox[secondary] - b.bbox[secondary])) {
    const center = group.bbox[primary] + group.bbox[extent] / 2;
    const band = bands.find(items => Math.abs(center - (items[0].bbox[primary] + items[0].bbox[extent] / 2))
      <= Math.min(group.bbox[extent], items[0].bbox[extent]) * .5);
    if (band) band.push(group); else bands.push([group]);
  }
  return bands.flatMap(items => items.sort((a, b) => a.bbox[secondary] - b.bbox[secondary]));
}

export function orderObjectiveOptions(group: ObjectiveGridGroup, orientation: ObjectiveOptionOrientation, reverse: boolean) {
  const axis = orientation === "horizontal" ? "x" : "y";
  const ordered = [...group.options].sort((a, b) => a.bbox[axis] - b.bbox[axis]);
  return reverse ? ordered.reverse() : ordered;
}

export function validateObjectiveGroups(groups: ObjectiveGridGroup[], block: AnalysisBox, optionCount: number) {
  if (!validObjectiveBox(block)) return "请先框选有效的选择题大区";
  if (!groups.length) return "没有检测到完整题组，请调整范围或手工配置";
  if (!Number.isInteger(optionCount) || optionCount < 2 || optionCount > 12) return "每题选项数须为 2 至 12";
  const ids = new Set<string>();
  for (const group of groups) {
    if (!group.group_id || ids.has(group.group_id) || !validObjectiveBox(group.bbox) || !containsObjectiveBox(block, group.bbox)) return "题组编号或边界无效，请重新解析";
    ids.add(group.group_id);
    if (group.options.length !== optionCount) return "存在选项缺失的题组，请调整范围或手工配置";
    if (group.options.some(option => !validObjectiveBox(option.bbox) || !containsObjectiveBox(group.bbox, option.bbox))) return "选项框超出本题区域，请重新解析";
    if (group.options.some((option, index) => group.options.slice(index + 1).some(other => objectiveBoxesOverlap(option.bbox, other.bbox)))) return "选项框重叠，请重新解析或手工配置";
  }
  if (groups.some((group, index) => groups.slice(index + 1).some(other => objectiveBoxesOverlap(group.bbox, other.bbox)))) return "题组区域重叠，请重新解析或手工配置";
  return undefined;
}

export function buildObjectiveGridRegions({ groups, block, assignments, questions, optionCount, orientation, reverse }: {
  groups: ObjectiveGridGroup[]; block: AnalysisBox; assignments: Record<string, string>; questions: Question[];
  optionCount: number; orientation: ObjectiveOptionOrientation; reverse: boolean;
}): { regions: LayoutRegion[]; error?: string } {
  const invalid = validateObjectiveGroups(groups, block, optionCount);
  if (invalid) return { regions: [], error: invalid };
  const byID = new Map(questions.map(question => [question.id, question]));
  const used = new Set<string>();
  const regions: LayoutRegion[] = [];
  for (const group of groups) {
    const question = byID.get(assignments[group.group_id]);
    if (!question || !isObjectiveQuestion(question)) return { regions: [], error: "请为每个题组关联本场正式选择题" };
    if (used.has(question.id)) return { regions: [], error: "同一题不能关联多个题组，请核对题号" };
    if (question.options?.length && question.options.length !== optionCount) return { regions: [], error: `第 ${question.question_no} 题的选项数与本大区不一致，请分区处理` };
    if (question.question_type === "true_false" && optionCount !== 2) return { regions: [], error: "判断题须配置两个选项" };
    used.add(question.id);
    const bbox = group.bbox;
    const coordinate = (value: number) => Number(Math.max(0, Math.min(1, value)).toFixed(8));
    // 服务返回的是页面坐标；OMR 消费的选项是本题裁图内坐标，保存时必须只在这里转换一次。
    regions.push({ id: crypto.randomUUID(), question_id: question.id, label: question.question_no, ...bbox,
      option_coordinate_space: "question_relative",
      option_regions: orderObjectiveOptions(group, orientation, reverse).map((option, index) => ({
        id: crypto.randomUUID(), label: String.fromCharCode(65 + index),
        x: coordinate((option.bbox.x - bbox.x) / bbox.width), y: coordinate((option.bbox.y - bbox.y) / bbox.height),
        width: coordinate(option.bbox.width / bbox.width), height: coordinate(option.bbox.height / bbox.height)
      })) });
  }
  return { regions };
}
