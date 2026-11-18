import { useCallback, useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type SubmitEvent, type KeyboardEvent } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { streamSchoolConversation, type SchoolChatAttachment, type SchoolChatMessage, type SchoolConversationContext } from "../../api/schoolAiChat";
import { requestMessages } from "../../pages/schoolAiChatMessages";
import { pastedConversationImages, readConversationAttachments } from "./readConversationAttachments";
import { readConversationThreads, type ConversationThread } from "./conversationStorage";
import { emptyConversationEvidence, mergeConversationEvidence, acceptConversationEvent, conversationAccountKey, contextFromEvidence, type ConversationEvidence, type ConversationStreamIdentity } from "./conversationState";
import { type PendingConversation } from "./conversationPresentation";
import { conversationRecovery } from "./conversationRecovery";
import { conversationModelPreferenceKey } from "./conversationModelPreference";
import type { SchoolConversationProps } from './conversationTypes';
import { hasBusinessConversationEvent, publicConversationSources } from "./conversationWebSearch";
import { CONVERSATION_REFRESH_PREFIX, readConversationRefresh } from "./conversationRefresh";
import { restoreConversationRefresh, REFRESH_INTERRUPTED_MESSAGE } from "./conversationRefreshState";

import { useConversationModelSettings } from "./useConversationModelSettings";
import { useConversationPersistence } from "./useConversationPersistence";
import { useConversationScroll } from "./useConversationScroll";
import { useCopyConversationReply } from "./useCopyConversationReply";
import { compactConversationHistory, conversationHistoryMessages, conversationRequestMemory, toggleConversationMemoryPin, type ConversationHistoryExcerpt } from "./conversationHistory";

export const MAX_ATTACHMENTS = 3;
export const MAX_ACTIVE_CHATS = 5;

function titleFromMessage(content: string) {
  const title = content.replace(/\s+/g, " ").trim();
  return title.length > 28 ? `${title.slice(0, 28)}…` : title;
}

function replyWithPublicSources(message: SchoolChatMessage, evidence: ConversationEvidence, publicReply: boolean): SchoolChatMessage {
  const { web_search: _mode, web_sources: _sources, ...reply } = message;
  const sources = publicReply ? publicConversationSources(evidence.sources) : [];
  return { ...reply, ...(publicReply ? { web_search: true } : {}), ...(sources.length ? { web_sources: sources } : {}) };
}

