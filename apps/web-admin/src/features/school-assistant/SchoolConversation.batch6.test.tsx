// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolChatMessage, SchoolConversationEvent } from "../../api/schoolAiChat";
import { SchoolConversation } from "./SchoolConversation";
import { conversationAccountKey } from "./conversationState";
import { conversationModelPreferenceKey } from "./conversationModelPreference";
import { CONVERSATION_REFRESH_PREFIX } from "./conversationRefresh";

const mocks = vi.hoisted(() => ({
  model: vi.fn(), stream: vi.fn(), attachment: vi.fn(), capabilities: vi.fn(),
  warning: vi.fn(), success: vi.fn(), error: vi.fn(), confirm: vi.fn(), markdownId: 0,
}));

vi.mock("../../api/schoolAiChat", () => ({ getSchoolChatModel: mocks.model, streamSchoolConversation: mocks.stream }));
vi.mock("../../api/schoolAssistant", () => ({ getSchoolAssistantCapabilities: mocks.capabilities }));
vi.mock("./conversationAttachments", () => ({ readChatAttachment: mocks.attachment }));
vi.mock("antd", async () => {
  const { forwardRef } = await import("react");
  function Button({ children, icon, htmlType = "button", type: _type, size: _size, shape: _shape, loading: _loading, ...props }: Readonly<ComponentProps<"button"> & { icon?: ReactNode; htmlType?: "button" | "submit"; type?: string; size?: string; shape?: string; loading?: boolean }>) {
    return <button {...props} type={htmlType}>{icon}{children}</button>;
  }
  const TextArea = forwardRef<HTMLTextAreaElement, ComponentProps<"textarea"> & { autoSize?: unknown }>(function TextArea({ autoSize: _autoSize, ...props }, ref) {
    return <textarea {...props} ref={ref} />;
  });
  return {
    App: { useApp: () => ({ message: { warning: mocks.warning, success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
    Button, Input: { TextArea },
    Select: ({ value, options, onChange, disabled, "aria-label": label }: { value?: string; options: { value: string; label: string; disabled?: boolean }[]; onChange: (value: string) => void; disabled?: boolean; "aria-label": string }) => <select aria-label={label} value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>{options.map(option => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}</select>,
    Tooltip: ({ children }: Readonly<{ children: ReactNode }>) => children,
    Drawer: ({ open, children }: Readonly<{ open: boolean; children: ReactNode }>) => open ? <div>{children}</div> : null,
  };
});
vi.mock("../../components/MathMarkdown", async () => {
  const { useState } = await import("react");
  function MathMarkdown({ children }: Readonly<{ children: string }>) {
    const [identity] = useState(() => ++mocks.markdownId);
    return <div data-markdown-instance={identity}>{children}</div>;
  }
  return { MathMarkdown };
});
vi.mock("./ConversationEvidenceView", () => ({
  ConversationEvidenceView: ({ evidence, allowActions = true, onNavigate }: Readonly<{ evidence?: { tools: { label: string; status: string }[]; sources: { id: string }[]; actions: { id: string; label: string }[] }; allowActions?: boolean; onNavigate?: (path: string) => void }>) => <div data-evidence>
    {evidence?.tools.map((tool) => <span key={tool.label}>{tool.label}:{tool.status}</span>)}
    {evidence?.sources.map((source) => <span key={source.id}>{source.id}</span>)}
    {allowActions && onNavigate && evidence?.actions.map((action) => <button key={action.id} type="button" onClick={() => onNavigate(action.id)}>{action.label}</button>)}
  </div>,
}));

const user: SessionUser = {
  id: "owner", username: "owner", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "tenant", school: "学校", currentExam: "", permissions: ["dashboard:read"], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] },
};
const readyModel = { available: true, display_name: "测试模型", model_name: "synthetic", provider_key: "provider", message: "" };
interface StreamSession {
  signal: AbortSignal;
  callback: (event: SchoolConversationEvent) => void;
  resolve: () => void;
  reject: (error: Error) => void;
}
let sessions: StreamSession[];
function delta(seq: number, text: string, channel: "content" | "reasoning" = "content", request_id = "request"): SchoolConversationEvent {
  return { type: "delta", request_id, seq, channel, delta: text };
}
function done(seq: number, content: string, reasoning_content?: string): SchoolConversationEvent {
  return { type: "done", request_id: "request", seq, completion: { message: { role: "assistant", content, reasoning_content }, model_name: "synthetic", display_name: "测试模型", provider_key: "provider", usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 1, reasoning_tokens: 0, total_tokens: 2 } } };
}

describe("school conversation account, stream, and presentation boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  let scroll: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.localStorage.clear();
    window.sessionStorage.clear();
    sessions = [];
    mocks.markdownId = 0;
    mocks.model.mockResolvedValue({ model: readyModel });
    mocks.capabilities.mockResolvedValue({ web_search: { available: false, message: "联网搜索尚未配置" } });
    mocks.attachment.mockImplementation(async (file: File) => ({ name: file.name, media_type: "text/plain", size: file.size, content: "private attachment" }));
    // 只模拟传输边界：回调和 Promise 可分别延迟，用于验证流事件与 finally 的先后约束。
    mocks.stream.mockImplementation((_messages: SchoolChatMessage[], _context: unknown, signal: AbortSignal, callback: StreamSession["callback"]) => new Promise<void>((resolve, reject) => {
      sessions.push({ signal, callback, resolve, reject });
      signal.addEventListener("abort", () => reject(new DOMException("stopped", "AbortError")), { once: true });
    }));
    scroll = vi.fn(function (this: HTMLElement, options: ScrollToOptions) {
      this.scrollTop = Math.max(0, Math.min(options.top ?? this.scrollTop, this.scrollHeight - this.clientHeight));
    });
    Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: scroll });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  async function render(props: Partial<ComponentProps<typeof SchoolConversation>> = {}) {
    await act(async () => root.render(<SchoolConversation user={user} {...props} />));
  }
  function button(name: string) {
    const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((element) => element.getAttribute("aria-label") === name || element.textContent?.includes(name));
    if (!found) throw new Error(`Missing button: ${name}`);
    return found;
  }
  async function click(name: string) { await act(async () => button(name).click()); }
  async function draft(text: string) {
    await act(async () => {
      const input = host.querySelector<HTMLTextAreaElement>("textarea")!;
      Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")!.set!.call(input, text);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  async function send(text = "synthetic question") { await draft(text); await click("发送消息"); }
  async function emit(event: SchoolConversationEvent, index = sessions.length - 1) {
    await act(async () => {
      try { sessions[index]!.callback(event); } catch (error) { sessions[index]!.reject(error as Error); }
    });
  }
  async function finish(index = sessions.length - 1) { await act(async () => sessions[index]!.resolve()); }

  it("keeps progress with its background thread and removes it when that reply completes", async () => {
    await render(); await send("first progress question");
    await emit({ type: "progress", request_id: "request", seq: 1, phase: "reading" }, 0);
    expect(host.querySelector("output")?.textContent).toBe("正在读取网页");
    await click("开启新对话"); await send("second progress question");
    await emit({ type: "progress", request_id: "request", seq: 1, phase: "understanding" }, 1);
    await emit({ type: "progress", request_id: "request", seq: 2, phase: "answering" }, 0);
    expect(host.querySelector("output")?.textContent).toBe("正在理解问题");
    await click("first progress question");
    expect(host.querySelector("output")?.textContent).toBe("正在生成回答");
    await emit(done(3, "first completed result"), 0); await finish(0);
    expect(host.querySelector("output")).toBeNull();
    await click("second progress question");
    expect(host.querySelector("output")?.textContent).toBe("正在理解问题");
    await emit(done(2, "second completed result"), 1); await finish(1);
    expect(host.querySelector("output")).toBeNull();
  });
  async function chooseModel(id: string) {
    await act(async () => {
      const selector = host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')!;
      selector.value = id; selector.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }

  it("switches approved providers for the next request without changing in-flight messages", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", provider_key: "other", model_name: "other-model" };
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render();
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')!;
    expect(host.querySelector(".school-chat-composer-tools")?.contains(selector)).toBe(true);
    expect(host.querySelector(".school-chat-topbar select")).toBeNull();
    await act(async () => { selector.value = "second"; selector.dispatchEvent(new Event("change", { bubbles: true })); });
    await send();
    expect(mocks.stream.mock.calls[0][4]).toBe("second");
    expect(selector.disabled).toBe(true);
    await emit(done(1, "answer")); await finish();
    expect(selector.disabled).toBe(false);
    await act(async () => { selector.value = "first"; selector.dispatchEvent(new Event("change", { bubbles: true })); });
    await send("next question");
    expect(mocks.stream.mock.calls[1][4]).toBe("first");
  });

  it("allows a new thread to choose another provider while a background reply keeps its original model", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", display_name: "第二供应商", model_name: "other-model" };
    const preference = conversationModelPreferenceKey(conversationAccountKey(user));
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await send("original provider question");
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')!;
    expect(selector.disabled).toBe(true);

    await click("开启新对话");
    expect(selector.disabled).toBe(false);
    await chooseModel("second"); await send("parallel provider question");
    expect(mocks.stream.mock.calls.map(call => call[4])).toEqual(["first", "second"]);
    expect(sessions[0]?.signal.aborted).toBe(false);
    expect(sessions[1]?.signal.aborted).toBe(false);
    await emit(delta(1, "parallel partial"), 1);

    // 切换后才送达旧流元数据和正文，旧回复只能写回原对话，不能改新对话的模型偏好。
    await emit({ type: "model", request_id: "request", seq: 1, model: first }, 0);
    await emit(delta(2, "original partial"), 0);
    await emit(done(3, "original final"), 0); await finish(0);
    expect(selector.value).toBe("second");
    expect(window.localStorage.getItem(preference)).toBe("second");
    expect(host.querySelector(".school-chat-live-body")?.textContent).toContain("parallel partial");
    expect(host.querySelector(".school-chat-messages")?.textContent).not.toContain("original final");
    expect(host.querySelector(".school-chat-activity")?.textContent).toContain("1/5 个对话进行中");

    await emit(done(2, "parallel final"), 1); await finish(1);
    await click("original provider question");
    expect(host.querySelector(".is-assistant")?.textContent).toContain("original final");
    expect(host.querySelector(".school-chat-messages")?.textContent).not.toContain("parallel final");
    await click("parallel provider question");
    expect(host.querySelector(".is-assistant")?.textContent).toContain("parallel final");
  });

  it("shows each running thread's original provider and unlocks a completed thread while another reply continues", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", model_name: "other-model" };
    const preference = conversationModelPreferenceKey(conversationAccountKey(user));
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await send("first running question");
    await click("开启新对话"); await chooseModel("second"); await send("second running question");
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')!;
    expect(selector.value).toBe("second");
    expect(selector.disabled).toBe(true);

    await click("first running question");
    expect(selector.value).toBe("first");
    expect(selector.disabled).toBe(true);
    await chooseModel("second");
    expect(selector.value).toBe("first");
    expect(window.localStorage.getItem(preference)).toBe("second");
    await emit(delta(1, "first partial"), 0);
    expect(selector.value).toBe("first");

    await click("开启新对话");
    expect(selector.value).toBe("second");
    expect(selector.disabled).toBe(false);
    await chooseModel("first");
    await click("second running question");
    expect(selector.value).toBe("second");
    expect(selector.disabled).toBe(true);
    await emit(delta(1, "second partial"), 1);
    expect(selector.value).toBe("second");

    await emit(done(2, "first completed"), 0); await finish(0);
    await click("first running question");
    expect(selector.value).toBe("first");
    expect(selector.disabled).toBe(false);
    await chooseModel("second");
    expect(window.localStorage.getItem(preference)).toBe("second");
    expect(sessions[1]?.signal.aborted).toBe(false);
    expect(mocks.stream.mock.calls.map(call => call[4])).toEqual(["first", "second"]);
  });

  it("locks provider changes in the sending frame and releases that lock immediately when opening a new thread", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", model_name: "other-model" };
    const preference = conversationModelPreferenceKey(conversationAccountKey(user));
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await draft("same-frame first question");
    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')!;

    // 不等待 React 提交发送状态，强制触发可用模型变更，验证同步请求锁而非控件 disabled。
    await act(async () => {
      host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      selector.value = "second"; selector.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(mocks.stream.mock.calls[0][4]).toBe("first");
    expect(selector.value).toBe("first");
    expect(selector.disabled).toBe(true);
    expect(window.localStorage.getItem(preference)).toBe("first");

    await act(async () => {
      button("开启新对话").click();
      selector.value = "second"; selector.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(selector.value).toBe("second");
    expect(selector.disabled).toBe(false);
    expect(window.localStorage.getItem(preference)).toBe("second");
    await send("same-frame new question");
    expect(mocks.stream.mock.calls.map(call => call[4])).toEqual(["first", "second"]);
    expect(sessions[0]?.signal.aborted).toBe(false);
  });

  it("keeps the second provider's streamed reasoning expandable after completion and history restoration", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", display_name: "第二供应商", model_name: "reasoning-model" };
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await chooseModel("second"); await send("reasoning history question");
    expect(mocks.stream.mock.calls[0][4]).toBe("second");
    await emit(delta(1, "先核对已知条件", "reasoning"));
    await click("思考过程");
    expect(host.querySelector(".school-chat-reasoning-content")?.textContent).toBe("先核对已知条件");
    await emit(delta(2, "，再计算结果。", "reasoning"));
    await emit(delta(3, "计算结果是 42。"));
    expect(host.querySelector(".school-chat-reasoning-content")?.textContent).toBe("先核对已知条件，再计算结果。");
    await emit(done(4, "计算结果是 42。", "先核对已知条件，再计算结果。")); await finish();
    expect(host.querySelector(".is-assistant .school-chat-reasoning-toggle")?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector(".is-assistant .school-chat-reasoning-content")?.textContent).toBe("先核对已知条件，再计算结果。");

    // 重新挂载模拟页面刷新，恢复后的历史不能只剩正文而丢掉供应商已返回的思考文本。
    await act(async () => root.unmount());
    root = createRoot(host);
    await render(); await click("reasoning history question");
    expect(host.querySelector(".school-chat-reasoning-toggle")?.getAttribute("aria-expanded")).toBe("false");
    await click("思考过程");
    expect(host.querySelector(".school-chat-reasoning-content")?.textContent).toBe("先核对已知条件，再计算结果。");
    expect(host.querySelector(".is-assistant")?.textContent).toContain("计算结果是 42。");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')?.value).toBe("second");
  });

  it("does not let an in-flight callback or forced change replace the chosen provider", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", available: false, model_name: "disabled" };
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await chooseModel("second"); await send();
    expect(mocks.stream.mock.calls[0][4]).toBe("first");
    await emit({ type: "model", request_id: "request", seq: 1, model: second });
    await chooseModel("second");
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')?.value).toBe("first");
    await act(async () => sessions[0]!.reject(new Error("请求连接已断开")));
    expect(host.querySelector<HTMLSelectElement>('select[aria-label="切换对话模型"]')?.disabled).toBe(false);
    await click("重试");
    expect(mocks.stream.mock.calls[1][4]).toBe("first");
  });

  it("restores the account's preferred model only when it remains available in the server list", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", model_name: "other" };
    const preference = conversationModelPreferenceKey(conversationAccountKey(user));
    window.localStorage.setItem(preference, "second");
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await send();
    expect(mocks.stream.mock.calls[0][4]).toBe("second");
    await emit(done(1, "answer")); await finish();
    const nextUser = { ...user, id: "another-account" };
    window.localStorage.setItem(conversationModelPreferenceKey(conversationAccountKey(nextUser)), "revoked");
    await render({ user: nextUser }); await send();
    expect(mocks.stream.mock.calls[1][4]).toBe("first");
    expect(window.localStorage.getItem(preference)).toBe("second");
  });

  it.each([false, true])("ignores an unavailable remembered model, public computer=%s", async (publicComputer) => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", available: false };
    const currentUser = { ...user, publicComputer };
    const preference = conversationModelPreferenceKey(conversationAccountKey(currentUser));
    window.localStorage.setItem(preference, "second");
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render({ user: currentUser }); await send();
    expect(mocks.stream.mock.calls[0][4]).toBe("first");
    expect(window.localStorage.getItem(preference)).toBe(publicComputer ? "second" : "first");
  });

  it("persists a personal model choice but keeps a public computer choice only in memory", async () => {
    const first = { ...readyModel, id: "first" };
    const second = { ...readyModel, id: "second", model_name: "other" };
    mocks.model.mockResolvedValue({ model: first, models: [first, second] });
    await render(); await chooseModel("second");
    expect(window.localStorage.getItem(conversationModelPreferenceKey(conversationAccountKey(user)))).toBe("second");
    const publicUser = { ...user, publicComputer: true };
    await render({ user: publicUser }); await chooseModel("second"); await send();
    expect(mocks.stream.mock.calls[0][4]).toBe("second");
    expect(window.localStorage.getItem(conversationModelPreferenceKey(conversationAccountKey(publicUser)))).toBeNull();
  });

  it("late model metadata cannot select or persist a provider for the next school's account", async () => {
    let resolveOld!: (value: unknown) => void;
    mocks.model.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; }));
    const nextUser = { ...user, tenant: "next-school" };
    const nextModel = { ...readyModel, id: "next" };
    mocks.model.mockResolvedValue({ model: nextModel, models: [nextModel] });
    await render(); await render({ user: nextUser });
    const old = { ...readyModel, id: "foreign", display_name: "旧学校供应商" };
    await act(async () => resolveOld({ model: old, models: [old] }));
    await send(); expect(mocks.stream.mock.calls[0][4]).toBe("next");
    expect(host.textContent).not.toContain("旧学校供应商");
    expect(window.localStorage.getItem(conversationModelPreferenceKey(conversationAccountKey(user)))).toBeNull();
  });

  it("preserves loading, unavailable, and ready model labels and composer placeholders", async () => {
    let resolveModel!: (value: unknown) => void;
    mocks.model.mockImplementationOnce(() => new Promise((resolve) => { resolveModel = resolve; }));
    await render();
    expect(host.querySelector(".school-chat-model-status")?.textContent).toBe("正在读取模型");
    expect(host.querySelector("textarea")?.placeholder).toBe("等待学校模型就绪");
    await act(async () => resolveModel({ model: { ...readyModel, available: false, message: "模型停用" } }));
    expect(host.querySelector(".school-chat-model-status")?.textContent).toBe("模型未就绪");
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("模型停用");
  });

  it("trims the question and preserves the exam context at the transport boundary", async () => {
    const context = { exam_id: "exam", exam_name: "测试考试", origin_module: "exam_workspace" };
    await render({ initialContext: context, compact: true });
    await send("  question  ");
    expect(mocks.stream.mock.calls[0]?.slice(0, 2)).toEqual([[{ role: "user", content: "question" }], context]);
    expect(host.querySelector(".is-compact")).not.toBeNull();
    expect(button("移除当前考试上下文").disabled).toBe(true);
  });

  it("rejects replayed and foreign request events and ignores events after done", async () => {
    await render(); await send();
    await emit(delta(1, "first"));
    await emit(delta(1, " replay"));
    await emit(delta(2, " foreign", "content", "other"));
    await emit(delta(2, " second"));
    expect(host.querySelector(".school-chat-live-body")?.textContent).toContain("first second");
    await emit(done(3, "final reply"));
    await emit(delta(4, " late"));
    await finish();
    expect(host.querySelector(".school-chat-messages")?.textContent).toContain("final reply");
    expect(host.textContent).not.toContain("foreign");
    expect(host.textContent).not.toContain("late");
    expect(host.querySelector(".school-chat-waiting")).toBeNull();
  });

  it("keeps the markdown component instance while content deltas append", async () => {
    await render(); await send();
    await emit(delta(1, "first"));
    const first = host.querySelector(".school-chat-live-body [data-markdown-instance]");
    await emit(delta(2, " second"));
    expect(host.querySelector(".school-chat-live-body [data-markdown-instance]")).toBe(first);
    expect(first?.textContent).toBe("first second");
  });

  it("pauses automatic scroll while pending reasoning is expanded and carries expansion into the reply", async () => {
    await render(); await send();
    await emit(delta(1, "reasoning", "reasoning"));
    await click("思考过程");
    scroll.mockClear();
    await emit(delta(2, "content"));
    expect(scroll).not.toHaveBeenCalled();
    await emit(done(3, "complete", "reasoning")); await finish();
    expect(host.querySelector(".school-chat-reasoning-toggle")?.getAttribute("aria-expanded")).toBe("true");
    await click("思考过程");
    expect(host.querySelector(".school-chat-reasoning-content")).toBeNull();
  });

  it("resumes automatic scroll on the next delta after reasoning collapses", async () => {
    await render(); await send(); await emit(delta(1, "reasoning", "reasoning"));
    await click("思考过程"); await click("思考过程");
    scroll.mockClear(); await emit(delta(2, "content"));
    expect(scroll).toHaveBeenCalledExactlyOnceWith({ top: 0, behavior: "instant" });
  });

  it("hands scroll control to an upward wheel before the browser scroll event arrives", async () => {
    await render(); await send();
    const flow = host.querySelector<HTMLDivElement>(".school-chat-flow")!;
    Object.defineProperties(flow, { scrollHeight: { configurable: true, value: 1200 }, clientHeight: { value: 400 } });
    flow.scrollTop = 800;
    await emit(delta(1, "正文"));
    expect(scroll).toHaveBeenLastCalledWith({ top: 1200, behavior: "instant" });
    scroll.mockClear();
    // 流增量可能紧跟滚轮事件到达；此时浏览器尚未来得及改变 scrollTop 或派发 scroll。
    await act(async () => flow.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -8 })));
    await emit(delta(2, "重新生成"));
    await emit(done(3, "回复结束")); await finish();
    expect(scroll).not.toHaveBeenCalled();
    expect(flow.scrollTop).toBe(800);
  });

  it("keeps the reader's position after scrolling away and resumes only after returning to the bottom", async () => {
    await render(); await send();
    const flow = host.querySelector<HTMLDivElement>(".school-chat-flow")!;
    let contentHeight = 1200;
    Object.defineProperties(flow, { scrollHeight: { get: () => contentHeight }, clientHeight: { value: 400 } });
    await emit(delta(1, "正文"));
    await act(async () => { flow.scrollTop = 792; flow.dispatchEvent(new Event("scroll")); });
    scroll.mockClear(); contentHeight = 1600;
    await emit(delta(2, "新增段落"));
    await emit({ type: "tool_started", request_id: "request", seq: 3, call_id: "tool", tool_name: "exam_progress", label: "查询考试" });
    expect(scroll).not.toHaveBeenCalled();
    expect(flow.scrollTop).toBe(792);

    await act(async () => { flow.scrollTop = 1200; flow.dispatchEvent(new Event("scroll")); });
    // 回到底部后，即使下一段很长，也按用户此前的跟随意愿滚动，不能把内容增长误判成上翻。
    contentHeight = 2200;
    await emit(delta(4, "继续追加的长段落"));
    expect(scroll).toHaveBeenCalledExactlyOnceWith({ top: 2200, behavior: "instant" });
    expect(flow.scrollTop).toBe(1800);
  });

  it("does not resume after reasoning collapses when the user has scrolled away", async () => {
    await render(); await send(); await emit(delta(1, "思考", "reasoning"));
    await click("思考过程");
    const flow = host.querySelector<HTMLDivElement>(".school-chat-flow")!;
    Object.defineProperties(flow, { scrollHeight: { value: 1200 }, clientHeight: { value: 400 } });
    await act(async () => { flow.scrollTop = 300; flow.dispatchEvent(new Event("scroll")); });
    await click("思考过程"); scroll.mockClear();
    await emit(delta(2, "正文"));
    expect(scroll).not.toHaveBeenCalled();
    expect(flow.scrollTop).toBe(300);
  });

  it("resets following when switching threads and ignores background deltas", async () => {
    await render(); await send("第一条对话");
    const flow = host.querySelector<HTMLDivElement>(".school-chat-flow")!;
    Object.defineProperties(flow, { scrollHeight: { value: 1200 }, clientHeight: { value: 400 } });
    await act(async () => { flow.scrollTop = 300; flow.dispatchEvent(new Event("scroll")); });
    await click("开启新对话"); await send("第二条对话");
    expect(flow.scrollTop).toBe(800);
    scroll.mockClear(); await emit(delta(1, "后台继续生成"), 0);
    expect(scroll).not.toHaveBeenCalled();
    await emit(delta(1, "当前对话生成"), 1);
    expect(scroll).toHaveBeenCalledExactlyOnceWith({ top: 1200, behavior: "instant" });
  });

  it("binds evidence to the completed reply and keeps business history out of storage", async () => {
    await render({ onNavigate: vi.fn() }); await send();
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "tool", tool_name: "exam_progress", label: "查询考试" });
    await emit({ type: "sources", request_id: "request", seq: 2, sources: [{ id: "source-one", kind: "workspace", status: "available", label: "来源", read_at: "2026-10-04T00:00:00Z", consistency: "source_local", scope_label: "考试" }], actions: [{ id: "action-one", label: "进入业务页", target: { kind: "exam_section", exam_id: "exam", section: "grading" } }] });
    expect(host.querySelector(".school-chat-live-body")?.textContent).toContain("source-one");
    expect(host.querySelector(".school-chat-live-body button")).toBeNull();
    await emit(done(3, "first reply")); await finish();
    await send("followup"); await emit(done(1, "second reply")); await finish();
    const replies = host.querySelectorAll(".is-assistant");
    expect(replies[0]?.textContent).toContain("source-one");
    expect(replies[1]?.textContent).not.toContain("source-one");
    expect(JSON.parse(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)!)).toEqual([]);
  });

  it("keeps a reasoning-only partial reply after abort and rejects late data", async () => {
    await render(); await send(); await emit(delta(1, "partial reasoning", "reasoning"));
    await click("思考过程"); await click("中断当前对话");
    expect(sessions[0]?.signal.aborted).toBe(true);
    await emit(delta(2, "late content"));
    expect(host.querySelector(".is-assistant")?.textContent).toContain("partial reasoning");
    expect(host.querySelector(".school-chat-interrupted")?.textContent).toBe("已中断，尚未生成正文");
    expect(host.textContent).not.toContain("late content");
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("turns an empty aborted reply into a retryable failure and discards stale callbacks", async () => {
    await render(); await send(); await click("中断当前对话");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("已停止回复");
    await click("重试");
    expect(sessions).toHaveLength(2);
    await emit(delta(99, "old request"), 0);
    await emit(delta(1, "new request"), 1);
    expect(host.querySelector(".school-chat-live-body")?.textContent).toContain("new request");
    expect(host.textContent).not.toContain("old request");
  });

  it("reports an unfinished stream and retries the same user messages", async () => {
    await render(); await send(); await finish();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("对话流意外中断");
    const firstMessages = mocks.stream.mock.calls[0]?.[0];
    await click("重试");
    expect(mocks.stream.mock.calls[1]?.[0]).toEqual(firstMessages);
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("preserves error events and failed tool evidence without navigation", async () => {
    await render({ onNavigate: vi.fn() }); await send();
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "tool", tool_name: "exam_progress", label: "查询考试" });
    await emit({ type: "error", request_id: "request", seq: 2, code: "unavailable", message: "synthetic failure" });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("操作失败，请稍后重试");
    expect(host.textContent).not.toContain("synthetic failure");
    expect(host.querySelector(".school-conversation-failure")?.textContent).toContain("查询考试:failed");
  });

  it("blocks a sixth request before React commits the five pending states", async () => {
    await render(); await draft("parallel question");
    await act(async () => {
      const form = host.querySelector("form")!;
      for (let index = 0; index < 6; index += 1) form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(sessions).toHaveLength(5);
    expect(host.querySelector(".school-chat-activity")?.textContent).toContain("5/5 个对话进行中");
    await click("开启新对话");
    expect(host.querySelector("textarea")?.placeholder).toBe("已达到 5 个并行对话上限");
    await emit(done(1, "finished"), 0); await finish(0);
    await send("sixth question");
    expect(sessions).toHaveLength(6);
  });

  it("does not append a second reply when stopped after done but before the transport settles", async () => {
    await render(); await send(); await emit(done(1, "completed reply"));
    await click("中断当前对话");
    expect(host.querySelectorAll(".is-assistant")).toHaveLength(1);
    expect(host.querySelector(".is-assistant")?.textContent).toContain("completed reply");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(host.querySelector(".school-chat-waiting")).toBeNull();
  });

  it("keeps the completed reply and original failure behavior when the transport rejects after done", async () => {
    await render(); await send(); await emit(done(1, "completed reply"));
    await act(async () => sessions[0]!.reject(new Error("请求连接已断开")));
    expect(host.querySelectorAll(".is-assistant")).toHaveLength(1);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("请求连接已断开");
    await click("重试");
    expect(sessions).toHaveLength(1);
  });

  it("keeps a background request alive when only the initial exam context changes", async () => {
    await render({ initialContext: { exam_id: "first", exam_name: "原考试" } });
    await send(); await emit(delta(1, "partial"));
    await render({ initialContext: { exam_id: "second", exam_name: "新考试" } });
    expect(sessions[0]?.signal.aborted).toBe(false);
    expect(host.querySelector(".school-chat-welcome")).not.toBeNull();
    await emit(done(2, "background reply")); await finish();
    await click("synthetic question");
    expect(host.querySelector(".school-conversation-context")?.textContent).toContain("原考试");
    expect(host.querySelector(".is-assistant")?.textContent).toContain("background reply");
  });

  it("does not send Enter during composition or with Shift but sends plain Enter once", async () => {
    await render(); await draft("keyboard question");
    const input = host.querySelector("textarea")!;
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true }));
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", shiftKey: true, bubbles: true, cancelable: true }));
    });
    expect(sessions).toHaveLength(0);
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(sessions).toHaveLength(1);
  });

  it("sends attachment content but persists only its display metadata", async () => {
    await render();
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    const file = new File(["private attachment"], "sample.txt", { type: "text/plain" });
    Object.defineProperty(input, "files", { configurable: true, value: [file] });
    await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
    await click("发送消息");
    expect(mocks.stream.mock.calls[0]?.[0][0].attachments[0]?.content).toBe("private attachment");
    await emit(done(1, "attachment reply")); await finish();
    const serialized = window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)!;
    expect(serialized).toContain("sample.txt");
    expect(serialized).not.toContain("private attachment");
  });

  it.each(["new conversation", "changed context", "removed context", "changed account"])("cancels attachment reads on %s and rejects a late result", async (destination) => {
    let finish!: (value: unknown) => void;
    mocks.attachment.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    await render(destination === "removed context" ? { initialContext: { exam_id: "old-exam" } } : {});
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["old"], "old.pdf", { type: "application/pdf" })] });
    await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
    const signal = mocks.attachment.mock.calls[0]![1] as AbortSignal;
    if (destination === "new conversation") await click("开启新对话");
    else if (destination === "changed context") await render({ initialContext: { exam_id: "new-exam" } });
    else if (destination === "removed context") await click("移除当前考试上下文");
    else await render({ user: { ...user, id: "next-account" } });
    expect(signal.aborted).toBe(true);
    await act(async () => finish({ name: "old.pdf", content: "old content", size: 3, media_type: "application/pdf" }));
    expect(host.querySelector(".school-chat-attachments")).toBeNull();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("does not show an old attachment error after permission changes", async () => {
    let fail!: (error: Error) => void;
    mocks.attachment.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }));
    await render();
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["old"], "old.pdf")] });
    await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
    await render({ user: { ...user, permissions: [] } });
    await act(async () => fail(new Error("旧账户解析失败")));
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("blocks duplicate submissions for the active thread immediately", async () => {
    await render(); await send();
    await act(async () => host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(sessions).toHaveLength(1);
  });

  it("rebuilds memory and aborts streams when the account permission scope changes", async () => {
    await render(); await send(); await emit(delta(1, "old partial"));
    await render({ user: { ...user, permissions: [] } });
    expect(sessions[0]?.signal.aborted).toBe(true);
    await emit(delta(2, "late old account"));
    expect(host.querySelector(".school-chat-welcome")).not.toBeNull();
    expect(host.textContent).not.toContain("old partial");
    expect(host.textContent).not.toContain("late old account");
  });

  it("preserves the active conversation when only a non-authorization display label changes", async () => {
    await render(); await send(); await emit(delta(1, "partial"));
    await render({ user: { ...user, displayName: "另一个显示名" } });
    expect(sessions[0]?.signal.aborted).toBe(false);
    expect(host.querySelector(".school-chat-live-body")?.textContent).toContain("partial");
  });

  it("ignores a late model response after the owning account unmounts", async () => {
    let resolveOld!: (value: unknown) => void;
    mocks.model.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
    await render(); await render({ user: { ...user, id: "new-owner" } });
    await act(async () => resolveOld({ model: { ...readyModel, display_name: "旧账户模型" } }));
    expect(host.querySelector(".school-chat-model-status")?.textContent).toContain("synthetic");
    expect(host.textContent).not.toContain("旧账户模型");
  });

  it("aborts a deleted pending thread and prevents its late reply from returning", async () => {
    await render(); await send();
    await click("删除对话：synthetic question");
    await act(async () => mocks.confirm.mock.calls[0]![0].onOk());
    expect(sessions[0]?.signal.aborted).toBe(true);
    await emit(done(1, "deleted late reply"));
    expect(host.querySelector(".school-chat-history-empty")).not.toBeNull();
    expect(host.textContent).not.toContain("deleted late reply");
  });

  it("persists completed general chat but never public-computer history", async () => {
    await render(); await send(); await emit(done(1, "general reply")); await finish();
    const stored = JSON.parse(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)!);
    expect(stored[0]?.messages.at(-1)).toEqual({ role: "assistant", content: "general reply" });
    const publicUser = { ...user, publicComputer: true };
    await render({ user: publicUser }); await send(); await emit(done(1, "public reply")); await finish();
    expect(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(publicUser)}`)).toBeNull();
  });

  it("restores completed messages and reasoning when the page reloads during a followup", async () => {
    await render(); await send("已完成的一般问题");
    await emit(done(1, "已完成的回复", "已返回的思考")); await finish();
    await send("尚未完成的追问");
    // 完整历史沿用长期缓存；未完成纯文字追问只在当前标签页恢复，不能自动重发。
    await act(async () => root.unmount());
    root = createRoot(host);
    await render(); await click("已完成的一般问题");
    expect(host.querySelector(".is-assistant")?.textContent).toContain("已完成的回复");
    expect(host.textContent).toContain("尚未完成的追问");
    expect(host.textContent).toContain("上次回复未完成");
    await click("思考过程");
    expect(host.querySelector(".school-chat-reasoning-content")?.textContent).toBe("已返回的思考");
  });

  it("restores the first unanswered question on refresh and waits for an explicit continue", async () => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render(); await click("联网搜索"); await send("联网搜索北京的天气");
    const firstSignal = sessions[0]!.signal;
    expect(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)).not.toContain("联网搜索北京的天气");
    await act(async () => root.unmount()); root = createRoot(host); await render();
    expect(firstSignal.aborted).toBe(true);
    expect(host.querySelector(".is-user")?.textContent).toBe("联网搜索北京的天气");
    expect(host.textContent).toContain("上次回复未完成");
    expect(host.querySelector(".school-chat-waiting")).toBeNull();
    expect(mocks.stream).toHaveBeenCalledTimes(1);
    await click("重新生成");
    expect(mocks.stream).toHaveBeenCalledTimes(2);
    expect(mocks.stream.mock.calls[1]?.[0]).toEqual([{ role: "user", content: "联网搜索北京的天气", web_search: true }]);
    expect(mocks.stream.mock.calls[1]?.[5]).toBe(true);
  });

  it("shows the recovered partial reply separately and never sends it or reasoning back as verified history", async () => {
    await render(); await send("查找公开资料");
    await emit(delta(1, "临时推理不得恢复", "reasoning"));
    await emit(delta(2, "已经收到的部分正文"));
    await act(async () => window.dispatchEvent(new Event("pagehide")));
    const stored = window.sessionStorage.getItem(`${CONVERSATION_REFRESH_PREFIX}${conversationAccountKey(user)}`)!;
    expect(stored).toContain("已经收到的部分正文");
    expect(stored).not.toContain("临时推理不得恢复");
    await act(async () => root.unmount()); root = createRoot(host); await render();
    expect(host.textContent).toContain("上次未完成的回复");
    expect(host.textContent).toContain("已经收到的部分正文");
    expect(mocks.stream).toHaveBeenCalledTimes(1);
    await click("重新生成");
    expect(mocks.stream.mock.calls[1]?.[0]).toEqual([{ role: "user", content: "查找公开资料" }]);
    expect(host.textContent).not.toContain("已经收到的部分正文");
  });

  it("removes a temporary unanswered checkpoint as soon as the request enters a business tool", async () => {
    await render(); await send("查看本校考试");
    const key = `${CONVERSATION_REFRESH_PREFIX}${conversationAccountKey(user)}`;
    expect(window.sessionStorage.getItem(key)).toContain("查看本校考试");
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "private", tool_name: "exam_progress", label: "业务工具" });
    expect(window.sessionStorage.getItem(key)).not.toContain("查看本校考试");
    await act(async () => root.unmount()); root = createRoot(host); await render();
    expect(host.textContent).not.toContain("查看本校考试");
    expect(mocks.stream).toHaveBeenCalledTimes(1);
  });

  it("preserves initial thinking text and switches to partial markdown without inventing reply text", async () => {
    await render(); await send();
    expect(host.querySelector(".school-chat-thinking")?.textContent).toBe("思考中");
    await emit(delta(1, "partial body"));
    expect(host.querySelector(".school-chat-thinking")).toBeNull();
    expect(host.querySelector(".school-chat-live-body [data-markdown-instance]")?.textContent).toBe("partial body");
  });

  it("enables explicit web search only after the server exposes the capability", async () => {
    await render();
    expect(button("联网搜索").disabled).toBe(true);
    await send("普通问题");
    expect(mocks.stream.mock.calls[0]?.[5]).toBe(false);
  });

  it("captures web search for retry and preserves mode tags until the API filters public history", async () => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render(); await click("联网搜索"); await send("公开的教育政策");
    expect(mocks.stream.mock.calls[0]?.[5]).toBe(true);
    expect(mocks.stream.mock.calls[0]?.[0]).toEqual([expect.objectContaining({ role: "user", content: "公开的教育政策", web_search: true })]);
    expect(button("联网搜索").disabled).toBe(true);
    await act(async () => sessions[0]!.reject(new Error("搜索超时")));
    await click("联网搜索");
    expect(button("联网搜索").getAttribute("aria-pressed")).toBe("false");
    await click("重试");
    expect(mocks.stream.mock.calls[1]?.[5]).toBe(true);
  });

  it.each([false, true])("联网请求转校内问答后，后续公开追问不能携带业务正文，中断=%s", async interrupted => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render(); await click("联网搜索"); await send("查看本场考试进度");
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "school-business", tool_name: "school_business", label: "切换校内只读问答" });
    await emit(delta(2, "只属于本校的私有考试进度"));
    if (interrupted) await click("中断当前对话");
    else { await emit(done(3, "只属于本校的私有考试进度")); await finish(); }

    expect(host.querySelector(".is-assistant")?.textContent).toContain("只属于本校的私有考试进度");
    expect(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`) ?? "").not.toContain("私有考试进度");
    await send("北京今天的天气");
    const sent = mocks.stream.mock.calls[1]?.[0] as SchoolChatMessage[];
    expect(sent.find(message => message.role === "assistant")?.web_search).not.toBe(true);
    expect(sent.find(message => message.role === "assistant")?.web_sources).toBeUndefined();
    const { publicSearchMessages } = await vi.importActual<typeof import("../../api/schoolAiChat")>("../../api/schoolAiChat");
    expect(publicSearchMessages(sent)).toEqual([{ role: "user", content: "北京今天的天气" }]);
  });

  it.each([
    ["invalid_web_search_assessment", "本次搜索结果未能核实，请补充问题或稍后重试。"],
    ["invalid_web_search_selection", "暂时无法选择需要读取的网页，请补充具体问题后重试。"],
    ["invalid_web_search_citation", "回答中的引用未通过核验，请重试。"],
  ])("按受控错误码展示搜索失败说明 %s", async (code, message) => {
    await render(); await send();
    await emit({ type: "error", request_id: "request", seq: 1, code, message: "internal=private-provider-detail" });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(message);
    expect(host.textContent).not.toContain("private-provider-detail");
  });

  it("keeps public web sources and the exam context without classifying a web-only reply as business", async () => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render({ initialContext: { exam_id: "exam", exam_name: "本场考试" } });
    await click("联网搜索"); await send("教育部公开通知");
    const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "公开通知", read_at: "2026-11-01T00:00:00Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://www.moe.gov.cn/notice", excerpt: "公开的通知摘要" };
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "search", tool_name: "web_search", label: "联网搜索" });
    await emit({ type: "sources", request_id: "request", seq: 2, sources: [source], actions: [] });
    await emit({ type: "tool_finished", request_id: "request", seq: 3, call_id: "search", tool_name: "web_search", label: "联网搜索", status: "completed" });
    await emit(done(4, "根据公开通知回答")); await finish();
    expect(host.querySelector(".school-conversation-context")?.textContent).toContain("本场考试");
    const serialized = window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)!;
    expect(serialized).toContain("https://www.moe.gov.cn/notice");
    expect(serialized).not.toContain("exam_id");
    await act(async () => root.unmount()); root = createRoot(host);
    await render(); await click("教育部公开通知");
    expect(host.querySelector(".is-assistant [data-evidence]")?.textContent).toContain("web-1");
    await click("联网搜索"); await send("继续写作");
    expect(JSON.stringify(mocks.stream.mock.calls[1]?.[0])).not.toContain("web_sources");
    expect(JSON.stringify(mocks.stream.mock.calls[1]?.[0])).not.toContain("www.moe.gov.cn");
  });

  it("网页正文读取后仍保留公开追问上下文，不因读取工具误判业务而截断", async () => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render(); await click("联网搜索"); await send("北京今天的天气");
    await emit({ type: "tool_started", request_id: "request", seq: 1, call_id: "fetch", tool_name: "web_fetch", label: "读取网页正文" });
    await emit({ type: "tool_finished", request_id: "request", seq: 2, call_id: "fetch", tool_name: "web_fetch", label: "读取网页正文", status: "completed" });
    const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "天气来源", read_at: "2026-11-02T03:04:05Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://example.org/weather", excerpt: "选中的天气片段", note: "已读取网页正文。" };
    await emit({ type: "sources", request_id: "request", seq: 3, sources: [source], actions: [] });
    await emit(done(4, "已读取北京今天的天气资料。[1]")); await finish();
    await send("明天呢");
    const { publicSearchMessages } = await vi.importActual<typeof import("../../api/schoolAiChat")>("../../api/schoolAiChat");
    const request = publicSearchMessages(mocks.stream.mock.calls[1]?.[0] as SchoolChatMessage[]);
    expect(request.map(message => message.content)).toEqual(["北京今天的天气", "已读取北京今天的天气资料。[1]", "明天呢"]);
    expect(JSON.stringify(request)).not.toContain("已读取网页正文");
    expect(JSON.stringify(request)).not.toContain("example.org");
    expect(window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(user)}`)).toContain("已读取网页正文。");
  });

  it("blocks files and oversized questions in web-search mode", async () => {
    mocks.capabilities.mockResolvedValue({ web_search: { available: true, message: "已配置" } });
    await render(); await click("联网搜索");
    expect(button("上传文件").disabled).toBe(true);
    await send("问".repeat(501));
    expect(sessions).toHaveLength(0);
    expect(mocks.warning).toHaveBeenCalledWith("联网搜索请单独发送 500 字以内的公开问题，不附带文件");
  });
});
