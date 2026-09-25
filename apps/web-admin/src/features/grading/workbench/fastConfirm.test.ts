import { describe, expect, it } from "vitest";
import type { WorkbenchContext } from "./gradingWorkbench.types";
import { fastConfirmCandidate } from "./fastConfirm";

const context = (): WorkbenchContext => ({
  task: { id: "task", assigned_to: "grader", status: "assigned", revision: 1, anonymous_code: "A001" },
  reviewContext: {
    question_snapshot: { scoring_policy_snapshot: { mode: "AI_FAST_CONFIRM", confidence_threshold: 0.9 }, risk_tier: "R2", subject_code: "chinese" },
    draft: null
  },
  question: { score: 10, rubric: { points: [] } },
  aiGrades: [{ id: "ai", suggested_score: 8, max_score: 10, confidence: 0.97, evidence: [{ type: "text", answer_text: "依据" }], risk_flags: [], mock: false, status: "succeeded" }],
  warnings: [],
  segmentImageUrl: "/api/v1/review-tasks/task/segment-image"
} as unknown as WorkbenchContext);

describe("fast confirmation eligibility", () => {
  it("allows an assigned low-risk evidence-backed suggestion", () => {
    expect(fastConfirmCandidate(context(), "grader")?.score).toBe(8);
  });
  it("requires current ownership, a strict confidence floor and complete evidence", () => {
    const owned = context();
    expect(fastConfirmCandidate(owned, "another-grader")).toBeNull();
    owned.aiGrades[0].confidence = 0.94;
    expect(fastConfirmCandidate(owned, "grader")).toBeNull();
    owned.aiGrades[0].confidence = 0.98;
    owned.aiGrades[0].evidence = [];
    expect(fastConfirmCandidate(owned, "grader")).toBeNull();
    owned.aiGrades[0].evidence = [{ type: "text" }];
    expect(fastConfirmCandidate(owned, "grader")).toBeNull();
    owned.aiGrades[0].evidence = [{ type: "text", answer_text: "  " }];
    expect(fastConfirmCandidate(owned, "grader")).toBeNull();
    owned.aiGrades[0].evidence = [{ type: "text", answer_text: "依据" }];
    owned.warnings = ["最新建议失败"];
    expect(fastConfirmCandidate(owned, "grader")).toBeNull();
  });
  it("routes high-risk, mathematical and rubric-based answers to full review", () => {
    const highRisk = context();
    highRisk.reviewContext.question_snapshot.risk_tier = "R3";
    expect(fastConfirmCandidate(highRisk, "grader")).toBeNull();
    const math = context();
    math.reviewContext.question_snapshot.subject_code = "mathematics";
    expect(fastConfirmCandidate(math, "grader")).toBeNull();
    const rubric = context();
    const points = rubric.question?.rubric?.points;
    if (!points) throw new Error("missing fixture rubric");
    points.push({ id: "p1", score: 10 } as (typeof points)[number]);
    expect(fastConfirmCandidate(rubric, "grader")).toBeNull();
  });
});
