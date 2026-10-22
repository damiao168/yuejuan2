import { describe, expect, it } from "vitest";
import type { MathRubricScoreResponse, MathUnderstandingResponse } from "../../../api/mathUnderstanding";
import type { AiGrade } from "../../../api/review";
import { createDraftSnapshot, createInitialDraft } from "./gradingWorkbench.model";
import type { WorkbenchContext } from "./gradingWorkbench.types";
import { mathEvidenceBinding, type MathStepSelection, type MathWorkbenchEvidence } from "./mathWorkbenchEvidence";
import { canAdoptWorkbenchGrade, imageSuggestionState, reviewDraftState, selectWorkbenchGrade, visibleMathSelection } from "./workbenchReviewState";

function context(): WorkbenchContext {
  return {
    task: { assigned_to: "teacher", status: "in_progress" },
    reviewContext: { question_snapshot: { risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } } }
  } as unknown as WorkbenchContext;
}

function mathFixture() {
  const understanding: MathUnderstandingResponse = {
    artifact: { id: "artifact", subject_code: "mathematics", answer_segment_id: "segment", exam_question_snapshot_id: "snapshot", version: 3, stage: "verified", correction_revision: 2, engine_version: "fixture", blocks: [], formulas: [], verifications: [], rubric_evidence: [], created_at: "2026-09-14T00:00:00Z", solution_graph: { id: "graph", answer_segment_id: "segment", builder_version: "fixture", formula_model_version: "fixture", overall_confidence: 1, requires_human_review: true, steps: [], edges: [] } },
    correction_revision: 0, corrections: []
  };
  const score: MathRubricScoreResponse = { schema_version: "math-rubric-score-v1", scope: "teacher_suggestion_only", artifact_id: "artifact", artifact_version: 3, correction_revision: 0, verified_correction_revision: 2, exam_question_snapshot_id: "snapshot", rubric_id: "rubric-id", rubric_version: "rubric", rubric_snapshot_hash: "fixture", max_score: 2, verified_score: 2, unresolved_score: 0, suggested_score: 2, score_range: { min: 2, max: 2 }, requires_human_review: true, criterion_decisions: [], rubric_evidence: [], matched_points: [], missing_points: [] };
  const grade: AiGrade = { id: "current", tenant_id: "tenant", question_id: "question", question_no: "Q1", question_type: "calculation", grader_type: "math", confidence: 1, matched_points: [], missing_points: [], evidence: [], risk_flags: [], created_by: "fixture", answer_segment_id: "segment", math_artifact_id: "artifact", math_artifact_version: 3, math_correction_revision: 2, math_scoring_version: "math-rubric-score-v1", rubric_version: "rubric", delivery_mode: "teacher_suggestion", needs_human_review: true, status: "succeeded", mock: false, suggested_score: 2, max_score: 2, created_at: "2026-09-14T00:00:00Z" };
  const state: MathWorkbenchEvidence = { segmentId: "segment", phase: "ready", understanding, score, message: "" };
  return { state, grade };
}

function selectionFor(state: MathWorkbenchEvidence): MathStepSelection {
  return { segmentId: "segment", binding: mathEvidenceBinding(state.understanding!), stepId: "step", label: "步骤", bbox: { x: 0, y: 0, width: 0.5, height: 0.5 } };
}

