import type { SchoolChatMessage, SchoolConversationMemory, SchoolConversationMemoryEntry } from "../../api/schoolAiChat";

export const MAX_CONVERSATION_HISTORY_MESSAGES = 200;
export const MAX_CONVERSATION_REQUEST_MESSAGES = 49;
const MAX_HISTORY_TEXT_UNITS = 200_000;
const MAX_HISTORY_ATTACHMENT_UNITS = 16 * 1024 * 1024;
const MAX_REQUEST_TEXT_RUNES = 180_000;
const MAX_REQUEST_IMAGE_BYTES = 9 * 1024 * 1024;
const MAX_POOL_ENTRIES = 64;
const MAX_POOL_RUNES = 16_000;
const MAX_MEMORY_ENTRIES = 16;
const MAX_MEMORY_RUNES = 4_000;
const MAX_ENTRY_RUNES = 1_000;

export type ConversationHistoryExcerpt = SchoolConversationMemory | { version: 1; userNotes: string[] };

function runeLength(content: string) {
  let count = 0;
  for (let index = 0; index < content.length; index += content.codePointAt(index)! > 0xffff ? 2 : 1) count++;
  return count;
}

// 哈希只生成本地来源标识；它不证明正文完整性、身份或授权，服务端仍把来源与原文视为客户端材料。
function contentHash(content: string) {
  let hash = 0x811c9dc5;
  for (let index = 0; index < content.length; index++) hash = Math.imul(hash ^ content.charCodeAt(index), 0x01000193);
  return (hash >>> 0).toString(16).padStart(8, "0");
}

function validSource(value: unknown): value is string { return typeof value === "string" && /^[a-zA-Z0-9_-]{1,100}$/.test(value); }
function validOrder(value: unknown): value is number { return typeof value === "number" && Number.isSafeInteger(value) && value >= 0; }

function sourceMessages(messages: SchoolChatMessage[], previous?: SchoolConversationMemory) {
  // 新问题排在已知来源之后；裁显示历史和刷新恢复时保留现有标识，避免同一片段反复变成新记忆。
  let order = Math.max(-1, ...(previous?.entries.map(entry => entry.order) ?? []), ...messages.map(message => validOrder(message.memory_order) ? message.memory_order : -1)) + 1;
  return messages.map(message => {
    if (message.role !== "user" || validSource(message.memory_source_id) && validOrder(message.memory_order)) return message;
    const next = order++;
    return { ...message, memory_source_id: `m-${contentHash(message.content)}-${next}`, memory_order: next };
  });
}

function fragments(content: string) {
  // 按句、行保留原文片段，长句再分块；中部约束可独立选择，不靠固定头尾截取。
  return (content.match(/[^。！？!?；;\n]+[。！？!?；;\n]?/gu) ?? []).flatMap(piece => {
    const text = piece.trim();
    if (!text) return [];
    const runes = Array.from(text);
    return Array.from({ length: Math.ceil(runes.length / MAX_ENTRY_RUNES) }, (_, index) => runes.slice(index * MAX_ENTRY_RUNES, (index + 1) * MAX_ENTRY_RUNES).join(""));
  });
}

function messageEntries(messages: readonly SchoolChatMessage[]) {
  return messages.flatMap(message => {
    // 不读取附件、助手正文和推理；显式移除的材料也不由自动选择重新加入。
    if (message.role !== "user" || message.memory_excluded || !validSource(message.memory_source_id) || !validOrder(message.memory_order)) return [];
    return fragments(message.content).map((content, index): SchoolConversationMemoryEntry => ({
      source_id: `${message.memory_source_id}-${index.toString().padStart(3, "0")}`, order: message.memory_order!, content,
      ...(message.memory_pinned ? { pinned: true } : {}),
    }));
  });
}

function importance(content: string) {
  // 词面标记只提升候选优先级，不推断撤销对象，也不删除相冲突的旧要求。
  if (/撤销|取消此前|取消之前|不再|改为|更正|纠正|以此为准|不要再/.test(content)) return 3;
  if (/必须|不要|只能|保持|要求|约定|目标|请用|请按|格式|限制|记住/.test(content)) return 2;
  return 0;
}

