import { describe, expect, it } from "vitest";
import { defaultReleaseVisibility, releaseVisibilityLabels, visibleReleaseContent } from "./useScoreReleaseWorkflow";

describe("冻结发布公开策略摘要", () => {
  it("包含题干、知识点和分布开关，新公开内容由管理员明确勾选", () => {
    for (const field of ["show_question_stem", "show_knowledge_analysis", "show_score_distribution"] as const) {
      expect(defaultReleaseVisibility[field]).toBe(false);
      expect(releaseVisibilityLabels[field]).toBeTruthy();
    }
  });

  it("关闭逐题详情时摘要不承诺公开题干、答案、评分要点等子内容", () => {
    const summary = visibleReleaseContent({ ...defaultReleaseVisibility, show_question_scores: false, show_question_stem: true, show_knowledge_analysis: true, show_feedback: true, show_rubric_summary: true, show_score_distribution: true });
    expect(summary).toEqual(["群体统计", "百分位", "具体排名", "成绩分布"]);
  });
});
