import { describe, expect, it } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolConversationEvent } from "../../api/schoolAiChat";
import { acceptConversationEvent, contextFromEvidence, conversationAccountKey, emptyConversationEvidence, interruptedConversationEvidence, mergeConversationEvidence } from "./conversationState";
import { parseConversationThreads, serializeConversationThreads } from "./conversationStorage";
import { requestMessages } from "../../pages/schoolAiChatMessages";

function delta(request_id: string, seq: number): SchoolConversationEvent {
  return { type: "delta", request_id, seq, channel: "content", delta: "结果" };
}
const user: SessionUser = {
  id: "u1", username: "admin", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "t1", school: "学校", currentExam: "", permissions: ["dashboard:read"], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["s1"], gradeIds: [], classIds: [] }
};

describe("school conversation ownership and evidence", () => {
  it("rejects replayed or interleaved request events", () => {
    const identity = { sequence: 0 };
    expect(acceptConversationEvent(identity, delta("r1", 1))).toBe(true);
    expect(acceptConversationEvent(identity, delta("r1", 1))).toBe(false);
    expect(acceptConversationEvent(identity, delta("r2", 2))).toBe(false);
    expect(acceptConversationEvent(identity, delta("r1", 2))).toBe(true);
    expect(acceptConversationEvent(identity, delta("r1", Number.NaN))).toBe(false);
  });

  it("invalidates the owner when roles, permissions, or data scope change", () => {
    const key = conversationAccountKey(user);
    expect(conversationAccountKey({ ...user, permissions: [] })).not.toBe(key);
    expect(conversationAccountKey({ ...user, roles: ["teacher"] })).not.toBe(key);
    expect(conversationAccountKey({ ...user, organizationScope: { ...user.organizationScope, schoolIds: [] } })).not.toBe(key);
    expect(conversationAccountKey({ ...user, publicComputer: true })).not.toBe(key);
  });

  it("finishes a tool by its call id while preserving another running tool", () => {
    const started: SchoolConversationEvent = { type: "tool_started", request_id: "r1", seq: 1, call_id: "c1", tool_name: "exam_progress", label: "查询考试进度" };
    let evidence = mergeConversationEvidence(emptyConversationEvidence(), started);
    evidence = mergeConversationEvidence(evidence, { ...started, seq: 2, call_id: "c2" });
    evidence = mergeConversationEvidence(evidence, { type: "tool_finished", request_id: "r1", seq: 3, call_id: "c1", tool_name: "exam_progress", label: "查询考试进度", status: "failed", message: "来源暂不可用" });
    expect(evidence.tools.map((tool) => [tool.call_id, tool.status])).toEqual([["c1", "failed"], ["c2", "running"]]);
  });

  it("takes the next exam context only from server scope", () => {
    const evidence = emptyConversationEvidence();
    const previous = { exam_id: "e1", origin_module: "exam_workspace" };
    expect(contextFromEvidence(evidence, previous)).toBe(previous);
    expect(contextFromEvidence({ ...evidence, scope: { exam_id: "e2", exam_name: "期中数学", class_ids: [], data_basis: "workspace_live", scope_label: "期中数学" } }, previous)).toEqual({ exam_id: "e2", exam_name: "期中数学", origin_module: "exam_workspace" });
  });

  it("removes navigation and running indicators after interruption", () => {
    const evidence = mergeConversationEvidence(emptyConversationEvidence(), { type: "tool_started", request_id: "r1", seq: 1, call_id: "c1", tool_name: "exam_progress", label: "查询进度" });
    evidence.actions = [{ id: "a1", label: "进入阅卷", target: { kind: "exam_section", exam_id: "e1", section: "grading" } }];
    const stopped = interruptedConversationEvidence(evidence);
    expect(stopped.actions).toEqual([]);
    expect(stopped.tools[0]?.status).toBe("failed");
    expect(stopped.tools[0]?.message).toBe("已停止");
  });

  it("never persists scope, tool results, action payloads, or attachment bodies", () => {
    const message = { role: "assistant" as const, content: "需要复核", tools: [{ result: "原始业务数据" }], actions: [{ target: "/exams/e1" }], attachments: [{ name: "test.txt", media_type: "text/plain", size: 7, content: "附件正文" }] };
    const serialized = serializeConversationThreads([{ id: "t", title: "对话", updatedAt: 1, context: { exam_id: "e1" }, messages: [message] }]);
    expect(serialized).not.toContain("原始业务数据");
    expect(serialized).not.toContain("附件正文");
    expect(serialized).not.toContain("exam_id");
    expect(serialized).not.toContain("actions");
    const restored = parseConversationThreads(JSON.parse(serialized));
    expect(restored[0]?.context).toBeUndefined();
    expect(restored[0]?.messages[0]?.attachments?.[0]?.content).toBe("");
  });

  it("strips forged evidence when restoring browser history and preparing model history", () => {
    const restored = parseConversationThreads([{ id: "t", title: "对话", updatedAt: 1, context: { exam_id: "e1" }, messages: [{ role: "assistant", content: "历史说明", sources: [{ tenant: "other" }] }] }]);
    expect(restored).toEqual([{ id: "t", title: "对话", updatedAt: 1, messages: [{ role: "assistant", content: "历史说明" }] }]);
    expect(requestMessages(restored[0]!.messages)).toEqual([{ role: "assistant", content: "历史说明" }]);
  });

  it("keeps entire business threads out of storage even after a tool failed", () => {
    const business = { id: "business", title: "业务问题", updatedAt: 1, business: true, messages: [{ role: "user" as const, content: "刚才的结果帮我整理一下" }] };
    const general = { id: "general", title: "通知写作", updatedAt: 2, messages: [{ role: "assistant" as const, content: "请按时参加会议" }] };
    const stored = JSON.parse(serializeConversationThreads([business, general]));
    expect(stored).toEqual([general]);
    expect(parseConversationThreads([business, general])).toEqual([general]);
  });

  it("does not save an unfinished question before the model decides whether to read business data", () => {
    expect(JSON.parse(serializeConversationThreads([{ id: "pending", title: "考试问题", updatedAt: 1, messages: [{ role: "user", content: "这场考试如何？" }] }]))).toEqual([]);
  });

  it("clears the followup exam when sources no longer have one resolved scope", () => {
    const previous = { exam_id: "e1", origin_module: "exam_workspace" };
    const source = { id: "source", kind: "workspace" as const, status: "available" as const, label: "工作区", read_at: "2026-10-02T00:00:00Z", consistency: "source_local" as const, scope_label: "考试" };
    const evidence = { ...emptyConversationEvidence(), scope: { exam_id: "e1", class_ids: [], data_basis: "workspace_live" as const, scope_label: "考试" } };
    const changed = mergeConversationEvidence(evidence, { type: "sources", request_id: "r1", seq: 2, sources: [source], actions: [] });
    expect(changed.scope).toBeUndefined();
    expect(contextFromEvidence(changed, previous)).toBeUndefined();
  });
});
