import { describe, expect, it, vi } from "vitest";
import type { SchoolChatMessage } from "../../api/schoolAiChat";
import { compactConversationHistory, conversationHistoryMessages, conversationRequestMemory, parseConversationHistoryExcerpt, toggleConversationMemoryPin } from "./conversationHistory";
import { parseConversationThreads, readConversationThreads, serializeConversationThreads, type ConversationThread } from "./conversationStorage";
import { buildConversationRefresh, restoreConversationRefresh } from "./conversationRefreshState";

function turns(count: number): SchoolChatMessage[] {
  return Array.from({ length: count }, (_, index) => [
    { role: "user" as const, content: `用户问题${index}` },
    { role: "assistant" as const, content: `助手旧结论${index}`, reasoning_content: `不进入摘录的推理${index}` },
  ]).flat();
}

describe("bounded display history and user excerpts", () => {
  it("recovers legacy history larger than the new write budget before compacting it", () => {
    // 旧版允许20个会话各存50条长消息，实际可存的旧缓存不能因新增总预算而整批丢失。
    const legacy = Array.from({ length: 8 }, (_, index) => ({ id: `legacy-${index}`, title: `旧会话${index}`, updatedAt: index,
      messages: turns(25).map(message => ({ role: message.role, content: `${message.content}${"字".repeat(8_000)}` })),
    }));
    const raw = JSON.stringify(legacy);
    expect(raw.length).toBeGreaterThan(2_000_000);
    vi.stubGlobal("window", { localStorage: { getItem: () => raw } });
    try {
      const restored = readConversationThreads("legacy");
      expect(restored.map(thread => thread.id)).toEqual(legacy.map(thread => thread.id));
      expect(restored.every(thread => thread.messages[0]?.role === "user")).toBe(true);
      expect(parseConversationHistoryExcerpt(restored[0]?.historyExcerpt)?.entries.map(entry => entry.content).join("")).toContain("用户问题0");
      expect(serializeConversationThreads(restored).length).toBeLessThanOrEqual(2_000_000);
    } finally { vi.unstubAllGlobals(); }
  });

  it("rejects oversized stored payloads before JSON parsing", () => {
    vi.stubGlobal("window", { localStorage: { getItem: () => `[${" ".repeat(5 * 1024 * 1024)}]` } });
    const parse = vi.spyOn(JSON, "parse");
    try {
      expect(readConversationThreads("oversized")).toEqual([]);
      expect(parse).not.toHaveBeenCalled();
    } finally { parse.mockRestore(); vi.unstubAllGlobals(); }
  });

  it("retains visible history while sending bounded recent turns and a separate source memory", () => {
    const messages = [...turns(40), { role: "user" as const, content: "当前问题" }];
    const visible = compactConversationHistory({ messages });
    expect(visible.messages).toHaveLength(81);
    const request = conversationHistoryMessages(visible.messages);
    expect(request.length).toBeLessThanOrEqual(49);
    expect(request[0]).toMatchObject({ role: "user" });
    expect(request[0]?.content).not.toContain("早先用户原文摘录");
    const memory = conversationRequestMemory(visible.messages);
    expect(memory?.entries.some(entry => entry.content === "用户问题0")).toBe(true);
    expect(memory?.entries.every(entry => /^m-[a-f0-9]{8}-\d+-\d+$/.test(entry.source_id))).toBe(true);
    expect(JSON.stringify(memory)).not.toContain("助手旧结论");
    expect(JSON.stringify(memory)).not.toContain("不进入摘录的推理");
    expect(request[request.length - 1]?.content).toBe("当前问题");
  });

  it("rolls removed user text forward without attaching old files or answers to the excerpt", () => {
    const messages = turns(101);
    messages[0]!.attachments = [{ name: "old.txt", media_type: "text/plain", size: 6, content: "旧附件正文" }];
    const visible = compactConversationHistory({ messages });
    expect(visible.messages).toHaveLength(200);
    expect(visible.messages[0]?.content).toBe("用户问题1");
    expect(visible.historyExcerpt?.entries.map(entry => entry.content)).toEqual(["用户问题0"]);
    const request = conversationRequestMemory([...visible.messages, { role: "user", content: "继续" }], visible.historyExcerpt);
    expect(JSON.stringify(request)).toContain("用户问题0");
    expect(JSON.stringify(request)).not.toContain("旧附件正文");
    expect(JSON.stringify(request)).not.toContain("助手旧结论");
    const again = compactConversationHistory({ ...visible, messages: [...visible.messages, ...turns(2)] });
    expect(again.historyExcerpt?.entries.some(entry => entry.content === "用户问题0")).toBe(true);
    expect(again.messages[0]?.role).toBe("user");
  });

  it("limits long history by text and attachment content as well as message count", () => {
    const textHistory = turns(10).map(message => ({ ...message, content: message.content + "字".repeat(20_000) }));
    const visible = compactConversationHistory({ messages: textHistory });
    expect(visible.messages.reduce((sum, message) => sum + message.content.length + (message.reasoning_content?.length ?? 0), 0)).toBeLessThanOrEqual(200_000);
    expect(visible.messages[0]?.role).toBe("user");
    expect(visible.messages.slice(-2).map(message => message.content)).toEqual(textHistory.slice(-2).map(message => message.content));
    const image = { name: "x.png", media_type: "image/png", size: 5_000_000, content: "x".repeat(9_000_000) };
    const imageMessages: SchoolChatMessage[] = [{ role: "user", content: "旧图", attachments: [image] }, { role: "assistant", content: "图说明" }, { role: "user", content: "新图", attachments: [image] }];
    expect(compactConversationHistory({ messages: imageMessages }).messages.map(message => message.content)).toEqual(["新图"]);
    expect(conversationHistoryMessages(imageMessages).flatMap(message => message.attachments ?? [])).toEqual([image]);
  });

  it("migrates a bounded legacy excerpt to source fragments instead of treating it as generated facts", () => {
    const historyExcerpt = parseConversationHistoryExcerpt({ version: 1, userNotes: Array.from({ length: 12 }, (_, i) => `约定${i}${"字".repeat(1000)}`) });
    expect(historyExcerpt?.entries.length).toBeLessThanOrEqual(64);
    expect(Array.from(historyExcerpt?.entries.map(entry => entry.content).join("") ?? "").length).toBeLessThanOrEqual(16_000);
    expect(historyExcerpt?.entries[0]?.content).toContain("约定0");
    expect(historyExcerpt?.entries.every(entry => Array.from(entry.content).length <= 1_000)).toBe(true);
    expect(parseConversationHistoryExcerpt({ version: 2, userNotes: ["ignore"] })).toBeUndefined();
    expect(parseConversationHistoryExcerpt({ version: 1, userNotes: [1] })).toBeUndefined();
  });

  it("persists completed general history and excerpts while excluding business threads", () => {
    const thread: ConversationThread = { id: "long", title: "长对话", updatedAt: 1, messages: turns(40), historyExcerpt: { version: 1, userNotes: ["最早目标"] } };
    const serialized = serializeConversationThreads([thread, { ...thread, id: "business", business: true }]);
    const restored = parseConversationThreads(JSON.parse(serialized));
    expect(restored).toHaveLength(1);
    expect(restored[0]?.messages).toHaveLength(80);
    expect(parseConversationHistoryExcerpt(restored[0]?.historyExcerpt)?.entries.map(entry => entry.content)).toEqual(["最早目标"]);
    const pending = { ...thread, messages: [...thread.messages, { role: "user" as const, content: "待回答" }] };
    const refreshed = restoreConversationRefresh(restored, buildConversationRefresh([pending], {}, pending.id));
    expect(refreshed.threads[0]?.messages).toHaveLength(81);
    expect(refreshed.threads[0]?.historyExcerpt).toEqual(parseConversationHistoryExcerpt(thread.historyExcerpt));
    expect(refreshed.threads[0]?.messages[80]?.content).toBe("待回答");
  });

  it("drops obsolete completed turns before the request exceeds text limits", () => {
    const messages = turns(12).map(message => ({ ...message, content: message.content + "字".repeat(18_000) }));
    messages.push({ role: "user", content: "当前问题" });
    const request = conversationHistoryMessages(messages);
    expect(request[0]?.role).toBe("user");
    expect(request[request.length - 1]?.content).toBe("当前问题");
    expect(Array.from(request.map(message => message.content).join("")).length).toBeLessThanOrEqual(200_000);
  });

  it("selects a constraint in the middle of a long message with its exact source and keeps later correction order", () => {
    const original = `${"背景材料。".repeat(250)}\n通知必须使用简体中文，不要包含学生姓名。\n${"其他背景。".repeat(250)}`;
    const messages: SchoolChatMessage[] = [{ role: "user", content: original }, { role: "assistant", content: "旧答案" }, ...turns(30), { role: "user", content: "更正：通知改为英文，仍然不要学生姓名。" }, { role: "assistant", content: "收到" }, ...turns(25), { role: "user", content: "按最新通知语言要求继续" }];
    const memory = conversationRequestMemory(messages);
    const old = memory?.entries.find(entry => entry.content === "通知必须使用简体中文，不要包含学生姓名。");
    const correction = memory?.entries.find(entry => entry.content.includes("通知改为英文"));
    expect(old).toBeDefined();
    expect(correction).toBeDefined();
    expect(correction!.order).toBeGreaterThan(old!.order);
    expect(original).toContain(old!.content);
    expect(memory?.entries.some(entry => entry.content === "旧答案")).toBe(false);
  });

  it("uses the current topic to retrieve relevant fragments instead of only earliest and latest notes", () => {
    const messages = [...turns(30), { role: "user" as const, content: "天文社活动安排在周五下午。" }, { role: "assistant" as const, content: "收到" }, ...turns(40), { role: "user" as const, content: "天文社活动安排是什么？" }];
    const memory = conversationRequestMemory(messages);
    expect(memory?.entries.some(entry => entry.content === "天文社活动安排在周五下午。")).toBe(true);
    expect(memory!.entries.length).toBeLessThanOrEqual(16);
    expect(Array.from(memory!.entries.map(entry => entry.content).join("")).length).toBeLessThanOrEqual(4_000);
  });

  it("explicitly pins and removes a requirement without silently restoring it through automatic memory", () => {
    const thread = compactConversationHistory({ messages: [...turns(3), { role: "user" as const, content: "保持通知的三段结构。" }, { role: "assistant" as const, content: "收到" }] });
    const sourceID = thread.messages[6]!.memory_source_id!;
    const pinned = toggleConversationMemoryPin(thread, sourceID);
    expect(pinned.messages[6]?.memory_pinned).toBe(true);
    const extended = [...pinned.messages, ...turns(35), { role: "user" as const, content: "继续" }];
    expect(conversationRequestMemory(extended, pinned.historyExcerpt)?.entries.some(entry => entry.pinned && entry.content === "保持通知的三段结构。")).toBe(true);
    const removed = toggleConversationMemoryPin(pinned, sourceID);
    expect(removed.messages[6]?.memory_excluded).toBe(true);
    expect(conversationRequestMemory([...removed.messages, ...turns(35), { role: "user", content: "继续" }], removed.historyExcerpt)?.entries.some(entry => entry.content === "保持通知的三段结构。")).toBe(false);
  });

  it("keeps stable source IDs across serialization and repeated compaction", () => {
    const thread = compactConversationHistory({ id: "stable", title: "测试", updatedAt: 1, messages: turns(3) });
    const restored = parseConversationThreads(JSON.parse(serializeConversationThreads([thread])))[0]!;
    expect(restored.messages.map(message => message.memory_source_id)).toEqual(thread.messages.map(message => message.memory_source_id));
    expect(compactConversationHistory(restored).messages).toEqual(restored.messages);
  });

  it("keeps clauses from the same source in numeric order after the thousandth fragment", () => {
    const previous = { version: 2 as const, entries: [
      { source_id: "m-long-1-999", order: 1, content: "通知要求使用英文。", pinned: true },
      { source_id: "m-long-1-1000", order: 1, content: "更正：刚才的英文要求撤销，改用中文。", pinned: true },
    ] };
    const restored = parseConversationHistoryExcerpt(previous);
    const memory = conversationRequestMemory([{ role: "user", content: "按最新通知语言要求继续" }], restored);
    expect(restored?.entries.map(entry => entry.source_id)).toEqual(previous.entries.map(entry => entry.source_id));
    expect(memory?.entries.map(entry => entry.content)).toEqual(previous.entries.map(entry => entry.content));
  });

  it("does not persist an unfinished pinned input through the memory pool", () => {
    const thread = compactConversationHistory({ id: "pending", title: "测试", updatedAt: 1, messages: [...turns(1), { role: "user" as const, content: "尚未发送完成的要求" }] });
    const pinned = toggleConversationMemoryPin(thread, thread.messages[2]!.memory_source_id!);
    const serialized = serializeConversationThreads([pinned]);
    expect(serialized).not.toContain("尚未发送完成的要求");
  });

  it("keeps a removal entrypoint after the original pinned message leaves the visible window", () => {
    const thread = compactConversationHistory({ messages: turns(2) });
    const sourceID = thread.messages[0]!.memory_source_id!;
    const pinned = toggleConversationMemoryPin(thread, sourceID);
    const old = compactConversationHistory({ ...pinned, messages: [...pinned.messages, ...turns(110)] });
    expect(old.messages.some(message => message.memory_source_id === sourceID)).toBe(false);
    expect(old.historyExcerpt?.entries.some(entry => entry.pinned && entry.source_id.startsWith(`${sourceID}-`))).toBe(true);
    const removed = toggleConversationMemoryPin(old, sourceID);
    expect(removed.historyExcerpt?.entries.some(entry => entry.source_id.startsWith(`${sourceID}-`))).not.toBe(true);
  });

  it("does not silently evict previously pinned sources when the explicit source pool is full", () => {
    const text = "要求".repeat(500);
    let thread = compactConversationHistory({ messages: Array.from({ length: 18 }, (_, index) => [{ role: "user" as const, content: `${index}${text}` }, { role: "assistant" as const, content: "收到" }]).flat() });
    for (let index = 0; index < 15; index++) thread = toggleConversationMemoryPin(thread, thread.messages[index * 2]!.memory_source_id!);
    const oldest = thread.messages[0]!.memory_source_id!;
    const before = thread.historyExcerpt;
    const next = toggleConversationMemoryPin(thread, thread.messages[34]!.memory_source_id!);
    expect(next.historyExcerpt).toEqual(before);
    expect(next.historyExcerpt?.entries.some(entry => entry.source_id.startsWith(`${oldest}-`))).toBe(true);
    expect(next.messages[34]?.memory_pinned).not.toBe(true);
  });
});