function memoryEntryOrder(left: SchoolConversationMemoryEntry, right: SchoolConversationMemoryEntry) {
  if (left.order !== right.order) return left.order - right.order;
  const leftSegment = /^(.*)-(\d+)$/.exec(left.source_id);
  const rightSegment = /^(.*)-(\d+)$/.exec(right.source_id);
  // 来源 ID 已经保存在浏览器中，不能靠改编号修复顺序；同一来源的第1000段仍应排在第999段之后。
  if (leftSegment && rightSegment && leftSegment[1] === rightSegment[1]) {
    const leftIndex = Number(leftSegment[2]);
    const rightIndex = Number(rightSegment[2]);
    if (Number.isSafeInteger(leftIndex) && Number.isSafeInteger(rightIndex) && leftIndex !== rightIndex) return leftIndex - rightIndex;
  }
  return left.source_id.localeCompare(right.source_id);
}

function boundedPool(entries: SchoolConversationMemoryEntry[]): SchoolConversationMemory | undefined {
  const unique = [...new Map(entries.map(entry => [entry.source_id, entry])).values()];
  const earliest = Math.min(...unique.map(entry => entry.order));
  const ranked = unique.sort((left, right) => Number(Boolean(right.pinned)) - Number(Boolean(left.pinned)) || importance(right.content) - importance(left.content)
    || Number(right.order === earliest) - Number(left.order === earliest) || right.order - left.order);
  let remaining = MAX_POOL_RUNES;
  const selected: SchoolConversationMemoryEntry[] = [];
  for (const entry of ranked) {
    const size = runeLength(entry.content);
    if (size <= remaining && selected.length < MAX_POOL_ENTRIES) { selected.push(entry); remaining -= size; }
  }
  selected.sort(memoryEntryOrder);
  return selected.length ? { version: 2, entries: selected } : undefined;
}

export function parseConversationHistoryExcerpt(value: unknown): SchoolConversationMemory | undefined {
  if (!value || typeof value !== "object") return undefined;
  const item = value as { version?: unknown; userNotes?: unknown; entries?: unknown };
  if (item.version === 1 && Array.isArray(item.userNotes) && item.userNotes.length <= 12 && item.userNotes.every(note => typeof note === "string" && note.length <= 8_000)) {
    return boundedPool(messageEntries(sourceMessages(item.userNotes.map(content => ({ role: "user" as const, content: content as string })))));
  }
  if (item.version !== 2 || !Array.isArray(item.entries) || item.entries.length > MAX_POOL_ENTRIES) return undefined;
  const entries: SchoolConversationMemoryEntry[] = [];
  for (const value of item.entries) {
    if (!value || typeof value !== "object") return undefined;
    const entry = value as Partial<SchoolConversationMemoryEntry>;
    if (!validSource(entry.source_id) || !validOrder(entry.order) || typeof entry.content !== "string" || !entry.content.trim()
      || runeLength(entry.content) > MAX_ENTRY_RUNES || entry.pinned !== undefined && typeof entry.pinned !== "boolean") return undefined;
    entries.push({ source_id: entry.source_id, order: entry.order, content: entry.content, ...(entry.pinned ? { pinned: true } : {}) });
  }
  return boundedPool(entries);
}

function nextTurn(messages: readonly SchoolChatMessage[], start: number) {
  let next = start + 1;
  while (next < messages.length && messages[next]?.role !== "user") next++;
  return next;
}

function lastQuestionIndex(messages: readonly SchoolChatMessage[]) {
  for (let index = messages.length - 1; index >= 0; index--) if (messages[index]?.role === "user") return index;
  return -1;
}

function historySize(message: SchoolChatMessage) {
  return { text: message.content.length + (message.reasoning_content?.length ?? 0), attachments: message.attachments?.reduce((sum, attachment) => sum + attachment.content.length, 0) ?? 0 };
}

