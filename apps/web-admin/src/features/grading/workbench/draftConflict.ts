import type { DraftFallbackSnapshot } from "./gradingWorkbench.types";

export const draftConflictFields = [
  "score",
  "rubricSelections",
  "comments",
  "studentFeedback",
  "privateNote",
  "viewer"
] as const;

export type DraftConflictField = (typeof draftConflictFields)[number];
export type DraftConflictChoice = "local" | "server";
export type DraftConflictChoices = Record<DraftConflictField, DraftConflictChoice>;

export interface DraftConflictResolution {
  taskId: string;
  serverRevision: number;
  local: DraftFallbackSnapshot;
  server: DraftFallbackSnapshot;
}

export function initialDraftConflictChoices(choice: DraftConflictChoice = "local"): DraftConflictChoices {
  return Object.fromEntries(draftConflictFields.map((field) => [field, choice])) as DraftConflictChoices;
}

export function draftConflictValue(snapshot: DraftFallbackSnapshot, field: DraftConflictField): unknown {
  return field === "viewer" ? snapshot.viewer : snapshot.draft[field];
}

export function mergeDraftConflict(
  local: DraftFallbackSnapshot,
  server: DraftFallbackSnapshot,
  choices: DraftConflictChoices
): DraftFallbackSnapshot {
  return {
    draft: {
      ...server.draft,
      // These local-only editor fields are not part of the server draft API.
      // Keeping them avoids losing a reason or OCR correction while resolving
      // an optimistic-lock conflict in the persisted fields below.
      reason: local.draft.reason,
      disputeReason: local.draft.disputeReason,
      answerText: local.draft.answerText,
      score: choices.score === "local" ? local.draft.score : server.draft.score,
      rubricSelections: choices.rubricSelections === "local" ? local.draft.rubricSelections : server.draft.rubricSelections,
      comments: choices.comments === "local" ? local.draft.comments : server.draft.comments,
      studentFeedback: choices.studentFeedback === "local" ? local.draft.studentFeedback : server.draft.studentFeedback,
      privateNote: choices.privateNote === "local" ? local.draft.privateNote : server.draft.privateNote
    },
    viewer: choices.viewer === "local" ? local.viewer : server.viewer
  };
}
