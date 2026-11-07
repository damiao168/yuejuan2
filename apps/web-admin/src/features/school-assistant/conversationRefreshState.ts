import type { PendingConversation } from "./conversationPresentation";
import type { ConversationRefreshSnapshot } from "./conversationRefresh";
import type { ConversationThread } from "./conversationStorage";

export const REFRESH_INTERRUPTED_MESSAGE = "上次回复未完成，已恢复问题和收到的内容。可重新生成回复。";

function canCheckpoint(thread: ConversationThread) {
  return !thread.business && !Object.values(thread.context ?? {}).some(Boolean) &&
    !thread.messages.some(message => message.attachments?.length);
}

export function buildConversationRefresh(threads: readonly ConversationThread[], pending: Readonly<Record<string, PendingConversation>>, activeId: string | null): ConversationRefreshSnapshot {
  // 未分类的纯文字只在当前标签页短期暂存；业务范围一旦确定，下一次写入就移除整条记录。
  const eligible = threads.filter(canCheckpoint);
  const entries = eligible.flatMap(thread => {
    const current = pending[thread.id] ?? thread.interruptedReply;
    const question = thread.messages[thread.messages.length - 1];
    if (question?.role !== "user") return [];
    return [{
      threadId: thread.id, title: thread.title, updatedAt: thread.updatedAt,
      question: { role: "user" as const, content: question.content, ...(question.web_search === true ? { web_search: true as const } : {}) },
      partialContent: current?.content ?? "", ...(current?.modelConfigID ? { modelConfigID: current.modelConfigID } : {}),
    }];
  }).slice(0, 5);
  return { version: 1, savedAt: Date.now(), activeId: eligible.some(thread => thread.id === activeId) ? activeId : null, entries };
}

export function restoreConversationRefresh(history: ConversationThread[], snapshot: ConversationRefreshSnapshot | null) {
  if (!snapshot) return { threads: history, activeId: history[0]?.id ?? null };
  const restored = snapshot.entries.map(entry => {
    const previous = history.find(thread => thread.id === entry.threadId);
    const messages = [...(previous?.messages ?? []), entry.question].slice(-49);
    return {
      id: entry.threadId, title: previous?.title ?? entry.title, updatedAt: Math.max(previous?.updatedAt ?? 0, entry.updatedAt), messages,
      interruptedReply: { content: entry.partialContent, ...(entry.modelConfigID ? { modelConfigID: entry.modelConfigID } : {}) },
    } satisfies ConversationThread;
  });
  const ids = new Set(restored.map(thread => thread.id));
  const threads: ConversationThread[] = [...restored, ...history.filter(thread => !ids.has(thread.id))].slice(0, 20);
  return { threads, activeId: threads.some(thread => thread.id === snapshot.activeId) ? snapshot.activeId : null };
}