/** 显示历史按整轮限制；旧用户原文保留为有界来源池，不提升为模型事实。 */
export function compactConversationHistory<T extends { messages: SchoolChatMessage[]; historyExcerpt?: ConversationHistoryExcerpt }>(thread: T): Omit<T, "messages" | "historyExcerpt"> & { messages: SchoolChatMessage[]; historyExcerpt?: SchoolConversationMemory } {
  const previous = parseConversationHistoryExcerpt(thread.historyExcerpt);
  const messages = sourceMessages(thread.messages, previous);
  let start = Math.max(0, messages.length - MAX_CONVERSATION_HISTORY_MESSAGES);
  while (start > 0 && start < messages.length && messages[start]?.role !== "user") start++;
  const sizes = messages.map(historySize);
  let text = sizes.slice(start).reduce((sum, size) => sum + size.text, 0);
  let attachments = sizes.slice(start).reduce((sum, size) => sum + size.attachments, 0);
  const lastQuestion = lastQuestionIndex(messages);
  while (start < lastQuestion && (text > MAX_HISTORY_TEXT_UNITS || attachments > MAX_HISTORY_ATTACHMENT_UNITS)) {
    const end = nextTurn(messages, start);
    for (let index = start; index < end; index++) { text -= sizes[index]!.text; attachments -= sizes[index]!.attachments; }
    start = end;
  }
  const excluded = messages.filter(message => message.memory_excluded).map(message => `${message.memory_source_id}-`);
  const oldEntries = previous?.entries.filter(entry => !excluded.some(prefix => entry.source_id.startsWith(prefix))) ?? [];
  const historyExcerpt = boundedPool([...oldEntries, ...messageEntries(messages.slice(0, start)), ...messageEntries(messages.filter(message => message.memory_pinned))]);
  return { ...thread, messages: messages.slice(start), historyExcerpt };
}

function requestSize(message: SchoolChatMessage) {
  return {
    text: runeLength(message.content) + (message.attachments?.reduce((sum, attachment) => sum + (attachment.media_type.startsWith("image/") ? 0 : runeLength(attachment.content)), 0) ?? 0),
    images: message.attachments?.reduce((sum, attachment) => sum + (attachment.media_type.startsWith("image/") && attachment.content ? attachment.size : 0), 0) ?? 0,
  };
}

function requestStart(messages: SchoolChatMessage[]) {
  let start = Math.max(0, messages.length - MAX_CONVERSATION_REQUEST_MESSAGES);
  while (start > 0 && start < messages.length && messages[start]?.role !== "user") start++;
  const lastQuestion = lastQuestionIndex(messages);
  const sizes = messages.map((message, index) => index >= start ? requestSize(message) : { text: 0, images: 0 });
  let text = sizes.slice(start).reduce((sum, size) => sum + size.text, 0);
  let images = sizes.slice(start).reduce((sum, size) => sum + size.images, 0);
  while (start < lastQuestion && (text > MAX_REQUEST_TEXT_RUNES || images > MAX_REQUEST_IMAGE_BYTES)) {
    const end = nextTurn(messages, start);
    for (let index = start; index < end; index++) { text -= sizes[index]!.text; images -= sizes[index]!.images; }
    start = end;
  }
  return start;
}

/** 近期历史不再夹带伪造的旧 user 消息；记忆通过单独受限字段传输。 */
export function conversationHistoryMessages(messages: SchoolChatMessage[], _previous?: ConversationHistoryExcerpt): SchoolChatMessage[] {
  return messages.slice(requestStart(messages));
}

function queryTerms(query: string) {
  const terms = new Set(query.toLowerCase().match(/[a-z0-9_]{2,}|[\p{Script=Han}]{2,}/gu) ?? []);
  for (const word of [...terms]) if (/\p{Script=Han}/u.test(word)) for (let index = 0; index + 1 < word.length; index++) terms.add(word.slice(index, index + 2));
  return [...terms].slice(0, 128);
}

