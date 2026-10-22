import { useRef, type Dispatch } from "react";
import type { AnalysisBox, TemplateFillGradingGroup } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion, TemplateGradingGroup, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { fillGradingGroupMembers, isFillGradingCandidate, validManualFillGradingGroup } from "./fillGradingGroups";
import { containsObjectiveBox, objectiveBoxesOverlap } from "./objectiveGridGeometry";
import type { TemplateLayoutAction } from "./templateLayoutReducer";

/** 切换批阅范围保留逐题位置；拆开后在当前来源会话保留实测组，允许再切回共框。 */
export function useFillGradingGroups({ scopeKey, layout, suggestions, selectedRegion, selectedQuestionId, pageNo, questions, readonly, dispatchLayout }: {
  scopeKey: string; layout: TemplateLayout; suggestions: Map<number, TemplateFillGradingGroup[]>;
  selectedRegion?: LayoutRegion; selectedQuestionId?: string; pageNo?: number;
  questions: Question[]; readonly: boolean; dispatchLayout: Dispatch<TemplateLayoutAction>;
}) {
  const remembered = useRef({ scope: scopeKey, groups: new Map<string, { pageNo: number; group: TemplateFillGradingGroup }>(),
    areas: new Map<string, { pageNo: number; group: TemplateGradingGroup }>(), preferred: new Map<string, string>() });
  if (remembered.current.scope !== scopeKey) remembered.current = { scope: scopeKey, groups: new Map(), areas: new Map(), preferred: new Map() };
  const keyFor = (pageNo: number, labels: string[]) => `${pageNo}:${[...labels].sort().join("|")}`;
  for (const page of layout.pages) {
    for (const group of page.grading_groups ?? []) remembered.current.areas.set(group.group_id, { pageNo: page.page_no, group });
    const grouped = new Map<string, LayoutRegion[]>();
    for (const region of page.question_regions) if (region.grading_group_id && region.grading_bbox) {
      grouped.set(region.grading_group_id, [...(grouped.get(region.grading_group_id) ?? []), region]);
    }
    for (const [groupId, members] of grouped) {
      const labels = members.flatMap(member => questions.find(question => question.id === member.question_id)?.question_no ?? []);
      const key = keyFor(page.page_no, labels);
      remembered.current.groups.set(key, { pageNo: page.page_no,
        group: { group_id: groupId, question_labels: labels, bbox: members[0].grading_bbox!, reason_codes: [] } });
      for (const member of members) if (member.question_id) remembered.current.preferred.set(member.question_id, key);
    }
  }
  for (const [pageNo, groups] of suggestions) for (const group of groups) {
    remembered.current.groups.set(keyFor(pageNo, group.question_labels), { pageNo, group });
  }
  const selectedQuestion = questions.find(question => question.id === (selectedRegion?.question_id ?? selectedQuestionId));
  const page = layout.pages.find(item => pageNo ? item.page_no === pageNo : item.question_regions.some(region => region.id === selectedRegion?.id))
    ?? (pageNo ? undefined : layout.pages[0]);
  const activeArea = page?.grading_groups?.find(group => group.question_ids.includes(selectedQuestion?.id ?? ""))
    ?? (!selectedQuestion || selectedQuestion.question_type !== "fill_blank" ? page?.grading_groups?.[0] : undefined);
  const area = activeArea ?? [...remembered.current.areas.values()].find(entry => entry.pageNo === page?.page_no
    && entry.group.question_ids.includes(selectedQuestion?.id ?? "") && validManualFillGradingGroup(entry.group, layout, entry.pageNo, questions))?.group;
  const choices = selectedQuestion?.question_type === "fill_blank" && page ? [...remembered.current.groups.values()]
    .filter(entry => entry.pageNo === page.page_no && entry.group.question_labels.includes(selectedQuestion.question_no))
    .flatMap(entry => {
      const members = fillGradingGroupMembers(entry.group, page.question_regions, page.identity_regions, questions);
      return members ? [{ ...entry, members }] : [];
    }) : [];
  const preferredKey = selectedQuestion ? remembered.current.preferred.get(selectedQuestion.id) : undefined;
  const selected = choices.find(entry => keyFor(entry.pageNo, entry.group.question_labels) === preferredKey)
    ?? (choices.length === 1 ? choices[0] : undefined);
  const currentMembers = selectedRegion?.grading_group_id ? page?.question_regions
    .filter(region => region.grading_group_id === selectedRegion.grading_group_id) ?? [] : [];
  const fillQuestions = questions.filter(question => question.question_type === "fill_blank" && !layout.pages.some(other => other.page_no !== page?.page_no
    && (other.question_regions.some(region => region.question_id === question.id) || other.grading_groups?.some(group => group.question_ids.includes(question.id)))));
  const memberIds = area?.question_ids ?? selected?.members.map(member => member.question_id!)
    ?? (currentMembers.length ? currentMembers.flatMap(member => member.question_id ?? []) : selectedQuestion?.question_type === "fill_blank" ? [selectedQuestion.id] : []);
  const fillGradingGroup = page && fillQuestions.length ? {
    mode: activeArea || selectedRegion?.grading_group_id ? "shared" as const : "individual" as const,
    questionLabels: memberIds.flatMap(id => questions.find(question => question.id === id)?.question_no ?? []), canShare: Boolean(area || selected),
    bbox: area?.bbox ?? selectedRegion?.grading_bbox ?? selected?.group.bbox,
    questionIds: memberIds,
    missingQuestionLabels: memberIds.filter(id => !page.question_regions.some(region => region.question_id === id))
      .flatMap(id => questions.find(question => question.id === id)?.question_no ?? []),
    options: fillQuestions.map(question => ({ value: question.id, label: `第 ${question.question_no} 题`,
      bbox: page.question_regions.find(region => region.question_id === question.id) }))
  } : undefined;
  const setFillGradingMode = (mode: "shared" | "individual") => {
    if (readonly || !page) return;
    if (mode === "individual" && activeArea) {
      dispatchLayout({ type: "removeFillGradingArea", pageNo: page.page_no, groupId: activeArea.group_id }); return;
    }
    if (mode === "shared" && area && validManualFillGradingGroup(area, layout, page.page_no, questions)) {
      dispatchLayout({ type: "setFillGradingArea", pageNo: page.page_no, group: area }); return;
    }
    if (!selectedRegion) return;
    if (mode === "individual" && selectedRegion.grading_group_id && selectedRegion.grading_bbox) {
      dispatchLayout({ type: "setFillGradingGroup", pageNo: page.page_no, mode,
        groupId: selectedRegion.grading_group_id, bbox: selectedRegion.grading_bbox, questionIds: [] });
      return;
    }
    if (!selected) return;
    dispatchLayout({ type: "setFillGradingGroup", pageNo: page.page_no, mode,
      groupId: selected.group.group_id, bbox: selected.group.bbox, questionIds: selected.members.map(member => member.question_id!) });
  };
  const setFillGradingRegion = (questionIds: string[], bbox: AnalysisBox) => {
    if (readonly || !page) return false;
    const group: TemplateGradingGroup = { group_id: activeArea?.group_id ?? selectedRegion?.grading_group_id ?? `manual-fill-${crypto.randomUUID()}`,
      question_ids: [...questionIds], bbox: { ...bbox } };
    if (!validManualFillGradingGroup(group, layout, page.page_no, questions)) return false;
    const pending = layout.editor_state?.candidates.filter(candidate => candidate.page_no === page.page_no && candidate.region.review_state !== "discarded") ?? [];
    if (pending.some(candidate => ["identity", "objective_group"].includes(candidate.role) && objectiveBoxesOverlap(bbox, candidate.region))) return false;
    // 教师确认的是本组成员与外框；其他题号、身份或选择题线索仍保留待核对，原单题语义状态保持不变。
    const questionNos = questions.filter(question => questionIds.includes(question.id)).map(question => question.question_no);
    const candidateIds = layout.editor_state?.candidates.filter(candidate => candidate.page_no === page.page_no
      && isFillGradingCandidate(candidate, questionIds, questionNos) && containsObjectiveBox(bbox, candidate.region)).map(candidate => candidate.id);
    dispatchLayout({ type: "setFillGradingArea", pageNo: page.page_no, group, candidateIds, expectedLayout: layout });
    return true;
  };
  const resetFillGradingGroups = () => {
    // 清空题区与切回逐题含义不同；清空后不能再从本会话记忆恢复已经删除的人工整区。
    remembered.current = { scope: scopeKey, groups: new Map(), areas: new Map(), preferred: new Map() };
  };
  return { fillGradingGroup, setFillGradingMode, setFillGradingRegion, resetFillGradingGroups };
}
