import { describe, expect, it } from "vitest";
import { planQuestion } from "./scoringPlan";

const question = { question_id: "q1", question_no: "1", question_type: "short_answer", total: 30, queued: 0, confirmed: 0, review: 0, failed: 0 };

describe("scoring preflight routes", () => {
  it("offers AI suggestions only for supported subjective types and allowed modes", () => {
    expect(planQuestion(question, "AI_ASSIST", "R2").lane).toBe("ai");
    expect(planQuestion({ ...question, question_type: "coding" }, "AI_ASSIST", "R2").lane).toBe("human");
    expect(planQuestion(question, "HUMAN_PRIMARY", "R3").lane).toBe("human");
    expect(planQuestion(question, "SNAPSHOT_UNAVAILABLE", "-").lane).toBe("human");
  });
});
