import type { SchoolChatAttachment, SchoolChatMessage, SchoolConversationContext } from "../../api/schoolAiChat";
import { publicConversationSources } from "./conversationWebSearch";

export interface ConversationThread {
  id: string;
  title: string;
  updatedAt: number;
  messages: SchoolChatMessage[];
  context?: SchoolConversationContext;
  business?: boolean;
  // 刷新恢复的片段仅用于显示；未完成的正文不能作为下一轮模型历史或已核实证据。
  interruptedReply?: { content: string; modelConfigID?: string };
}

function validAttachment(value: unknown): value is SchoolChatAttachment {
  if (!value || typeof value !== "object") return false;
  const item = value as Partial<SchoolChatAttachment>;
  return typeof item.name === "string" && typeof item.media_type === "string" && typeof item.content === "string" && typeof item.size === "number";
}

function validMessage(value: unknown): value is SchoolChatMessage {
  if (!value || typeof value !== "object") return false;
  const item = value as Partial<SchoolChatMessage>;
  return (item.role === "user" || item.role === "assistant") && typeof item.content === "string" &&
    (item.reasoning_content === undefined || typeof item.reasoning_content === "string") &&
    (!item.attachments || (Array.isArray(item.attachments) && item.attachments.every(validAttachment)));
}

function storedMessage(message: SchoolChatMessage): SchoolChatMessage {
  // 业务证据与考试范围不落盘；公开网页仅恢复受控字段，后续请求仍须重新搜索。
  const sources = message.role === "assistant" ? publicConversationSources(message.web_sources) : [];
  return {
    role: message.role, content: message.content,
    ...(message.web_search === true ? { web_search: true } : {}),
    ...(sources.length ? { web_sources: sources } : {}),
    ...(message.reasoning_content ? { reasoning_content: message.reasoning_content } : {}),
    ...(message.attachments?.length ? { attachments: message.attachments.map(({ name, media_type, size }) => ({ name, media_type, size, content: "" })) } : {})
  };
}

export function serializeConversationThreads(threads: ConversationThread[]) {
  // 只要本会话曾尝试查询业务，后续追问也可能引用业务数据；整段会话都不能落盘。
  // 在途追问不落盘，但不能因此覆盖掉此前已完成的一般问答；一旦开始业务查询，仍移除整段缓存。
  return JSON.stringify(threads.filter((thread) => !thread.business).map(({ id, title, updatedAt, messages }) => {
    let completedEnd = messages.length;
    while (completedEnd > 0 && messages[completedEnd - 1]?.role !== "assistant") completedEnd--;
    return { id, title, updatedAt, messages: messages.slice(Math.max(0, completedEnd - 50), completedEnd).map(storedMessage) };
  }).filter((thread) => thread.messages.length > 0).slice(0, 20));
}

export function parseConversationThreads(value: unknown): ConversationThread[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 20).filter((item): item is ConversationThread =>
    item && !item.business && typeof item.id === "string" && typeof item.title === "string" && typeof item.updatedAt === "number" &&
    Array.isArray(item.messages) && item.messages.length <= 50 && item.messages.every(validMessage)
  ).map(({ id, title, updatedAt, messages }) => ({ id, title, updatedAt, messages: messages.map(storedMessage) }));
}

export function readConversationThreads(storageKey: string): ConversationThread[] {
  try {
    const raw = window.localStorage.getItem(storageKey);
    return raw ? parseConversationThreads(JSON.parse(raw)) : [];
  } catch { return []; }
}