/** 只作词面相关性检索；保留原文、来源和顺序，不把候选标签当作已理解的语义。 */
export function conversationRequestMemory(messages: SchoolChatMessage[], previous?: ConversationHistoryExcerpt): SchoolConversationMemory | undefined {
  const pool = parseConversationHistoryExcerpt(previous);
  const sourced = sourceMessages(messages, pool);
  const start = requestStart(sourced);
  const current = sourced[lastQuestionIndex(sourced)];
  const candidates = [...new Map([...(pool?.entries ?? []), ...messageEntries(sourced.slice(0, start)), ...messageEntries(sourced.slice(0, -1).filter(message => message.memory_pinned))].map(entry => [entry.source_id, entry])).values()];
  const terms = queryTerms(current?.content ?? "");
  const relevance = (entry: SchoolConversationMemoryEntry) => terms.reduce((score, term) => score + (entry.content.toLowerCase().includes(term) ? term.length : 0), 0);
  const firstOrder = Math.min(...candidates.map(entry => entry.order));
  const ranked = candidates.sort((left, right) => Number(Boolean(right.pinned)) - Number(Boolean(left.pinned)) || importance(right.content) - importance(left.content)
    || relevance(right) - relevance(left) || Number(right.order === firstOrder) - Number(left.order === firstOrder) || right.order - left.order);
  let remaining = MAX_MEMORY_RUNES;
  const selected: SchoolConversationMemoryEntry[] = [];
  for (const entry of ranked) {
    const size = runeLength(entry.content);
    if (size <= remaining && selected.length < MAX_MEMORY_ENTRIES) { selected.push(entry); remaining -= size; }
  }
  selected.sort(memoryEntryOrder);
  return selected.length ? { version: 2, entries: selected } : undefined;
}

export function toggleConversationMemoryPin<T extends { messages: SchoolChatMessage[]; historyExcerpt?: ConversationHistoryExcerpt }>(thread: T, sourceID: string) {
  const current = compactConversationHistory(thread);
  const target = current.messages.find(message => message.role === "user" && message.memory_source_id === sourceID);
  if (!target) {
    // 显示窗口已淘汰原消息时，仍允许从“已保留的要求”移除来源，不能使旧记忆失去删除入口。
    const entries = current.historyExcerpt?.entries.filter(entry => entry.source_id !== sourceID && !entry.source_id.startsWith(`${sourceID}-`));
    return { ...current, historyExcerpt: entries?.length ? { version: 2 as const, entries } : undefined };
  }
  if (!target.content.trim()) return current;
  const pinned = !target.memory_pinned;
  const messages = current.messages.map(message => message === target ? { ...message, memory_pinned: pinned, memory_excluded: !pinned } : message);
  if (pinned) {
    const allPinned = [...new Map([...(current.historyExcerpt?.entries.filter(entry => entry.pinned) ?? []), ...messageEntries(messages.filter(message => message.memory_pinned))].map(entry => [entry.source_id, entry])).values()];
    // 用户明确保留的来源不能被下一次保留静默驱逐；超出源池时保留原状态，让界面解释容量限制。
    if (allPinned.length > MAX_POOL_ENTRIES || allPinned.reduce((sum, entry) => sum + runeLength(entry.content), 0) > MAX_POOL_RUNES) return current;
  }
  // 取消保留同时移除来源池中的该条材料；自然语言撤销保留原文，不能靠关键词推断删除对象。
  return compactConversationHistory({ ...current, messages });
}

export function pinnedConversationMemorySources(thread: { historyExcerpt?: ConversationHistoryExcerpt }) {
  const entries = parseConversationHistoryExcerpt(thread.historyExcerpt)?.entries.filter(entry => entry.pinned) ?? [];
  const sources = new Map<string, string[]>();
  for (const entry of entries) {
    const source = entry.source_id.replace(/-\d+$/, "");
    sources.set(source, [...(sources.get(source) ?? []), entry.content]);
  }
  return [...sources].map(([sourceID, content]) => ({ sourceID, content: content.join("\n") }));
}

export function hasConversationHistoryExcerpt(thread: { messages: SchoolChatMessage[]; historyExcerpt?: ConversationHistoryExcerpt }) {
  return Boolean(parseConversationHistoryExcerpt(thread.historyExcerpt)?.entries.length || thread.messages.length > MAX_CONVERSATION_REQUEST_MESSAGES);
}
