export const CONVERSATION_REFRESH_PREFIX = "edugrade.ai-chat.refresh.v1:";
export const CONVERSATION_REFRESH_TTL_MS = 30 * 60 * 1_000;
const MAX_ENTRIES = 5;
const MAX_TEXT_LENGTH = 20_000;
const MAX_SERIALIZED_LENGTH = 1_300_000;

export interface ConversationRefreshEntry {
  threadId: string;
  title: string;
  updatedAt: number;
  question: { role: "user"; content: string; web_search?: true };
  // 部分正文只用于中断展示，不能拼入已完成回复或下一次模型历史。
  partialContent: string;
  modelConfigID?: string;
}

export interface ConversationRefreshSnapshot {
  version: 1;
  savedAt: number;
  activeId: string | null;
  entries: ConversationRefreshEntry[];
}

function object(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function identifier(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 200;
}

function shortText(value: string, limit: number): string {
  if (value.length <= limit) return value;
  const truncated = value.slice(0, limit);
  const last = truncated.codePointAt(truncated.length - 1) ?? 0;
  return last >= 0xd800 && last <= 0xdbff ? truncated.slice(0, -1) : truncated;
}

function refreshEntry(value: unknown): ConversationRefreshEntry | null {
  const entry = object(value);
  const question = object(entry?.question);
  // 恢复记录也按白名单读取，不能从手工篡改的缓存带回附件、业务范围或证据。
  if (!entry || entry.business === true || entry.context !== undefined || !identifier(entry.threadId)
    || typeof entry.title !== "string" || typeof entry.updatedAt !== "number" || !Number.isFinite(entry.updatedAt) || entry.updatedAt <= 0
    || question?.role !== "user" || typeof question.content !== "string" || !question.content.trim()
    || question.attachments !== undefined || entry.attachments !== undefined || typeof entry.partialContent !== "string") return null;
  return {
    threadId: entry.threadId,
    title: shortText(entry.title, 120),
    updatedAt: entry.updatedAt,
    question: { role: "user", content: shortText(question.content, MAX_TEXT_LENGTH), ...(question.web_search === true ? { web_search: true } : {}) },
    partialContent: shortText(entry.partialContent, MAX_TEXT_LENGTH),
    ...(identifier(entry.modelConfigID) ? { modelConfigID: entry.modelConfigID } : {}),
  };
}

function refreshSnapshot(value: unknown): ConversationRefreshSnapshot | null {
  const snapshot = object(value);
  const now = Date.now();
  if (snapshot?.version !== 1 || typeof snapshot.savedAt !== "number" || !Number.isFinite(snapshot.savedAt)
    || snapshot.savedAt <= 0 || now - snapshot.savedAt >= CONVERSATION_REFRESH_TTL_MS || snapshot.savedAt > now + 5_000
    || snapshot.activeId !== null && !identifier(snapshot.activeId) || !Array.isArray(snapshot.entries)) return null;
  const entries: ConversationRefreshEntry[] = [];
  const seen = new Set<string>();
  for (const value of snapshot.entries.slice(0, MAX_ENTRIES)) {
    const entry = refreshEntry(value);
    if (entry && !seen.has(entry.threadId)) { seen.add(entry.threadId); entries.push(entry); }
  }
  return { version: 1, savedAt: snapshot.savedAt, activeId: snapshot.activeId as string | null, entries };
}

function removeRefreshKey(storageKey: string): void {
  try { window.sessionStorage.removeItem(storageKey); } catch { /* 存储被禁用时不影响当前内存对话。 */ }
}

/** 仅恢复当前标签页的短期检查点；权限和账户隔离由调用方构造的storageKey负责。 */
export function readConversationRefresh(storageKey: string, publicComputer = false): ConversationRefreshSnapshot | null {
  if (typeof window === "undefined") return null;
  if (publicComputer) { removeRefreshKey(storageKey); return null; }
  try {
    const raw = window.sessionStorage.getItem(storageKey);
    if (!raw) return null;
    const snapshot = raw.length <= MAX_SERIALIZED_LENGTH ? refreshSnapshot(JSON.parse(raw)) : null;
    if (!snapshot) removeRefreshKey(storageKey);
    return snapshot;
  } catch { removeRefreshKey(storageKey); return null; }
}

/** 个人电脑允许在当前标签页短期保存尚未分类的纯文字输入和部分正文，这是刷新恢复的新边界。
 * sessionStorage也是持久存储，不能承诺未分类文字绝无业务；调用方须排除业务/考试上下文/附件线程，业务事件到达即删。
 */
export function writeConversationRefresh(storageKey: string, snapshot: ConversationRefreshSnapshot | null, publicComputer = false): void {
  if (typeof window === "undefined") return;
  if (publicComputer || !snapshot) { removeRefreshKey(storageKey); return; }
  const sanitized = refreshSnapshot(snapshot);
  if (!sanitized) { removeRefreshKey(storageKey); return; }
  try { window.sessionStorage.setItem(storageKey, JSON.stringify(sanitized)); } catch { /* 存储满或被禁用时，当前请求仍留在内存中。 */ }
}

/** 退出或认证失效时只清理刷新恢复检查点，不影响其他功能的标签页状态。 */
export function clearConversationRefresh(): void {
  if (typeof window === "undefined") return;
  try {
    const keys: string[] = [];
    for (let i = 0; i < window.sessionStorage.length; i++) {
      const key = window.sessionStorage.key(i);
      if (key?.startsWith(CONVERSATION_REFRESH_PREFIX)) keys.push(key);
    }
    for (const key of keys) removeRefreshKey(key);
  } catch { /* 浏览器完全禁用存储时，不阻止退出和清空内存身份。 */ }
}
