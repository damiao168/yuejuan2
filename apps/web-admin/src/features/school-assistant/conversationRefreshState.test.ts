import { describe, expect, it } from "vitest";
import { buildConversationRefresh, restoreConversationRefresh } from "./conversationRefreshState";
import { serializeConversationThreads, type ConversationThread } from "./conversationStorage";

const base: ConversationThread = { id: "thread", title: "公开问题", updatedAt: 100, messages: [{ role: "user", content: "待回答的问题" }] };

describe("refresh checkpoints and model history", () => {
  it("keeps the unanswered question separate from completed local history", () => {
    const snapshot = buildConversationRefresh([base], {}, base.id);
    expect(snapshot.entries[0]?.question.content).toBe("待回答的问题");
    const restored = restoreConversationRefresh([], snapshot);
    expect(restored.activeId).toBe(base.id);
    expect(restored.threads[0]?.messages).toMatchObject(base.messages);
    expect(restored.threads[0]?.messages[0]?.memory_source_id).toMatch(/^m-/);
    expect(restored.threads[0]?.interruptedReply).toEqual({ content: "" });
    expect(JSON.parse(serializeConversationThreads(restored.threads))).toEqual([]);
    expect(buildConversationRefresh(restored.threads, {}, restored.activeId).entries).toEqual(snapshot.entries);
  });

  it("omits entire business, exam-context and attachment threads", () => {
    const attachment = { name: "doc.txt", content: "private", media_type: "text/plain", size: 7 };
    const threads = [{ ...base, business: true }, { ...base, id: "exam", context: { exam_id: "exam-id" } },
      { ...base, id: "file", messages: [{ role: "user" as const, content: "旧附件", attachments: [attachment] }, ...base.messages] }];
    expect(buildConversationRefresh(threads, {}, "file")).toMatchObject({ activeId: null, entries: [] });
  });

  it("keeps an explicit new-chat selection and merges a followup once", () => {
    const history = { ...base, messages: [{ role: "user" as const, content: "之前的问题" }, { role: "assistant" as const, content: "之前的回答" }] };
    const snapshot = buildConversationRefresh([base], {}, null);
    const restored = restoreConversationRefresh([history], snapshot);
    expect(restored.activeId).toBeNull();
    expect(restored.threads[0]?.messages.map(message => message.content)).toEqual(["之前的问题", "之前的回答", "待回答的问题"]);
    expect(restoreConversationRefresh([history], buildConversationRefresh(restored.threads, {}, null)).threads[0]?.messages).toEqual(restored.threads[0]?.messages);
  });
});
