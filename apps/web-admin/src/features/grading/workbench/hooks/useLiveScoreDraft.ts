import { useCallback, type Dispatch, type RefObject, type SetStateAction } from "react";
import type { ScoreDraft } from "../gradingWorkbench.types";

// 连续录分先更新当前草稿引用；正式交分仅由教师的下一份操作发起。
export function useLiveScoreDraft(liveDraft: RefObject<ScoreDraft>, setDraft: Dispatch<SetStateAction<ScoreDraft>>) {
  return useCallback<Dispatch<SetStateAction<ScoreDraft>>>((update) => {
    const next = typeof update === "function" ? update(liveDraft.current) : update;
    liveDraft.current = next;
    setDraft(next);
  }, [liveDraft, setDraft]);
}
