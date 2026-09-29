import { useEffect } from "react";
import { isInputTarget } from "../gradingWorkbench.model";
import { gradingShortcutIntent } from "../keyboardShortcuts";

export interface UseGradingKeyboardOptions {
  hasTask: boolean;
  canEditDraft: boolean;
  canSubmit: boolean;
  canReturn: boolean;
  canUndoScoreChange: boolean;
  hasAiSuggestion: boolean;
  maxScore: number;
  rubricPointCount: number;
  quickSubmit?: boolean;
  onSubmit: () => void;
  onSetScore: (score: number) => void;
  onToggleCriterion: (index: number) => void;
  onAdoptAi: () => void;
  onFlagException: () => void;
  onReturnTask: () => void;
  onUndoScoreChange: () => void;
  onZoom: (direction: -1 | 1) => void;
}

export function useGradingKeyboard(options: UseGradingKeyboardOptions) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      // 输入法组合、弹层和控件自身的 Enter 优先，避免打字或确认对话框时误提交评分。
      const intent = gradingShortcutIntent({
        key: event.key,
        code: event.code,
        ctrlKey: event.ctrlKey,
        metaKey: event.metaKey,
        altKey: event.altKey,
        isComposing: event.isComposing || event.keyCode === 229,
        repeat: event.repeat,
        blocked: Boolean(document.querySelector('.ant-modal-wrap:not([style*="display: none"]), .ant-drawer-open, .ant-popover:not(.ant-popover-hidden)')),
        quickSubmit: options.quickSubmit,
        isInputTarget: isInputTarget(event.target) || (event.key === "Enter" && !event.ctrlKey && !event.metaKey && event.target instanceof Element && Boolean(event.target.closest('button, a, [role="button"], [role="checkbox"], [role="combobox"]'))),
        hasTask: options.hasTask,
        canEditDraft: options.canEditDraft,
        canSubmit: options.canSubmit,
        canReturn: options.canReturn,
        canUndoScoreChange: options.canUndoScoreChange,
        hasAiSuggestion: options.hasAiSuggestion,
        maxScore: options.maxScore,
        rubricPointCount: options.rubricPointCount
      });
      if (!intent) return;
      event.preventDefault();
      switch (intent.type) {
        case "submit":
          options.onSubmit();
          break;
        case "set_score":
          options.onSetScore(intent.score);
          break;
        case "toggle_criterion":
          options.onToggleCriterion(intent.index);
          break;
        case "adopt_ai":
          options.onAdoptAi();
          break;
        case "flag_exception":
          options.onFlagException();
          break;
        case "return_task":
          options.onReturnTask();
          break;
        case "undo_score_change":
          options.onUndoScoreChange();
          break;
        case "zoom":
          options.onZoom(intent.direction);
          break;
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [options]);
}
