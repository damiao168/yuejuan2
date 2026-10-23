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

  it.each([
    { questionType: "short_answer", mode: "AI_ASSIST", lane: "ai", note: "自动识别学生作答并生成建议分，由教师确认最终分数" },
    { questionType: "essay", mode: "AI_FAST_CONFIRM", lane: "ai", note: "自动识别学生作答并生成建议分，由教师确认最终分数" },
    { questionType: "numeric", mode: "RULE_AUTO", lane: "rule", note: "规则判分；异常转人工" },
    { questionType: "short_answer", mode: "RULE_AUTO", lane: "human", note: "人工评分或复核" },
    { questionType: "coding", mode: "DUAL_HUMAN", lane: "human", note: "同校两名阅卷员互盲双评，分数不一致即仲裁" },
    { questionType: "single_choice", mode: "SNAPSHOT_UNAVAILABLE", lane: "human", note: "策略快照暂不可读，按人工处理" },
  ])("保留 $questionType / $mode 的分流和提示", ({ questionType, mode, lane, note }) => {
    expect(planQuestion({ ...question, question_type: questionType }, mode, "R2")).toEqual({
      questionId: question.question_id,
      questionNo: question.question_no,
      questionType,
      count: question.total,
      mode,
      riskTier: "R2",
      lane,
      note,
    });
  });
});
