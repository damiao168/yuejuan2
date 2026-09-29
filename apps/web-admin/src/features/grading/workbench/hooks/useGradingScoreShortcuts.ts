import { useCallback, useRef, useState, type Dispatch, type SetStateAction } from "react";
import type { ScoreDraft, ScoreDraftChange } from "../gradingWorkbench.types";

interface RubricShortcutPoint {
  id: string;
  score: number;
}

interface UseGradingScoreShortcutsOptions {
  draft: ScoreDraft;
  setDraft: Dispatch<SetStateAction<ScoreDraft>>;
  rubricPoints: RubricShortcutPoint[];
  onInfo: (message: string) => void;
}

export function useGradingScoreShortcuts({ draft, setDraft, rubricPoints, onInfo }: UseGradingScoreShortcutsOptions) {
  const lastChange = useRef<ScoreDraftChange | null>(null);
  // 这里只撤销最近一次本地分数和采分点修改；不回滚评语，也不会撤销已提交的服务端评分。
  const [canUndo, setCanUndo] = useState(false);

  const rememberCurrent = useCallback(() => {
    lastChange.current = { score: draft.score, rubricSelections: { ...draft.rubricSelections } };
    setCanUndo(true);
  }, [draft]);

  const reset = useCallback(() => {
    lastChange.current = null;
    setCanUndo(false);
  }, []);

  const setScore = useCallback((score: number) => {
    rememberCurrent();
    setDraft((current) => ({ ...current, score }));
    onInfo(`已打分 ${score} 分`);
  }, [onInfo, rememberCurrent, setDraft]);

  const toggleCriterion = useCallback((index: number) => {
    const point = rubricPoints[index];
    if (!point) return;
    rememberCurrent();
    setDraft((current) => {
      const nextScore = (current.rubricSelections[point.id] ?? 0) > 0 ? 0 : point.score;
      const rubricSelections = { ...current.rubricSelections, [point.id]: nextScore };
      const score = rubricPoints.reduce((total, item) => total + (rubricSelections[item.id] ?? 0), 0);
      return { ...current, rubricSelections, score };
    });
    onInfo(`已${(draft.rubricSelections[point.id] ?? 0) > 0 ? "取消" : "计入"}评分点 ${index + 1}`);
  }, [draft.rubricSelections, onInfo, rememberCurrent, rubricPoints, setDraft]);

  const undo = useCallback(() => {
    const previous = lastChange.current;
    if (!previous) return;
    setDraft((current) => ({ ...current, ...previous }));
    reset();
    onInfo("已撤销本次本地评分修改");
  }, [onInfo, reset, setDraft]);

  return { canUndo, rememberCurrent, reset, setScore, toggleCriterion, undo };
}
