import { describe, expect, it } from "vitest";
import { gradingShortcutIntent, type GradingShortcutContext } from "./keyboardShortcuts";

const allowed = (overrides: Partial<GradingShortcutContext> = {}): GradingShortcutContext => ({
  key: "",
  isInputTarget: false,
  hasTask: true,
  canEditDraft: true,
  canSubmit: true,
  canReturn: true,
  canUndoScoreChange: true,
  hasAiSuggestion: true,
  maxScore: 9,
  rubricPointCount: 0,
  ...overrides
});

describe("grading workbench keyboard shortcuts", () => {
  it("maps the supported scoring and safety shortcuts to explicit intents", () => {
    expect(gradingShortcutIntent(allowed({ key: "Enter" }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "Enter", ctrlKey: true }))).toEqual({ type: "submit" });
    expect(gradingShortcutIntent(allowed({ key: "Enter", metaKey: true }))).toEqual({ type: "submit" });
    expect(gradingShortcutIntent(allowed({ key: "Enter", quickSubmit: true }))).toEqual({ type: "submit" });
    expect(gradingShortcutIntent(allowed({ key: "1" }))).toEqual({ type: "set_score", score: 1 });
    expect(gradingShortcutIntent(allowed({ key: "2", rubricPointCount: 3 }))).toEqual({ type: "toggle_criterion", index: 1 });
    expect(gradingShortcutIntent(allowed({ key: "A" }))).toEqual({ type: "adopt_ai" });
    expect(gradingShortcutIntent(allowed({ key: "F" }))).toEqual({ type: "flag_exception" });
    expect(gradingShortcutIntent(allowed({ key: "R" }))).toEqual({ type: "return_task" });
    expect(gradingShortcutIntent(allowed({ key: "Z" }))).toEqual({ type: "undo_score_change" });
    expect(gradingShortcutIntent(allowed({ key: "+" }))).toEqual({ type: "zoom", direction: 1 });
    expect(gradingShortcutIntent(allowed({ key: "-" }))).toEqual({ type: "zoom", direction: -1 });
    expect(gradingShortcutIntent(allowed({ key: "", code: "NumpadAdd" }))).toEqual({ type: "zoom", direction: 1 });
    expect(gradingShortcutIntent(allowed({ key: "", code: "NumpadSubtract" }))).toEqual({ type: "zoom", direction: -1 });
  });

  it("never handles a shortcut while an editable input is focused", () => {
    for (const key of ["Enter", "1", "A", "F", "R", "Z", "+", "-"]) {
      expect(gradingShortcutIntent(allowed({ key, isInputTarget: true }))).toBeNull();
    }
  });

  it("does not turn a missing rubric point into an unrelated total score", () => {
    expect(gradingShortcutIntent(allowed({ key: "4", rubricPointCount: 3, maxScore: 10 }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "9", rubricPointCount: 3, maxScore: 10 }))).toBeNull();
  });

  it("ignores input composition, repeated keydown, dialogs and platform shortcuts", () => {
    for (const guard of [{ isComposing: true }, { repeat: true }, { blocked: true }, { altKey: true }]) {
      expect(gradingShortcutIntent(allowed({ key: "Enter", ctrlKey: true, ...guard }))).toBeNull();
      expect(gradingShortcutIntent(allowed({ key: "1", ...guard }))).toBeNull();
    }
    expect(gradingShortcutIntent(allowed({ key: "a", metaKey: true }))).toBeNull();
  });

  it("does not bypass edit, submit, return, or task permissions", () => {
    expect(gradingShortcutIntent(allowed({ key: "Enter", canSubmit: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "A", ctrlKey: true }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "1", canEditDraft: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "F", canReturn: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "R", canReturn: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "Z", canUndoScoreChange: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "A", hasAiSuggestion: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "+", hasTask: false }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "9", maxScore: 8 }))).toBeNull();
    expect(gradingShortcutIntent(allowed({ key: "4", rubricPointCount: 3, maxScore: 3 }))).toBeNull();
  });

  it("respects a consumed event and a disabled single-character preference", () => {
    expect(gradingShortcutIntent(allowed({ key: "Enter", ctrlKey: true, defaultPrevented: true }))).toBeNull();
    for (const key of ["1", "A", "F", "R", "Z", "+", "-"]) {
      expect(gradingShortcutIntent(allowed({ key, singleKeyShortcutsEnabled: false }))).toBeNull();
    }
    expect(gradingShortcutIntent(allowed({ key: "Enter", ctrlKey: true, singleKeyShortcutsEnabled: false }))).toEqual({ type: "submit" });
    expect(gradingShortcutIntent(allowed({ key: "Enter", quickSubmit: true, singleKeyShortcutsEnabled: false }))).toEqual({ type: "submit" });
  });
});