// 控制器由账户布局持有，离开对话视图不销毁；账户或授权卸载时先失效回调，再终止请求。
export function useAccountConversation({ user, initialContext, compact = false, onNavigate }: SchoolConversationProps, enabled = true) {
  const { message: toast, modal } = App.useApp();
  // 使用完整授权范围隔离一般聊天历史；旧 v1 没有授权信息，不能自动恢复到新的业务助手。
  const storageKey = `edugrade.ai-chat.v2:${conversationAccountKey(user)}`;
  const refreshKey = `${CONVERSATION_REFRESH_PREFIX}${conversationAccountKey(user)}`;
  const modelPreferenceKey = conversationModelPreferenceKey(conversationAccountKey(user));
  const [restored] = useState(() => {
    const state = restoreConversationRefresh(user.publicComputer ? [] : readConversationThreads(storageKey), readConversationRefresh(refreshKey, user.publicComputer));
    return { ...state, activeId: Object.values(initialContext ?? {}).some(Boolean) ? null : state.activeId };
  });
  const [threads, setThreads] = useState<ConversationThread[]>(restored.threads);
  const [activeId, setActiveId] = useState<string | null>(restored.activeId);
  const activeIdRef = useRef<string | null>(restored.activeId);
  const activateThread = useCallback((id: string | null) => {
    // ref 与页面导航同步，发送和开启新对话同一帧发生时也按当前对话判断在途锁。
    activeIdRef.current = id;
    setActiveId(id);
  }, []);
  const [draft, setDraft] = useState("");
  const [draftAttachments, setDraftAttachments] = useState<SchoolChatAttachment[]>([]);
  const [attaching, setAttaching] = useState(false);
  const { model, setModel, chatModels, selectedModelID, modelLoading, modelError, chooseModel, webSearchCapability } =
    useConversationModelSettings(modelPreferenceKey, user.publicComputer, enabled);
  const [webSearch, setWebSearch] = useState(() => {
    const messages = restored.threads.find(thread => thread.id === restored.activeId)?.messages;
    return messages?.[messages.length - 1]?.web_search === true;
  });
  const [sendErrors, setSendErrors] = useState<Record<string, string>>(() => Object.fromEntries(restored.threads.filter(thread => thread.interruptedReply).map(thread => [thread.id, REFRESH_INTERRUPTED_MESSAGE])));
  const [pending, setPending] = useState<Record<string, PendingConversation>>({});
  const [replyEvidence, setReplyEvidence] = useState(new Map<SchoolChatMessage, ConversationEvidence>());
  const [failedEvidence, setFailedEvidence] = useState<Record<string, ConversationEvidence>>({});
  const [seedContext, setSeedContext] = useState<SchoolConversationContext | undefined>(initialContext);
  const controllers = useRef(new Map<string, AbortController>());
  const attachmentRead = useRef<AbortController | null>(null);
  const live = useRef(true);
  const businessThreads = useRef(new Set<string>());
  const [historyOpen, setHistoryOpen] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const activeThread = threads.find((thread) => thread.id === activeId);
  const activePending = activeId ? pending[activeId] : undefined;
  const { scrollFlow, followingReply, scrollConversation, wheelConversation, toggleReasoning, reasoningExpanded, resetReasoning } =
    useConversationScroll(activeId, activeThread?.messages.length, activePending);
  const { copiedMessage, copyReply } = useCopyConversationReply(toast);
  const activeContext = activeThread ? activeThread.context : seedContext;
  const activeCount = Object.keys(pending).length;

  useEffect(() => {
    live.current = true;
    return () => {
      // 卸载或授权改变后先使回调失效；网络终止较晚时也不能重新显示旧身份的结果。
      live.current = false;
      attachmentRead.current?.abort();
      for (const controller of controllers.current.values()) controller.abort();
      controllers.current.clear();
    };
  }, []);

  const contextIdentity = JSON.stringify([initialContext?.exam_id, initialContext?.origin_module, initialContext?.origin_section]);
  const previousContext = useRef(contextIdentity);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 同一入口的名称或投影刷新不能清空正在写的对话；只有考试、来源模块或分区改变才重置。
  useEffect(() => {
    // 首次挂载和StrictMode重跑保留恢复的选择；只有实际换了业务入口才开启新对话。
    if (previousContext.current === contextIdentity) return;
    previousContext.current = contextIdentity;
    attachmentRead.current?.abort();
    attachmentRead.current = null;
    setAttaching(false);
    setSeedContext(initialContext);
    activateThread(null);
    setDraft("");
    setDraftAttachments([]);
    setWebSearch(false);
  }, [initialContext?.exam_id, initialContext?.origin_module, initialContext?.origin_section]);

  useEffect(() => {
    const visibleMessages = new Set(threads.flatMap((thread) => thread.messages));
    setReplyEvidence((current) => new Map([...current].filter(([reply]) => visibleMessages.has(reply))));
  }, [threads]);

  const persistRefresh = useConversationPersistence({ threads, pending, activeId, businessThreads, live, refreshKey, storageKey, publicComputer: user.publicComputer });

  const openNew = useCallback(() => {
    attachmentRead.current?.abort();
    attachmentRead.current = null;
    setAttaching(false);
    activateThread(null);
    setDraft("");
    setDraftAttachments([]);
    setWebSearch(false);
    setHistoryOpen(false);
    inputRef.current?.focus();
  }, [activateThread]);

  const complete = useCallback(async (threadId: string, messages: SchoolChatMessage[], context?: SchoolConversationContext, requestWebSearch = false, historyExcerpt?: ConversationHistoryExcerpt) => {
    if (controllers.current.has(threadId) || controllers.current.size >= MAX_ACTIVE_CHATS) return;
    const controller = new AbortController();
    const startedAt = Date.now();
    // 每条在途对话固定本次模型；其他对话更改偏好不能改写请求或当前对话显示的模型。
    const requestModelID = selectedModelID;
    const metrics = { modelConfigID: requestModelID, modelName: model?.model_name };
    // ref 立即登记在途请求，防止 React 状态提交前连续操作突破单对话或全局并发限制。
    controllers.current.set(threadId, controller);
    setThreads(current => current.map(thread => thread.id === threadId ? { ...thread, interruptedReply: undefined, contextOmitted: false, metrics: undefined } : thread));
    setPending((current) => ({ ...current, [threadId]: { startedAt, content: "", reasoning: "", evidence: emptyConversationEvidence(), modelConfigID: requestModelID, webSearch: requestWebSearch, contextOmitted: false, metrics } }));
    setSendErrors((current) => { const next = { ...current }; delete next[threadId]; return next; });
    setFailedEvidence((current) => { const next = { ...current }; delete next[threadId]; return next; });
    let partial: PendingConversation = { startedAt, content: "", reasoning: "", evidence: emptyConversationEvidence(), modelConfigID: requestModelID, webSearch: requestWebSearch, contextOmitted: false, metrics };
    const identity: ConversationStreamIdentity = { sequence: 0 };
    let completed = false;
    // 联网开关只代表允许搜索。服务端若转入校内问答，本轮包括中断正文都不能成为下一次公开搜索的历史。
    let businessSeen = false;
    try {
      // 公开模式在 API 边界按模式标记筛选历史，先通用规范化会丢掉这些标记，导致上下文无法承接。
      await streamSchoolConversation(requestWebSearch ? messages.slice(-49) : requestMessages(conversationHistoryMessages(messages, historyExcerpt)), context, controller.signal, (event) => {
        // 先挡住旧挂载、已停止或替换的请求，再推进序号；晚到回调不能污染当前流。
        if (!live.current || controller.signal.aborted || controllers.current.get(threadId) !== controller || completed || !acceptConversationEvent(identity, event)) return;
        // SSE 也只按受控错误码展示；不直接透传模型或上游接口的内部说明。
        if (event.type === "error") throw new ApiClientError(0, event.code, event.message);
        // 多模型选择是本页偏好；并行请求的元数据不能把选择器切换到其他模型。
        if (event.type === "model" && !chatModels.length) setModel(event.model);
        if (event.type === "context") {
          const omitted = event.context_budget.accepted && (event.context_budget.dropped_messages > 0 || event.context_budget.memory_omitted);
          partial = { ...partial, contextOmitted: Boolean(partial.contextOmitted || omitted) };
          // 探测包和未接受的请求不能冒充当前对话占用；显示最近一次真正接受的业务模型请求。
          if (event.context_budget.accepted && event.context_budget.phase !== "tool_capability") {
            partial = { ...partial, metrics: { ...partial.metrics, contextBudget: event.context_budget } };
          }
          setPending(current => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "delta") {
          partial = { ...partial, [event.channel]: partial[event.channel] + event.delta };
          setPending((current) => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "progress" || event.type === "tool_started" || event.type === "tool_finished" || event.type === "sources") {
          if (hasBusinessConversationEvent(event)) {
            businessSeen = true;
            businessThreads.current.add(threadId);
            persistRefresh();
            setThreads((current) => current.map((thread) => thread.id === threadId && !thread.business ? { ...thread, business: true } : thread));
          }
          partial = { ...partial, evidence: mergeConversationEvidence(partial.evidence, event) };
          setPending((current) => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "done") {
          completed = true;
          partial = { ...partial, evidence: mergeConversationEvidence(partial.evidence, event), metrics: { ...partial.metrics, generation: event.generation_metrics } };
          // 公开历史要求问答两条都有标记；业务回复不标记，即使用户当时打开联网也会在 API 边界断开这组历史。
          const reply = replyWithPublicSources(event.completion.message, partial.evidence, requestWebSearch && !businessSeen);
          // 证据以回复对象为键，历史裁剪或删除消息时不会把旧导航绑定到另一条回复。
          setReplyEvidence((current) => new Map(current).set(reply, partial.evidence));
          setThreads((current) => current.map((thread) => thread.id === threadId ? compactConversationHistory({
            ...thread, messages: [...thread.messages, reply], updatedAt: Date.now(), context: contextFromEvidence(partial.evidence, context), contextOmitted: partial.contextOmitted, metrics: partial.metrics
          }) : thread));
        }
      }, requestModelID, requestWebSearch, requestWebSearch ? undefined : conversationRequestMemory(messages, historyExcerpt));
      if (!completed) throw new Error("对话流意外中断");
    } catch (error) {
      if (!live.current || controllers.current.get(threadId) !== controller) return;
      // 中断后保留本次已接受的上下文估算；缺少供应商最终用量时，不能沿用上一次的速率。
      setThreads(current => current.map(thread => thread.id === threadId ? { ...thread, contextOmitted: partial.contextOmitted, metrics: partial.metrics } : thread));
      const recovery = conversationRecovery(error, controller.signal.aborted, completed, partial);
      if (recovery?.kind === "reply") {
        const reply = replyWithPublicSources(recovery.reply, recovery.evidence, requestWebSearch && !businessSeen);
        setReplyEvidence((current) => new Map(current).set(reply, recovery.evidence));
        setThreads((current) => current.map((thread) => thread.id === threadId ? compactConversationHistory({
          ...thread, messages: [...thread.messages, reply], updatedAt: Date.now()
        }) : thread));
      } else if (recovery?.kind === "failure") {
        setSendErrors((current) => ({ ...current, [threadId]: recovery.message }));
        setFailedEvidence((current) => ({ ...current, [threadId]: recovery.evidence }));
      }
    } finally {
      if (live.current && controllers.current.get(threadId) === controller) {
        controllers.current.delete(threadId);
        setPending((current) => { const next = { ...current }; delete next[threadId]; return next; });
      }
    }
  }, [chatModels.length, selectedModelID, model?.model_name, persistRefresh, setModel]);

  const send = useCallback((event?: SubmitEvent) => {
    event?.preventDefault();
    const content = draft.trim();
    if ((!content && draftAttachments.length === 0) || (activeId && controllers.current.has(activeId)) || controllers.current.size >= MAX_ACTIVE_CHATS || attachmentRead.current || attaching || !model?.available || content.length > 20_000) return;
    if (webSearch && (!webSearchCapability.available || !content || [...content].length > 500 || draftAttachments.length > 0)) {
      toast.warning(webSearchCapability.available ? "联网搜索请单独发送 500 字以内的公开问题，不附带文件" : webSearchCapability.message);
      return;
    }
    followingReply.current = true;
    const id = activeThread?.id ?? crypto.randomUUID();
    resetReasoning(id);
    const appended: SchoolChatMessage[] = [...(activeThread?.messages ?? []), {
      role: "user" as const, content, attachments: draftAttachments.length ? draftAttachments : undefined, ...(webSearch ? { web_search: true } : {})
    }];
    const history = compactConversationHistory({ messages: appended, ...(activeThread?.historyExcerpt ? { historyExcerpt: activeThread.historyExcerpt } : {}) });
    setThreads((current) => {
      const existing = current.find((thread) => thread.id === id);
      const next: ConversationThread = {
        id, title: existing?.title ?? titleFromMessage(content || draftAttachments[0]?.name || "附件对话"), updatedAt: Date.now(), ...history, context: activeContext, business: existing?.business || (!webSearch && Boolean(activeContext?.exam_id))
      };
      return [next, ...current.filter((thread) => thread.id !== id)]
        .filter((thread, index) => index < 20 || controllers.current.has(thread.id));
    });
    activateThread(id);
    setDraft("");
    setDraftAttachments([]);
    void complete(id, history.messages, activeContext, webSearch, history.historyExcerpt);
  }, [activeId, activeThread, activeContext, activateThread, attaching, complete, draft, draftAttachments, model?.available, toast, webSearch, webSearchCapability, followingReply, resetReasoning]);

  const stop = () => { if (activeId) controllers.current.get(activeId)?.abort(); };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      send();
    }
  };

  const deleteThread = (thread: ConversationThread) => {
    modal.confirm({
      title: "删除这条对话？",
      content: `“${thread.title}”删除后无法恢复。`,
      okText: "删除",
      cancelText: "取消",
      okButtonProps: { danger: true },
      onOk: () => {
        controllers.current.get(thread.id)?.abort();
        setSendErrors((current) => { const next = { ...current }; delete next[thread.id]; return next; });
        setThreads((current) => current.filter((item) => item.id !== thread.id));
        if (activeId === thread.id) openNew();
      }
    });
  };

  const addFiles = async (files: File[]) => {
    if (webSearch) {
      toast.warning("联网搜索只使用文字问题，请先关闭联网搜索再上传文件");
      return;
    }
    // 粘贴与选择共用读取锁和名额；同一帧粘贴后按回车也要先等附件读取完成。
    if (!files.length || attachmentRead.current || (activeId && controllers.current.has(activeId)) || controllers.current.size >= MAX_ACTIVE_CHATS) return;
    const available = MAX_ATTACHMENTS - draftAttachments.length;
    if (available <= 0) {
      toast.warning(`每次最多上传 ${MAX_ATTACHMENTS} 个文件`);
      return;
    }
    if (files.length > available) toast.warning(`每次最多上传 ${MAX_ATTACHMENTS} 个文件`);
    setAttaching(true);
    const controller = new AbortController();
    attachmentRead.current = controller;
    const accepted = await readConversationAttachments(files.slice(0, available), controller.signal,
      () => live.current, (file, error) => toast.error(`${file.name}：${getUserErrorMessage(error, "文件读取失败")}`));
    if (live.current && !controller.signal.aborted && attachmentRead.current === controller) {
      if (accepted.length > 0) setDraftAttachments((current) => [...current, ...accepted]);
      setAttaching(false);
      attachmentRead.current = null;
    }
  };

  const selectFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    void addFiles(files);
  };

  const pasteImages = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = pastedConversationImages(event.clipboardData);
    // 没有图片时保留浏览器原生文字粘贴；图片与 HTML 同时存在时不粘贴网页标记。
    if (!files.length) return;
    event.preventDefault();
    if (!model?.available) return;
    void addFiles(files);
    const text = event.clipboardData.getData("text/plain");
    if (text) {
      const { selectionStart, selectionEnd } = event.currentTarget;
      setDraft(current => `${current.slice(0, selectionStart)}${text}${current.slice(selectionEnd)}`.slice(0, 20_000));
    }
  };

  const retry = () => {
    if (activeThread && activeThread.messages[activeThread.messages.length - 1]?.role === "user" && !controllers.current.has(activeThread.id) && controllers.current.size < MAX_ACTIVE_CHATS) {
      followingReply.current = true;
      // 重试沿用原问题的模式，不能因用户切到另一条对话后修改开关而改变请求。
      void complete(activeThread.id, activeThread.messages, activeThread.context, activeThread.messages[activeThread.messages.length - 1]?.web_search === true, activeThread.historyExcerpt);
    }
  };

  const clearContext = () => {
    if (activePending) return;
    attachmentRead.current?.abort(); attachmentRead.current = null; setAttaching(false);
    if (activeId) setThreads((current) => current.map((thread) => thread.id === activeId ? { ...thread, context: undefined } : thread));
    else setSeedContext(undefined);
  };

  const selectThread: (id: string) => void = (id) => {
      attachmentRead.current?.abort(); attachmentRead.current = null; setAttaching(false);
      const questions = threads.find(thread => thread.id === id)?.messages.filter(message => message.role === "user") ?? [];
      setWebSearch(questions[questions.length - 1]?.web_search === true);
      activateThread(id); setHistoryOpen(false);
    };

  const selectModel = (id: string) => {
    const currentThread = activeIdRef.current;
    if (currentThread && controllers.current.has(currentThread)) return;
    chooseModel(id);
  };

  const suggestQuestion: (question: string) => void = (question) => { setDraft(question); inputRef.current?.focus(); };

  const toggleMemoryPin = (sourceID: string) => {
    if (!activeId || controllers.current.has(activeId)) return;
    const message = activeThread?.messages.find(item => item.memory_source_id === sourceID);
    if (!activeThread) return;
    const next = toggleConversationMemoryPin(activeThread, sourceID);
    if (message && !message.memory_pinned && !next.messages.find(item => item.memory_source_id === sourceID)?.memory_pinned) {
      toast.warning("保留要求已达到容量上限，请先移除旧要求，或将关键要求单独发送后保留。");
      return;
    }
    if (message && !message.memory_pinned && [...message.content].length > 4_000) toast.warning("内容较长，发送时仍会按容量选取原文片段，可将关键要求单独发送后保留。");
    setThreads(current => current.map(thread => thread.id === activeId ? toggleConversationMemoryPin(thread, sourceID) : thread));
  };

  return { compact, onNavigate, user, threads, activeId, pending, openNew, deleteThread, selectThread, historyOpen, setHistoryOpen, model, chatModels, activePending, selectedModelID, modelLoading, selectModel, activeCount, activeContext, clearContext, activeThread, replyEvidence, failedEvidence, sendErrors, reasoningExpanded, toggleReasoning, copiedMessage, copyReply, retry, modelError, suggestQuestion, scrollFlow, scrollConversation, wheelConversation, send, draftAttachments, setDraftAttachments, inputRef, draft, setDraft, onInputKeyDown, pasteImages, fileInputRef, selectFiles, attaching, stop, webSearch, setWebSearch, webSearchCapability, toggleMemoryPin };
}

export type AccountConversationModel = ReturnType<typeof useAccountConversation>;
