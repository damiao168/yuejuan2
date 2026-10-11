import type { AiGrade } from "../../../api/review";
import { latestGrade } from "./gradingWorkbench.model";
import type { DraftFallbackSnapshot, DraftSaveStatus, WorkbenchContext } from "./gradingWorkbench.types";
import type { useMathWorkbenchEvidence } from "./hooks/useMathWorkbenchEvidence";
import { mathEvidenceBinding, mathSuggestionState, type MathStepSelection } from "./mathWorkbenchEvidence";
import { requiresExplicitSecondOpinion } from "./reviewContext";

type MathEvidenceState = ReturnType<typeof useMathWorkbenchEvidence>["state"];

export function selectWorkbenchGrade(grades: AiGrade[], subjectCode: string, state: MathEvidenceState, dirty: boolean) {
  if (subjectCode === "mathematics") {
    const current = latestGrade(grades.filter((grade) => mathSuggestionState(grade, state, dirty).current));
    if (current) return current;
  }
  return latestGrade(grades);
}

export function canAdoptWorkbenchGrade(grade: AiGrade | undefined, ctx: WorkbenchContext | null, requesting: boolean, subjectCode: string, state: MathEvidenceState, dirty: boolean): boolean {
  if (!grade || grade.delivery_mode === "shadow_only" || requesting) return false;
  if (requiresExplicitSecondOpinion(ctx!.reviewContext)) return false;
  return subjectCode !== "mathematics" || mathSuggestionState(grade, state, dirty).current;
}

export function visibleMathSelection(selection: MathStepSelection | null, state: MathEvidenceState, dirty: boolean): MathStepSelection | null {
  if (!selection || dirty || ["conflict", "loading", "unavailable"].includes(state.phase)) return null;
  if (!state.understanding || selection.binding !== mathEvidenceBinding(state.understanding)) return null;
  return selection;
}

interface ReviewDraftStateOptions {
  ctx: WorkbenchContext | null;
  currentUserId: string;
  canWork: boolean;
  hasSession: boolean;
  resolvingConflict: boolean;
  hasConflict: boolean;
  hydrated: boolean;
  saveStatus: DraftSaveStatus;
  actioning: string | null;
  snapshot: DraftFallbackSnapshot;
  lastSavedSnapshot: string;
}

export function reviewDraftState(options: Readonly<ReviewDraftStateOptions>) {
  const { ctx, snapshot } = options;
  const ownsSelectedTask = Boolean(ctx?.task.assigned_to && ctx.task.assigned_to === options.currentUserId);
  const canEditDraft = options.canWork && options.hasSession && ownsSelectedTask && !options.resolvingConflict
    && !options.hasConflict && Boolean(ctx && ["assigned", "in_progress", "returned"].includes(ctx.task.status));
  const unsyncedDraft = Boolean(options.hydrated && ownsSelectedTask && ctx?.task.status !== "submitted"
    && JSON.stringify(snapshot) !== options.lastSavedSnapshot);
  const canSubmit = canEditDraft && options.saveStatus !== "conflict" && !options.actioning && snapshot.draft.score !== null;
  return { ownsSelectedTask, canEditDraft, unsyncedDraft, canSubmit };
}
