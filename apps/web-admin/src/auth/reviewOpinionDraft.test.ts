import { afterEach, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../api/businessCommand";
import { clearAllReviewDraftFallbacks } from "./reviewDraftFallback";
import { loadOpinionDraft, removeOpinionDraft, saveOpinionDraft } from "./reviewOpinionDraft";

afterEach(() => { clearAllReviewDraftFallbacks(); setBusinessCommandScope("", ""); vi.restoreAllMocks(); });

it("同一会话按业务记录与来源隔离，修订变化须重新核对，零分和长说明保留", () => {
  setBusinessCommandScope("school", "teacher");
  expect(saveOpinionDraft("appeal", "A", "release1", 3, { score: 0, reason: "核对过程后给零分" })).toBe(true);
  expect(loadOpinionDraft("appeal", "A", "release1", 3)).toEqual({ needsReview: false, values: { score: 0, reason: "核对过程后给零分" } });
  expect(loadOpinionDraft("appeal", "A", "release1", 4)?.needsReview).toBe(true);
  expect(loadOpinionDraft("appeal", "B", "release1", 3)).toBeUndefined();
  expect(loadOpinionDraft("appeal", "A", "release2", 3)).toBeUndefined();
  expect(loadOpinionDraft("arbitration", "A", "release1", 3)).toBeUndefined();
  removeOpinionDraft("appeal", "A", "release1"); expect(loadOpinionDraft("appeal", "A", "release1", 3)).toBeUndefined();
});

it("不同学校、账号及同账号重新登录不能恢复旧意见；无登录范围时拒绝保存", () => {
  setBusinessCommandScope("school", "teacher"); saveOpinionDraft("appeal", "A", "release", 1, { reason: "本账号独立意见" });
  setBusinessCommandScope("school", "other"); expect(loadOpinionDraft("appeal", "A", "release", 1)).toBeUndefined();
  setBusinessCommandScope("other-school", "teacher"); expect(loadOpinionDraft("appeal", "A", "release", 1)).toBeUndefined();
  setBusinessCommandScope("school", "teacher"); expect(loadOpinionDraft("appeal", "A", "release", 1)).toBeUndefined();
  setBusinessCommandScope("", ""); expect(saveOpinionDraft("appeal", "A", "release", 1, {})).toBe(false);
});
