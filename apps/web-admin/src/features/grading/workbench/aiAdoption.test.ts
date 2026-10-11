import { describe, expect, it } from "vitest";
import type { RubricPoint } from "../../../api/papers";
import type { AiGrade } from "../../../api/review";
import { aiPointAdoption } from "./aiAdoption";

function grade(code: string, score: number): AiGrade {
  return {
    id: "grade", tenant_id: "tenant", answer_segment_id: "segment", question_id: "question", question_no: "1",
    question_type: "short_answer", grader_type: "ai", rubric_version: "v1", suggested_score: score,
    max_score: 5, confidence: 1, matched_points: [{ code, label: code, score }], missing_points: [],
    evidence: [], risk_flags: [], needs_human_review: true, mock: false, created_by: "teacher", created_at: "2026-10-05T00:00:00Z",
  };
}
function point(id: string): RubricPoint {
  return { id, description: id, score: 5, required: true, evidence_requirements: [] };
}

describe("评分点自有属性检查兼容 ES2020", () => {
  it("零分仍是已写入的评分点", () => {
    expect(aiPointAdoption(grade("p1", 0), [point("p1")], "v1")).toEqual({ p1: 0 });
  });
  it("拒绝匹配项与缺失项之间重复的零分评分点", () => {
    const suggestion = grade("p1", 0);
    suggestion.missing_points = [{ code: "p1", label: "p1", score: 0 }];
    expect(aiPointAdoption(suggestion, [point("p1")], "v1")).toBeNull();
  });
  it("继承属性名称不能被误认为已经匹配", () => {
    const result = aiPointAdoption(grade("toString", 1), [point("toString")], "v1");
    expect(result).toEqual({ toString: 1 });
  });
});
