import { describe, expect, it } from "vitest";
import type { ScoringRunItem } from "../../../api/review";
import { matchesScoringReviewFilter, scoringReviewCategory, scoringReviewCounts } from "./scoringReviewCategories";

const item = (value: Partial<ScoringRunItem>) => value as ScoringRunItem;

describe("阅卷待办业务分类", () => {
  it("将19道题分成2道已确认、11道选项核对、5道建议分确认和1道识别核对", () => {
    const items = [
      ...Array.from({ length: 11 }, () => item({ state: "review", question_type: "single_choice", reason_code: "omr_manual_only_profile" })),
      ...Array.from({ length: 5 }, () => item({ state: "review", question_type: "short_answer", decision_status: "suggestion_ready" })),
      item({ state: "review", question_type: "fill_blank", reason_code: "answer_observation_uncertain" }),
      item({ state: "confirmed", question_type: "fill_blank", decision_status: "auto_confirmed" }),
      item({ state: "confirmed", question_type: "short_answer", decision_status: "human_confirmed" })
    ];
    expect(scoringReviewCounts(items)).toEqual({ choice: 11, suggestion: 5, recognition: 1, manual: 0 });
    expect(items.filter(entry => matchesScoringReviewFilter(entry, "all"))).toHaveLength(19);
    expect(items.filter(entry => matchesScoringReviewFilter(entry, "suggestion"))).toHaveLength(5);
  });

  it.each(["single_choice", "multiple_choice", "true_false"])("%s优先进入选项核对", question_type => {
    expect(scoringReviewCategory(item({ state: "review", question_type, decision_status: "suggestion_ready", reason_code: "answer_low_confidence" }))).toBe("choice");
  });

  it.each(["answer_observation_uncertain", "answer_association_uncertain", "observation_unreadable", "observation_uncertain", "answer_low_confidence", "ocr_low_confidence"])("通过具体识别原因%s归类", reason_code => {
    expect(scoringReviewCategory(item({ state: "review", question_type: "fill_blank", reason_code }))).toBe("recognition");
  });

  it("建议分置信不足不等于识别不确定，缺少建议事实也不能猜成已有建议", () => {
    expect(scoringReviewCategory(item({ state: "review", decision_status: "suggestion_ready", reason_code: "ai_low_confidence", recognition_confidence: 0.4 }))).toBe("suggestion");
    expect(scoringReviewCategory(item({ state: "review", reason_code: "ai_low_confidence", recognition_confidence: 0.4 }))).toBe("manual");
    expect(scoringReviewCategory(item({ state: "review", question_type: "short_answer", score: 3, grade_source: "ai_suggestion" }))).toBe("manual");
    expect(scoringReviewCategory(item({ state: "review", reason_code: "ai_suggestion_ready" }))).toBe("suggestion");
  });

  it.each(["confirmed", "auto_confirmed", "human_confirmed", "failed", "pending", "queued", "processing", "cancelled", "cancelling"])("%s不因残留待复核决定或原因被重新计入", state => {
    expect(scoringReviewCategory(item({ state, question_type: "single_choice", decision_status: "review_needed", reason_code: "answer_observation_uncertain" }))).toBeUndefined();
    expect(matchesScoringReviewFilter(item({ state, decision_status: "suggestion_ready" }), "suggestion")).toBe(false);
  });

  it("兼容缺少旧state的明确待复核决定，后台处理中或已确认决定仍优先排除", () => {
    expect(scoringReviewCategory(item({ decision_status: "review_needed", reason_code: "answer_low_confidence" }))).toBe("recognition");
    expect(scoringReviewCategory(item({ decision_status: "manual_only" }))).toBe("manual");
    expect(scoringReviewCategory(item({ state: "review", decision_status: "human_confirmed" }))).toBeUndefined();
    expect(scoringReviewCategory(item({ state: "review", decision_status: "suggestion_ready", processing_status: "running" }))).toBeUndefined();
    expect(scoringReviewCounts([item({ state: "review", processing_status: "failed" })])).toEqual({ choice: 0, suggestion: 0, recognition: 0, manual: 0 });
  });
});
