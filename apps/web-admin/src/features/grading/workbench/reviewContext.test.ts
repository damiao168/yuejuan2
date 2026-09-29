import { describe, expect, it } from "vitest";
import type { ReviewTaskContext } from "../../../api/review";
import {
  frozenRubricFromContext,
  questionFromContext,
  requiresExplicitSecondOpinion,
  secondOpinionMetadata,
  subjectToolDescriptor
} from "./reviewContext";

function fixture(overrides: Partial<ReviewTaskContext> = {}): ReviewTaskContext {
  return {
    task: { id: "task-1", tenant_id: "tenant-1", exam_id: "exam-1", question_id: "question-1", question_no: "Q1", answer_segment_id: "segment-1", submission_id: "submission-1", anonymous_code: "A001", source: "manual_sample", status: "assigned", priority: 0, assigned_to: "reviewer-1", grade_round: "first", revision: 4, created_by: "admin-1", created_at: "2026-08-09T00:00:00Z", updated_at: "2026-08-09T00:00:00Z" },
    expected_revision: 4,
    question_snapshot: { id: "snapshot-1", tenant_id: "tenant-1", exam_id: "exam-1", question_id: "question-1", snapshot_version: 2, subject_profile_id: "profile-1", subject_profile_code: "senior-mathematics", subject_profile_version: 1, education_stage: "senior", subject_code: "mathematics", archetype_code: "structured_steps", allowed_evidence_types: ["math_step", "unit_value"], risk_tier: "R3", profile_snapshot: {}, archetype_snapshot: {}, rubric_snapshot: {}, scoring_policy_snapshot: { mode: "HUMAN_PRIMARY" }, content_hash: "hash", created_at: "2026-08-09T00:00:00Z" },
    question: { id: "question-1", exam_id: "exam-1", question_no: "Q1", question_type: "subjective", score: 10 },
    answer_artifact: { answer_segment_id: "segment-1", status: "ready", segment_image_url: "/segment" },
    frozen_rubric: { id: "rubric-1", question_id: "question-1", version: "2", status: "frozen", max_score: 10, points: [{ code: "step-1", label: "列式正确", max_score: 4 }] },
    ai_candidates: [],
    scoring_evidence: [],
    ai_second_opinion: { available: true, presentation: "explicit_second_opinion", score_prefill_allowed: false, metadata: { model_version: "v1", confidence: 0.8 } },
    claim: { state: "claimed", can_renew: true },
    draft: null,
    subject_tool_hints: { subject_code: "mathematics", archetype_code: "structured_steps", allowed_evidence_types: ["math_step", "unit_value"], parser_policy: {}, evidence_policy: {}, response_schema: {} },
    ...overrides
  } as ReviewTaskContext;
}

describe("review task context presentation", () => {
  it("supplies an empty option list for the compact review context", () => {
    expect(questionFromContext(fixture()).options).toEqual([]);
  });
  it("keeps immutable R3 human-primary policy active before any AI suggestion exists", () => {
    expect(requiresExplicitSecondOpinion(fixture({ ai_second_opinion: undefined }))).toBe(true);
  });
  it("keeps R3 HUMAN_PRIMARY AI material behind an explicit second-opinion action", () => {
    const context = fixture();
    expect(requiresExplicitSecondOpinion(context)).toBe(true);
    expect(secondOpinionMetadata(context, false)).toBeNull();
    expect(secondOpinionMetadata(context, true)).toMatchObject({ model_version: "v1" });
  });

  it("normalizes the frozen rubric instead of reading a mutable question rubric", () => {
    expect(frozenRubricFromContext(fixture()).points).toEqual([
      { id: "step-1", description: "列式正确", score: 4, required: false }
    ]);
  });

  it("loads subject-specific tools from server hints", () => {
    expect(subjectToolDescriptor(fixture())).toMatchObject({
      group: "math_science",
      capabilities: ["公式放大", "步骤评分", "单位校验"]
    });
  });

  it("selects the four acceptance subject-tool variants from frozen server hints", () => {
    const descriptorFor = (subjectCode: string, allowedEvidenceTypes: string[] = []) => {
      const context = fixture();
      context.question_snapshot.subject_code = subjectCode as ReviewTaskContext["question_snapshot"]["subject_code"];
      context.subject_tool_hints.subject_code = subjectCode as ReviewTaskContext["subject_tool_hints"]["subject_code"];
      context.subject_tool_hints.allowed_evidence_types = allowedEvidenceTypes as ReviewTaskContext["subject_tool_hints"]["allowed_evidence_types"];
      return subjectToolDescriptor(context);
    };

    expect(descriptorFor("chinese")).toMatchObject({ group: "writing", capabilities: ["连续阅读", "段落定位", "Trait 评分"] });
    expect(descriptorFor("mathematics", ["math_step", "unit_value"])).toMatchObject({ group: "math_science", capabilities: ["公式放大", "步骤评分", "单位校验"] });
    expect(descriptorFor("physics", ["math_step", "unit_value"])).toMatchObject({ group: "math_science", capabilities: ["公式放大", "步骤评分", "单位校验"] });
    expect(descriptorFor("history")).toMatchObject({ group: "humanities", capabilities: ["材料高亮", "概念证据", "关系证据"] });
  });
});
