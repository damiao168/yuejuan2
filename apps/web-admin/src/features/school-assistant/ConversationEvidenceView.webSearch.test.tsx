// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { ConversationEvidenceView } from "./ConversationEvidenceView";
import { emptyConversationEvidence } from "./conversationState";
import type { SessionUser } from "../../auth/session";
import { parseConversationThreads } from "./conversationStorage";
import { publicConversationEvidence } from "./conversationWebSearch";
import { ConversationMessages, PendingConversationView } from "./SchoolConversationDisplay";

vi.mock("antd", () => ({
  Button: ({ children }: { children: ReactNode }) => <button type="button">{children}</button>,
  Collapse: ({ items }: { items: { key: string; label: ReactNode; children: ReactNode }[] }) => <>{items.map(item => <div key={item.key}>{item.label}{item.children}</div>)}</>,
}));
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: Readonly<{ children: string }>) => <p>{children}</p> }));

describe("public web-search evidence", () => {
  it("最终只引用第二项时，正文和来源保留相同编号，不补出未引用的第一项", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const source = { id: "web-2", kind: "web" as const, status: "available" as const, label: "采用的气象资料", read_at: "2026-11-01T00:00:00Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://weather.example.org/beijing" };
    const thread = { id: "web-thread", title: "天气", updatedAt: 1, messages: [{ role: "assistant" as const, content: "北京天气以气象资料为准 [2]", web_search: true, web_sources: [source] }] };
    try {
      await act(async () => root.render(<ConversationMessages thread={thread} evidence={new Map()} user={{} as SessionUser} reasoningExpanded={false} copiedMessage="" onCopy={async () => undefined} />));
      expect(host.textContent).toContain("北京天气以气象资料为准 [2]");
      expect(host.querySelector("a")?.textContent).toContain("[2]采用的气象资料");
      expect(host.querySelectorAll("a")).toHaveLength(1);
      expect(host.textContent).toContain("参考来源（1）");
      expect(host.textContent).not.toContain("[1]");
    } finally { await act(async () => root.unmount()); }
  });

  it("允许联网时先理解问题，只在真实工具运行时显示搜索，空来源不显示参考资料", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const pending = { content: "", reasoning: "", webSearch: true, evidence: emptyConversationEvidence() };
    const tool = { call_id: "web-search", tool_name: "web_search", label: "联网搜索", status: "running" as const };
    try {
      await act(async () => root.render(<PendingConversationView pending={pending} user={{} as SessionUser} onReasoningToggle={() => undefined} />));
      expect(host.querySelector("output")?.textContent).toContain("正在理解问题");
      expect(host.textContent).not.toContain("正在搜索公开资料");
      await act(async () => root.render(<PendingConversationView pending={{ ...pending, evidence: { ...pending.evidence, tools: [tool] } }} user={{} as SessionUser} onReasoningToggle={() => undefined} />));
      expect(host.querySelector('[aria-label="联网搜索进度"]')?.textContent).toContain("正在搜索公开资料");
      await act(async () => root.render(<PendingConversationView pending={{ ...pending, evidence: { ...pending.evidence, tools: [{ ...tool, status: "completed" }] } }} user={{} as SessionUser} onReasoningToggle={() => undefined} />));
      expect(host.querySelector("output")?.textContent).toContain("正在整理回答");
      expect(host.textContent).not.toContain("参考来源");
      expect(host.textContent).not.toContain("搜索摘要");
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps source number two when history restoration filters source number one", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const source = { kind: "web", status: "available", label: "来源", read_at: "2026-11-01T00:00:00Z", consistency: "source_local", scope_label: "公开网页" };
    const restored = parseConversationThreads([{ id: "thread", title: "公开资料", updatedAt: 1, messages: [{ role: "assistant", content: "依据第二个来源 [2]", web_sources: [
      { ...source, id: "web-1", url: "https://[2606:4700:4700::1111]/" },
      { ...source, id: "web-2", url: "https://example.org/page" },
    ] }] }]);
    const message = restored[0]!.messages[0]!;
    expect(message.web_sources?.map(item => item.id)).toEqual(["web-2"]);
    const host = document.createElement("div");
    const root = createRoot(host);
    try {
      await act(async () => root.render(<ConversationEvidenceView evidence={publicConversationEvidence(message.web_sources)} user={{} as SessionUser} />));
      expect(host.querySelector("a")?.textContent).toContain("[2]");
      expect(host.querySelector("a")?.textContent).not.toContain("[1]");
    } finally { await act(async () => root.unmount()); }
  });

  it("读取正文保留公开进度，完成后区分正文和摘要并显示实际读取时间", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const tool = { call_id: "fetch-1", tool_name: "web_fetch", label: "读取网页正文", status: "running" as const };
    const pending = { content: "", reasoning: "", webSearch: true, evidence: { ...emptyConversationEvidence(), tools: [tool] } };
    const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "完整的公开通知", read_at: "2026-11-02T03:04:05Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://example.org/notice", excerpt: "核实过的正文片段", note: "已读取网页正文。" };
    try {
      await act(async () => root.render(<PendingConversationView pending={pending} user={{} as SessionUser} onReasoningToggle={() => undefined} />));
      expect(host.querySelector('[aria-label="联网搜索进度"]')?.textContent).toContain("正在读取网页");
      expect(host.querySelector('[aria-label="业务查询进度"]')).toBeNull();
      expect(host.querySelector("output")?.textContent).toContain("正在读取网页");
      await act(async () => root.render(<ConversationEvidenceView evidence={{ ...emptyConversationEvidence(), tools: [{ ...tool, status: "completed" }], sources: [source, { ...source, id: "web-2", label: "只返回摘要的通知", note: undefined }] }} user={{} as SessionUser} />));
      expect(host.querySelectorAll("section")[0]?.textContent).toContain("网页正文");
      expect(host.querySelectorAll("section")[1]?.textContent).toContain("搜索摘要");
      expect(host.querySelector("time")?.getAttribute("datetime")).toBe("2026-11-02T03:04:05.000Z");
      expect(host.textContent).toContain("读取于");
      expect(host.textContent).not.toContain("已查询1项资料");
    } finally { await act(async () => root.unmount()); }
  });

  it("回复未逐句标号时仍展示后端保留的可核对资料", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "气象部门资料", read_at: "2026-11-02T03:04:05Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://example.org/weather", excerpt: "已核实的公开天气片段", note: "已读取网页正文。回答未逐句标注，以下为已核实资料。" };
    const thread = { id: "weather", title: "天气", updatedAt: 1, messages: [{ role: "assistant" as const, content: "北京的天气信息已整理。", web_search: true, web_sources: [source] }] };
    try {
      await act(async () => root.render(<ConversationMessages thread={thread} evidence={new Map()} user={{} as SessionUser} reasoningExpanded={false} copiedMessage="" onCopy={async () => undefined} />));
      expect(host.textContent).toContain("北京的天气信息已整理。");
      expect(host.querySelector("a")?.textContent).toContain("气象部门资料");
      expect(host.textContent).toContain("参考来源（1）");
      expect(host.textContent).toContain("网页正文");
      expect(host.textContent).toContain("已核实资料，回答未逐句标注。");
    } finally { await act(async () => root.unmount()); }
  });

  it("shows real search progress and opens only safe returned public sources", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const user = {} as SessionUser;
    const evidence = { ...emptyConversationEvidence(), tools: [{ call_id: "search", tool_name: "web_search", label: "联网搜索", status: "running" as const }] };
    try {
      await act(async () => root.render(<ConversationEvidenceView evidence={evidence} user={user} />));
      expect(host.querySelector('[aria-label="联网搜索进度"]')?.textContent).toContain("正在搜索公开资料");
      const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "公开通知", read_at: "2026-11-01T00:00:00Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://www.moe.gov.cn/notice", excerpt: "通知摘要" };
      await act(async () => root.render(<ConversationEvidenceView evidence={{ ...emptyConversationEvidence(), sources: [source, { ...source, id: "web-2", label: "无效链接", url: "javascript:alert(1)" }] }} user={user} />));
      expect(host.textContent).toContain("参考来源（2）");
      expect(host.textContent).toContain("搜索摘要");
      expect(host.querySelectorAll("a")).toHaveLength(1);
      const link = host.querySelector("a")!;
      expect(link.href).toBe("https://www.moe.gov.cn/notice");
      expect(link.textContent).toContain("[1]");
      expect(link.target).toBe("_blank");
      expect(link.rel).toBe("noopener noreferrer");
      expect(host.textContent).not.toContain("数据来源");
    } finally { await act(async () => root.unmount()); }
  });
});
