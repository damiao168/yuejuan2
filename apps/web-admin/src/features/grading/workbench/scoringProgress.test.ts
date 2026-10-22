import { describe, expect, it } from "vitest";
import type { ScoringRunItem } from "../../../api/review";
import { hasObservedAnswer, hasScoringResult, imageRecognitionSummary, scoringProgressLabel } from "./scoringProgress";

const item = (value: Partial<ScoringRunItem>) => value as ScoringRunItem;

describe("评分执行事实与结果分开统计", () => {
  it("does not count manual routing or a failed task as recognition", () => {
    expect(hasObservedAnswer(item({ state: "review", reason_code: "scoring_rule_missing" }))).toBe(false);
    expect(hasObservedAnswer(item({ state: "failed", runtime_status: "succeeded" }))).toBe(false);
    expect(hasObservedAnswer(item({ recognition_completed: false, recognized_answer: "旧输入", recognition_source: "ocr_text" }))).toBe(false);
  });

  it("accepts an explicit blank observation without fabricated text", () => {
    expect(hasObservedAnswer(item({ recognition_completed: true, recognition_decision: "blank" }))).toBe(true);
    expect(hasScoringResult(item({ score: 0, grade_source: "ai_suggestion" }))).toBe(true);
    expect(hasScoringResult(item({ score: 0 }))).toBe(false);
  });

  it("distinguishes successful suggestions from waiting dependencies and blockers", () => {
    expect(scoringProgressLabel(item({ processing_status: "succeeded", decision_status: "suggestion_ready" }))).toBe("建议分待确认");
    expect(scoringProgressLabel(item({ processing_status: "waiting_dependency", stage: "observe" }))).toBe("识别学生作答");
    expect(scoringProgressLabel(item({ processing_status: "blocked", stage: "observe" }))).toBe("需要处理");
  });

  it("describes completed image recognition and suggestions without inventing transcribed text", () => {
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: true, decision_status: "suggestion_ready" }))).toEqual({ label: "作答图已识别", detail: "建议已生成" });
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: false, decision_status: "suggestion_ready", score: 4, grade_source: "ai_suggestion" }))).toEqual({ label: "建议已生成", detail: "请查看作答图" });
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_source: "imported_answer", recognized_answer: "导入文本", decision_status: "suggestion_ready" }))).toEqual({ label: "建议已生成", detail: "请查看作答图" });
    expect(imageRecognitionSummary(item({ route: "omr", recognition_completed: true, recognition_decision: "selected", recognized_answer: "B" }))).toBeUndefined();
  });

  it("keeps blank, queued and failed image processing distinct", () => {
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: true, recognition_decision: "blank", decision_status: "suggestion_ready" }))).toEqual({ label: "未作答", detail: "建议已生成" });
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: false, processing_status: "queued" }))).toEqual({ label: "等待处理", detail: "尚无已完成的识别结果" });
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: false, processing_status: "failed" }))).toEqual({ label: "暂无识别结果", detail: "处理失败，请查看作答图" });
    expect(imageRecognitionSummary(item({ route: "image_ai", recognition_completed: true, processing_status: "failed" }))).toEqual({ label: "作答图已识别", detail: "后续处理失败，请核对" });
  });
});
