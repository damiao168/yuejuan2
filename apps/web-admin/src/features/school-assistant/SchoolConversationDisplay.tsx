import { useState, type CSSProperties, type RefObject } from "react";
import { Button, Tooltip } from "antd";
import { Bot, Check, ChevronDown, Copy, FileText, MessageSquareText, Plus, RotateCcw, Trash2 } from "lucide-react";
import type { SessionUser } from "../../auth/session";
import type { SchoolChatMessage, SchoolChatModel } from "../../api/schoolAiChat";
import { MathMarkdown } from "../../components/MathMarkdown";
import { ConversationEvidenceView } from "./ConversationEvidenceView";
import type { ConversationEvidence } from "./conversationState";
import type { ConversationThread } from "./conversationStorage";
import type { PendingConversation } from "./conversationPresentation";

interface ReasoningProps {
  content: string;
  defaultExpanded?: boolean;
  onToggle?: (expanded: boolean) => void;
}

// 组件在模块级定义，增量刷新不会因组件类型变化重建 Markdown 或展开状态。
function ReasoningDisclosure({ content, defaultExpanded = false, onToggle }: Readonly<ReasoningProps>) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  return <div className={`school-chat-reasoning ${expanded ? "is-expanded" : ""}`}>
    <button type="button" className="school-chat-reasoning-toggle" aria-expanded={expanded} onClick={() => {
      const next = !expanded;
      onToggle?.(next);
      setExpanded(next);
    }}><span>思考过程</span><span className="school-chat-reasoning-control">{expanded ? "收起" : "展开"}<ChevronDown size={14} /></span></button>
    {expanded ? <div className="school-chat-reasoning-content"><MathMarkdown promoteInlineMath={false}>{content}</MathMarkdown></div> : null}
  </div>;
}

interface HistoryProps {
  threads: readonly ConversationThread[];
  activeId: string | null;
  pending: Readonly<Record<string, PendingConversation>>;
  onNew: () => void;
  onSelect: (id: string) => void;
  onDelete: (thread: ConversationThread) => void;
}

export function ConversationHistory({ threads, activeId, pending, onNew, onSelect, onDelete }: Readonly<HistoryProps>) {
  return <>
    <div className="school-chat-history-head"><span>对话历史</span></div>
    <p className="school-conversation-history-note">查询学校数据的对话仅在本次页面保留。</p>
    <Button className="school-chat-new" icon={<Plus size={17} />} onClick={onNew}>开启新对话</Button>
    <div className="school-chat-thread-list">
      {threads.length === 0 ? <p className="school-chat-history-empty">还没有对话记录</p> : threads.map((thread) => <div className={`school-chat-thread ${activeId === thread.id ? "is-active" : ""}`} key={thread.id}>
        <button type="button" onClick={() => onSelect(thread.id)} title={thread.title}><MessageSquareText size={15} /><span>{thread.title}</span>{pending[thread.id] && <i className="school-chat-thread-running" aria-label="回复中" />}</button>
        <Tooltip title="删除对话"><button type="button" className="school-chat-delete" aria-label={`删除对话：${thread.title}`} onClick={() => onDelete(thread)}><Trash2 size={14} /></button></Tooltip>
      </div>)}
    </div>
  </>;
}

function MessageContent({ message }: Readonly<{ message: SchoolChatMessage }>) {
  if (message.role !== "assistant") return message.content;
  if (message.content) return <MathMarkdown promoteInlineMath={false}>{message.content}</MathMarkdown>;
  return <span className="school-chat-interrupted">已中断，尚未生成正文</span>;
}

interface MessagesProps {
  thread: ConversationThread;
  evidence: ReadonlyMap<SchoolChatMessage, ConversationEvidence>;
  user: SessionUser;
  onNavigate?: (path: string) => void;
  reasoningExpanded: boolean;
  copiedMessage: string;
  onCopy: (content: string, key: string) => Promise<void>;
}

export function ConversationMessages({ thread, evidence, user, onNavigate, reasoningExpanded, copiedMessage, onCopy }: Readonly<MessagesProps>) {
  return <>{thread.messages.map((message, index) => {
    const messageKey = `${thread.id}-${index}`;
    return <article className={`school-chat-message is-${message.role}`} key={messageKey}>
      {message.role === "assistant" ? <div className="school-chat-avatar"><Bot size={18} /></div> : null}
      <div className="school-chat-message-body">
        {message.role === "assistant" ? <ConversationEvidenceView evidence={evidence.get(message)} user={user} onNavigate={onNavigate} /> : null}
        {message.attachments?.length ? <div className="school-chat-message-files">{message.attachments.map((attachment) => <span key={`${messageKey}-${attachment.name}`}><FileText size={14} />{attachment.name}</span>)}</div> : null}
        {message.role === "assistant" && message.reasoning_content ? <ReasoningDisclosure content={message.reasoning_content} defaultExpanded={index === thread.messages.length - 1 && reasoningExpanded} /> : null}
        <MessageContent message={message} />
        {message.role === "assistant" && message.content ? <div className="school-chat-message-actions"><Button type="text" size="small" icon={copiedMessage === messageKey ? <Check size={14} /> : <Copy size={14} />} onClick={() => void onCopy(message.content, messageKey)}>{copiedMessage === messageKey ? "已复制" : "复制"}</Button></div> : null}
      </div>
    </article>;
  })}</>;
}

