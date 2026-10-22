import type { SetStateAction } from "react";
import type { LayoutRegion, OptionRegion, TemplateGradingGroup, TemplateLayout, TemplatePage } from "../../../api/configuration";
import { containsObjectiveBox, objectiveBoxesOverlap, validObjectiveBox } from "./objectiveGridGeometry";
import { normalizeIdentityRegionLabel, sameIdentityRegion } from "./identityRegionFields";
import { commitPageAnalysis, markRegionEdited, type PageAnalysisCommit } from "./regionLifecycle";
import { isFillGradingCandidate } from "./fillGradingGroups";

export type TemplateLayoutAction =
  | { type: "set"; value: SetStateAction<TemplateLayout> }
  | { type: "updateRegion"; regionId: string; patch: Partial<LayoutRegion> }
  | { type: "replaceQuestionRegion"; questionId: string; pageNo: number; region: LayoutRegion }
  | { type: "addRegion"; pageNo: number; region: LayoutRegion }
  | { type: "addIdentityRegion"; pageNo: number; region: LayoutRegion }
  | { type: "acceptSuggestedRegion"; pageNo: number; questionId: string; region: LayoutRegion }
  | { type: "acceptSuggestedIdentity"; pageNo: number; region: LayoutRegion }
  | { type: "removeRegion"; regionId: string }
  | { type: "updateOption"; regionId: string; optionId: string; patch: Partial<OptionRegion> }
  | { type: "addOption"; regionId: string; option: OptionRegion }
  | { type: "removeOption"; regionId: string; optionId: string }
  | { type: "suggestRegions"; pages: Map<number, LayoutRegion[]>; preserveExisting?: boolean }
  | { type: "suggestIdentityRegions"; pages: Map<number, LayoutRegion[]> }
  | { type: "applyObjectiveGrid"; pageNo: number; regions: LayoutRegion[]; expectedLayout: TemplateLayout }
  | { type: "setOMRProfile"; mode: "manual_only" | "template_difference" }
  | { type: "setFillGradingGroup"; pageNo: number; mode: "shared" | "individual"; groupId: string;
    bbox: { x: number; y: number; width: number; height: number }; questionIds: string[] }
  | { type: "setFillGradingArea"; pageNo: number; group: TemplateGradingGroup; candidateIds?: string[]; expectedLayout?: TemplateLayout }
  | { type: "removeFillGradingArea"; pageNo: number; groupId: string }
  | { type: "commitAnalysisPages"; commit: PageAnalysisCommit; expectedLayout?: TemplateLayout }
  | { type: "discardCandidate"; candidateId: string }
  | { type: "confirmRegion"; regionId: string }
  | { type: "confirmPageRegions"; pageNo: number };

type PageAction = Exclude<TemplateLayoutAction, { type: "set" | "setOMRProfile" | "applyObjectiveGrid" | "commitAnalysisPages" | "discardCandidate" | "setFillGradingGroup" | "setFillGradingArea" | "removeFillGradingArea" }>;

function updateMatchingRegion(page: TemplatePage, regionId: string, update: (region: LayoutRegion) => LayoutRegion): TemplatePage {
  if (!page.question_regions.some((region) => region.id === regionId)) return page;
  return { ...page, question_regions: page.question_regions.map((region) => region.id === regionId ? update(region) : region) };
}

function replaceQuestionRegion(page: TemplatePage, action: Extract<PageAction, { type: "replaceQuestionRegion" }>): TemplatePage {
  if (page.page_no !== action.pageNo && !page.question_regions.some((region) => region.question_id === action.questionId)) return page;
  // 拖到其他页时同时移除原页同题区域，避免同一题被两个区域重复覆盖。
  const regions = page.question_regions.filter((region) => region.question_id !== action.questionId);
  if (page.page_no === action.pageNo) regions.push(markRegionEdited(action.region));
  return { ...page, question_regions: regions };
}

function applySuggestions(page: TemplatePage, pages: Map<number, LayoutRegion[]>): TemplatePage {
  // 同题建议可能移到另一页；整次建议命中的题先跨页移除旧区域，未命中的人工标注保留。
  const suggestions = pages.get(page.page_no) ?? [];
  const matchedIDs = new Set([...pages.values()].flatMap(regions => regions.map(region => region.question_id)));
  if (!suggestions.length && !page.question_regions.some(region => region.question_id && matchedIDs.has(region.question_id))) return page;
  return { ...page, question_regions: page.question_regions
    .filter((region) => !region.question_id || !matchedIDs.has(region.question_id))
    .concat(suggestions) };
}

