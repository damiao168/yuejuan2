import { describe, expect, it } from "vitest";
import {
  draftConflictFields,
  draftConflictValue,
  initialDraftConflictChoices,
  mergeDraftConflict
} from "./draftConflict";
import type { DraftFallbackSnapshot } from "./gradingWorkbench.types";

function snapshot(prefix: "local" | "server"): DraftFallbackSnapshot {
  return {
    draft: {
      score: prefix === "local" ? 8 : 6,
      rubricSelections: { accuracy: prefix === "local" ? 4 : 2 },
      comments: `${prefix}-comments`,
      studentFeedback: `${prefix}-feedback`,
      privateNote: `${prefix}-private`,
      reason: `${prefix}-reason`,
      disputeReason: `${prefix}-dispute`,
      answerText: `${prefix}-answer`
    },
    viewer: {
      mode: prefix === "local" ? "ocr" : "segment",
      scale: prefix === "local" ? 1.5 : 1,
      rotation: prefix === "local" ? 90 : 0,
      offset: prefix === "local" ? { x: 10, y: 20 } : { x: 0, y: 0 },
      fit: prefix === "server"
    }
  };
}

describe("draft conflict field resolution", () => {
  it("exposes both local and server values for every persisted field", () => {
    const local = snapshot("local");
    const server = snapshot("server");
    expect(draftConflictFields).toEqual([
      "score", "rubricSelections", "comments", "studentFeedback", "privateNote", "viewer"
    ]);
    for (const field of draftConflictFields) {
      expect(draftConflictValue(local, field)).not.toEqual(draftConflictValue(server, field));
    }
  });

  it("restores the selected side independently for each field", () => {
    const local = snapshot("local");
    const server = snapshot("server");
    const choices = initialDraftConflictChoices("server");
    choices.score = "local";
    choices.comments = "local";
    choices.privateNote = "local";
    choices.viewer = "local";

    const merged = mergeDraftConflict(local, server, choices);
    expect(merged.draft.score).toBe(local.draft.score);
    expect(merged.draft.comments).toBe(local.draft.comments);
    expect(merged.draft.privateNote).toBe(local.draft.privateNote);
    expect(merged.viewer).toEqual(local.viewer);
    expect(merged.draft.rubricSelections).toEqual(server.draft.rubricSelections);
    expect(merged.draft.studentFeedback).toBe(server.draft.studentFeedback);
    expect(merged.draft.reason).toBe(local.draft.reason);
    expect(merged.draft.disputeReason).toBe(local.draft.disputeReason);
    expect(merged.draft.answerText).toBe(local.draft.answerText);
  });
});
