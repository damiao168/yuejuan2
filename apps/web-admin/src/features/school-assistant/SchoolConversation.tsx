import { useCallback, useEffect, useRef, useState, type ChangeEvent, type SubmitEvent, type KeyboardEvent } from "react";
import { App, Button, Drawer, Input, Tooltip } from "antd";
import { ArrowUp, Bot, FileText, Menu, Paperclip, Square, X } from "lucide-react";
import type { SessionUser } from "../../auth/session";
import { getUserErrorMessage } from "../../api/client";
import { getSchoolChatModel, streamSchoolConversation, type SchoolChatAttachment, type SchoolChatMessage, type SchoolChatModel, type SchoolConversationContext } from "../../api/schoolAiChat";
import { requestMessages } from "../../pages/schoolAiChatMessages";
import { readChatAttachment } from "./conversationAttachments";
import { readConversationThreads, serializeConversationThreads, type ConversationThread } from "./conversationStorage";
import { emptyConversationEvidence, mergeConversationEvidence, acceptConversationEvent, conversationAccountKey, contextFromEvidence, type ConversationEvidence, type ConversationStreamIdentity } from "./conversationState";
import { ConversationFlow, ConversationHistory } from "./SchoolConversationDisplay";
import { conversationModelStatus, conversationPlaceholder, type PendingConversation } from "./conversationPresentation";
import { conversationRecovery } from "./conversationRecovery";

import "../../pages/school-ai-chat.css";
import "./school-conversation.css";


export interface SchoolConversationProps {
  readonly user: SessionUser;
  readonly initialContext?: SchoolConversationContext;
  readonly compact?: boolean;
  readonly onNavigate?: (path: string) => void;
}

const MAX_ATTACHMENTS = 3;
const MAX_ACTIVE_CHATS = 5;

function titleFromMessage(content: string) {
  const title = content.replace(/\s+/g, " ").trim();
  return title.length > 28 ? `${title.slice(0, 28)}…` : title;
}

