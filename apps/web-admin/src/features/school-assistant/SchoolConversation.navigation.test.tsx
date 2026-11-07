// @vitest-environment jsdom
import { useState, type ComponentProps, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolChatMessage, SchoolConversationEvent } from "../../api/schoolAiChat";
import { SchoolAIChatPage } from "../../pages/SchoolAIChatPage";
import { RouteContent } from "../../router/RouteContent";
import { SchoolAssistantProvider } from "./SchoolAssistantProvider";
import { conversationAccountKey } from "./conversationState";

const mocks = vi.hoisted(() => ({ model: vi.fn(), capabilities: vi.fn(), stream: vi.fn(), toast: vi.fn(), confirm: vi.fn() }));

vi.mock("../../api/schoolAiChat", () => ({ getSchoolChatModel: mocks.model, streamSchoolConversation: mocks.stream }));
vi.mock("../../api/schoolAssistant", () => ({ getSchoolAssistantCapabilities: mocks.capabilities }));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: string }>) => <div>{children}</div> }));
vi.mock("antd", async () => {
  const { forwardRef } = await import("react");
  function Button({ children, icon, htmlType = "button", type: _type, size: _size, shape: _shape, loading: _loading, ...props }: Readonly<ComponentProps<"button"> & { icon?: ReactNode; htmlType?: "button" | "submit"; type?: string; size?: string; shape?: string; loading?: boolean }>) {
    return <button {...props} type={htmlType}>{icon}{children}</button>;
  }
  const TextArea = forwardRef<HTMLTextAreaElement, ComponentProps<"textarea"> & { autoSize?: unknown }>(function TextArea({ autoSize: _autoSize, ...props }, ref) {
    return <textarea {...props} ref={ref} />;
  });
  return {
    App: { useApp: () => ({ message: { warning: mocks.toast, success: mocks.toast, error: mocks.toast }, modal: { confirm: mocks.confirm } }) },
    Button, Input: { TextArea },
    Tooltip: ({ children }: Readonly<{ children: ReactNode }>) => children,
    Drawer: ({ open, children, onClose }: Readonly<{ open: boolean; children: ReactNode; onClose?: () => void }>) => open ? <aside><button type="button" onClick={onClose}>关闭助手</button>{children}</aside> : null,
    Collapse: ({ items }: Readonly<{ items: { key: string; label: ReactNode; children: ReactNode }[] }>) => <>{items.map(item => <section key={item.key}>{item.label}{item.children}</section>)}</>,
    Select: ({ value, options, onChange, disabled, "aria-label": label }: { value?: string; options: { value: string; label: string; disabled?: boolean }[]; onChange: (value: string) => void; disabled?: boolean; "aria-label": string }) => <select aria-label={label} value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>{options.map(option => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}</select>,
    Alert: ({ message, description }: Readonly<{ message: ReactNode; description: ReactNode }>) => <div role="alert">{message}{description}</div>,
    Empty: () => null,
    Result: ({ title, subTitle }: Readonly<{ title: ReactNode; subTitle: ReactNode }>) => <div role="alert">{title}{subTitle}</div>,
    Spin: () => <span aria-hidden="true" />,
  };
});

const user: SessionUser = {
  id: "navigation-owner", username: "navigation-owner", displayName: "学校管理员", name: "学校管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "navigation-tenant", school: "测试学校", currentExam: "", permissions: ["dashboard:read"], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] },
};
const readyModel = { available: true, id: "navigation-model", display_name: "测试模型", model_name: "synthetic", provider_key: "provider", message: "" };

interface StreamSession {
  requestId: string;
  signal: AbortSignal;
  callback: (event: SchoolConversationEvent) => void;
  resolve: () => void;
  reject: (error: Error) => void;
}
let sessions: StreamSession[];

// 保留真实 Provider、路由分阶段挂载和对话正文；传输保持未完成，才能区分离开页面与停止请求。
function NavigationHarness({ currentUser }: Readonly<{ currentUser: SessionUser }>) {
  const [path, navigate] = useState("/ai-chat");
  return <SchoolAssistantProvider user={currentUser} locationKey={path} onNavigate={navigate}>
    <nav aria-label="测试主导航">
      <button type="button" onClick={() => navigate("/ai-chat")}>AI 主栏目</button>
      <button type="button" onClick={() => navigate("/members/students")}>学生主栏目</button>
    </nav>
    <RouteContent scopeKey={conversationAccountKey(currentUser)} locationKey={path} title={path === "/ai-chat" ? "学校 AI 助手" : "学生管理"}>
      {path === "/ai-chat" ? <SchoolAIChatPage user={currentUser} /> : <section data-testid="ready-student-page">学生列表已就绪</section>}
    </RouteContent>
  </SchoolAssistantProvider>;
}

async function openAI() {
  fireEvent.click(screen.getByRole("button", { name: "AI 主栏目" }));
  await waitFor(() => expect(screen.getByRole("textbox", { name: "对话输入" })).not.toBeNull());
}

async function leaveAI() {
  fireEvent.click(screen.getByRole("button", { name: "学生主栏目" }));
  // 目标页完全就绪后，RouteContent 会移除旧页；在候选页加载时立即返回会错误地保留旧对话组件。
  await waitFor(() => {
    expect(screen.getByTestId("ready-student-page")).not.toBeNull();
    expect(document.querySelector(".route-content-stage-pending")).toBeNull();
    expect(document.querySelector(".route-content .school-conversation-shell")).toBeNull();
  });
}

async function send(question: string) {
  await waitFor(() => expect((screen.getByRole("textbox", { name: "对话输入" }) as HTMLTextAreaElement).disabled).toBe(false));
  fireEvent.change(screen.getByRole("textbox", { name: "对话输入" }), { target: { value: question } });
  fireEvent.click(screen.getByRole("button", { name: "发送消息" }));
  await waitFor(() => expect(sessions.length).toBeGreaterThan(0));
}

async function emit(index: number, event: SchoolConversationEvent) {
  await act(async () => sessions[index]!.callback(event));
}

async function delta(index: number, seq: number, content: string) {
  await emit(index, { type: "delta", request_id: sessions[index]!.requestId, seq, channel: "content", delta: content });
}

async function done(index: number, seq: number, content: string) {
  await emit(index, {
    type: "done", request_id: sessions[index]!.requestId, seq,
    completion: { message: { role: "assistant", content }, model_name: "synthetic", display_name: "测试模型", provider_key: "provider", usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 1, reasoning_tokens: 0, total_tokens: 2 } },
  });
}

