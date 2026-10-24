export type GradingShortcutIntent =
  | { type: "submit" }
  | { type: "set_score"; score: number }
  | { type: "toggle_criterion"; index: number }
  | { type: "focus_score" }
  | { type: "adopt_ai" }
  | { type: "toggle_bookmark" }
  | { type: "return_task" }
  | { type: "undo_score_change" }
  | { type: "zoom"; direction: -1 | 1 };

export interface GradingShortcutContext {
  key: string;
  code?: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
  repeat?: boolean;
  blocked?: boolean;
  defaultPrevented?: boolean;
  singleKeyShortcutsEnabled?: boolean;
  quickSubmit?: boolean;
  isInputTarget: boolean;
  hasTask: boolean;
  canEditDraft: boolean;
  canSubmit: boolean;
  canReturn: boolean;
  canToggleBookmark: boolean;
  canUndoScoreChange: boolean;
  hasAiSuggestion: boolean;
  maxScore: number;
  rubricPointCount: number;
}

function numericShortcutIntent(context: GradingShortcutContext, key: string): GradingShortcutIntent | null {
  const index = Number(key) - 1;
  // 有评分点时数字键只操作对应点，越界不能改成设置总分。
  if (context.rubricPointCount > 0) return index < context.rubricPointCount ? { type: "toggle_criterion", index } : null;
  const score = Number(key);
  return score <= context.maxScore ? { type: "set_score", score } : null;
}

function draftShortcutIntent(context: GradingShortcutContext, key: string): GradingShortcutIntent | null {
  if (/^[1-9]$/.test(key)) return numericShortcutIntent(context, key);
  switch (key) {
    case "g":
      return { type: "focus_score" };
    case "a":
      return context.hasAiSuggestion ? { type: "adopt_ai" } : null;
    case "r":
      return context.canReturn ? { type: "return_task" } : null;
    case "z":
      return context.canUndoScoreChange ? { type: "undo_score_change" } : null;
    default:
      return null;
  }
}

/**
 * 将浏览器键盘事件映射为工作台操作，不修改 React 状态。
 * 实际操作及其确认和错误处理由组件负责。
 */
export function gradingShortcutIntent(context: GradingShortcutContext): GradingShortcutIntent | null {
  if (context.defaultPrevented || context.isInputTarget || context.isComposing || context.repeat || context.blocked || context.altKey) return null;

  const key = context.key.toLowerCase();
  if (key === "enter") {
    return context.canSubmit && (context.ctrlKey || context.metaKey || context.quickSubmit) ? { type: "submit" } : null;
  }

  // 保留 Ctrl+Enter 与旧工作台的兼容性，
  // 不接管 Ctrl+A 等浏览器快捷键。
  if (context.ctrlKey || context.metaKey) return null;

  // 关闭单字符键不影响用户另行开启的 Enter 提交，也不影响 Ctrl / ⌘ + Enter。
  if (context.singleKeyShortcutsEnabled === false) return null;

  // 缩放只要求存在任务，不能因草稿不可编辑而一起禁用。
  if (key === "+" || context.code === "NumpadAdd") {
    return context.hasTask ? { type: "zoom", direction: 1 } : null;
  }
  if (key === "-" || context.code === "NumpadSubtract") {
    return context.hasTask ? { type: "zoom", direction: -1 } : null;
  }

  // 个人标记有独立的资格与版本校验；取消既有标记不依赖当前任务还能否写分或退回。
  if (key === "f") return context.hasTask && context.canToggleBookmark ? { type: "toggle_bookmark" } : null;

  if (!context.hasTask || !context.canEditDraft) return null;

  return draftShortcutIntent(context, key);
}
