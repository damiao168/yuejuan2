import { useCallback, useEffect, useRef, useState, type ChangeEvent, type FormEvent, type KeyboardEvent } from "react";
import { App, Button, Drawer, Input, Tooltip } from "antd";
import { ArrowUp, Bot, Check, ChevronDown, Copy, FileText, Menu, MessageSquareText, Paperclip, Plus, RotateCcw, Square, Trash2, X } from "lucide-react";
import { GlobalWorkerOptions, getDocument } from "pdfjs-dist";
import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import type { SessionUser } from "../auth/session";
import { getUserErrorMessage } from "../api/client";
import { getSchoolChatModel, streamSchoolChat, type SchoolChatAttachment, type SchoolChatMessage, type SchoolChatModel } from "../api/schoolAiChat";
import { MathMarkdown } from "../components/MathMarkdown";
import { interruptedMessage, requestMessages } from "./schoolAiChatMessages";
import "./school-ai-chat.css";

interface ChatThread {
  id: string;
  title: string;
  updatedAt: number;
  messages: SchoolChatMessage[];
}

GlobalWorkerOptions.workerSrc = pdfWorker;

const MAX_ATTACHMENTS = 3;
const MAX_ACTIVE_CHATS = 5;
const MAX_TEXT_FILE_BYTES = 512 * 1024;
const MAX_PDF_FILE_BYTES = 10 * 1024 * 1024;
const MAX_ATTACHMENT_CHARACTERS = 60_000;
const supportedTextExtensions = new Set([
  "txt", "md", "csv", "json", "xml", "html", "css", "js", "jsx", "ts", "tsx",
  "py", "java", "go", "sql", "yaml", "yml", "log", "ini", "conf"
]);

function validStoredAttachment(value: unknown): value is SchoolChatAttachment {
  if (!value || typeof value !== "object") return false;
  const attachment = value as Partial<SchoolChatAttachment>;
  return typeof attachment.name === "string" && typeof attachment.media_type === "string" &&
    typeof attachment.content === "string" && typeof attachment.size === "number";
}

function readThreads(storageKey: string): ChatThread[] {
  try {
    const raw = window.localStorage.getItem(storageKey);
    if (!raw) return [];
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value)) return [];
    return value.slice(0, 20).filter((item): item is ChatThread =>
      typeof item?.id === "string" && typeof item.title === "string" &&
      typeof item.updatedAt === "number" && Array.isArray(item.messages) &&
      item.messages.length <= 50 && item.messages.every((message: SchoolChatMessage) =>
        (message.role === "user" || message.role === "assistant") && typeof message.content === "string" &&
        (message.reasoning_content === undefined || typeof message.reasoning_content === "string") &&
        (!message.attachments || (Array.isArray(message.attachments) && message.attachments.every(validStoredAttachment)))));
  } catch {
    return [];
  }
}

function attachmentMediaType(file: File) {
  if (file.type) return file.type;
  const extension = file.name.split(".").pop()?.toLowerCase();
  if (extension === "pdf") return "application/pdf";
  if (extension === "md") return "text/markdown";
  if (extension === "csv") return "text/csv";
  if (extension === "json") return "application/json";
  return "text/plain";
}

async function readPDFText(file: File) {
  const document = await getDocument({ data: await file.arrayBuffer() }).promise;
  try {
    const pages: string[] = [];
    let length = 0;
    for (let pageNumber = 1; pageNumber <= document.numPages; pageNumber += 1) {
      const page = await document.getPage(pageNumber);
      const text = await page.getTextContent();
      const pageText = text.items.map((item) => "str" in item ? item.str : "").join(" ").trim();
      length += Array.from(pageText).length;
      if (length > MAX_ATTACHMENT_CHARACTERS) throw new Error("文件文字过多，请拆分后上传");
      pages.push(`第 ${pageNumber} 页\n${pageText}`);
    }
    return pages.join("\n\n").trim();
  } finally {
    await document.destroy();
  }
}

async function readChatAttachment(file: File): Promise<SchoolChatAttachment> {
  const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
  const isPDF = extension === "pdf" || file.type === "application/pdf";
  const isText = file.type.startsWith("text/") || supportedTextExtensions.has(extension) || file.type === "application/json";
  if (!isPDF && !isText) throw new Error("仅支持 PDF、文本、Markdown、表格文本、代码和日志文件");
  if (file.size > (isPDF ? MAX_PDF_FILE_BYTES : MAX_TEXT_FILE_BYTES)) {
    throw new Error(isPDF ? "PDF 文件不能超过 10 MB" : "文本文件不能超过 512 KB");
  }
  const content = isPDF ? await readPDFText(file) : (await file.text()).trim();
  if (!content || content.includes("\x00")) throw new Error("文件为空或不是可读取的文本文件");
  if (Array.from(content).length > MAX_ATTACHMENT_CHARACTERS) throw new Error("文件文字不能超过 60000 字，请拆分后上传");
  return { name: file.name, media_type: attachmentMediaType(file), content, size: file.size };
}