function AccountConversation({ user, initialContext, compact = false, onNavigate }: SchoolConversationProps) {
  const { message: toast, modal } = App.useApp();
  // 使用完整授权范围隔离一般聊天历史；旧 v1 没有授权信息，不能自动恢复到新的业务助手。
  const storageKey = `edugrade.ai-chat.v2:${conversationAccountKey(user)}`;
  const [threads, setThreads] = useState<ConversationThread[]>(() => user.publicComputer ? [] : readConversationThreads(storageKey));
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [draftAttachments, setDraftAttachments] = useState<SchoolChatAttachment[]>([]);
  const [attaching, setAttaching] = useState(false);
  const [model, setModel] = useState<SchoolChatModel | null>(null);
  const [modelLoading, setModelLoading] = useState(true);
  const [modelError, setModelError] = useState("");
  const [sendErrors, setSendErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<Record<string, PendingConversation>>({});
  const [replyEvidence, setReplyEvidence] = useState(new Map<SchoolChatMessage, ConversationEvidence>());
  const [failedEvidence, setFailedEvidence] = useState<Record<string, ConversationEvidence>>({});
  const [seedContext, setSeedContext] = useState<SchoolConversationContext | undefined>(initialContext);
  const controllers = useRef(new Map<string, AbortController>());
  const attachmentRead = useRef<AbortController | null>(null);
  const live = useRef(true);
  const expandedReasoning = useRef(new Set<string>());
  const [historyOpen, setHistoryOpen] = useState(false);
  const [copiedMessage, setCopiedMessage] = useState("");
  const scrollEnd = useRef<HTMLDivElement>(null);
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

  useEffect(() => {
    attachmentRead.current?.abort();
    attachmentRead.current = null;
    setAttaching(false);
    setSeedContext(initialContext);
    setActiveId(null);
    setDraft("");
    setDraftAttachments([]);
  }, [initialContext?.exam_id, initialContext?.origin_module, initialContext?.origin_section]);

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
    let mounted = true;
    getSchoolChatModel()
      .then((response) => { if (mounted) setModel(response.model); })
      .catch((error: unknown) => { if (mounted) setModelError(getUserErrorMessage(error, "无法加载对话模型")); })
      .finally(() => { if (mounted) setModelLoading(false); });
    return () => { mounted = false; };
  }, []);

  useEffect(() => {
    // 切换对话或正文、消息、工具进度变化时触发滚动；展开推理时保留阅读位置，推理增量本身不触发。
    if (activeId && expandedReasoning.current.has(activeId)) return;
    scrollEnd.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [activeId, activeThread?.messages.length, activePending?.content, activePending?.evidence.tools.length]);

  const openNew = useCallback(() => {
    attachmentRead.current?.abort();
    attachmentRead.current = null;
    setAttaching(false);
    setActiveId(null);
    setDraft("");
    setDraftAttachments([]);
    setHistoryOpen(false);
    inputRef.current?.focus();
  }, []);

  const complete = useCallback(async (threadId: string, messages: SchoolChatMessage[], context?: SchoolConversationContext) => {
    if (controllers.current.has(threadId) || controllers.current.size >= MAX_ACTIVE_CHATS) return;
    const controller = new AbortController();
    // ref 立即登记在途请求，防止 React 状态提交前连续操作突破单对话或全局并发限制。
    controllers.current.set(threadId, controller);
    setPending((current) => ({ ...current, [threadId]: { content: "", reasoning: "", evidence: emptyConversationEvidence() } }));
    setSendErrors((current) => { const next = { ...current }; delete next[threadId]; return next; });
    setFailedEvidence((current) => { const next = { ...current }; delete next[threadId]; return next; });
    let partial: PendingConversation = { content: "", reasoning: "", evidence: emptyConversationEvidence() };
    const identity: ConversationStreamIdentity = { sequence: 0 };
    let completed = false;
    try {
      await streamSchoolConversation(requestMessages(messages.slice(-49)), context, controller.signal, (event) => {
        // 先挡住旧挂载、已停止或替换的请求，再推进序号；晚到回调不能污染当前流。
        if (!live.current || controller.signal.aborted || controllers.current.get(threadId) !== controller || completed || !acceptConversationEvent(identity, event)) return;
        if (event.type === "error") throw new Error(event.message || "模型暂时无法回答，请稍后重试");
        if (event.type === "model") setModel(event.model);
        if (event.type === "delta") {
          partial = { ...partial, [event.channel]: partial[event.channel] + event.delta };
          setPending((current) => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "tool_started" || event.type === "tool_finished" || event.type === "sources") {
          if (event.type !== "sources" || event.sources.length || event.actions.length) {
            setThreads((current) => current.map((thread) => thread.id === threadId && !thread.business ? { ...thread, business: true } : thread));
          }
          partial = { ...partial, evidence: mergeConversationEvidence(partial.evidence, event) };
          setPending((current) => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "done") {
          completed = true;
          const reply = event.completion.message;
          // 证据以回复对象为键，历史裁剪或删除消息时不会把旧导航绑定到另一条回复。
          setReplyEvidence((current) => new Map(current).set(reply, partial.evidence));
          setThreads((current) => current.map((thread) => thread.id === threadId ? {
            ...thread, messages: [...thread.messages, reply].slice(-50), updatedAt: Date.now(), context: contextFromEvidence(partial.evidence, context)
          } : thread));
        }
      });
      if (!completed) throw new Error("对话流意外中断");
    } catch (error) {
      if (!live.current || controllers.current.get(threadId) !== controller) return;
      const recovery = conversationRecovery(error, controller.signal.aborted, completed, partial);
      if (recovery?.kind === "reply") {
        setReplyEvidence((current) => new Map(current).set(recovery.reply, recovery.evidence));
        setThreads((current) => current.map((thread) => thread.id === threadId ? {
          ...thread, messages: [...thread.messages, recovery.reply].slice(-50), updatedAt: Date.now()
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
  }, []);

  const send = useCallback((event?: SubmitEvent) => {
    event?.preventDefault();
    const content = draft.trim();
    if ((!content && draftAttachments.length === 0) || (activeId && controllers.current.has(activeId)) || controllers.current.size >= MAX_ACTIVE_CHATS || attaching || !model?.available || content.length > 20_000) return;
    const id = activeThread?.id ?? crypto.randomUUID();
    expandedReasoning.current.delete(id);
    const messages: SchoolChatMessage[] = [...(activeThread?.messages ?? []), {
      role: "user" as const, content, attachments: draftAttachments.length ? draftAttachments : undefined
    }].slice(-49);
    setThreads((current) => {
      const existing = current.find((thread) => thread.id === id);
      const next: ConversationThread = {
        id, title: existing?.title ?? titleFromMessage(content || draftAttachments[0]?.name || "附件对话"), updatedAt: Date.now(), messages, context: activeContext, business: existing?.business || Boolean(activeContext?.exam_id)
      };
      return [next, ...current.filter((thread) => thread.id !== id)]
        .filter((thread, index) => index < 20 || controllers.current.has(thread.id));
    });
    setActiveId(id);
    setDraft("");
    setDraftAttachments([]);
    void complete(id, messages, activeContext);
  }, [activeId, activeThread, activeContext, attaching, complete, draft, draftAttachments, model?.available]);

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

  const selectFiles = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    // ref 在状态提交前挡住第二次选择；换对话、换上下文或卸载会取消这次读取。
    if (attachmentRead.current) return;
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
      void complete(activeThread.id, activeThread.messages, activeThread.context);
    }
  };

  const historyContent = <ConversationHistory threads={threads} activeId={activeId} pending={pending} onNew={openNew}
    onSelect={(id) => {
      attachmentRead.current?.abort(); attachmentRead.current = null; setAttaching(false);
      setActiveId(id); setHistoryOpen(false);
    }} onDelete={deleteThread} />;

  const clearContext = () => {
    if (activePending) return;
    attachmentRead.current?.abort(); attachmentRead.current = null; setAttaching(false);
    if (activeId) setThreads((current) => current.map((thread) => thread.id === activeId ? { ...thread, context: undefined } : thread));
    else setSeedContext(undefined);
  };

  return <div className={`school-chat-shell school-conversation-shell${compact ? " is-compact" : ""}`}>
    <aside className="school-chat-history" aria-label="对话历史">{historyContent}</aside>
    <Drawer title="对话历史" placement="left" open={historyOpen} onClose={() => setHistoryOpen(false)} width={280} styles={{ body: { padding: 0 } }} className="school-chat-mobile-drawer">
      <div className="school-chat-history school-chat-history-mobile">{historyContent}</div>
    </Drawer>

    <section className="school-chat-main" aria-label="AI 对话内容">
      <header className="school-chat-topbar">
        <div className="school-chat-topbar-title"><Button className="school-chat-mobile-menu" type="text" icon={<Menu size={18} />} aria-label="打开对话历史" onClick={() => setHistoryOpen(true)} /><Bot size={21} /><span>学校 AI 助手</span></div>
        <div className="school-chat-model-status"><i className={model?.available ? "is-online" : ""} />{conversationModelStatus(model, modelLoading)}{activeCount > 0 ? ` · ${activeCount}/${MAX_ACTIVE_CHATS} 个对话进行中` : ""}</div>
      </header>
      {activeContext?.exam_id ? <div className="school-conversation-context"><span>当前考试<strong>{activeContext.exam_name || activeContext.exam_id}</strong></span><Button type="text" size="small" icon={<X size={13} />} aria-label="移除当前考试上下文" disabled={Boolean(activePending)} onClick={clearContext}>移除</Button></div> : null}

      <ConversationFlow thread={activeThread} pending={activePending} evidence={replyEvidence}
        failedEvidence={activeThread && failedEvidence[activeThread.id]} sendError={activeThread && sendErrors[activeThread.id]}
        user={user} onNavigate={onNavigate} reasoningExpanded={Boolean(activeId && expandedReasoning.current.has(activeId))}
        onReasoningToggle={(expanded) => {
          if (activeId) { if (expanded) expandedReasoning.current.add(activeId); else expandedReasoning.current.delete(activeId); }
        }} copiedMessage={copiedMessage} onCopy={copyReply} retryDisabled={activeCount >= MAX_ACTIVE_CHATS} onRetry={retry}
        modelError={modelError} model={model} onSuggestion={(question) => { setDraft(question); inputRef.current?.focus(); }} scrollEndRef={scrollEnd} />

      <div className="school-chat-composer-wrap">
        <form className="school-chat-composer" onSubmit={send}>
          {draftAttachments.length > 0 ? <div className="school-chat-attachments">{draftAttachments.map((attachment) => <span key={`${attachment.name}-${attachment.size}`}><FileText size={15} /><b title={attachment.name}>{attachment.name}</b><small>{Math.max(1, Math.round(attachment.size / 1024))} KB</small><button type="button" aria-label={`移除附件：${attachment.name}`} onClick={() => setDraftAttachments((current) => current.filter((item) => item !== attachment))}><X size={13} /></button></span>)}</div> : null}
          <Input.TextArea
            ref={inputRef} value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={onInputKeyDown}
            placeholder={conversationPlaceholder(Boolean(model?.available), activeCount >= MAX_ACTIVE_CHATS, Boolean(activePending))} disabled={!model?.available || Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS}
            autoSize={{ minRows: 2, maxRows: 7 }} maxLength={20_000} aria-label="对话输入"
          />
          <div className="school-chat-composer-bottom">
            <input ref={fileInputRef} className="school-chat-file-input" type="file" multiple accept=".pdf,.txt,.md,.csv,.json,.xml,.html,.css,.js,.jsx,.ts,.tsx,.py,.java,.go,.sql,.yaml,.yml,.log,.ini,.conf,text/*,application/pdf,application/json" onChange={(event) => void selectFiles(event)} />
            <Tooltip title="支持 PDF 和常见文本文件，最多 3 个"><Button type="text" size="small" icon={<Paperclip size={17} />} loading={attaching} disabled={Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS || draftAttachments.length >= MAX_ATTACHMENTS} onClick={() => fileInputRef.current?.click()}>上传文件</Button></Tooltip>
            {activePending ? <Button type="primary" shape="circle" icon={<Square size={15} fill="currentColor" />} aria-label="中断当前对话" onClick={stop} /> : <Button type="primary" shape="circle" htmlType="submit" icon={<ArrowUp size={19} />} aria-label="发送消息" disabled={(!draft.trim() && draftAttachments.length === 0) || !model?.available || activeCount >= MAX_ACTIVE_CHATS || attaching} />}
          </div>
        </form>
        <p className="school-chat-disclaimer">业务回答以本轮来源为依据；正式操作请进入业务页面核实。</p>
      </div>
    </section>
  </div>;
}

/** 页面与模块抽屉复用同一对话；授权变化重建内存，避免旧账户的流和证据进入新范围。 */
export function SchoolConversation(props: SchoolConversationProps) {
  return <AccountConversation key={conversationAccountKey(props.user)} {...props} />;
}

