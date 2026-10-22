import type { TemplateLayout } from "../../../api/configuration";
import { templateLayoutReducer, type TemplateLayoutAction } from "./templateLayoutReducer";

export interface TemplateLayoutHistory { past: TemplateLayout[]; present: TemplateLayout; future: TemplateLayout[] }
export type TemplateHistoryAction = TemplateLayoutAction | { type: "undoEditor" } | { type: "redoEditor" }
  | { type: "resetEditor"; layout: TemplateLayout };

/** 候选应用、丢弃和人工修改与正式区域共用历史；切换已保存版本时清空历史，不能撤销到另一份底图。 */
export function templateLayoutHistoryReducer(state: TemplateLayoutHistory, action: TemplateHistoryAction): TemplateLayoutHistory {
  if (action.type === "resetEditor") return { past: [], present: action.layout, future: [] };
  if (action.type === "undoEditor") return !state.past.length ? state : { past: state.past.slice(0, -1),
    present: state.past[state.past.length - 1], future: [state.present, ...state.future].slice(0, 50) };
  if (action.type === "redoEditor") return !state.future.length ? state : { past: [...state.past, state.present].slice(-50),
    present: state.future[0], future: state.future.slice(1) };
  const next = templateLayoutReducer(state.present, action);
  return next === state.present ? state : { past: [...state.past, state.present].slice(-50), present: next, future: [] };
}