function ReasoningDisclosure({ content, defaultExpanded = false, onToggle }: { content: string; defaultExpanded?: boolean; onToggle?: (expanded: boolean) => void }) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  return <div className={`school-chat-reasoning ${expanded ? "is-expanded" : ""}`}>
    <button type="button" className="school-chat-reasoning-toggle" aria-expanded={expanded} onClick={() => {
      const next = !expanded;
      onToggle?.(next);
      setExpanded(next);
    }}><span>思考过程</span><span className="school-chat-reasoning-control">{expanded ? "收起" : "展开"}<ChevronDown size={14} /></span></button>
    {expanded ? <div className="school-chat-reasoning-content">{content}</div> : null}
  </div>;
}

function titleFromMessage(content: string) {
  const title = content.replace(/\s+/g, " ").trim();
  return title.length > 28 ? `${title.slice(0, 28)}…` : title;
}

export function SchoolAIChatPage({ user }: { user: SessionUser }) {
  const { message: toast, modal } = App.useApp();
  const storageKey = `edugrade.ai-chat.v1:${user.tenant}:${user.id}`;
  const [threads, setThreads] = useState<ChatThread[]>(() => user.publicComputer ? [] : readThreads(storageKey));
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [draftAttachments, setDraftAttachments] = useState<SchoolChatAttachment[]>([]);
  const [attaching, setAttaching] = useState(false);
  const [model, setModel] = useState<SchoolChatModel | null>(null);
  const [modelLoading, setModelLoading] = useState(true);
  const [modelError, setModelError] = useState("");
  const [sendErrors, setSendErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<Record<string, { content: string; reasoning: string }>>({});
  const controllers = useRef(new Map<string, AbortController>());
  const expandedReasoning = useRef(new Set<string>());
  const [historyOpen, setHistoryOpen] = useState(false);
  const [copiedMessage, setCopiedMessage] = useState("");
  const scrollEnd = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const activeThread = threads.find((thread) => thread.id === activeId);
  const activePending = activeId ? pending[activeId] : undefined;
  const activeCount = Object.keys(pending).length;

  useEffect(() => () => { for (const controller of controllers.current.values()) controller.abort(); }, []);

  useEffect(() => {
    if (user.publicComputer) return;
    try {
      window.localStorage.setItem(storageKey, JSON.stringify(threads.slice(0, 20).map((thread) => ({
        ...thread,
        messages: thread.messages.slice(-50).map((message) => ({
          ...message,
          attachments: message.attachments?.map((attachment) => ({ ...attachment, content: "" }))
        }))
      }))));
    } catch {
      // Storage can be disabled or full; the current conversation remains in memory.
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
    if (activeId && expandedReasoning.current.has(activeId)) return;
    scrollEnd.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [activeId, activeThread?.messages.length, activePending?.content]);

  const openNew = useCallback(() => {
    setActiveId(null);
    setDraft("");
    setDraftAttachments([]);
    setHistoryOpen(false);
    inputRef.current?.focus();
  }, []);

  const complete = useCallback(async (threadId: string, messages: SchoolChatMessage[]) => {
    if (controllers.current.has(threadId) || controllers.current.size >= MAX_ACTIVE_CHATS) return;
    const controller = new AbortController();
    controllers.current.set(threadId, controller);
    setPending((current) => ({ ...current, [threadId]: { content: "", reasoning: "" } }));
    setSendErrors((current) => { const next = { ...current }; delete next[threadId]; return next; });
    let partial = { content: "", reasoning: "" };
    let completed = false;
    try {
      await streamSchoolChat(requestMessages(messages.slice(-49)), controller.signal, (event) => {
        if (event.type === "error") throw new Error("模型暂时无法回答，请稍后重试");
        if (event.type === "reasoning" || event.type === "content") {
          partial = { ...partial, [event.type === "reasoning" ? "reasoning" : "content"]: partial[event.type === "reasoning" ? "reasoning" : "content"] + (event.delta ?? "") };
          setPending((current) => ({ ...current, [threadId]: partial }));
        }
        if (event.type === "done" && event.completion) {
          completed = true;
          setThreads((current) => current.map((thread) => thread.id === threadId ? {
            ...thread, messages: [...thread.messages, event.completion!.message].slice(-50), updatedAt: Date.now()
          } : thread));
        }
      });
      if (!completed) throw new Error("对话流意外中断");
    } catch (error) {
      if (controller.signal.aborted) {
        const interrupted = completed ? null : interruptedMessage(partial);
        if (interrupted) setThreads((current) => current.map((thread) => thread.id === threadId ? {
          ...thread, messages: [...thread.messages, interrupted].slice(-50), updatedAt: Date.now()
        } : thread));
      } else {
        setSendErrors((current) => ({ ...current, [threadId]: getUserErrorMessage(error, "发送失败，请重试") }));
      }
    } finally {
      controllers.current.delete(threadId);
      setPending((current) => { const next = { ...current }; delete next[threadId]; return next; });
    }
  }, []);

  const send = useCallback((event?: FormEvent) => {
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
      const next: ChatThread = {
        id, title: existing?.title ?? titleFromMessage(content || draftAttachments[0]?.name || "附件对话"), updatedAt: Date.now(), messages
      };
      return [next, ...current.filter((thread) => thread.id !== id)]
        .filter((thread, index) => index < 20 || controllers.current.has(thread.id));
    });
    setActiveId(id);
    setDraft("");
    setDraftAttachments([]);
    void complete(id, messages);
  }, [activeId, activeThread, attaching, complete, draft, draftAttachments, model?.available]);

  const stop = () => { if (activeId) controllers.current.get(activeId)?.abort(); };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      send();
    }
  };

  const deleteThread = (thread: ChatThread) => {
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

  const selectFiles = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    const available = MAX_ATTACHMENTS - draftAttachments.length;
    if (available <= 0) {
      toast.warning(`每次最多上传 ${MAX_ATTACHMENTS} 个文件`);
      return;
    }
    if (files.length > available) toast.warning(`每次最多上传 ${MAX_ATTACHMENTS} 个文件`);
    setAttaching(true);
    const accepted: SchoolChatAttachment[] = [];
    for (const file of files.slice(0, available)) {
      try {
        accepted.push(await readChatAttachment(file));
      } catch (error) {
        toast.error(`${file.name}：${getUserErrorMessage(error, "文件读取失败")}`);
      }
    }
    if (accepted.length > 0) setDraftAttachments((current) => [...current, ...accepted]);
    setAttaching(false);
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
      void complete(activeThread.id, activeThread.messages);
    }
  };

  const historyContent = <>
      <div className="school-chat-history-head">
        <span>对话历史</span>
      </div>
      <Button className="school-chat-new" icon={<Plus size={17} />} onClick={openNew}>开启新对话</Button>
      <div className="school-chat-thread-list">
        {threads.length === 0 ? <p className="school-chat-history-empty">还没有对话记录</p> : threads.map((thread) => <div className={`school-chat-thread ${activeId === thread.id ? "is-active" : ""}`} key={thread.id}>
          <button type="button" onClick={() => { setActiveId(thread.id); setHistoryOpen(false); }} title={thread.title}><MessageSquareText size={15} /><span>{thread.title}</span>{pending[thread.id] && <i className="school-chat-thread-running" aria-label="回复中" />}</button>
          <Tooltip title="删除对话"><button type="button" className="school-chat-delete" aria-label={`删除对话：${thread.title}`} onClick={() => deleteThread(thread)}><Trash2 size={14} /></button></Tooltip>
        </div>)}
      </div>
    </>;

  return <div className="school-chat-shell">
    <aside className="school-chat-history" aria-label="对话历史">{historyContent}</aside>
    <Drawer title="对话历史" placement="left" open={historyOpen} onClose={() => setHistoryOpen(false)} width={280} styles={{ body: { padding: 0 } }} className="school-chat-mobile-drawer">
      <div className="school-chat-history school-chat-history-mobile">{historyContent}</div>
    </Drawer>

    <section className="school-chat-main" aria-label="AI 对话内容">
      <header className="school-chat-topbar">
        <div className="school-chat-topbar-title"><Button className="school-chat-mobile-menu" type="text" icon={<Menu size={18} />} aria-label="打开对话历史" onClick={() => setHistoryOpen(true)} /><Bot size={21} /><span>AI 对话</span></div>
        <div className="school-chat-model-status"><i className={model?.available ? "is-online" : ""} />{modelLoading ? "正在读取模型" : model?.available ? `${model.display_name} · ${model.model_name}` : "模型未就绪"}{activeCount > 0 ? ` · ${activeCount}/${MAX_ACTIVE_CHATS} 个对话进行中` : ""}</div>
      </header>

      <div className={`school-chat-flow ${!activeThread ? "is-empty" : ""}`} aria-live="polite">
        {!activeThread ? <div className="school-chat-welcome">
          <div className="school-chat-welcome-mark"><Bot size={30} strokeWidth={1.6} /></div>
          <h1>有什么可以帮你？</h1>
          <p>使用学校当前配置的模型，直接提问或继续已有对话。</p>
          {modelError || (model && !model.available) ? <div className="school-chat-notice" role="alert">{modelError || model?.message}</div> : null}
        </div> : <div className="school-chat-messages">
          {activeThread.messages.map((message, index) => {
            const messageKey = `${activeThread.id}-${index}`;
            return <article className={`school-chat-message is-${message.role}`} key={messageKey}>
              {message.role === "assistant" ? <div className="school-chat-avatar"><Bot size={18} /></div> : null}
              <div className="school-chat-message-body">
                {message.attachments?.length ? <div className="school-chat-message-files">{message.attachments.map((attachment) => <span key={`${messageKey}-${attachment.name}`}><FileText size={14} />{attachment.name}</span>)}</div> : null}
                {message.role === "assistant" && message.reasoning_content ? <ReasoningDisclosure content={message.reasoning_content} defaultExpanded={index === activeThread.messages.length - 1 && expandedReasoning.current.has(activeThread.id)} /> : null}
                {message.role === "assistant" ? message.content ? <MathMarkdown>{message.content}</MathMarkdown> : <span className="school-chat-interrupted">已中断，尚未生成正文</span> : message.content}
                {message.role === "assistant" && message.content ? <div className="school-chat-message-actions"><Button type="text" size="small" icon={copiedMessage === messageKey ? <Check size={14} /> : <Copy size={14} />} onClick={() => void copyReply(message.content, messageKey)}>{copiedMessage === messageKey ? "已复制" : "复制"}</Button></div> : null}
              </div>
            </article>;
          })}
          {activePending ? <div className="school-chat-waiting" role="status"><span className="school-chat-avatar"><Bot size={18} /></span><div className="school-chat-live-body">{activePending.reasoning ? <ReasoningDisclosure content={activePending.reasoning} onToggle={(expanded) => {
            if (activeId) { if (expanded) expandedReasoning.current.add(activeId); else expandedReasoning.current.delete(activeId); }
          }} /> : !activePending.content ? <span className="school-chat-thinking">思考中<span aria-hidden="true"><i /><i /><i /></span></span> : null}{activePending.content ? <MathMarkdown>{activePending.content}</MathMarkdown> : null}</div></div> : null}
          {sendErrors[activeThread.id] ? <div className="school-chat-send-error" role="alert"><span>{sendErrors[activeThread.id]}</span><Button type="link" size="small" icon={<RotateCcw size={14} />} disabled={activeCount >= MAX_ACTIVE_CHATS} onClick={retry}>重试</Button></div> : null}
          <div ref={scrollEnd} />
        </div>}
      </div>

      <div className="school-chat-composer-wrap">
        <form className="school-chat-composer" onSubmit={send}>
          {draftAttachments.length > 0 ? <div className="school-chat-attachments">{draftAttachments.map((attachment) => <span key={`${attachment.name}-${attachment.size}`}><FileText size={15} /><b title={attachment.name}>{attachment.name}</b><small>{Math.max(1, Math.round(attachment.size / 1024))} KB</small><button type="button" aria-label={`移除附件：${attachment.name}`} onClick={() => setDraftAttachments((current) => current.filter((item) => item !== attachment))}><X size={13} /></button></span>)}</div> : null}
          <Input.TextArea
            ref={inputRef} value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={onInputKeyDown}
            placeholder={model?.available ? activeCount >= MAX_ACTIVE_CHATS && !activePending ? "已达到 5 个并行对话上限" : "给 AI 发送消息" : "等待学校模型就绪"} disabled={!model?.available || Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS}
            autoSize={{ minRows: 2, maxRows: 7 }} maxLength={20_000} aria-label="对话输入"
          />
          <div className="school-chat-composer-bottom">
            <input ref={fileInputRef} className="school-chat-file-input" type="file" multiple accept=".pdf,.txt,.md,.csv,.json,.xml,.html,.css,.js,.jsx,.ts,.tsx,.py,.java,.go,.sql,.yaml,.yml,.log,.ini,.conf,text/*,application/pdf,application/json" onChange={(event) => void selectFiles(event)} />
            <Tooltip title="支持 PDF 和常见文本文件，最多 3 个"><Button type="text" size="small" icon={<Paperclip size={17} />} loading={attaching} disabled={Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS || draftAttachments.length >= MAX_ATTACHMENTS} onClick={() => fileInputRef.current?.click()}>上传文件</Button></Tooltip>
            {activePending ? <Button type="primary" shape="circle" icon={<Square size={15} fill="currentColor" />} aria-label="中断当前对话" onClick={stop} /> : <Button type="primary" shape="circle" htmlType="submit" icon={<ArrowUp size={19} />} aria-label="发送消息" disabled={(!draft.trim() && draftAttachments.length === 0) || !model?.available || activeCount >= MAX_ACTIVE_CHATS || attaching} />}
          </div>
        </form>
        <p className="school-chat-disclaimer">模型回答仅供参考，请核实重要信息。请勿输入学生个人敏感信息。</p>
      </div>
    </section>
  </div>;
}
