import { describe, expect, it } from "vitest";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import { currentExamBusinessStage, examBusinessStages } from "./businessStages";

function workspace(stage: ExamWorkspaceProjection["stage"]): ExamWorkspaceProjection {
  return {
    exam_id: "exam /1", exam_name: "考试", exam_status: "draft", revision: 1,
    stage, stages: [], stage_progress: [], blockers: [], warnings: [],
    counts: { paper_count: 0, question_count: 0, submission_count: 5, failed_submission_count: 0, quality_issue_submission_count: 0, unmatched_submission_count: 0, pending_review_count: 2, pending_arbitration_count: 0 },
    next_actions: [], risk_tier: "unknown", updated_at: "2026-10-10T00:00:00Z",
    subject_summary: { code: "math", label: "数学", total_score: 100, question_count: 10, configured_question_count: 8, frozen_question_count: 0, risk_tier_source: "none", question_types: {} }
  };
}

describe("考试业务阶段导航", () => {
  it.each([
    ["prepare", "preparation", ["current", "pending", "pending", "pending"]],
    ["capture", "capture", ["completed", "current", "pending", "pending"]],
    ["grading", "grading", ["completed", "completed", "current", "pending"]],
    ["quality", "grading", ["completed", "completed", "current", "pending"]],
    ["results", "results", ["completed", "completed", "completed", "current"]]
  ] as const)("生命周期 %s 决定当前导航阶段", (lifecycle, current, states) => {
    const data = workspace(lifecycle);
    expect(examBusinessStages(data).map((stage) => stage.state)).toEqual(states);
    expect(currentExamBusinessStage(data)?.key).toBe(current);
  });

  it("展示按服务端数量和已编码考试ID生成，成绩摘要仅在成绩阶段开放", () => {
    expect(examBusinessStages(workspace("capture")).map((stage) => stage.summary)).toEqual(["8/10 题已配置", "5 份答卷", "2 项待阅", "尚未开始"]);
    const stages = examBusinessStages(workspace("results"));
    expect(stages[3].summary).toBe("查看成绩");
    expect(stages[3].action_route).toBe("/exams/exam%20%2F1/scores");
  });
});
