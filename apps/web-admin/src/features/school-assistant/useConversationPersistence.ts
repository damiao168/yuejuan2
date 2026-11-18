import { useCallback, useEffect, useRef, type RefObject } from "react";
import type { PendingConversation } from "./conversationPresentation";
import { writeConversationRefresh } from "./conversationRefresh";
import { buildConversationRefresh } from "./conversationRefreshState";
import { serializeConversationThreads, type ConversationThread } from "./conversationStorage";

export function useConversationPersistence({
  threads,
  pending,
  activeId,
  businessThreads,
  live,
  refreshKey,
  storageKey,
  publicComputer,
}: {
  threads: ConversationThread[];
  pending: Record<string, PendingConversation>;
  activeId: string | null;
  businessThreads: RefObject<Set<string>>;
  live: RefObject<boolean>;
  refreshKey: string;
  storageKey: string;
  publicComputer: boolean;
}) {
  const checkpointState = useRef({ threads, pending, activeId });
  checkpointState.current = { threads, pending, activeId };
  const persistRefresh = useCallback(() => {
    if (!live.current) return;
    const state = checkpointState.current;
    writeConversationRefresh(
      refreshKey,
      buildConversationRefresh(
        state.threads.filter((thread) => !businessThreads.current.has(thread.id)),
        state.pending,
        state.activeId,
      ),
      publicComputer,
    );
  }, [businessThreads, live, refreshKey, publicComputer]);
  useEffect(() => {
    if (!live.current) return;
    writeConversationRefresh(
      refreshKey,
      buildConversationRefresh(
        threads.filter((thread) => !businessThreads.current.has(thread.id)),
        pending,
        activeId,
      ),
      publicComputer,
    );
  }, [activeId, businessThreads, live, pending, publicComputer, refreshKey, threads]);
  useEffect(() => {
    // 刷新会断开原HTTP流，只保存恢复检查点；不能在卸载时清除，也不能自动重发收费请求。
    window.addEventListener("pagehide", persistRefresh);
    window.addEventListener("beforeunload", persistRefresh);
    return () => {
      window.removeEventListener("pagehide", persistRefresh);
      window.removeEventListener("beforeunload", persistRefresh);
    };
  }, [persistRefresh]);

  useEffect(() => {
    // 公共电脑不落盘；个人电脑只保存一般聊天，业务会话与附件正文不写入历史。
    if (publicComputer) return;
    try {
      window.localStorage.setItem(storageKey, serializeConversationThreads(threads));
    } catch {
      // 浏览器存储禁用或已满时，当前对话仍保留在内存中。
    }
  }, [storageKey, threads, publicComputer]);
  return persistRefresh;
}
