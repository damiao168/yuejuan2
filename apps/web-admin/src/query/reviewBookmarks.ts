import { queryOptions } from "@tanstack/react-query";
import type { ReviewTaskContextScope } from "./reviewTaskContext";
import { assertReviewTaskContextScope } from "./reviewTaskContext";
import { getPersonalReviewBookmark, listPersonalReviewBookmarks, type PersonalReviewBookmarkFilter } from "../api/reviewBookmarks";

const removalEvent = "personal-review-bookmark-removed";

// 取消成功后立即停止本页尚在读取的材料；事件不持久化，也不能跨登录代次使用。
export function notifyReviewBookmarkRemoved(scope: ReviewTaskContextScope, bookmarkId: string) {
  window.dispatchEvent(new CustomEvent(removalEvent, { detail: { session: scope.session, bookmarkId } }));
}
export function onReviewBookmarkRemoved(scope: ReviewTaskContextScope, bookmarkId: string, stop: () => void) {
  const listener = (event: Event) => {
    const detail = (event as CustomEvent<{ session: ReviewTaskContextScope["session"]; bookmarkId: string }>).detail;
    if (detail.session.scope === scope.session.scope && detail.session.generation === scope.session.generation && detail.bookmarkId === bookmarkId) stop();
  };
  window.addEventListener(removalEvent, listener);
  return () => window.removeEventListener(removalEvent, listener);
}

export const reviewBookmarkKeys = {
  scope: (scope: ReviewTaskContextScope) => ["personal-review-bookmarks", scope.session.scope, scope.session.generation] as const,
  task: (scope: ReviewTaskContextScope, id: string) => [...reviewBookmarkKeys.scope(scope), "task", id] as const,
  list: (scope: ReviewTaskContextScope, filter: PersonalReviewBookmarkFilter) => [...reviewBookmarkKeys.scope(scope), "list", filter] as const
};
export function reviewBookmarkStateOptions(scope: ReviewTaskContextScope, taskId: string) {
  return queryOptions({
    queryKey: reviewBookmarkKeys.task(scope, taskId), staleTime: 0, gcTime: 0, retry: false,
    enabled: Boolean(taskId && scope.session.scope),
    queryFn: async ({ signal }) => {
      assertReviewTaskContextScope(scope);
      const result = await getPersonalReviewBookmark(taskId, signal);
      assertReviewTaskContextScope(scope);
      return result.bookmark;
    }
  });
}
export async function fetchReviewBookmarkPage(scope: ReviewTaskContextScope, filter: PersonalReviewBookmarkFilter, cursor: string, signal: AbortSignal) {
  assertReviewTaskContextScope(scope);
  const result = await listPersonalReviewBookmarks(filter, cursor, signal);
  assertReviewTaskContextScope(scope);
  return result;
}
