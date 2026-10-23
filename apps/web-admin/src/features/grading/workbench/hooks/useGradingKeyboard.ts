import { useEffect, useRef } from "react";
import { isInputTarget } from "../gradingWorkbench.model";
import { gradingShortcutIntent } from "../keyboardShortcuts";
import { useSingleKeyShortcuts } from "../shortcutPreference";

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
  const { enabled: singleKeyShortcutsEnabled } = useSingleKeyShortcuts();
  // options 更新会重绑监听；输入法状态保留在 ref 中，不能把重渲染当成组合结束。
  const composition = useRef({ active: false, awaitingKeyUp: false });
  useEffect(() => {
    const onCompositionStart = () => { composition.current = { active: true, awaitingKeyUp: false }; };
    // 部分输入法先结束组合再发送确认 Enter；松键前仍忽略提交，避免确认候选词时误交评分。
    const onCompositionEnd = () => { composition.current = { active: false, awaitingKeyUp: true }; };
    const onKeyUp = () => { composition.current.awaitingKeyUp = false; };
    const onPointerUp = () => { if (!composition.current.active) composition.current.awaitingKeyUp = false; };
    const onBlur = () => { composition.current = { active: false, awaitingKeyUp: false }; };
    const onKeyDown = (event: KeyboardEvent) => {
      // 系统候选窗鼠标确认可能没有页面 keyup；新的非 Enter 按键结束这次确认保护。
      if (event.key !== "Enter") composition.current.awaitingKeyUp = false;
      // 填分或开启连续阅卷后，焦点仍在该控件；只为这两种显式标记开放 Enter，文字和其它控件保留原行为。
      const submitInputEnter = event.key === "Enter" && event.target instanceof HTMLInputElement
        && (event.target.dataset.gradingScoreInput === "true" || (event.target.dataset.gradingQuickSubmit === "true" && event.target.checked))
        && !event.target.disabled && !event.target.readOnly;
      // 输入法组合、弹层和控件已消费的事件仍优先，不能因分数框例外而误交评分。
      const intent = gradingShortcutIntent({
        key: event.key,
        code: event.code,
        ctrlKey: event.ctrlKey,
        metaKey: event.metaKey,
        altKey: event.altKey,
        isComposing: event.isComposing || composition.current.active || (event.key === "Enter" && composition.current.awaitingKeyUp),
        repeat: event.repeat,
        defaultPrevented: event.defaultPrevented,
        singleKeyShortcutsEnabled,
        blocked: Boolean(document.querySelector('.ant-modal-wrap:not([style*="display: none"]), .ant-drawer-open, .ant-popover:not(.ant-popover-hidden)')),
        quickSubmit: options.quickSubmit,
        isInputTarget: !submitInputEnter && (isInputTarget(event.target) || (event.key === "Enter" && !event.ctrlKey && !event.metaKey && event.target instanceof Element && Boolean(event.target.closest('button, a, [role="button"], [role="checkbox"], [role="combobox"]')))),
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
    // 组合事件和松键可能被输入控件截住；捕获阶段记录生命周期，快捷键仍在冒泡阶段尊重控件消费。
    window.addEventListener("compositionstart", onCompositionStart, true);
    window.addEventListener("compositionend", onCompositionEnd, true);
    window.addEventListener("keyup", onKeyUp, true);
    window.addEventListener("pointerup", onPointerUp, true);
    window.addEventListener("blur", onBlur);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("compositionstart", onCompositionStart, true);
      window.removeEventListener("compositionend", onCompositionEnd, true);
      window.removeEventListener("keyup", onKeyUp, true);
      window.removeEventListener("pointerup", onPointerUp, true);
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [options, singleKeyShortcutsEnabled]);
}
