const storagePrefix = "edugrade.review-draft.";
const maxDraftsPerUser = 12;
const maxDraftAgeMs = 30 * 60 * 1000;

export interface ReviewDraftFallback<T> {
  updatedAt: number;
  snapshot: T;
}

const memoryDrafts = new Map<string, Map<string, ReviewDraftFallback<unknown>>>();

// 浏览器草稿只存内存，以服务端草稿及其修订号为准；
// 敏感评语、答案和评分细则不能在重载后继续保留，
// 也不能从公用电脑的浏览器存储中读取。
export function saveReviewDraftFallback<T>(userId: string, taskId: string, snapshot: T): boolean {
  if (!userId.trim() || !taskId.trim()) return false;
  const now = Date.now();
  const drafts = memoryDrafts.get(userId) ?? new Map<string, ReviewDraftFallback<unknown>>();
  for (const [id, value] of drafts) {
    if (now - value.updatedAt > maxDraftAgeMs) drafts.delete(id);
  }
  drafts.delete(taskId);
  drafts.set(taskId, { updatedAt: now, snapshot });
  while (drafts.size > maxDraftsPerUser) {
    const oldest = drafts.keys().next().value;
    if (typeof oldest !== "string") break;
    drafts.delete(oldest);
  }
  memoryDrafts.set(userId, drafts);
  purgeLegacyPersistentDrafts();
  return true;
}

export function loadReviewDraftFallback<T>(userId: string, taskId: string): ReviewDraftFallback<T> | null {
  purgeLegacyPersistentDrafts();
  const value = memoryDrafts.get(userId)?.get(taskId);
  if (!value || Date.now() - value.updatedAt > maxDraftAgeMs) {
    removeReviewDraftFallback(userId, taskId);
    return null;
  }
  return value as ReviewDraftFallback<T>;
}

export function removeReviewDraftFallback(userId: string, taskId: string) {
  const drafts = memoryDrafts.get(userId);
  drafts?.delete(taskId);
  if (drafts?.size === 0) memoryDrafts.delete(userId);
  purgeLegacyPersistentDrafts();
}

export function clearReviewDraftFallbacks(userId: string) {
  memoryDrafts.delete(userId);
  purgeLegacyPersistentDrafts();
}

export function clearAllReviewDraftFallbacks() {
  memoryDrafts.clear();
  purgeLegacyPersistentDrafts();
}

function purgeLegacyPersistentDrafts() {
  if (typeof window === "undefined") return;
  try {
    // 浏览器可以在读取storage属性时就拒绝访问；旧持久数据清理不能中断内存草稿或登录。
    const storage = window.localStorage;
    const keys: string[] = [];
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key?.startsWith(storagePrefix)) keys.push(key);
    }
    for (const key of keys) storage.removeItem(key);
  } catch {
    // 存储可能被禁用；仅使用内存的操作仍然安全。
  }
}