async function finish(index: number) { await act(async () => sessions[index]!.resolve()); }

describe("学校 AI 对话的主导航生命周期", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    window.sessionStorage.clear();
    sessions = [];
    mocks.model.mockResolvedValue({ model: readyModel, models: [readyModel] });
    mocks.capabilities.mockResolvedValue({ mode: "school_business", model_required: false, intents: [], web_search: { available: false, message: "未配置" } });
    mocks.stream.mockImplementation((_messages: SchoolChatMessage[], _context: unknown, signal: AbortSignal, callback: StreamSession["callback"]) => new Promise<void>((resolve, reject) => {
      sessions.push({ requestId: `navigation-request-${sessions.length}`, signal, callback, resolve, reject });
      signal.addEventListener("abort", () => reject(new DOMException("stopped", "AbortError")), { once: true });
    }));
    Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value: vi.fn() });
  });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it.each([false, true])("离开已就绪的主栏目后继续接收工具进度和回复，返回仍选择原对话，公共电脑=%s", async publicComputer => {
    const currentUser = { ...user, publicComputer };
    render(<NavigationHarness currentUser={currentUser} />);
    await send("跨栏目查询考试进度");
    await emit(0, { type: "tool_started", request_id: sessions[0]!.requestId, seq: 1, call_id: "progress", tool_name: "exam_progress", label: "读取考试进度" });
    await delta(0, 2, "离开前正文");
    await leaveAI();
    expect(sessions[0]!.signal.aborted).toBe(false);

    await delta(0, 3, "和后台增量");
    await emit(0, { type: "tool_finished", request_id: sessions[0]!.requestId, seq: 4, call_id: "progress", tool_name: "exam_progress", label: "读取考试进度", status: "completed" });
    await done(0, 5, "完整的跨栏目考试进度回复");
    await finish(0);
    await openAI();

    expect(document.querySelector(".school-chat-thread.is-active")?.textContent).toContain("跨栏目查询考试进度");
    expect(document.querySelector(".school-chat-messages .is-assistant")?.textContent).toContain("完整的跨栏目考试进度回复");
    expect(document.querySelector(".school-chat-messages .school-conversation-evidence")?.textContent).toContain("已查询1项资料");
    expect(document.querySelector(".school-chat-waiting")).toBeNull();
    expect(mocks.stream).toHaveBeenCalledOnce();
    // 导航保活只用账户内存；业务查询和公共电脑都不能借此扩大浏览器落盘范围。
    const stored = window.localStorage.getItem(`edugrade.ai-chat.v2:${conversationAccountKey(currentUser)}`);
    expect(stored ?? "").not.toContain("跨栏目查询考试进度");
    expect(stored ?? "").not.toContain("完整的跨栏目考试进度回复");
  });

  it("返回仍在回复的对话后继续显示后台增量，主动停止仍拒绝迟到结果", async () => {
    render(<NavigationHarness currentUser={user} />);
    await send("返回后停止的对话");
    await delta(0, 1, "离开前保留的正文");
    await leaveAI();
    expect(sessions[0]!.signal.aborted).toBe(false);
    await delta(0, 2, "和离开后的正文");
    // 从其他栏目提供的进度入口返回，不能新建请求或丢掉当前选中的线程。
    fireEvent.click(screen.getByRole("button", { name: "返回学校 AI 助手" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "中断当前对话" })).not.toBeNull());

    expect(document.querySelector(".school-chat-live-body")?.textContent).toContain("离开前保留的正文和离开后的正文");
    expect(mocks.stream).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "中断当前对话" }));
    await waitFor(() => expect(sessions[0]!.signal.aborted).toBe(true));
    await waitFor(() => expect(document.querySelector(".school-chat-waiting")).toBeNull());
    await delta(0, 3, "不应出现的迟到正文");
    await done(0, 4, "不应出现的迟到终稿");
    await finish(0);
    expect(document.querySelector(".school-chat-messages .is-assistant")?.textContent).toContain("离开前保留的正文和离开后的正文");
    expect(document.querySelector(".school-chat-messages")?.textContent).not.toContain("不应出现");
  });

  it.each([false, true])("后台接收最终选定来源后返回，引用编号及来源不会丢失，中断=%s", async interrupted => {
    mocks.capabilities.mockResolvedValue({ mode: "school_business", model_required: false, intents: [], web_search: { available: true, message: "已配置" } });
    render(<NavigationHarness currentUser={user} />);
    await waitFor(() => expect((screen.getByRole("button", { name: "联网搜索" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "联网搜索" }));
    await send("查询北京天气的公开资料");
    await leaveAI();
    const source = { id: "web-2", kind: "web" as const, status: "available" as const, label: "采用的公开气象资料", read_at: "2026-11-01T00:00:00Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://weather.example.org/beijing" };
    await delta(0, 1, "根据公开气象资料回答 [2]");
    await emit(0, { type: "sources", request_id: sessions[0]!.requestId, seq: 2, sources: [source], actions: [] });
    if (!interrupted) { await done(0, 3, "根据公开气象资料回答 [2]"); await finish(0); }
    await openAI();
    if (interrupted) {
      fireEvent.click(screen.getByRole("button", { name: "中断当前对话" }));
      await waitFor(() => expect(document.querySelector(".school-chat-waiting")).toBeNull());
    }
    expect(document.querySelector(".school-chat-messages .is-assistant")?.textContent).toContain("根据公开气象资料回答 [2]");
    const link = screen.getByRole("link", { name: /采用的公开气象资料/ });
    expect(link.textContent).toContain("[2]");
    expect(link.getAttribute("href")).toBe("https://weather.example.org/beijing");
    expect(screen.getByText("参考来源（1）")).not.toBeNull();
    expect(mocks.stream).toHaveBeenCalledOnce();
  });

  it.each(["账户", "权限"])("离开期间改变%s会终止原请求，旧回调和收尾不能影响新范围", async boundary => {
    const view = render(<NavigationHarness currentUser={user} />);
    await send("旧范围问题");
    await delta(0, 1, "旧范围部分正文");
    await leaveAI();
    expect(sessions[0]!.signal.aborted).toBe(false);
    const nextUser = boundary === "账户" ? { ...user, id: "next-navigation-owner" } : { ...user, permissions: [] };
    view.rerender(<NavigationHarness currentUser={nextUser} />);
    await waitFor(() => expect(sessions[0]!.signal.aborted).toBe(true));
    await delta(0, 2, "旧范围迟到正文");
    await done(0, 3, "旧范围迟到终稿");
    await finish(0);
    await openAI();

    expect(document.querySelector(".school-chat-welcome")).not.toBeNull();
    expect(document.querySelector(".school-conversation-shell")?.textContent).not.toContain("旧范围");
    await send("新范围问题");
    expect(sessions).toHaveLength(2);
    await done(1, 1, "新范围回复");
    await finish(1);
    expect(document.querySelector(".school-chat-messages .is-assistant")?.textContent).toContain("新范围回复");
    expect(document.querySelector(".school-chat-messages")?.textContent).not.toContain("旧范围");
  });

  it("退出整个账户运行时仍终止隐藏页面的请求", async () => {
    const view = render(<NavigationHarness currentUser={user} />);
    await send("退出前尚未完成的问题");
    await leaveAI();
    expect(sessions[0]!.signal.aborted).toBe(false);
    view.unmount();
    expect(sessions[0]!.signal.aborted).toBe(true);
    await done(0, 1, "退出后的迟到回复");
    await finish(0);
    expect(screen.queryByText("退出后的迟到回复")).toBeNull();
  });
});
