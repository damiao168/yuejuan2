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
  const currentDraft = useRef(draft);
  currentDraft.current = draft;
  // 这里只撤销最近一次本地分数和采分点修改；不回滚评语，也不会撤销已提交的服务端评分。
  const [canUndo, setCanUndo] = useState(false);

  const rememberCurrent = useCallback(() => {
    lastChange.current = { score: currentDraft.current.score, rubricSelections: { ...currentDraft.current.rubricSelections } };
    setCanUndo(true);
  }, []);

  const reset = useCallback(() => {
    lastChange.current = null;
    setCanUndo(false);
  }, []);

  const updateDraft = useCallback<Dispatch<SetStateAction<ScoreDraft>>>((update) => {
    const previous = currentDraft.current;
    const next = typeof update === "function" ? update(previous) : update;
    // 直接填分和快捷评分共用撤销记录；只改评语时不能覆盖上一笔可撤销的分数。
    if (next.score !== previous.score || JSON.stringify(next.rubricSelections) !== JSON.stringify(previous.rubricSelections)) rememberCurrent();
    currentDraft.current = next;
    setDraft(next);
  }, [rememberCurrent, setDraft]);

  const setScore = useCallback((score: number) => {
    updateDraft((current) => ({ ...current, score }));
    onInfo(`已打分 ${score} 分`);
  }, [onInfo, updateDraft]);

  const toggleCriterion = useCallback((index: number) => {
    const point = rubricPoints[index];
    if (!point) return;
    updateDraft((current) => {
      const nextScore = (current.rubricSelections[point.id] ?? 0) > 0 ? 0 : point.score;
      const rubricSelections = { ...current.rubricSelections, [point.id]: nextScore };
      const score = rubricPoints.reduce((total, item) => total + (rubricSelections[item.id] ?? 0), 0);
      return { ...current, rubricSelections, score };
    });
    onInfo(`已${(draft.rubricSelections[point.id] ?? 0) > 0 ? "取消" : "计入"}评分点 ${index + 1}`);
  }, [draft.rubricSelections, onInfo, rubricPoints, updateDraft]);

  const undo = useCallback(() => {
    const previous = lastChange.current;
    if (!previous) return;
    currentDraft.current = { ...currentDraft.current, ...previous };
    setDraft(currentDraft.current);
    reset();
    onInfo("已撤销本次本地评分修改");
  }, [onInfo, reset, setDraft]);

  return { canUndo, rememberCurrent, reset, updateDraft, setScore, toggleCriterion, undo };
}