const hiddenStatusStyle: CSSProperties = {
  position: "absolute", width: 1, height: 1, padding: 0, margin: -1,
  overflow: "hidden", clipPath: "inset(50%)", whiteSpace: "nowrap", border: 0,
};

interface PendingProps {
  pending: PendingConversation;
  user: SessionUser;
  onReasoningToggle: (expanded: boolean) => void;
}

export function PendingConversationView({ pending, user, onReasoningToggle }: Readonly<PendingProps>) {
  const thinking = !pending.reasoning && !pending.content;
  return <div className="school-chat-waiting"><span className="school-chat-avatar"><Bot size={18} /></span><div className="school-chat-live-body">
    <ConversationEvidenceView evidence={pending.evidence} user={user} allowActions={false} />
    {/* output 只含短状态；富文本和证据沿用外层 flow 的播报边界，不复制到原生状态区域。 */}
    <output className={thinking ? "school-chat-thinking" : undefined} style={thinking ? undefined : hiddenStatusStyle}>
      {thinking ? <>思考中<span aria-hidden="true"><i /><i /><i /></span></> : "正在回复"}
    </output>
    {pending.reasoning ? <ReasoningDisclosure content={pending.reasoning} onToggle={onReasoningToggle} /> : null}
    {pending.content ? <MathMarkdown promoteInlineMath={false}>{pending.content}</MathMarkdown> : null}
  </div></div>;
}

interface FlowProps {
  thread?: ConversationThread;
  pending?: PendingConversation;
  evidence: ReadonlyMap<SchoolChatMessage, ConversationEvidence>;
  failedEvidence?: ConversationEvidence;
  sendError?: string;
  user: SessionUser;
  onNavigate?: (path: string) => void;
  reasoningExpanded: boolean;
  onReasoningToggle: (expanded: boolean) => void;
  copiedMessage: string;
  onCopy: (content: string, key: string) => Promise<void>;
  retryDisabled: boolean;
  onRetry: () => void;
  modelError: string;
  model: SchoolChatModel | null;
  onSuggestion: (question: string) => void;
  scrollEndRef: RefObject<HTMLDivElement | null>;
}

export function ConversationFlow({ thread, pending, evidence, failedEvidence, sendError, user, onNavigate, reasoningExpanded, onReasoningToggle, copiedMessage, onCopy, retryDisabled, onRetry, modelError, model, onSuggestion, scrollEndRef }: Readonly<FlowProps>) {
  return <div className={`school-chat-flow ${!thread ? "is-empty" : ""}`} aria-live="polite">
    {!thread ? <div className="school-chat-welcome">
      <div className="school-chat-welcome-mark"><Bot size={30} strokeWidth={1.6} /></div>
      <h1>有什么可以帮你？</h1>
      <p>直接提问，助手会按需查询学校数据，也可以帮你撰写和分析文字。</p>
      <div className="school-conversation-suggestions" aria-label="建议问题">{["列出我能查看的考试", "这场考试进度怎么样？", "为什么成绩还不能发布？", "帮我起草一份考试通知"].map((question) => <button type="button" key={question} onClick={() => onSuggestion(question)}>{question}</button>)}</div>
      {modelError || (model && !model.available) ? <div className="school-chat-notice" role="alert">{modelError || model?.message}</div> : null}
    </div> : <div className="school-chat-messages">
      <ConversationMessages thread={thread} evidence={evidence} user={user} onNavigate={onNavigate}
        reasoningExpanded={reasoningExpanded} copiedMessage={copiedMessage} onCopy={onCopy} />
      {pending ? <PendingConversationView pending={pending} user={user} onReasoningToggle={onReasoningToggle} /> : null}
      {sendError ? <div className="school-conversation-failure"><ConversationEvidenceView evidence={failedEvidence} user={user} allowActions={false} /><div className="school-chat-send-error" role="alert"><span>{sendError}</span><Button type="link" size="small" icon={<RotateCcw size={14} />} disabled={retryDisabled} onClick={onRetry}>重试</Button></div></div> : null}
      <div ref={scrollEndRef} />
    </div>}
  </div>;
}
