// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useGradingKeyboard, type UseGradingKeyboardOptions } from "./useGradingKeyboard";

vi.mock("../shortcutPreference", () => ({ useSingleKeyShortcuts: () => ({ enabled: true }) }));

function Harness({ options }: Readonly<{ options: UseGradingKeyboardOptions }>) {
  useGradingKeyboard(options);
  return <input aria-label="评分说明" />;
}

describe("输入法组合与确认键的快捷键边界", () => {
  let root: Root;
  let host: HTMLDivElement;
  let options: UseGradingKeyboardOptions;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
    options = {
      hasTask: true, canEditDraft: true, canSubmit: true, canReturn: true, canToggleBookmark: true, canUndoScoreChange: true,
      hasAiSuggestion: true, maxScore: 10, rubricPointCount: 0,
      onSubmit: vi.fn(), onSetScore: vi.fn(), onToggleCriterion: vi.fn(), onAdoptAi: vi.fn(),
      onToggleBookmark: vi.fn(), onReturnTask: vi.fn(), onUndoScoreChange: vi.fn(), onZoom: vi.fn(),
    };
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  async function render() { await act(async () => root.render(<Harness options={options} />)); }
  function composition(type: "compositionstart" | "compositionend", target: EventTarget = window) {
    target.dispatchEvent(new CompositionEvent(type, { bubbles: true }));
  }
  function key(type: "keydown" | "keyup", value: string, init: KeyboardEventInit = {}) {
    const event = new KeyboardEvent(type, { key: value, bubbles: true, cancelable: true, ...init });
    window.dispatchEvent(event);
    return event;
  }

  it.each([
    { name: "Ctrl+Enter", init: { ctrlKey: true }, quickSubmit: false },
    { name: "Meta+Enter", init: { metaKey: true }, quickSubmit: false },
    { name: "单独 Enter", init: {}, quickSubmit: true },
  ])("标准组合顺序中 $name 只在确认松键后提交", async ({ init, quickSubmit }) => {
    options.quickSubmit = quickSubmit;
    await render();
    composition("compositionstart");
    key("keydown", "1");
    key("keyup", "1");
    key("keydown", "Enter", { ...init, isComposing: true });
    composition("compositionend");
    expect(options.onSetScore).not.toHaveBeenCalled();
    expect(options.onSubmit).not.toHaveBeenCalled();
    key("keyup", "Enter", init);
    expect(key("keydown", "Enter", init).defaultPrevented).toBe(true);
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("先结束组合再发送 isComposing=false 的确认 Enter 仍不能提交", async () => {
    await render();
    composition("compositionstart");
    // 合成 WebKit 倒序只验证应用处理该顺序，不代表真实系统输入法验收。
    composition("compositionend");
    expect(key("keydown", "Enter", { ctrlKey: true }).defaultPrevented).toBe(false);
    expect(options.onSubmit).not.toHaveBeenCalled();
    key("keyup", "Enter");
    key("keydown", "1");
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSetScore).toHaveBeenCalledWith(1);
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("系统候选窗确认缺少 keyup 时，下一次非 Enter 评分键结束保护", async () => {
    await render();
    composition("compositionstart");
    composition("compositionend");
    key("keydown", "1");
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSetScore).toHaveBeenCalledWith(1);
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("组合结束后无 keyup 且立即按 Enter 时，保守忽略到释放后再提交", async () => {
    await render();
    composition("compositionstart");
    composition("compositionend");
    key("keydown", "Enter", { ctrlKey: true });
    key("keydown", "Enter", { ctrlKey: true, repeat: true });
    expect(options.onSubmit).not.toHaveBeenCalled();
    key("keyup", "Enter");
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("组合中及结束后的 options 重渲染保留保护并使用最新回调", async () => {
    await render();
    const oldSubmit = options.onSubmit;
    composition("compositionstart");
    options = { ...options, onSubmit: vi.fn() };
    await render();
    key("keydown", "Enter", { ctrlKey: true });
    composition("compositionend");
    options = { ...options, maxScore: 9 };
    await render();
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).not.toHaveBeenCalled();
    key("keyup", "Enter");
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).toHaveBeenCalledOnce();
    expect(oldSubmit).not.toHaveBeenCalled();
  });

  it("控件阻止组合事件冒泡时仍记录输入法生命周期", async () => {
    await render();
    const input = host.querySelector("input")!;
    input.addEventListener("compositionstart", (event) => event.stopPropagation());
    input.addEventListener("compositionend", (event) => event.stopPropagation());
    composition("compositionstart", input);
    key("keydown", "1");
    composition("compositionend", input);
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSetScore).not.toHaveBeenCalled();
    expect(options.onSubmit).not.toHaveBeenCalled();
    input.dispatchEvent(new KeyboardEvent("keyup", { key: "Enter", bubbles: true }));
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("页面鼠标确认或窗口失焦后清理未收到松键的组合状态", async () => {
    await render();
    composition("compositionstart");
    composition("compositionend");
    window.dispatchEvent(new Event("pointerup"));
    key("keydown", "1");
    composition("compositionstart");
    window.dispatchEvent(new Event("blur"));
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSetScore).toHaveBeenCalledWith(1);
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("卸载删除全部监听且新工作台不继承旧组合状态", async () => {
    const add = vi.spyOn(window, "addEventListener");
    const remove = vi.spyOn(window, "removeEventListener");
    await render();
    composition("compositionstart");
    const registrations = [...add.mock.calls];
    await act(async () => root.render(null));
    for (const [type, listener, capture] of registrations) {
      if (capture === undefined) expect(remove).toHaveBeenCalledWith(type, listener);
      else expect(remove).toHaveBeenCalledWith(type, listener, capture);
    }
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).not.toHaveBeenCalled();
    await render();
    key("keydown", "Enter", { ctrlKey: true });
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });
});
