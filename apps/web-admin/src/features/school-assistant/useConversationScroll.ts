import { useCallback, useEffect, useRef, type UIEventHandler, type WheelEventHandler } from "react";
import type { PendingConversation } from "./conversationPresentation";

export function useConversationScroll(
  activeId: string | null,
  messageCount: number | undefined,
  activePending: PendingConversation | undefined,
) {
  const expandedReasoning = useRef(new Set<string>());
  const scrollFlow = useRef<HTMLDivElement>(null);
  const followingReply = useRef(true);
  const scrollThread = useRef<string | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 正文、消息数量和工具进度是滚动触发键；推理增量不能打断用户正在阅读的位置。
  useEffect(() => {
    if (scrollThread.current !== activeId) {
      scrollThread.current = activeId;
      followingReply.current = true;
    }
    // 用户上翻后保留阅读位置；是否跟随由滚动事件记录，不能用追加正文后的距离反推。
    if (!followingReply.current || (activeId && expandedReasoning.current.has(activeId))) return;
    const flow = scrollFlow.current;
    // 只滚消息容器，不启动持续的平滑动画，以免下一段输出和鼠标争抢滚动位置。
    flow?.scrollTo({ top: flow.scrollHeight, behavior: "instant" });
  }, [activeId, messageCount, activePending?.content, activePending?.evidence.tools.length]);

  const toggleReasoning: (expanded: boolean) => void = (expanded) => {
    if (activeId) {
      if (expanded) expandedReasoning.current.add(activeId);
      else expandedReasoning.current.delete(activeId);
    }
  };

  const scrollConversation: UIEventHandler<HTMLDivElement> = (event) => {
    const flow = event.currentTarget;
    // 仅容忍小数像素舍入；轻微上滚也要暂停，拖动滚动条和触摸滚动同样走此边界。
    followingReply.current = flow.scrollHeight - flow.scrollTop - flow.clientHeight <= 2;
  };

  const wheelConversation: WheelEventHandler<HTMLDivElement> = (event) => {
    // 滚轮默认滚动之前就交还控制权，避免同一帧到达的正文先把页面拉回底部。
    if (event.deltaY < 0) followingReply.current = false;
  };

  const reasoningExpanded = Boolean(activeId && expandedReasoning.current.has(activeId));
  const resetReasoning = useCallback((threadId: string) => {
    expandedReasoning.current.delete(threadId);
  }, []);
  return {
    scrollFlow,
    followingReply,
    scrollConversation,
    wheelConversation,
    toggleReasoning,
    reasoningExpanded,
    resetReasoning,
  };
}
