// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearConversationRefresh, CONVERSATION_REFRESH_PREFIX, CONVERSATION_REFRESH_TTL_MS, readConversationRefresh, writeConversationRefresh, type ConversationRefreshSnapshot } from "./conversationRefresh";

const key = `${CONVERSATION_REFRESH_PREFIX}account-and-permissions`;
function snapshot(): ConversationRefreshSnapshot {
  return { version: 1, savedAt: Date.now(), activeId: "thread", entries: [{ threadId: "thread", title: "公开问题", updatedAt: Date.now(), question: { role: "user", content: "查询公开资料", web_search: true }, partialContent: "已找到部分资料", modelConfigID: "model" }] };
}

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-02T08:00:00Z")); sessionStorage.clear(); localStorage.clear(); });
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

describe("current-tab conversation refresh checkpoint", () => {
  it("recovers the last question and independent partial text only under the same account scope key", () => {
    const value = snapshot();
    writeConversationRefresh(key, value);
    expect(readConversationRefresh(key)).toEqual(value);
    expect(readConversationRefresh(`${CONVERSATION_REFRESH_PREFIX}other-account`)).toBeNull();
    expect(readConversationRefresh(`${CONVERSATION_REFRESH_PREFIX}same-account-reduced-permission`)).toBeNull();
    expect(localStorage.length).toBe(0);
  });

  it("preserves explicit new-conversation selection and removes only the selected checkpoint on null", () => {
    writeConversationRefresh(key, { ...snapshot(), activeId: null });
    expect(readConversationRefresh(key)?.activeId).toBeNull();
    writeConversationRefresh(`${key}-other`, snapshot());
    writeConversationRefresh(key, null);
    expect(readConversationRefresh(key)).toBeNull();
    expect(readConversationRefresh(`${key}-other`)).not.toBeNull();
  });

  it("expires checkpoints without refreshing their timestamp on read and drops malformed or future data", () => {
    writeConversationRefresh(key, snapshot());
    vi.advanceTimersByTime(CONVERSATION_REFRESH_TTL_MS - 1);
    expect(readConversationRefresh(key)).not.toBeNull();
    vi.advanceTimersByTime(1);
    expect(readConversationRefresh(key)).toBeNull();
    expect(sessionStorage.getItem(key)).toBeNull();
    for (const raw of ["broken-json", "null", JSON.stringify({ ...snapshot(), version: 2 }), JSON.stringify({ ...snapshot(), savedAt: Date.now() + 60_000 }), JSON.stringify({ ...snapshot(), activeId: 123 }), " ".repeat(1_300_001)]) {
      sessionStorage.setItem(key, raw);
      expect(readConversationRefresh(key)).toBeNull();
      expect(sessionStorage.getItem(key)).toBeNull();
    }
  });

  it("bounds entry count and text, preserves Unicode boundaries, and ignores forged metadata", () => {
    const value = snapshot();
    const entry = value.entries[0]!;
    sessionStorage.setItem(key, JSON.stringify({ ...value, entries: Array.from({ length: 8 }, (_, i) => ({ ...entry, threadId: `thread-${i}`, title: "题".repeat(500), question: { ...entry.question, content: "字".repeat(19_999) + "😀超出限制", reasoning_content: "不可恢复" }, partialContent: "字".repeat(19_999) + "😀超出限制", sources: [{ kind: "workspace" }], reasoning: "不可恢复" })) }));
    const restored = readConversationRefresh(key)!;
    expect(restored.entries).toHaveLength(5);
    expect(restored.entries[0]?.title).toHaveLength(120);
    expect(restored.entries[0]?.question.content).toHaveLength(19_999);
    expect(restored.entries[0]?.partialContent).toHaveLength(19_999);
    expect(JSON.stringify(restored)).not.toContain("不可恢复");
    expect(JSON.stringify(restored)).not.toContain("workspace");
  });

  it("rejects explicit business, context or attachment records and deduplicates thread IDs", () => {
    const value = snapshot();
    const entry = value.entries[0]!;
    sessionStorage.setItem(key, JSON.stringify({ ...value, entries: [{ ...entry, business: true }, { ...entry, context: { exam_id: "exam" } }, { ...entry, question: { ...entry.question, attachments: [] } }, { ...entry }, { ...entry }] }));
    expect(readConversationRefresh(key)?.entries).toEqual([entry]);
  });

  it("never reads or writes a public-computer checkpoint", () => {
    writeConversationRefresh(key, snapshot());
    expect(readConversationRefresh(key, true)).toBeNull();
    expect(sessionStorage.getItem(key)).toBeNull();
    writeConversationRefresh(key, snapshot(), true);
    expect(sessionStorage.getItem(key)).toBeNull();
  });

  it("tolerates disabled storage and clears only refresh keys on logout", () => {
    writeConversationRefresh(key, snapshot());
    writeConversationRefresh(`${key}-other`, snapshot());
    sessionStorage.setItem("unrelated-state", "keep");
    localStorage.setItem(key, "persistent-history-separate");
    clearConversationRefresh();
    expect(sessionStorage.getItem(key)).toBeNull();
    expect(sessionStorage.getItem(`${key}-other`)).toBeNull();
    expect(sessionStorage.getItem("unrelated-state")).toBe("keep");
    expect(localStorage.getItem(key)).toBe("persistent-history-separate");
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("storage disabled"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage disabled"); });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("storage disabled"); });
    expect(readConversationRefresh(key)).toBeNull();
    expect(() => writeConversationRefresh(key, snapshot())).not.toThrow();
    expect(() => clearConversationRefresh()).not.toThrow();
  });
});
