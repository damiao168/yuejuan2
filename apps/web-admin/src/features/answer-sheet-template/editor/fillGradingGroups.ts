import type { TemplateFillGradingGroup } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion, TemplateEditorCandidate, TemplateGradingGroup, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { containsObjectiveBox, objectiveBoxesOverlap, validObjectiveBox } from "./objectiveGridGeometry";

const normalize = (label: string) => label.replace(/\s+/g, "").toLowerCase();

/** 整区确认只结案本组答题候选；显式题号、选项或身份反证仍需单独核对，不能仅凭框内位置丢弃。 */
export function isFillGradingCandidate(candidate: TemplateEditorCandidate, questionIds: string[], questionNos?: string[]) {
  if (!["answer", "uncertain"].includes(candidate.role) || candidate.region.option_regions?.length
    || candidate.region.reason_codes?.some(code => /IDENTITY|BARCODE|OBJECTIVE|OPTION|FORBIDDEN|WARNING_CATEGORY|NON_ANSWER/.test(code))
    || candidate.region.question_id && !questionIds.includes(candidate.region.question_id)) return false;
  const questionNo = candidate.question_no ?? candidate.region.label?.match(/^填空第 (.+) 题 · 待核对$/u)?.[1];
  return !questionNo || !questionNos || questionNos.some(label => normalize(label) === normalize(questionNo));
}

/** 人工整区以正式成员和完整外框为准；缺少单题 ROI 允许保留为空，但已有位置不能跨页或落在框外。 */
export function validManualFillGradingGroup(group: TemplateGradingGroup, layout: TemplateLayout, pageNo: number, questions: Question[]) {
  const page = layout.pages.find(item => item.page_no === pageNo);
  const ids = new Set(group.question_ids);
  if (!page || !group.group_id.trim() || ids.size < 2 || ids.size !== group.question_ids.length || !validObjectiveBox(group.bbox)
    || group.question_ids.some(id => questions.filter(question => question.id === id && question.question_type === "fill_blank").length !== 1)) return false;
  const existing = layout.pages.flatMap(item => item.question_regions.filter(region => region.question_id && ids.has(region.question_id))
    .map(region => ({ region, pageNo: item.page_no })));
  if (new Set(existing.map(item => item.region.question_id)).size !== existing.length
    || existing.some(item => item.pageNo !== pageNo || !containsObjectiveBox(group.bbox, item.region) || item.region.option_regions?.length)) return false;
  return ![...page.identity_regions, ...page.question_regions.filter(region => !region.question_id || !ids.has(region.question_id))]
    .some(region => objectiveBoxesOverlap(group.bbox, region))
    && !layout.pages.some(item => item.page_no !== pageNo && item.grading_groups?.some(other => other.question_ids.some(id => ids.has(id))))
    && !(page.grading_groups ?? []).some(other => other.group_id !== group.group_id && !other.question_ids.some(id => ids.has(id))
      && objectiveBoxesOverlap(group.bbox, other.bbox));
}

/** 共框只使用独立实测的完整外框；题号、单题 ROI、个人信息的冲突不能靠大区并集消除。 */
export function fillGradingGroupMembers(group: TemplateFillGradingGroup, regions: LayoutRegion[], identities: LayoutRegion[], questions: Question[]) {
  if (!group.group_id?.trim() || !validObjectiveBox(group.bbox) || group.question_labels.length < 2
    || new Set(group.question_labels.map(normalize)).size !== group.question_labels.length) return;
  const members: LayoutRegion[] = [];
  for (const label of group.question_labels) {
    const formal = questions.filter(question => normalize(question.question_no) === normalize(label));
    if (formal.length !== 1 || formal[0].question_type !== "fill_blank") return;
    const matching = regions.filter(region => region.question_id === formal[0].id);
    if (matching.length !== 1 || !containsObjectiveBox(group.bbox, matching[0])) return;
    members.push(matching[0]);
  }
  if (identities.some(region => objectiveBoxesOverlap(group.bbox, region))
    || regions.some(region => !members.includes(region) && objectiveBoxesOverlap(group.bbox, region))) return;
  return members;
}

/** 自动共框不改原语义状态；只有组内所有题目已对应且像素外框有依据时才能追加批阅配置。 */
export function applyAutomaticFillGradingGroups(groups: TemplateFillGradingGroup[], regions: LayoutRegion[], identities: LayoutRegion[], questions: Question[]) {
  const assignments = new Map<string, TemplateFillGradingGroup>();
  for (const group of groups) {
    if (!group.reason_codes.includes("LOCAL_FILL_SECTION_VERIFIED")) continue;
    const members = fillGradingGroupMembers(group, regions, identities, questions);
    if (!members || members.some(region => region.semantic_check !== "supported")
      || groups.some(other => other !== group && other.question_labels.some(label => group.question_labels.map(normalize).includes(normalize(label))))) continue;
    for (const member of members) assignments.set(member.id, group);
  }
  return regions.map(region => {
    const group = assignments.get(region.id);
    return group ? { ...region, grading_group_id: group.group_id, grading_bbox: { ...group.bbox } } : region;
  });
}

/** 人工题区优先后可能使自动组缺员；仅撤去失效自动批阅配置，不能丢掉有效的单题 ROI。 */
export function clearInvalidAutomaticFillGroups(regions: LayoutRegion[], identities: LayoutRegion[]): LayoutRegion[] {
  return regions.map(region => {
    if (!region.grading_group_id || region.review_state !== "suggested" || !["model", "local_detector"].includes(region.origin ?? "")) return region;
    const members = regions.filter(other => other.grading_group_id === region.grading_group_id);
    const box = region.grading_bbox;
    const valid = box && validObjectiveBox(box) && members.length >= 2 && members.every(member => member.grading_bbox
      && ["x", "y", "width", "height"].every(key => member.grading_bbox![key as keyof typeof box] === box[key as keyof typeof box])
      && containsObjectiveBox(box, member))
      && ![...identities, ...regions.filter(other => !members.includes(other))].some(other => objectiveBoxesOverlap(box, other));
    if (valid) return region;
    const { grading_group_id: _groupId, grading_bbox: _box, ...individual } = region;
    return individual;
  });
}