describe("batch8 review derived state (new helpers only)", () => {
  function options() {
    return { ctx: context(), currentUserId: "teacher", canWork: true, hasSession: true, resolvingConflict: false, hasConflict: false, hydrated: true, saveStatus: "saved" as const, actioning: null, snapshot: createDraftSnapshot(createInitialDraft(null), "segment", 1, 0, { x: 0, y: 0 }, true), lastSavedSnapshot: "" };
  }

  it("treats zero as submit-ready but not a missing score", () => {
    const input = options();
    expect(reviewDraftState(input).canSubmit).toBe(false);
    input.snapshot.draft.score = 0;
    expect(reviewDraftState(input)).toMatchObject({ canEditDraft: true, canSubmit: true, unsyncedDraft: true });
    input.lastSavedSnapshot = JSON.stringify(input.snapshot);
    expect(reviewDraftState(input).unsyncedDraft).toBe(false);
  });

  it.each(["pending", "submitted", "needs_arbitration", "completed"])("does not edit a %s task", (status) => {
    const input = options();
    input.ctx.task.status = status as WorkbenchContext["task"]["status"];
    expect(reviewDraftState(input).canEditDraft).toBe(false);
  });

  it.each(["canWork", "hasSession"] as const)("requires %s for editing", (flag) => {
    const input = options(); input[flag] = false;
    expect(reviewDraftState(input).canEditDraft).toBe(false);
  });

  it("keeps ownership, conflict resolution and autosave conflict gates distinct", () => {
    const input = options(); input.snapshot.draft.score = 2;
    input.ctx.task.assigned_to = "other";
    expect(reviewDraftState(input)).toMatchObject({ ownsSelectedTask: false, canEditDraft: false, canSubmit: false, unsyncedDraft: false });
    input.ctx.task.assigned_to = "teacher";
    expect(reviewDraftState({ ...input, resolvingConflict: true }).canEditDraft).toBe(false);
    expect(reviewDraftState({ ...input, hasConflict: true }).canEditDraft).toBe(false);
    expect(reviewDraftState({ ...input, saveStatus: "conflict" })).toMatchObject({ canEditDraft: true, canSubmit: false });
  });

  it("uses a bound current math grade ahead of newer stale history, without exposing shadow-only material", () => {
    const { grade, state } = mathFixture();
    const stale = { ...grade, id: "stale", math_artifact_version: 2, created_at: "2026-09-15T00:00:00Z" };
    const shadow = { ...grade, id: "shadow", delivery_mode: "shadow_only", created_at: "2026-09-16T00:00:00Z" } as AiGrade;
    expect(selectWorkbenchGrade([stale, shadow, grade], "mathematics", state, false)).toBe(grade);
    expect(selectWorkbenchGrade([stale, shadow, grade], "history", state, false)).toBe(stale);
    expect(selectWorkbenchGrade([shadow], "mathematics", state, false)).toBeUndefined();
    expect(canAdoptWorkbenchGrade(grade, context(), false, "mathematics", state, false)).toBe(true);
    expect(canAdoptWorkbenchGrade(stale, context(), false, "mathematics", state, false)).toBe(false);
  });

  it("does not adopt during a math request, dirty edit or explicit human-primary second opinion", () => {
    const { grade, state } = mathFixture();
    const ctx = context();
    expect(canAdoptWorkbenchGrade(grade, ctx, true, "mathematics", state, false)).toBe(false);
    expect(canAdoptWorkbenchGrade(grade, ctx, false, "mathematics", state, true)).toBe(false);
    ctx.reviewContext.question_snapshot.risk_tier = "R3";
    ctx.reviewContext.question_snapshot.scoring_policy_snapshot.mode = "HUMAN_PRIMARY";
    expect(canAdoptWorkbenchGrade(grade, ctx, false, "mathematics", state, false)).toBe(false);
  });

  it("blocks old AI adoption after a current failure while keeping manual zero-score submission available", () => {
    const { grade, state } = mathFixture();
    const input = options();
    input.ctx.reviewContext.ai_failure = { failure_kind: "output_truncated" };
    input.snapshot.draft.score = 0;
    expect(canAdoptWorkbenchGrade(grade, input.ctx, false, "history", state, false)).toBe(false);
    expect(canAdoptWorkbenchGrade(grade, input.ctx, false, "mathematics", state, false)).toBe(false);
    expect(reviewDraftState(input)).toMatchObject({ canEditDraft: true, canSubmit: true });
    expect(input.snapshot.draft.score).toBe(0);
  });

  it("allows a current image-v3 suggestion without v2 math artifacts but rejects changed input, crop, question and snapshot bindings", () => {
    const { grade: mathGrade, state } = mathFixture();
    const crop = "a".repeat(64);
    const grade: AiGrade = { ...mathGrade, id: "image-grade", answer_version: "input-v3", raw_output: {
      schema_version: "grading-image-v3", input_version_id: "input-v3", assessment_snapshot_id: "snapshot",
      observation: { input_version_id: "input-v3", crop_sha256: crop, members: [{ question_id: "question" }] }
    }, math_artifact_id: undefined, math_artifact_version: undefined, math_correction_revision: undefined, math_scoring_version: undefined };
    const ctx = context();
    Object.assign(ctx.task, { answer_segment_id: "segment", question_id: "question" });
    Object.assign(ctx.reviewContext.question_snapshot, { id: "snapshot" });
    ctx.reviewContext.question = { id: "question", score: 2 } as WorkbenchContext["reviewContext"]["question"];
    ctx.reviewContext.frozen_rubric = { version: "rubric" } as WorkbenchContext["reviewContext"]["frozen_rubric"];
    ctx.reviewContext.ai_second_opinion = { available: true, presentation: "explicit_second_opinion", score_prefill_allowed: true,
      metadata: { ...grade, input_binding: { input_mode: "image", input_protocol: "grading-image-v3", input_version_id: "input-v3",
        crop_sha256: crop, assessment_snapshot_id: "snapshot", answer_segment_id: "segment", question_id: "question", subjective_run_id: "run" } } };
    const unavailable = { ...state, phase: "unavailable" as const, score: null, understanding: null };
    expect(imageSuggestionState(grade, ctx).current).toBe(true);
    expect(canAdoptWorkbenchGrade(grade, ctx, false, "mathematics", unavailable, false)).toBe(true);
    // 有成功角色不代表已有汇总；失败角色不能被当成零分，也不能把父候选分直接预填。
    const incomplete = { ...grade, raw_output: { ...grade.raw_output, panel_execution: { decision: "teacher_review", role_results: [{ role: "primary_a", status: "succeeded", score: 2 }] } } };
    expect(imageSuggestionState(incomplete, ctx).current).toBe(false);
    expect(canAdoptWorkbenchGrade(incomplete, ctx, false, "mathematics", unavailable, false)).toBe(false);
    const aggregateZero = { ...grade, suggested_score: 0, raw_output: { ...grade.raw_output, panel_execution: { decision: "primary_consensus", final_score: 0 } } };
    expect(canAdoptWorkbenchGrade(aggregateZero, ctx, false, "mathematics", unavailable, false)).toBe(true);
    expect(selectWorkbenchGrade([{ ...mathGrade, created_at: "2026-10-01" }, grade], "mathematics", state, false, ctx)).toBe(grade);
    for (const field of ["input_version_id", "crop_sha256", "question_id", "assessment_snapshot_id"]) {
      const changed = structuredClone(ctx);
      (changed.reviewContext.ai_second_opinion!.metadata.input_binding as Record<string, unknown>)[field] = "changed";
      expect(canAdoptWorkbenchGrade(grade, changed, false, "mathematics", unavailable, false)).toBe(false);
    }
    const duplicate = { ...grade, raw_output: { ...grade.raw_output, observation: { input_version_id: "input-v3", crop_sha256: crop, members: [{ question_id: "question" }, { question_id: "question" }] } } };
    expect(canAdoptWorkbenchGrade(duplicate, ctx, false, "mathematics", unavailable, false)).toBe(false);
    expect(canAdoptWorkbenchGrade(grade, ctx, false, "mathematics", unavailable, true)).toBe(false);
    ctx.reviewContext.question_snapshot.risk_tier = "R3";
    ctx.reviewContext.question_snapshot.scoring_policy_snapshot.mode = "HUMAN_PRIMARY";
    expect(canAdoptWorkbenchGrade(grade, ctx, false, "mathematics", unavailable, false)).toBe(false);
  });

  it.each(["conflict", "loading", "unavailable"] as const)("clears step selection while %s", (phase) => {
    const { state } = mathFixture();
    const selection = selectionFor(state);
    expect(visibleMathSelection(selection, state, false)).toBe(selection);
    expect(visibleMathSelection(selection, { ...state, phase }, false)).toBeNull();
  });

  it("clears a step from an old binding or unsaved correction", () => {
    const { state } = mathFixture();
    const selection = selectionFor(state);
    expect(visibleMathSelection(selection, state, true)).toBeNull();
    expect(visibleMathSelection({ ...selection, binding: "old" }, state, false)).toBeNull();
    expect(visibleMathSelection(selection, { ...state, understanding: null }, false)).toBeNull();
  });
});
