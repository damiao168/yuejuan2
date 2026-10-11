// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSingleKeyShortcuts } from "../shortcutPreference";
import { useGradingKeyboard, type UseGradingKeyboardOptions } from "./useGradingKeyboard";

describe("阅卷快捷键偏好与控件事件", () => {
  let root: Root;
  let host: HTMLDivElement;
  let options: UseGradingKeyboardOptions;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    localStorage.clear(); window.dispatchEvent(new StorageEvent("storage", { key: null }));
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
    options = { hasTask: true, canEditDraft: true, canSubmit: true, canReturn: true, canUndoScoreChange: true,
      hasAiSuggestion: true, maxScore: 10, rubricPointCount: 0, onSubmit: vi.fn(), onSetScore: vi.fn(), onToggleCriterion: vi.fn(),
      onAdoptAi: vi.fn(), onFlagException: vi.fn(), onReturnTask: vi.fn(), onUndoScoreChange: vi.fn(), onZoom: vi.fn() };
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); });
  function Harness() {
    useGradingKeyboard(options);
    const preference = useSingleKeyShortcuts();
    return <button onClick={() => preference.setEnabled(!preference.enabled)}>{preference.enabled ? "启用" : "关闭"}</button>;
  }
  async function mount() { await act(async () => root.render(<Harness />)); }
  function key(value: string, init: KeyboardEventInit = {}, consumed = false) {
    const event = new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true, ...init });
    if (consumed) event.preventDefault();
    window.dispatchEvent(event); return event;
  }

  it("帮助中的关闭立即同步到全局监听，保留提交组合键", async () => {
    await mount(); key("1"); expect(options.onSetScore).toHaveBeenCalledWith(1);
    await act(async () => host.querySelector("button")!.click());
    key("2"); key("a"); key("+"); key("Enter", { ctrlKey: true });
    expect(options.onSetScore).toHaveBeenCalledOnce(); expect(options.onAdoptAi).not.toHaveBeenCalled(); expect(options.onZoom).not.toHaveBeenCalled();
    expect(options.onSubmit).toHaveBeenCalledOnce();
    expect(localStorage.getItem("edugrade.grading-single-key-shortcuts")).toBe("disabled");
  });

  it("已被控件消费、输入法组合及弹层中的按键不触发评分", async () => {
    await mount(); key("1", {}, true); key("Enter", { ctrlKey: true, isComposing: true });
    const modal = document.createElement("div"); modal.className = "ant-modal-wrap"; document.body.appendChild(modal);
    key("1"); key("Enter", { ctrlKey: true }); modal.remove();
    expect(options.onSetScore).not.toHaveBeenCalled(); expect(options.onSubmit).not.toHaveBeenCalled();
  });

  it("存储写入被禁止时仍能在当前会话关闭单字符快捷键", async () => {
    await mount(); vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    await act(async () => host.querySelector("button")!.click()); key("1");
    expect(host.textContent).toBe("关闭"); expect(options.onSetScore).not.toHaveBeenCalled();
  });

  it("其他标签页关闭快捷键后同步已有工作台", async () => {
    await mount();
    await act(async () => {
      localStorage.setItem("edugrade.grading-single-key-shortcuts", "disabled");
      window.dispatchEvent(new StorageEvent("storage", { key: "edugrade.grading-single-key-shortcuts" }));
    });
    key("1"); expect(host.textContent).toBe("关闭"); expect(options.onSetScore).not.toHaveBeenCalled();
  });
});
