// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { SessionUser } from "../../auth/session";
import { ConversationFlow, PendingConversationView } from "./SchoolConversationDisplay";
import { emptyConversationEvidence, type ConversationEvidence } from "./conversationState";
import { conversationRecovery } from "./conversationRecovery";
import { conversationModelStatus, conversationPlaceholder, type PendingConversation } from "./conversationPresentation";

const user: SessionUser = {
  id: "owner", username: "owner", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "tenant", school: "学校", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] },
};
const model = { available: true, display_name: "测试模型", model_name: "synthetic", provider_key: "provider", message: "" };

describe("native pending status and real rich-text content boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function renderPending(content = "", reasoning = "", evidence = emptyConversationEvidence()) {
    await act(async () => root.render(<PendingConversationView pending={{ content, reasoning, evidence }} user={user} onReasoningToggle={() => undefined} />));
  }

  it("shows initial thinking inside a nonempty native output without changing its layout class", async () => {
    await renderPending();
    const output = host.querySelector("output")!;
    expect(output.textContent).toBe("思考中");
    expect(output.className).toBe("school-chat-thinking");
    expect(output.style.position).toBe("");
    expect(host.querySelector('.school-chat-waiting[role="status"]')).toBeNull();
    expect(output.querySelector("span")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("keeps real Markdown blocks outside the short native reply status", async () => {
    await renderPending("# 标题\n\n正文");
    const output = host.querySelector("output")!;
    expect(output.textContent).toBe("正在回复");
    expect(output.style.position).toBe("absolute");
    expect(output.getAttribute("aria-hidden")).toBeNull();
    expect(output.querySelector("div,p,h1,section,ul")).toBeNull();
    expect(host.querySelector(".math-markdown h1")?.textContent).toBe("标题");
    expect(host.querySelector(".math-markdown p")?.textContent).toBe("正文");
  });

  it("keeps evidence and expanded reasoning outside output while retaining their own markup", async () => {
    await renderPending("", "推理段落", { tools: [{ call_id: "tool", tool_name: "exam_progress", label: "查询考试", status: "running" }], sources: [], actions: [] });
    await act(async () => host.querySelector<HTMLButtonElement>(".school-chat-reasoning-toggle")!.click());
    expect(host.querySelector("output")?.textContent).toBe("正在回复");
    expect(host.querySelector("output .school-conversation-tools")).toBeNull();
    expect(host.querySelector(".school-conversation-tools")?.textContent).toContain("查询考试");
    expect(host.querySelector(".school-chat-reasoning-content p")?.textContent).toBe("推理段落");
  });

  it("does not rewrite the short status for each new content delta", async () => {
    await renderPending("第一段");
    const output = host.querySelector("output");
    await renderPending("第一段后续");
    expect(host.querySelector("output")).toBe(output);
    expect(output?.textContent).toBe("正在回复");
    expect(host.querySelector(".math-markdown p")?.textContent).toBe("第一段后续");
  });

  it("does not add native live statuses to static messages", async () => {
    await act(async () => root.render(<ConversationFlow thread={{ id: "thread", title: "一般聊天", updatedAt: 1, messages: [{ role: "user", content: "历史消息" }] }}
      evidence={new Map()} user={user} reasoningExpanded={false} onReasoningToggle={() => undefined} copiedMessage=""
      onCopy={async () => undefined} retryDisabled={false} onRetry={() => undefined} modelError="" model={model}
      onSuggestion={() => undefined} scrollEndRef={{ current: null }} />));
    expect(host.querySelector("output")).toBeNull();
    expect(host.querySelector(".school-chat-flow")?.getAttribute("aria-live")).toBe("polite");
    expect(host.querySelector(".school-chat-message-body")?.textContent).toBe("历史消息");
  });
});

describe("conversation terminal recovery", () => {
  const evidence: ConversationEvidence = {
    tools: [{ call_id: "tool", tool_name: "exam_progress", label: "查询考试", status: "running" }], sources: [],
    actions: [{ id: "action", label: "导航", target: { kind: "exam_section", exam_id: "exam", section: "grading" } }],
    scope: { exam_id: "exam", scope_label: "考试", class_ids: [], data_basis: "exam_live" },
  };
  const partial: PendingConversation = { content: "部分正文", reasoning: "部分推理", evidence };

  it("retains partial text but removes navigation and stops running tools after abort", () => {
    const recovery = conversationRecovery(new Error("stop"), true, false, partial);
    expect(recovery?.kind).toBe("reply");
    if (recovery?.kind !== "reply") throw new Error("Expected a partial reply");
    expect(recovery.reply).toEqual({ role: "assistant", content: "部分正文", reasoning_content: "部分推理" });
    expect(recovery.evidence.actions).toEqual([]);
    expect(recovery.evidence.tools[0]?.status).toBe("failed");
    expect(recovery.evidence.scope).toBe(evidence.scope);
    expect(evidence.tools[0]?.status).toBe("running");
  });

  it("retains reasoning-only partial output instead of replacing it with a failure", () => {
    const recovery = conversationRecovery(new Error("stop"), true, false, { ...partial, content: "" });
    expect(recovery?.kind === "reply" && recovery.reply.content).toBe("");
    expect(recovery?.kind === "reply" && recovery.reply.reasoning_content).toBe("部分推理");
  });

  it("returns a retryable stopped state if no partial text exists", () => {
    const recovery = conversationRecovery(new Error("stop"), true, false, { ...partial, content: "", reasoning: "" });
    expect(recovery?.kind === "failure" && recovery.message).toBe("已停止回复");
  });

  it("does not duplicate a completed reply when stopping", () => {
    expect(conversationRecovery(new Error("stop"), true, true, partial)).toBeNull();
  });

  it("preserves non-abort error behavior even after completion", () => {
    const recovery = conversationRecovery(new Error("连接已断开"), false, true, partial);
    expect(recovery?.kind === "failure" && recovery.message).toBe("连接已断开");
  });

  it("uses the existing safe fallback for non-user-facing internal errors", () => {
    const recovery = conversationRecovery(new Error("internal failure"), false, false, partial);
    expect(recovery?.kind === "failure" && recovery.message).toBe("发送失败，请重试");
  });
});

describe("conversation status text", () => {
  it("preserves loading, available, and unavailable model wording", () => {
    expect(conversationModelStatus(null, true)).toBe("正在读取模型");
    expect(conversationModelStatus(model, false)).toBe("测试模型 · synthetic");
    expect(conversationModelStatus({ ...model, available: false }, false)).toBe("模型未就绪");
  });
  it("preserves model, concurrency, and pending placeholder priority", () => {
    expect(conversationPlaceholder(false, true, false)).toBe("等待学校模型就绪");
    expect(conversationPlaceholder(true, true, false)).toBe("已达到 5 个并行对话上限");
    expect(conversationPlaceholder(true, true, true)).toBe("问考试、阅卷和发布问题，或描述你需要的帮助…");
    expect(conversationPlaceholder(true, false, false)).toBe("问考试、阅卷和发布问题，或描述你需要的帮助…");
  });
});
