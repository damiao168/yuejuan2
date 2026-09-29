import type { SetStateAction } from "react";
import type { LayoutRegion, OptionRegion, TemplateLayout } from "../../../api/configuration";

export type TemplateLayoutAction =
  | { type: "set"; value: SetStateAction<TemplateLayout> }
  | { type: "updateRegion"; regionId: string; patch: Partial<LayoutRegion> }
  | { type: "replaceQuestionRegion"; questionId: string; pageNo: number; region: LayoutRegion }
  | { type: "addRegion"; pageNo: number; region: LayoutRegion }
  | { type: "removeRegion"; regionId: string }
  | { type: "updateOption"; regionId: string; optionId: string; patch: Partial<OptionRegion> }
  | { type: "addOption"; regionId: string; option: OptionRegion }
  | { type: "removeOption"; regionId: string; optionId: string }
  | { type: "suggestRegions"; pages: Map<number, LayoutRegion[]> }
  | { type: "setOMRProfile"; mode: "manual_only" | "template_difference" };

export function templateLayoutReducer(layout: TemplateLayout, action: TemplateLayoutAction): TemplateLayout {
  if (action.type === "set") return typeof action.value === "function" ? action.value(layout) : action.value;
  if (action.type === "setOMRProfile") return {
    ...layout, omr_profile: action.mode === "template_difference"
      ? { mode: action.mode, version: "opencv-template-difference-bubble-v1" }
      : { mode: action.mode, version: "opencv-fill-v1" }
  };
  return { ...layout, pages: layout.pages.map((page) => {
    switch (action.type) {
      case "updateRegion": return page.question_regions.some((region) => region.id === action.regionId)
        ? { ...page, question_regions: page.question_regions.map((region) =>
          region.id === action.regionId ? { ...region, ...action.patch } : region) } : page;
      // 拖到其他页时同时移除原页同题区域，避免同一题被两个区域重复覆盖。
      case "replaceQuestionRegion": return page.page_no === action.pageNo
        || page.question_regions.some((region) => region.question_id === action.questionId)
        ? { ...page, question_regions: page.question_regions
          .filter((region) => region.question_id !== action.questionId)
          .concat(page.page_no === action.pageNo ? [action.region] : []) } : page;
      case "addRegion": return page.page_no === action.pageNo
        ? { ...page, question_regions: [...page.question_regions, action.region] } : page;
      case "removeRegion": return page.question_regions.some((region) => region.id === action.regionId)
        ? { ...page, question_regions: page.question_regions.filter((region) => region.id !== action.regionId) } : page;
      case "updateOption": return page.question_regions.some((region) => region.id === action.regionId)
        ? { ...page, question_regions: page.question_regions.map((region) =>
          region.id === action.regionId ? { ...region, option_regions: (region.option_regions ?? []).map((option) =>
            option.id === action.optionId ? { ...option, ...action.patch } : option) } : region) } : page;
      case "addOption": return page.question_regions.some((region) => region.id === action.regionId)
        ? { ...page, question_regions: page.question_regions.map((region) =>
          region.id === action.regionId ? { ...region, option_regions: [...(region.option_regions ?? []), action.option] } : region) } : page;
      case "removeOption": return page.question_regions.some((region) => region.id === action.regionId)
        ? { ...page, question_regions: page.question_regions.map((region) =>
          region.id === action.regionId ? { ...region, option_regions: (region.option_regions ?? []).filter((option) =>
            option.id !== action.optionId) } : region) } : page;
      case "suggestRegions": {
        // 识别建议只替换命中题目的区域，未命中的人工标注必须保留。
        const suggestions = action.pages.get(page.page_no) ?? [];
        if (!suggestions.length) return page;
        const matchedIDs = new Set(suggestions.map((region) => region.question_id));
        return { ...page, question_regions: page.question_regions
          .filter((region) => !region.question_id || !matchedIDs.has(region.question_id))
          .concat(suggestions) };
      }
    }
  }) };
}
