import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiClientError } from "../../../../api/client";
import { deletePersonalReviewBookmark, putPersonalReviewBookmark, reviewBookmarkCodePointLength, type PersonalReviewBookmark } from "../../../../api/reviewBookmarks";
import { notifyReviewBookmarkRemoved, reviewBookmarkKeys, reviewBookmarkStateOptions } from "../../../../query/reviewBookmarks";
import { assertReviewTaskContextScope, isReviewTaskContextScopeCurrent, type ReviewTaskContextScope } from "../../../../query/reviewTaskContext";

interface Intent { id: string; taskId: string; bookmarkId: string | null; revision: number; reason: string | null }
export function usePersonalReviewBookmarks(scope: ReviewTaskContextScope, taskId: string) {
  const client = useQueryClient();
  const state = useQuery(reviewBookmarkStateOptions(scope, taskId));
  const intents = useRef(new Map<string, Intent>());
  const controllers = useRef(new Set<AbortController>());
  const busyRef = useRef(false);
  const activeScope = useRef(scope);
  activeScope.current = scope;
  const [busy, setBusy] = useState(false);
  const [uncertainTask, setUncertainTask] = useState("");
  const [error, setError] = useState("");
  const current = useCallback(() => activeScope.current === scope && isReviewTaskContextScopeCurrent(scope), [scope]);
  useEffect(() => {
    const purge = () => {
      for (const controller of controllers.current) controller.abort();
      controllers.current.clear(); intents.current.clear(); busyRef.current = false;
      setBusy(false); setUncertainTask(""); setError("");
      client.removeQueries({ queryKey: reviewBookmarkKeys.scope(scope) });
    };
    const onSession = () => { if (!isReviewTaskContextScopeCurrent(scope)) purge(); };
    window.addEventListener("business-command-scope-changed", onSession);
    return () => { window.removeEventListener("business-command-scope-changed", onSession); purge(); };
  }, [client, scope]);
  const write = useCallback(async (bookmark: PersonalReviewBookmark, reason: string | null) => {
    if (busyRef.current || !current()) return false;
    assertReviewTaskContextScope(scope);
    if (reason !== null && reviewBookmarkCodePointLength(reason) > 200) { setError("复查说明最多 200 个字符"); return false; }
    const key = bookmark.task_id || bookmark.id || "";
    const old = intents.current.get(key);
    // 一次只保留一项待确认意图；否则第二个清理操作会遮住第一个操作的重试入口。
    if (!old && intents.current.size > 0) { setError("上次操作结果尚未确定，请先重试原操作。"); return false; }
    if (old && old.reason !== reason) { setError("上次操作结果尚未确定，请先重试原操作。"); return false; }
    if (reason === null && !bookmark.id) return false;
    const intent = old ?? { id: crypto.randomUUID(), taskId: bookmark.task_id, bookmarkId: bookmark.id, revision: bookmark.revision, reason };
    intents.current.set(key, intent);
    const controller = new AbortController(); controllers.current.add(controller);
    let acknowledged = false;
    busyRef.current = true; setBusy(true); setError("");
    try {
      const result = intent.reason === null
        ? await deletePersonalReviewBookmark(intent.bookmarkId!, intent.revision, intent.id, controller.signal)
        : await putPersonalReviewBookmark(intent.taskId, intent.reason, intent.revision, intent.id, controller.signal);
      if (!current()) return false;
      acknowledged = true;
      intents.current.delete(key); setUncertainTask("");
      if (intent.reason === null) notifyReviewBookmarkRemoved(scope, result.bookmark_id);
      // 成功回执可能来自旧命令。只重新读取当前事实，绝不拿旧 marked 覆盖较新版本。
      if (intent.taskId) {
        const latest = await client.fetchQuery(reviewBookmarkStateOptions(scope, intent.taskId));
        if (!current()) return false;
        if (latest.revision < result.revision) throw new Error("书签状态尚未同步，请刷新确认。");
      }
      await client.invalidateQueries({ queryKey: [...reviewBookmarkKeys.scope(scope), "list"] });
      return current();
    } catch (cause) {
      if (!current()) return false;
      if (acknowledged) {
        setUncertainTask("");
        setError("操作已确认，当前状态读取失败，请刷新清单核对。");
        await client.invalidateQueries({ queryKey: reviewBookmarkKeys.scope(scope) });
        return true;
      }
      if (cause instanceof ApiClientError && [400, 409].includes(cause.status)) {
        // 已明确拒绝的请求不能沿用旧命令键；下次点击必须基于重新读取的标记版本。
        intents.current.delete(key); setUncertainTask("");
        if (intent.taskId) await client.invalidateQueries({ queryKey: reviewBookmarkKeys.task(scope, intent.taskId) });
        setError(cause.status === 409 ? "标记状态已变化，请核对最新状态后重试。" : "标记未能保存，请刷新后重试。");
      } else if (cause instanceof ApiClientError && [401, 403, 404].includes(cause.status)) {
        intents.current.delete(key); setUncertainTask("");
        client.removeQueries({ queryKey: reviewBookmarkKeys.scope(scope) });
        setError("当前记录不可用，请刷新清单。");
      } else {
        setUncertainTask(key); setError("操作结果尚未确定，请重试原操作确认。刷新后只读取当前状态，不自动补写。");
      }
      return false;
    } finally {
      controllers.current.delete(controller);
      if (current()) { busyRef.current = false; setBusy(false); }
    }
  }, [client, current, scope]);
  const retry = useCallback(async () => {
    const intent = intents.current.get(uncertainTask);
    if (!intent) return false;
    return write({ id: intent.bookmarkId, task_id: intent.taskId, revision: intent.revision, marked: intent.reason !== null, reason: intent.reason ?? "", can_edit_reason: false }, intent.reason);
  }, [uncertainTask, write]);
  return { state, write, retry, busy, error, uncertainTask };
}
export type PersonalReviewBookmarkController = ReturnType<typeof usePersonalReviewBookmarks>;
