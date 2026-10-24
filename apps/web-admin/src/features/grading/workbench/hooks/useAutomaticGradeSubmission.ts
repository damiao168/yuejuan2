import { useCallback, useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from "react";
import type { ScoreDraft } from "../gradingWorkbench.types";

export function useScoringDraftIntent(taskId: string, liveDraft: RefObject<ScoreDraft>, setDraft: Dispatch<SetStateAction<ScoreDraft>>) {
  const [edit, setEdit] = useState<{ taskId: string; sequence: number } | null>(null);
  const sequence = useRef(0);
  const updateDraft = useCallback<Dispatch<SetStateAction<ScoreDraft>>>((update) => {
    const previous = liveDraft.current;
    const next = typeof update === "function" ? update(previous) : update;
    liveDraft.current = next;
    setDraft(next);
    if (next.score !== previous.score || JSON.stringify(next.rubricSelections) !== JSON.stringify(previous.rubricSelections)) {
      setEdit({ taskId, sequence: ++sequence.current });
    }
  }, [liveDraft, setDraft, taskId]);
  return { edit, updateDraft };
}

interface AutomaticSubmissionOptions {
  taskId: string;
  edit: { taskId: string; sequence: number } | null;
  ready: boolean;
  marked: boolean;
  submit: () => Promise<void>;
}

// 只提交本次打开后教师主动编辑的分数；恢复历史草稿或查看答卷不能触发正式写分。
export function useAutomaticGradeSubmission({ taskId, edit, ready, marked, submit }: AutomaticSubmissionOptions) {
  const latest = useRef({ taskId, edit, ready, marked, submit });
  latest.current = { taskId, edit, ready, marked, submit };
  const attempted = useRef("");
  useEffect(() => {
    if (!ready || marked || !edit || edit.taskId !== taskId) return;
    const key = `${taskId}:${edit.sequence}`;
    if (attempted.current === key) return;
    // 先等草稿同步，再留出短暂的连续输入和标记时间，避免把输入 15 的中间值 1 提交。
    const timer = window.setTimeout(() => {
      const current = latest.current;
      if (!current.ready || current.marked || current.taskId !== taskId || current.edit !== edit) return;
      attempted.current = key;
      void current.submit();
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [taskId, edit, ready, marked]);
}
