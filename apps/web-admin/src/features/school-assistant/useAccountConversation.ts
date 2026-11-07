import { useCallback, useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type SubmitEvent, type KeyboardEvent } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { getSchoolChatModel, streamSchoolConversation, type SchoolChatAttachment, type SchoolChatMessage, type SchoolChatModel, type SchoolConversationContext } from "../../api/schoolAiChat";
import { getSchoolAssistantCapabilities } from "../../api/schoolAssistant";
import { requestMessages } from "../../pages/schoolAiChatMessages";
import { readChatAttachment } from "./conversationAttachments";
import { readConversationThreads, serializeConversationThreads, type ConversationThread } from "./conversationStorage";
import { emptyConversationEvidence, mergeConversationEvidence, acceptConversationEvent, conversationAccountKey, contextFromEvidence, type ConversationEvidence, type ConversationStreamIdentity } from "./conversationState";
import { type PendingConversation } from "./conversationPresentation";
import { conversationRecovery } from "./conversationRecovery";
import { conversationModelPreferenceKey, readConversationModelPreference, saveConversationModelPreference } from "./conversationModelPreference";
import type { UIEventHandler, WheelEventHandler } from 'react';
import type { SchoolConversationProps } from './conversationTypes';
import { hasBusinessConversationEvent, publicConversationSources } from "./conversationWebSearch";
import { CONVERSATION_REFRESH_PREFIX, readConversationRefresh, writeConversationRefresh } from "./conversationRefresh";
import { buildConversationRefresh, restoreConversationRefresh, REFRESH_INTERRUPTED_MESSAGE } from "./conversationRefreshState";

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
  const [model, setModel] = useState<SchoolChatModel | null>(null);
  const [chatModels, setChatModels] = useState<SchoolChatModel[]>([]);
  const [selectedModelID, setSelectedModelID] = useState<string>();
  const [modelLoading, setModelLoading] = useState(true);
  const [modelError, setModelError] = useState("");
  const [webSearch, setWebSearch] = useState(() => {
    const messages = restored.threads.find(thread => thread.id === restored.activeId)?.messages;
    return messages?.[messages.length - 1]?.web_search === true;
  });
  const [webSearchCapability, setWebSearchCapability] = useState({ available: false, message: "正在读取联网搜索状态" });
  const [sendErrors, setSendErrors] = useState<Record<string, string>>(() => Object.fromEntries(restored.threads.filter(thread => thread.interruptedReply).map(thread => [thread.id, REFRESH_INTERRUPTED_MESSAGE])));
  const [pending, setPending] = useState<Record<string, PendingConversation>>({});
  const [replyEvidence, setReplyEvidence] = useState(new Map<SchoolChatMessage, ConversationEvidence>());
  const [failedEvidence, setFailedEvidence] = useState<Record<string, ConversationEvidence>>({});
  const [seedContext, setSeedContext] = useState<SchoolConversationContext | undefined>(initialContext);
  const controllers = useRef(new Map<string, AbortController>());
  const attachmentRead = useRef<AbortController | null>(null);
  const live = useRef(true);
  const businessThreads = useRef(new Set<string>());
  const checkpointState = useRef({ threads, pending, activeId });
  checkpointState.current = { threads, pending, activeId };
  const persistRefresh = useCallback(() => {
    if (!live.current) return;
    const state = checkpointState.current;
    writeConversationRefresh(refreshKey, buildConversationRefresh(state.threads.filter(thread => !businessThreads.current.has(thread.id)), state.pending, state.activeId), user.publicComputer);
  }, [refreshKey, user.publicComputer]);
  const expandedReasoning = useRef(new Set<string>());
  const [historyOpen, setHistoryOpen] = useState(false);
  const [copiedMessage, setCopiedMessage] = useState("");
  const scrollFlow = useRef<HTMLDivElement>(null);
  const followingReply = useRef(true);
  const scrollThread = useRef<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const activeThread = threads.find((thread) => thread.id === activeId);
  const activePending = activeId ? pending[activeId] : undefined;
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
    if (!live.current) return;
    writeConversationRefresh(refreshKey, buildConversationRefresh(threads.filter(thread => !businessThreads.current.has(thread.id)), pending, activeId), user.publicComputer);
  }, [refreshKey, threads, pending, activeId, user.publicComputer]);
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
    const visibleMessages = new Set(threads.flatMap((thread) => thread.messages));
    setReplyEvidence((current) => new Map([...current].filter(([reply]) => visibleMessages.has(reply))));
  }, [threads]);

  useEffect(() => {
    // 公共电脑不落盘；个人电脑只保存一般聊天，业务会话与附件正文不写入历史。
    if (user.publicComputer) return;
    try {
      window.localStorage.setItem(storageKey, serializeConversationThreads(threads));
    } catch {
      // 浏览器存储禁用或已满时，当前对话仍保留在内存中。
    }
  }, [storageKey, threads, user.publicComputer]);

  useEffect(() => {
    // 其他栏目不预取模型；首次打开助手后保持控制器，后台增量才能继续归入原对话。
    if (!enabled) return;
    let mounted = true;
    getSchoolChatModel()
      .then((response) => {
        if (!mounted) return;
        const candidates = response.models ?? [];
        // 本机偏好只在新读取的可用名单内恢复；取消开放、停用和跨校授权变化都不能沿用旧编号。
        const preferredID = readConversationModelPreference(modelPreferenceKey, user.publicComputer);
        const selected = candidates.find(item => item.id === preferredID && item.available) ?? response.model;
        setModel(selected); setChatModels(candidates); setSelectedModelID(selected.id);
        saveConversationModelPreference(modelPreferenceKey, user.publicComputer, selected.available ? selected.id : undefined);
      })
      .catch((error: unknown) => { if (mounted) setModelError(getUserErrorMessage(error, "无法加载对话模型")); })
      .finally(() => { if (mounted) setModelLoading(false); });
    return () => { mounted = false; };
  }, [enabled, modelPreferenceKey, user.publicComputer]);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    getSchoolAssistantCapabilities(controller.signal)
      .then(response => {
        if (controller.signal.aborted) return;
        setWebSearchCapability(response.web_search ?? { available: false, message: "联网搜索尚未配置" });
      })
      .catch(() => {
        if (!controller.signal.aborted) setWebSearchCapability({ available: false, message: "暂时无法读取联网搜索状态" });
      });
    return () => controller.abort();
  }, [enabled]);

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
  }, [activeId, activeThread?.messages.length, activePending?.content, activePending?.evidence.tools.length]);

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

  const complete = useCallback(async (threadId: string, messages: SchoolChatMessage[], context?: SchoolConversationContext, requestWebSearch = false) => {
    if (controllers.current.has(threadId) || controllers.current.size >= MAX_ACTIVE_CHATS) return;
    const controller = new AbortController();
    const startedAt = Date.now();
    // 每条在途对话固定本次模型；其他对话更改偏好不能改写请求或当前对话显示的模型。
    const requestModelID = selectedModelID;
    // ref 立即登记在途请求，防止 React 状态提交前连续操作突破单对话或全局并发限制。
    controllers.current.set(threadId, controller);
    setThreads(current => current.map(thread => thread.id === threadId && thread.interruptedReply ? { ...thread, interruptedReply: undefined } : thread));
    setPending((current) => ({ ...current, [threadId]: { startedAt, content: "", reasoning: "", evidence: emptyConversationEvidence(), modelConfigID: requestModelID, webSearch: requestWebSearch } }));
    setSendErrors((current) => { const next = { ...current }; delete next[threadId]; return next; });
    setFailedEvidence((current) => { const next = { ...current }; delete next[threadId]; return next; });
    let partial: PendingConversation = { startedAt, content: "", reasoning: "", evidence: emptyConversationEvidence(), modelConfigID: requestModelID, webSearch: requestWebSearch };
    const identity: ConversationStreamIdentity = { sequence: 0 };
    let completed = false;
    // 联网开关只代表允许搜索。服务端若转入校内问答，本轮包括中断正文都不能成为下一次公开搜索的历史。
    let businessSeen = false;
    try {
      // 公开模式在 API 边界按模式标记筛选历史，先通用规范化会丢掉这些标记，导致上下文无法承接。
      await streamSchoolConversation(requestWebSearch ? messages.slice(-49) : requestMessages(messages.slice(-49)), context, controller.signal, (event) => {
        // 先挡住旧挂载、已停止或替换的请求，再推进序号；晚到回调不能污染当前流。
        if (!live.current || controller.signal.aborted || controllers.current.get(threadId) !== controller || completed || !acceptConversationEvent(identity, event)) return;
        // SSE 也只按受控错误码展示；不直接透传模型或上游接口的内部说明。
        if (event.type === "error") throw new ApiClientError(0, event.code, event.message);
        // 多模型选择是本页偏好；并行请求的元数据不能把选择器切换到其他模型。
        if (event.type === "model" && !chatModels.length) setModel(event.model);
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
          partial = { ...partial, evidence: mergeConversationEvidence(partial.evidence, event) };
          // 公开历史要求问答两条都有标记；业务回复不标记，即使用户当时打开联网也会在 API 边界断开这组历史。
          const reply = replyWithPublicSources(event.completion.message, partial.evidence, requestWebSearch && !businessSeen);
          // 证据以回复对象为键，历史裁剪或删除消息时不会把旧导航绑定到另一条回复。
          setReplyEvidence((current) => new Map(current).set(reply, partial.evidence));
          setThreads((current) => current.map((thread) => thread.id === threadId ? {
            ...thread, messages: [...thread.messages, reply].slice(-50), updatedAt: Date.now(), context: contextFromEvidence(partial.evidence, context)
          } : thread));
        }
      }, requestModelID, requestWebSearch);
      if (!completed) throw new Error("对话流意外中断");
    } catch (error) {
      if (!live.current || controllers.current.get(threadId) !== controller) return;
      const recovery = conversationRecovery(error, controller.signal.aborted, completed, partial);
      if (recovery?.kind === "reply") {
        const reply = replyWithPublicSources(recovery.reply, recovery.evidence, requestWebSearch && !businessSeen);
        setReplyEvidence((current) => new Map(current).set(reply, recovery.evidence));
        setThreads((current) => current.map((thread) => thread.id === threadId ? {
          ...thread, messages: [...thread.messages, reply].slice(-50), updatedAt: Date.now()
        } : thread));
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
  }, [chatModels.length, selectedModelID, persistRefresh]);

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
    expandedReasoning.current.delete(id);
    const messages: SchoolChatMessage[] = [...(activeThread?.messages ?? []), {
      role: "user" as const, content, attachments: draftAttachments.length ? draftAttachments : undefined, ...(webSearch ? { web_search: true } : {})
    }].slice(-49);
    setThreads((current) => {
      const existing = current.find((thread) => thread.id === id);
      const next: ConversationThread = {
        id, title: existing?.title ?? titleFromMessage(content || draftAttachments[0]?.name || "附件对话"), updatedAt: Date.now(), messages, context: activeContext, business: existing?.business || (!webSearch && Boolean(activeContext?.exam_id))
      };
      return [next, ...current.filter((thread) => thread.id !== id)]
        .filter((thread, index) => index < 20 || controllers.current.has(thread.id));
    });
    activateThread(id);
    setDraft("");
    setDraftAttachments([]);
    void complete(id, messages, activeContext, webSearch);
  }, [activeId, activeThread, activeContext, activateThread, attaching, complete, draft, draftAttachments, model?.available, toast, webSearch, webSearchCapability]);

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

  const readSelectedAttachments = async (files: File[], controller: AbortController) => {
    const accepted: SchoolChatAttachment[] = [];
    // 按选择顺序逐份读取；单份失败仍处理后续文件，取消后不再开始下一份。
    for (const file of files) {
      if (controller.signal.aborted || !live.current) break;
      try {
        accepted.push(await readChatAttachment(file, controller.signal));
      } catch (error) {
        if (!controller.signal.aborted && live.current) toast.error(`${file.name}：${getUserErrorMessage(error, "文件读取失败")}`);
      }
    }
    return accepted;
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
    const accepted = await readSelectedAttachments(files.slice(0, available), controller);
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
    const items = Array.from(event.clipboardData.items ?? []);
    const images = items.filter(item => item.kind === "file" && item.type.startsWith("image/"))
      .map(item => item.getAsFile()).filter((file): file is File => file !== null);
    const files = images.length ? images : Array.from(event.clipboardData.files ?? []).filter(file => file.type.startsWith("image/"));
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

  const copyReply = async (content: string, key: string) => {
    try {
      await navigator.clipboard.writeText(content);
      setCopiedMessage(key);
      toast.success("回复已复制");
      window.setTimeout(() => setCopiedMessage((current) => current === key ? "" : current), 1600);
    } catch {
      toast.error("复制失败，请手动选择内容");
    }
  };

  const retry = () => {
    if (activeThread && activeThread.messages[activeThread.messages.length - 1]?.role === "user" && !controllers.current.has(activeThread.id) && controllers.current.size < MAX_ACTIVE_CHATS) {
      followingReply.current = true;
      // 重试沿用原问题的模式，不能因用户切到另一条对话后修改开关而改变请求。
      void complete(activeThread.id, activeThread.messages, activeThread.context, activeThread.messages[activeThread.messages.length - 1]?.web_search === true);
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

  const selectModel: (id: string) => void = id => {
            const currentThread = activeIdRef.current;
            if (currentThread && controllers.current.has(currentThread)) return;
            const selected = chatModels.find(item => item.id === id && item.available);
            if (selected) {
              setSelectedModelID(id); setModel(selected); setModelError("");
              saveConversationModelPreference(modelPreferenceKey, user.publicComputer, id);
            }
          };

  const toggleReasoning: (expanded: boolean) => void = (expanded) => {
          if (activeId) { if (expanded) expandedReasoning.current.add(activeId); else expandedReasoning.current.delete(activeId); }
        };

  const suggestQuestion: (question: string) => void = (question) => { setDraft(question); inputRef.current?.focus(); };

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
  return { compact, onNavigate, user, threads, activeId, pending, openNew, deleteThread, selectThread, historyOpen, setHistoryOpen, model, chatModels, activePending, selectedModelID, modelLoading, selectModel, activeCount, activeContext, clearContext, activeThread, replyEvidence, failedEvidence, sendErrors, reasoningExpanded, toggleReasoning, copiedMessage, copyReply, retry, modelError, suggestQuestion, scrollFlow, scrollConversation, wheelConversation, send, draftAttachments, setDraftAttachments, inputRef, draft, setDraft, onInputKeyDown, pasteImages, fileInputRef, selectFiles, attaching, stop, webSearch, setWebSearch, webSearchCapability };
}

export type AccountConversationModel = ReturnType<typeof useAccountConversation>;
