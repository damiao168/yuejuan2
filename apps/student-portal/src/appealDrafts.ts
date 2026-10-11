import type { AuthUser, SelectedAppealRegion } from "./api";

const draftPrefix = "edugrade:student:appeal-draft:v1:";
const memoryDrafts = new Map<string, AppealDraft>();
const volatileKeys = new Set<string>();

export interface AppealDraft {
  reasonCode: string;
  reason: string;
  selectedRegion?: SelectedAppealRegion;
  questionNo?: string;
  releaseVersion?: number;
}

export function studentDraftOwner(user: Pick<AuthUser, "tenant_id" | "id">) {
  return JSON.stringify([user.tenant_id, user.id]);
}

export function appealDraftKey(owner: string, examID: string, releaseID: string, questionID: string) {
  return `${draftPrefix}${encodeURIComponent(owner)}:${encodeURIComponent(JSON.stringify([examID, releaseID, questionID]))}`;
}

function validRegion(value: unknown): value is SelectedAppealRegion {
  if (!value || typeof value !== "object") return false;
  const region = value as Partial<SelectedAppealRegion>;
  return region.coordinate_space === "canonical_image_normalized"
    && [region.x, region.y, region.width, region.height].every((number) => typeof number === "number" && Number.isFinite(number))
    && region.x! >= 0 && region.y! >= 0 && region.width! > 0 && region.height! > 0
    && region.x! + region.width! <= 1 && region.y! + region.height! <= 1;
}

export function readAppealDraft(key: string): AppealDraft | undefined {
  if (volatileKeys.has(key)) return memoryDrafts.get(key);
  try {
    const raw = window.sessionStorage.getItem(key);
    if (!raw) { memoryDrafts.delete(key); return undefined; }
    const value = JSON.parse(raw) as Partial<AppealDraft>;
    if (typeof value.reasonCode !== "string" || typeof value.reason !== "string" || value.reason.length > 500) return undefined;
    const draft: AppealDraft = { reasonCode: value.reasonCode, reason: value.reason, ...(validRegion(value.selectedRegion) ? { selectedRegion: value.selectedRegion } : {}), ...(typeof value.questionNo === "string" ? {questionNo: value.questionNo} : {}), ...(Number.isSafeInteger(value.releaseVersion) ? {releaseVersion: value.releaseVersion} : {}) };
    memoryDrafts.set(key, draft);
    return draft;
  } catch { return memoryDrafts.get(key); }
}

// 草稿按身份和不可变发布隔离，只保存在当前标签页会话；换题及重登可恢复，退出登录则清除。
// 存储不可用时继续保留内存输入，并由界面和离开提示说明重载不能恢复。
export function writeAppealDraft(key: string, draft: AppealDraft): boolean {
  memoryDrafts.set(key, draft);
  try {
    window.sessionStorage.setItem(key, JSON.stringify(draft));
    volatileKeys.delete(key);
    return true;
  } catch {
    volatileKeys.add(key);
    return false;
  }
}

export function removeAppealDraft(key: string) {
  memoryDrafts.delete(key);
  volatileKeys.delete(key);
  try { window.sessionStorage.removeItem(key); } catch { /* 会话存储不可用时只清理内存。 */ }
}

export function clearStudentAppealDrafts(keepOwner?: string) {
  const keepPrefix = keepOwner ? `${draftPrefix}${encodeURIComponent(keepOwner)}:` : undefined;
  const shouldRemove = (key: string) => key.startsWith(draftPrefix) && (!keepPrefix || !key.startsWith(keepPrefix));
  for (const key of memoryDrafts.keys()) if (shouldRemove(key)) removeAppealDraft(key);
  try {
    const keys = Object.keys(window.sessionStorage).filter(shouldRemove);
    for (const key of keys) window.sessionStorage.removeItem(key);
  } catch { /* 内存内容已清除；无法访问的会话存储不会被当前账号读取。 */ }
}

export function hasVolatileAppealDraft(owner: string) {
  const prefix = `${draftPrefix}${encodeURIComponent(owner)}:`;
  return [...volatileKeys].some((key) => key.startsWith(prefix) && Boolean(memoryDrafts.get(key)?.reason.trim() || memoryDrafts.get(key)?.selectedRegion));
}

// 恢复输入时也恢复持久化状态；内存兜底不能在切题后被界面误报为已保存。
export function isAppealDraftStored(key: string) {
 return !volatileKeys.has(key);
}

// 只枚举当前身份当前考试；旧键没有版本元数据时显示发布标识，不能猜测版本或复用旧圈选。
export function listAppealDrafts(owner: string, examID: string) {
  const prefix = `${draftPrefix}${encodeURIComponent(owner)}:`;
  const keys = new Set(memoryDrafts.keys());
  try { Object.keys(window.sessionStorage).forEach((key) => keys.add(key)); } catch { /* 存储拒绝时仍展示内存中可恢复的输入。 */ }
  const drafts: Array<{ key: string; releaseID: string; questionID: string; draft: AppealDraft }> = [];
  for (const key of keys) {
    if (!key.startsWith(prefix)) continue;
    try {
      const parts = JSON.parse(decodeURIComponent(key.slice(prefix.length)));
      if (!Array.isArray(parts) || parts.length !== 3 || parts[0] !== examID || parts.some((part) => typeof part !== "string")) continue;
      const draft = readAppealDraft(key);
      if (draft && (draft.reason.trim() || draft.selectedRegion)) drafts.push({ key, releaseID: parts[1], questionID: parts[2], draft });
    } catch { /* 损坏的草稿键不能阻断其他草稿恢复。 */ }
  }
  return drafts;
}
