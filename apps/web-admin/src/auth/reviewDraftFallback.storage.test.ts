// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { clearAllReviewDraftFallbacks, loadReviewDraftFallback, removeReviewDraftFallback, saveReviewDraftFallback } from "./reviewDraftFallback";
afterEach(() => { vi.restoreAllMocks(); clearAllReviewDraftFallbacks(); });
it.each(["getter", "cleanup"])("旧存储%s失败不影响纯内存草稿保存、读取和删除", failure => {
  if (failure === "getter") vi.spyOn(window, "localStorage", "get").mockImplementation(() => { throw new DOMException("blocked", "SecurityError"); });
  else { localStorage.setItem("edugrade.review-draft.legacy", "sensitive"); vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("cleanup failed"); }); }
  const draft = { score: 8, comments: "本次内存意见" };
  expect(saveReviewDraftFallback("teacher", "task", draft)).toBe(true);
  expect(loadReviewDraftFallback("teacher", "task")?.snapshot).toEqual(draft);
  removeReviewDraftFallback("teacher", "task"); expect(loadReviewDraftFallback("teacher", "task")).toBeNull();
});