function applyPageAction(page: TemplatePage, action: PageAction): TemplatePage {
  switch (action.type) {
    case "updateRegion": {
      if (page.identity_regions.some(region => region.id === action.regionId)) return {
        ...page, identity_regions: page.identity_regions.map(region => region.id === action.regionId
          ? { ...markRegionEdited(region), ...action.patch, origin: region.origin ?? "human", review_state: "edited", id: region.id, label: normalizeIdentityRegionLabel(action.patch.label ?? region.label),
            question_id: undefined, option_regions: undefined, option_coordinate_space: undefined } : region)
      };
      return updateMatchingRegion(page, action.regionId, (region) => ({ ...markRegionEdited(region), ...action.patch,
        origin: region.origin ?? "human", review_state: "edited" }));
    }
    case "replaceQuestionRegion": return replaceQuestionRegion(page, action);
    case "addRegion": return page.page_no === action.pageNo
      ? { ...page, question_regions: [...page.question_regions, markRegionEdited(action.region)] } : page;
    case "addIdentityRegion": return page.page_no === action.pageNo && validObjectiveBox(action.region)
      ? { ...page, identity_regions: [...page.identity_regions, { ...markRegionEdited(action.region),
        label: normalizeIdentityRegionLabel(action.region.label), question_id: undefined, option_regions: undefined,
        option_coordinate_space: undefined }] } : page;
    case "acceptSuggestedRegion": return page.page_no === action.pageNo
      ? { ...page, question_regions: [...page.question_regions, markRegionEdited(action.region)] } : page;
    case "acceptSuggestedIdentity": return page.page_no === action.pageNo
      ? { ...page, identity_regions: [...page.identity_regions, { ...markRegionEdited(action.region),
        label: normalizeIdentityRegionLabel(action.region.label), question_id: undefined, option_regions: undefined, option_coordinate_space: undefined }] } : page;
    case "confirmRegion": {
      const confirm = (region: LayoutRegion) => region.id === action.regionId ? { ...region,
        origin: region.origin ?? "human", review_state: "confirmed" as const } : region;
      return { ...page, registration_marks: page.registration_marks.map(confirm),
        question_regions: page.question_regions.map(confirm), identity_regions: page.identity_regions.map(confirm) };
    }
    case "confirmPageRegions": {
      if (page.page_no !== action.pageNo) return page;
      const confirm = (region: LayoutRegion) => ({ ...region, origin: region.origin ?? "human", review_state: "confirmed" as const });
      return { ...page, registration_marks: page.registration_marks.map(confirm),
        question_regions: page.question_regions.map(confirm), identity_regions: page.identity_regions.map(confirm) };
    }
    case "removeRegion": {
      if (!page.question_regions.some((region) => region.id === action.regionId)
        && !page.identity_regions.some(region => region.id === action.regionId)) return page;
      return { ...page, question_regions: page.question_regions.filter((region) => region.id !== action.regionId),
        identity_regions: page.identity_regions.filter(region => region.id !== action.regionId) };
    }
    case "updateOption": return updateMatchingRegion(page, action.regionId, (region) => ({
      ...markRegionEdited(region), option_regions: (region.option_regions ?? []).map((option) => option.id === action.optionId ? { ...option, ...action.patch } : option)
    }));
    case "addOption": return updateMatchingRegion(page, action.regionId, (region) => ({
      ...markRegionEdited(region), option_regions: [...(region.option_regions ?? []), action.option]
    }));
    case "removeOption": return updateMatchingRegion(page, action.regionId, (region) => ({
      ...markRegionEdited(region), option_regions: (region.option_regions ?? []).filter((option) => option.id !== action.optionId)
    }));
    case "suggestRegions": return applySuggestions(page, action.pages);
    case "suggestIdentityRegions": {
      const existing = [...page.identity_regions];
      // 模型返回时草稿可能已新增或挪动身份框，按最新字段与几何去重；重试不覆盖人工结果。
      for (const region of action.pages.get(page.page_no) ?? []) {
        const candidate = { ...region, label: normalizeIdentityRegionLabel(region.label), question_id: undefined,
          option_regions: undefined, option_coordinate_space: undefined };
        if (validObjectiveBox(candidate) && !existing.some(item => sameIdentityRegion(item, candidate))) existing.push(candidate);
      }
      return existing.length === page.identity_regions.length ? page : { ...page, identity_regions: existing };
    }
  }
}

