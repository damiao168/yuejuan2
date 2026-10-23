// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { fireEvent } from "@testing-library/react";
import { Checkbox } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SharedScoreControl, type SharedScoreValue } from "../../SharedScoreControl";
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
  function ScoreHarness() {
    const [value, setValue] = useState<SharedScoreValue>({ score: 5, rubricSelections: {} });
    useGradingKeyboard(options);
    return <><SharedScoreControl value={value} maxScore={10} rubricPoints={[{ id: "point", description: "解题过程", score: 10, required: false }]} onChange={setValue} />
      <Checkbox data-grading-quick-submit="true" checked={options.quickSubmit}>连续阅卷</Checkbox>
      <textarea aria-label="教师评语" /><input aria-label="搜索" /><input type="checkbox" aria-label="其它选项" /><output>{value.score}</output></>;
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

  // 使用真实评分控件复现填分后焦点仍在输入框的操作；不能只向 window 发键盘事件。
  it.each([
    { label: "连续阅卷 Enter", quickSubmit: true, init: {} },
    { label: "Ctrl+Enter", quickSubmit: false, init: { ctrlKey: true } },
    { label: "Meta+Enter", quickSubmit: false, init: { metaKey: true } },
  ])("分数输入框中 $label 提交一次，数字键仍用于输入", async ({ quickSubmit, init }) => {
    options.quickSubmit = quickSubmit;
    await act(async () => root.render(<ScoreHarness />));
    const input = host.querySelector<HTMLInputElement>('input[aria-label="最终得分"]')!;
    await act(async () => input.focus());
    await act(async () => { fireEvent.change(input, { target: { value: "4.5" } }); });
    fireEvent.keyDown(input, { key: "4" });
    expect(host.querySelector("output")?.textContent).toBe("4.5");
    expect(options.onSetScore).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Enter", ...init });
    expect(options.onSubmit).toHaveBeenCalledOnce();
    fireEvent.keyDown(input, { key: "Enter", repeat: true, ...init });
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("评分点输入支持连续提交，评语、搜索、输入法和弹窗仍不提交", async () => {
    options.quickSubmit = true;
    await act(async () => root.render(<ScoreHarness />));
    const score = host.querySelector<HTMLInputElement>('input[aria-label="解题过程得分"]')!;
    fireEvent.keyDown(score, { key: "Enter" });
    expect(options.onSubmit).toHaveBeenCalledOnce();
    fireEvent.keyDown(host.querySelector("textarea")!, { key: "Enter" });
    fireEvent.keyDown(host.querySelector('input[aria-label="搜索"]')!, { key: "Enter", ctrlKey: true });
    fireEvent.keyDown(score, { key: "Enter", isComposing: true });
    const modal = document.createElement("div"); modal.className = "ant-modal-wrap"; document.body.appendChild(modal);
    try { fireEvent.keyDown(score, { key: "Enter" }); } finally { modal.remove(); }
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });

  it("未开启连续阅卷、无提交权限或控件消费 Enter 时不提交", async () => {
    await act(async () => root.render(<ScoreHarness />));
    const score = host.querySelector<HTMLInputElement>('input[aria-label="最终得分"]')!;
    fireEvent.keyDown(score, { key: "Enter" });
    options.quickSubmit = true;
    options.canSubmit = false;
    fireEvent.keyDown(score, { key: "Enter" });
    options.canSubmit = true;
    const consumed = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    consumed.preventDefault();
    score.dispatchEvent(consumed);
    expect(options.onSubmit).not.toHaveBeenCalled();
  });

  it("开启连续阅卷后焦点停在开关上也能提交，其它复选框仍保留原行为", async () => {
    options.quickSubmit = true;
    await act(async () => root.render(<ScoreHarness />));
    const continuous = host.querySelector<HTMLInputElement>('input[data-grading-quick-submit="true"]')!;
    fireEvent.keyDown(continuous, { key: "Enter" });
    fireEvent.keyDown(host.querySelector('input[aria-label="其它选项"]')!, { key: "Enter" });
    expect(options.onSubmit).toHaveBeenCalledOnce();
  });
});
