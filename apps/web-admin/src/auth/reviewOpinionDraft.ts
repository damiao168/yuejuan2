import { getBusinessCommandSession } from "../api/businessCommand";
import { useEffect } from "react";
import { loadReviewDraftFallback, removeReviewDraftFallback, saveReviewDraftFallback } from "./reviewDraftFallback";

type OpinionDraft<T> = { revision: number; values: T };
export type PrivateOpinionDraftKind = "question-appeal" | "legacy-appeal" | "arbitration";
export function privateOpinionDraftPath(kind: PrivateOpinionDraftKind, recordId: string) {
  const resources = { "question-appeal": "question-appeals", "legacy-appeal": "appeals", arbitration: "arbitration-tasks" };
  return `/api/v1/${resources[kind]}/${encodeURIComponent(recordId)}/private-draft`;
}
function identity(kind: string, recordId: string, source: string) {
  const session = getBusinessCommandSession();
  return session.scope ? { owner: `${session.scope}:${session.generation}`, key: `opinion:${kind}:${recordId}:${source}` } : null;
}

// 自动暂存只存内存；跨刷新恢复由PrivateOpinionDraft显式保存到当前账号的服务端私稿，不能把正文写入浏览器存储。
export function saveOpinionDraft<T>(kind: string, recordId: string, source: string, revision: number, values: T): boolean {
  const id = identity(kind, recordId, source);
  return Boolean(id && saveReviewDraftFallback<OpinionDraft<T>>(id.owner, id.key, { revision, values }));
}
export function loadOpinionDraft<T>(kind: string, recordId: string, source: string, revision: number) {
  const id = identity(kind, recordId, source);
  const saved = id ? loadReviewDraftFallback<OpinionDraft<T>>(id.owner, id.key)?.snapshot : undefined;
  return saved ? { values: saved.values, needsReview: saved.revision !== revision } : undefined;
}
export function removeOpinionDraft(kind: string, recordId: string, source: string) {
  const id = identity(kind, recordId, source);
  if (id) removeReviewDraftFallback(id.owner, id.key);
}

// 站内切换可从内存恢复，浏览器刷新会清掉内存；仍保留浏览器原生离开提醒。
export function useOpinionDraftReloadWarning(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
}
