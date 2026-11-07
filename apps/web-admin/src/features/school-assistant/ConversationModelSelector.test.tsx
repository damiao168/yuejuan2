// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SchoolChatModel } from "../../api/schoolAiChat";
import { ConversationModelSelector } from "./ConversationModelSelector";

const first: SchoolChatModel = { id: "first", available: true, display_name: "其他兼容接口", model_name: "DeepSeek-V4-Flash", provider_key: "other", message: "" };
const second: SchoolChatModel = { ...first, id: "second", display_name: "学校模型", model_name: "Qwen3" };
const unavailable: SchoolChatModel = { ...first, id: "disabled", available: false, model_name: "Legacy", display_name: "暂时停用" };
const models = [first, second, unavailable];

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("conversation composer model selection", () => {
  it("shows a concise current model and keeps unavailable options distinct in its menu", async () => {
    const change = vi.fn();
    const view = render(<ConversationModelSelector models={models} model={first} value="first" loading={false} pending={false} onChange={change} />);
    expect(view.container.textContent).toBe("DeepSeek-V4-Flash");
    expect(view.container.querySelector('[title="DeepSeek-V4-Flash"]')).toBeTruthy();
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "切换对话模型" }));
    const available = await screen.findByTitle("Qwen3 · 学校模型");
    const disabled = screen.getByTitle("Legacy · 暂时停用（不可用）");
    expect(disabled.classList.contains("ant-select-item-option-disabled")).toBe(true);
    fireEvent.click(disabled);
    expect(change).not.toHaveBeenCalled();
    fireEvent.click(available);
    expect(change).toHaveBeenCalledWith("second", expect.objectContaining({ value: "second" }));
  });

  it("shows the active reply's original model and prevents switching until it finishes", () => {
    const change = vi.fn();
    const view = render(<ConversationModelSelector models={models} model={second} value="first" loading={false} pending onChange={change} />);
    const input = screen.getByRole("combobox", { name: "切换对话模型" });
    expect((input as HTMLInputElement).disabled).toBe(true);
    expect(view.container.textContent).toBe("DeepSeek-V4-Flash");
    expect(change).not.toHaveBeenCalled();
    view.rerender(<ConversationModelSelector models={models} model={second} value="second" loading={false} pending={false} onChange={change} />);
    expect((input as HTMLInputElement).disabled).toBe(false);
    expect(view.container.textContent).toBe("Qwen3");
  });

  it("counts authorized configurations separately from usable ones and distinguishes same-name providers", async () => {
    const custom = { ...first, id: "custom-flash", model_name: "deepseek-flash" };
    const official = { ...custom, id: "official-flash", display_name: "DeepSeek", provider_key: "deepseek", available: false, message: "普通回复检测未通过，请联系平台管理员重新检测" };
    const change = vi.fn();
    const view = render(<ConversationModelSelector models={[first, custom, official]} model={custom} value={custom.id} loading={false} pending={false} onChange={change} />);
    expect(view.container.textContent).toBe("deepseek-flash · 其他兼容接口");
    view.rerender(<ConversationModelSelector models={[first, custom, official]} model={first} value={first.id} loading={false} pending={false} onChange={change} />);
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "切换对话模型" }));
    expect(await screen.findByText("已开放 3 项 · 可用 2 项 · 不可用 1 项")).toBeTruthy();
    expect(screen.getByText(official.message)).toBeTruthy();
    const failedOption = screen.getByTitle("deepseek-flash · DeepSeek（不可用）");
    expect(failedOption.classList.contains("ant-select-item-option-disabled")).toBe(true);
    fireEvent.click(failedOption);
    expect(change).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTitle("deepseek-flash · 其他兼容接口"));
    expect(change).toHaveBeenCalledWith(custom.id, expect.objectContaining({ value: custom.id }));
  });

  it("avoids a meaningless dropdown with one model and preserves loading or unavailable feedback", () => {
    const view = render(<ConversationModelSelector models={[first]} model={first} value="first" loading={false} pending={false} onChange={vi.fn()} />);
    expect(within(view.container).queryByRole("combobox")).toBeNull();
    expect(view.container.textContent).toBe("DeepSeek-V4-Flash");
    view.rerender(<ConversationModelSelector models={[]} model={null} loading pending={false} onChange={vi.fn()} />);
    expect(view.container.textContent).toBe("正在读取模型");
    view.rerender(<ConversationModelSelector models={[unavailable]} model={unavailable} value="disabled" loading={false} pending={false} onChange={vi.fn()} />);
    expect(view.container.textContent).toBe("模型未就绪");
    expect(view.container.querySelector("i")?.className).not.toContain("is-online");
  });
});
