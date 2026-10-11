// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolChatMessage, SchoolConversationEvent } from "../../api/schoolAiChat";
import { SchoolConversation } from "./SchoolConversation";
import { conversationAccountKey } from "./conversationState";

const mocks = vi.hoisted(() => ({
  model: vi.fn(), stream: vi.fn(), attachment: vi.fn(),
  warning: vi.fn(), success: vi.fn(), error: vi.fn(), confirm: vi.fn(), markdownId: 0,
}));

vi.mock("../../api/schoolAiChat", () => ({ getSchoolChatModel: mocks.model, streamSchoolConversation: mocks.stream }));
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
    sessions = [];
    mocks.markdownId = 0;
    mocks.model.mockResolvedValue({ model: readyModel });
    mocks.attachment.mockImplementation(async (file: File) => ({ name: file.name, media_type: "text/plain", size: file.size, content: "private attachment" }));
    // 只模拟传输边界：回调和 Promise 可分别延迟，用于验证流事件与 finally 的先后约束。
    mocks.stream.mockImplementation((_messages: SchoolChatMessage[], _context: unknown, signal: AbortSignal, callback: StreamSession["callback"]) => new Promise<void>((resolve, reject) => {
      sessions.push({ signal, callback, resolve, reject });
      signal.addEventListener("abort", () => reject(new DOMException("stopped", "AbortError")), { once: true });
    }));
    scroll = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scroll });
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
    expect(scroll).toHaveBeenCalledExactlyOnceWith({ behavior: "smooth", block: "end" });
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
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("发送失败，请重试");
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
    expect(host.querySelector(".school-chat-model-status")?.textContent).toContain("5/5 个对话进行中");
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
    expect(host.querySelector(".school-chat-model-status")?.textContent).toContain("测试模型");
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

  it("preserves initial thinking text and switches to partial markdown without inventing reply text", async () => {
    await render(); await send();
    expect(host.querySelector(".school-chat-thinking")?.textContent).toBe("思考中");
    await emit(delta(1, "partial body"));
    expect(host.querySelector(".school-chat-thinking")).toBeNull();
    expect(host.querySelector(".school-chat-live-body [data-markdown-instance]")?.textContent).toBe("partial body");
  });
});
