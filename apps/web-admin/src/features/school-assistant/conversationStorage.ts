import type { SchoolChatAttachment, SchoolChatMessage, SchoolConversationContext } from "../../api/schoolAiChat";
import { publicConversationSources } from "./conversationWebSearch";
import { compactConversationHistory, MAX_CONVERSATION_HISTORY_MESSAGES, parseConversationHistoryExcerpt, type ConversationHistoryExcerpt } from "./conversationHistory";
import type { ConversationTurnMetrics } from "./conversationMetrics";

const MAX_STORED_CONVERSATION_UNITS = 2_000_000;
const MAX_READ_CONVERSATION_UNITS = 5 * 1024 * 1024;

export interface ConversationThread {
  id: string;
  title: string;
  updatedAt: number;
  messages: SchoolChatMessage[];
  context?: SchoolConversationContext;
  business?: boolean;
  historyExcerpt?: ConversationHistoryExcerpt;
  // 仅记录本次请求实际省略状态，不把估算值当作供应商计数或业务事实。
  contextOmitted?: boolean;
  // 指标只属于当前标签页收到的那次请求，不随历史恢复成当前模型的用量。
  metrics?: ConversationTurnMetrics;
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
    ...(message.role === "user" && typeof message.memory_source_id === "string" && /^[a-zA-Z0-9_-]{1,90}$/.test(message.memory_source_id)
      && Number.isSafeInteger(message.memory_order) && message.memory_order! >= 0 ? {
        memory_source_id: message.memory_source_id, memory_order: message.memory_order,
        ...(message.memory_pinned ? { memory_pinned: true } : {}), ...(message.memory_excluded ? { memory_excluded: true } : {}),
      } : {}),
    ...(message.web_search === true ? { web_search: true } : {}),
    ...(sources.length ? { web_sources: sources } : {}),
    ...(message.reasoning_content ? { reasoning_content: message.reasoning_content } : {}),
    ...(message.attachments?.length ? { attachments: message.attachments.map(({ name, media_type, size }) => ({ name, media_type, size, content: "" })) } : {})
  };
}

export function serializeConversationThreads(threads: ConversationThread[]) {
  // 只要本会话曾尝试查询业务，后续追问也可能引用业务数据；整段会话都不能落盘。
  // 在途追问不落盘，但不能因此覆盖掉此前已完成的一般问答；一旦开始业务查询，仍移除整段缓存。
  const completed = threads.filter((thread) => !thread.business).map(({ id, title, updatedAt, messages, historyExcerpt }) => {
    let completedEnd = messages.length;
    while (completedEnd > 0 && messages[completedEnd - 1]?.role !== "assistant") completedEnd--;
    const excerpt = parseConversationHistoryExcerpt(historyExcerpt);
    const incompleteSources = messages.slice(completedEnd).filter(message => message.role === "user" && message.memory_source_id).map(message => `${message.memory_source_id}-`);
    const completedExcerpt = excerpt ? { ...excerpt, entries: excerpt.entries.filter(entry => !incompleteSources.some(prefix => entry.source_id.startsWith(prefix))) } : undefined;
    return compactConversationHistory({ id, title, updatedAt, messages: messages.slice(0, completedEnd).map(storedMessage), ...(completedExcerpt ? { historyExcerpt: completedExcerpt } : {}) });
  }).filter((thread) => thread.messages.length > 0).slice(0, 20);
  let remaining = MAX_STORED_CONVERSATION_UNITS - 2;
  // 较长历史不能让一次写入挤爆浏览器配额；优先保留列表前面的近期会话。
  return `[${completed.flatMap(thread => {
    const serialized = JSON.stringify(thread);
    if (serialized.length + 1 > remaining) return [];
    remaining -= serialized.length + 1;
    return [serialized];
  }).join(",")}]`;
}

export function parseConversationThreads(value: unknown): ConversationThread[] {
  if (!Array.isArray(value)) return [];
  return value.slice(0, 20).filter((item): item is ConversationThread =>
    item && !item.business && typeof item.id === "string" && typeof item.title === "string" && typeof item.updatedAt === "number" &&
    Array.isArray(item.messages) && item.messages.length <= MAX_CONVERSATION_HISTORY_MESSAGES && item.messages.every(validMessage)
  ).map(({ id, title, updatedAt, messages, historyExcerpt }) => {
    const excerpt = parseConversationHistoryExcerpt(historyExcerpt);
    return compactConversationHistory({ id, title, updatedAt, messages: messages.map(storedMessage), ...(excerpt ? { historyExcerpt: excerpt } : {}) });
  });
}

export function readConversationThreads(storageKey: string): ConversationThread[] {
  try {
    const raw = window.localStorage.getItem(storageKey);
    if (!raw || raw.length > MAX_READ_CONVERSATION_UNITS) return [];
    // 旧版缓存可能超过新写入预算；先恢复并按会话裁剪，再保留总预算内的近期历史，不能整批清空。
    const restored = parseConversationThreads(JSON.parse(raw));
    return parseConversationThreads(JSON.parse(serializeConversationThreads(restored)));
  } catch { return []; }
}
