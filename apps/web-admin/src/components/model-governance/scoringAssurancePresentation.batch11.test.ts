import { describe, expect, it } from "vitest";
import {
  assuranceCalibrationStatusColor, assuranceCalibrationStatusLabel, assuranceCalibrationTransitionMessage,
  assuranceDisagreementStatusLabel, assuranceEvaluationStatusColor, assuranceEvaluationStatusLabel
} from "./scoringAssurancePresentation";

describe("评分保障展示决策", () => {
  it("评测和校准保留已知状态及未知回退", () => {
    const evaluation = ["draft", "completed", "invalidated", "unknown"];
    expect(evaluation.map(assuranceEvaluationStatusColor)).toEqual(["processing", "success", "default", "processing"]);
    expect(evaluation.map(assuranceEvaluationStatusLabel)).toEqual(["采集中", "已冻结", "已失效", "采集中"]);
    const calibration = ["draft", "completed", "approved", "invalidated", "unknown"];
    expect(calibration.map(assuranceCalibrationStatusColor)).toEqual(["warning", "processing", "success", "default", "warning"]);
    expect(calibration.map(assuranceCalibrationStatusLabel)).toEqual(["草稿", "待批准", "已批准", "已失效", "草稿"]);
  });

  it("校准反馈与分歧标签保持原文和默认分支", () => {
    expect([assuranceCalibrationTransitionMessage("complete"), assuranceCalibrationTransitionMessage("approve"), assuranceCalibrationTransitionMessage("invalidate")]).toEqual([
      "校准工件已完成。", "校准工件已批准；仅精确版本轴可引用。", "校准工件已失效。"
    ]);
    expect(["needs_review", "classified", "routed", "unknown"].map(assuranceDisagreementStatusLabel)).toEqual(["待审阅", "已分类", "已关联任务", "已关联任务"]);
  });
});