export function templateLayoutReducer(layout: TemplateLayout, action: TemplateLayoutAction): TemplateLayout {
  if (action.type === "set") return typeof action.value === "function" ? action.value(layout) : action.value;
  if (action.type === "removeFillGradingArea") {
    const { pageNo, groupId } = action;
    const target = layout.pages.find(page => page.page_no === pageNo);
    if (!target?.grading_groups?.some(group => group.group_id === groupId)) return layout;
    return { ...layout, pages: layout.pages.map(page => page !== target ? page : { ...page,
      grading_groups: page.grading_groups?.filter(group => group.group_id !== groupId) }) };
  }
  if (action.type === "setFillGradingArea") {
    if (action.expectedLayout && action.expectedLayout !== layout) return layout;
    const { pageNo, group, candidateIds } = action;
    const target = layout.pages.find(page => page.page_no === pageNo), ids = new Set(group.question_ids);
    if (!target || !group.group_id.trim() || ids.size < 2 || ids.size !== group.question_ids.length || !validObjectiveBox(group.bbox)) return layout;
    const members = target.question_regions.filter(region => region.question_id && ids.has(region.question_id));
    if (members.some(region => region.option_regions?.length || !containsObjectiveBox(group.bbox, region))
      || [...target.identity_regions, ...target.question_regions.filter(region => !members.includes(region))].some(region => objectiveBoxesOverlap(group.bbox, region))
      || layout.pages.some(page => page !== target && (page.question_regions.some(region => region.question_id && ids.has(region.question_id))
        || page.grading_groups?.some(other => other.question_ids.some(id => ids.has(id)))))
      || target.grading_groups?.some(other => other.group_id !== group.group_id && !other.question_ids.some(id => ids.has(id))
        && objectiveBoxesOverlap(group.bbox, other.bbox))) return layout;
    const pending = layout.editor_state?.candidates.filter(candidate => candidate.page_no === pageNo && candidate.region.review_state !== "discarded") ?? [];
    if (pending.some(candidate => ["identity", "objective_group"].includes(candidate.role) && objectiveBoxesOverlap(group.bbox, candidate.region))) return layout;
    const replacedGroups = new Set(members.flatMap(region => region.grading_group_id ? [region.grading_group_id] : []));
    // 独立整区只保存一次完整外框；原有单题 ROI 原样保留，缺失成员不复制或均分大框。
    return { ...layout, pages: layout.pages.map(page => page !== target ? page : { ...page,
      grading_groups: [...(page.grading_groups ?? []).filter(other => other.group_id !== group.group_id && !other.question_ids.some(id => ids.has(id))),
        { ...group, question_ids: [...group.question_ids], bbox: { ...group.bbox } }],
      question_regions: page.question_regions.map(region => region.grading_group_id && (replacedGroups.has(region.grading_group_id) || region.grading_group_id === group.group_id)
        ? (({ grading_group_id: _id, grading_bbox: _box, ...individual }) => individual)(region) : region) }),
      ...(layout.editor_state && candidateIds?.length ? { editor_state: { ...layout.editor_state,
        candidates: layout.editor_state.candidates.map(candidate => candidateIds.includes(candidate.id) && candidate.page_no === pageNo
          && isFillGradingCandidate(candidate, group.question_ids) && containsObjectiveBox(group.bbox, candidate.region)
          ? { ...candidate, region: { ...candidate.region, review_state: "discarded" as const } } : candidate) } } : {}) };
  }
  if (action.type === "setFillGradingGroup") {
    const { pageNo, mode, groupId, bbox, questionIds } = action;
    const target = layout.pages.find(page => page.page_no === pageNo);
    if (mode === "individual") {
      if (!target || !groupId || !target.question_regions.some(region => region.grading_group_id === groupId)) return layout;
      return { ...layout, pages: layout.pages.map(page => page !== target ? page : { ...page,
        question_regions: page.question_regions.map(region => region.grading_group_id !== groupId ? region
          : (({ grading_group_id: _groupId, grading_bbox: _box, ...individual }) => individual)(region)) }) };
    }
    const ids = new Set(questionIds);
    const members = target?.question_regions.filter(region => region.question_id && ids.has(region.question_id)) ?? [];
    if (!target || members.length < 2 || members.length !== ids.size || !groupId || !validObjectiveBox(bbox)
      || members.some(region => region.option_regions?.length || !containsObjectiveBox(bbox, region))
      || [...target.identity_regions, ...target.question_regions.filter(region => !members.includes(region))]
        .some(region => objectiveBoxesOverlap(bbox, region))) return layout;
    // 批阅方式不代表教师确认了识别类别，保留原语义与审核状态，防止切换后意外放行异常。
    const replacedGroups = new Set(members.flatMap(region => region.grading_group_id ? [region.grading_group_id] : []));
    return { ...layout, pages: layout.pages.map(page => page !== target ? page : { ...page,
      question_regions: page.question_regions.map(region => members.includes(region)
        ? { ...region, grading_group_id: groupId, grading_bbox: { ...bbox } }
        : region.grading_group_id && replacedGroups.has(region.grading_group_id)
          ? (({ grading_group_id: _groupId, grading_bbox: _box, ...individual }) => individual)(region) : region) }) };
  }
  if (action.type === "commitAnalysisPages") {
    // 等待识别时的编辑、删除和撤销都会改变草稿引用；旧响应不能覆盖这些后发生的操作。
    if (action.expectedLayout && action.expectedLayout !== layout) return layout;
    return commitPageAnalysis(layout, action.commit);
  }
  if (action.type === "discardCandidate") {
    const { candidateId } = action;
    return !layout.editor_state ? layout : { ...layout, editor_state: {
      ...layout.editor_state, candidates: layout.editor_state.candidates.map(candidate => candidate.id === candidateId
        ? { ...candidate, region: { ...candidate.region, review_state: "discarded" } } : candidate) } };
  }
  if (action.type === "applyObjectiveGrid") {
    const { pageNo, regions, expectedLayout } = action;
    // 网格的多个题区和选项必须一起落入同一草稿；覆盖确认晚于其他编辑时整批拒绝，不能只改一半。
    if (layout !== expectedLayout || !layout.pages.some(page => page.page_no === pageNo) || !regions.length) return layout;
    const ids = new Set<string>();
    for (const region of regions) {
      if (!region.question_id || ids.has(region.question_id) || !validObjectiveBox(region) || region.option_coordinate_space !== "question_relative"
        || !region.option_regions || region.option_regions.length < 2 || region.option_regions.length > 12
        || region.option_regions.some(option => !validObjectiveBox(option))
        || new Set(region.option_regions.map(option => option.label)).size !== region.option_regions.length) return layout;
      ids.add(region.question_id);
    }
    const target = layout.pages.find(page => page.page_no === pageNo)!;
    if (regions.some((region, index) => regions.slice(index + 1).some(other => objectiveBoxesOverlap(region, other))
      || [...target.identity_regions, ...target.question_regions.filter(other => !other.question_id || !ids.has(other.question_id))]
        .some(other => objectiveBoxesOverlap(region, other)))) return layout;
    return { ...layout, pages: layout.pages.map(page => {
      const retained = page.question_regions.filter(region => !region.question_id || !ids.has(region.question_id));
      if (page.page_no === pageNo) return { ...page, question_regions: [...retained, ...regions.map(markRegionEdited)] };
      return retained.length === page.question_regions.length ? page : { ...page, question_regions: retained };
    }) };
  }
  if (action.type === "setOMRProfile") return {
    ...layout, omr_profile: action.mode === "template_difference"
      ? { mode: action.mode, version: "opencv-template-difference-bubble-v1" }
      : { mode: action.mode, version: "opencv-fill-v1" }
  };
  // 自动结果和待关联框只补空缺；即使异步结果或双击晚于人工操作，也不能跨页替换原题区域。
  if (action.type === "acceptSuggestedRegion") {
    const { questionId, pageNo, region: accepted } = action;
    if (!questionId || action.region.question_id !== questionId || !validObjectiveBox(accepted)
      || !layout.pages.some(page => page.page_no === pageNo)
      || layout.pages.some(page => page.question_regions.some(region => region.question_id === questionId))) return layout;
    if (layout.editor_state) layout = { ...layout, editor_state: { ...layout.editor_state,
      candidates: layout.editor_state.candidates.filter(candidate => candidate.id !== accepted.id) } };
  }
  if (action.type === "acceptSuggestedIdentity") {
    const { pageNo, region: accepted } = action;
    const target = layout.pages.find(page => page.page_no === pageNo);
    if (!target || !validObjectiveBox(accepted) || target.identity_regions.some(region =>
      normalizeIdentityRegionLabel(region.label) === normalizeIdentityRegionLabel(accepted.label))) return layout;
    if (layout.editor_state) layout = { ...layout, editor_state: { ...layout.editor_state,
      candidates: layout.editor_state.candidates.filter(candidate => candidate.id !== accepted.id) } };
  }
  if (action.type === "removeRegion" && layout.editor_state) {
    const { regionId } = action;
    const page = layout.pages.find(item => [...item.question_regions, ...item.identity_regions].some(region => region.id === regionId));
    const removed = [...(page?.question_regions ?? []), ...(page?.identity_regions ?? [])].find(region => region.id === regionId);
    if (page && removed) layout = { ...layout, editor_state: { ...layout.editor_state,
      candidates: [...layout.editor_state.candidates, { id: removed.id, page_no: page.page_no, role: "uncertain",
        region: { ...removed, review_state: "discarded" } }] } };
  }
  if (action.type === "suggestRegions" && action.preserveExisting) {
    const occupied = new Set(layout.pages.flatMap(page => page.question_regions.map(region => region.question_id)).filter(Boolean));
    const pages = new Map([...action.pages].map(([pageNo, regions]) => [pageNo, regions.filter(region => {
      if (!region.question_id || occupied.has(region.question_id)) return false;
      occupied.add(region.question_id);
      return true;
    })] as const));
    action = { ...action, pages };
  }
  return { ...layout, pages: layout.pages.map((page) => applyPageAction(page, action)) };
}
