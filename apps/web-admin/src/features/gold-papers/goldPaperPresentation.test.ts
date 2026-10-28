import { describe, expect, it } from "vitest";
import { buildRubricEvidence, goldCoverageGapLabel, rubricPointsFromSnapshot } from "./goldPaperPresentation";

describe("gold paper presentation", () => {
  it("keeps rubric-point evidence instead of reducing a Gold version to total score", () => {
    const points = rubricPointsFromSnapshot({ points: [{ id: "step", description: "关键步骤", score: 2 }] });
    expect(buildRubricEvidence({ step: 1.5, ignored: 9 }, points)).toEqual({ step: 1.5 });
  });

  it("典型模式显示中文，未知机器标签不直接暴露在教师提示中", () => {
    expect(goldCoverageGapLabel("missing_pattern:unit_error")).toBe("缺少典型模式：单位错误");
    expect(goldCoverageGapLabel("missing_pattern:internal_unknown_tag")).toBe("缺少典型模式：其他典型错误");
  });
});
