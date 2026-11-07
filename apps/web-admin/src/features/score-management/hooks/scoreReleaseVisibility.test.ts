import { describe, expect, it } from "vitest";
import { defaultReleaseVisibility, presetVisibility, releaseVisibilityLabels, releaseVisibilityPreset, visibleReleaseContent } from "./useScoreReleaseWorkflow";

describe("冻结发布公开策略摘要", () => {
  it("题干、知识点和成绩分布仍由管理员明确勾选", () => {
    for (const field of ["show_question_stem", "show_knowledge_analysis", "show_score_distribution"] as const) {
      expect(defaultReleaseVisibility[field]).toBe(false);
      expect(releaseVisibilityLabels[field]).toBeTruthy();
    }
  });

  it("关闭逐题详情时摘要不承诺公开题干、答案、评分要点等子内容", () => {
    const summary = visibleReleaseContent({ ...defaultReleaseVisibility, show_question_scores: false, show_question_stem: true, show_knowledge_analysis: true, show_feedback: true, show_rubric_summary: true, show_score_distribution: true });
    expect(summary).toEqual(["群体统计", "百分位", "具体排名", "成绩分布"]);
  });

  it("已公开逐题成绩的摘要始终包含答案，兼容旧版隐藏标记但保留逐题门槛", () => {
    expect(visibleReleaseContent({ ...defaultReleaseVisibility, show_answers: false })).toContain("参考答案与本人答案");
    expect(visibleReleaseContent({ ...defaultReleaseVisibility, show_question_scores: false, show_answers: false })).not.toContain("参考答案与本人答案");
  });
});

describe("发布预设", () => {
  // 迁自原 ScoreWorkflowModals.recovery.test：预设保留复核依赖，任何预设都不顺带公开匿名范例卷或具体排名。
  it("公开模板保留复核依赖，任何模板都不顺带公开匿名范例卷或具体排名", () => {
    expect(releaseVisibilityPreset("basic", false).show_question_scores).toBe(false);
    const appeals = releaseVisibilityPreset("basic", true);
    expect(appeals.show_question_scores).toBe(true); expect(appeals.show_answers).toBe(true);
    for (const preset of [appeals, releaseVisibilityPreset("question", true)]) {
      expect(preset.show_high_score_paper).toBe(false); expect(preset.show_exact_rank).toBe(false);
    }
  });

  it("发布弹窗的两个预设都不公开排名和百分位", () => {
    for (const kind of ["basic", "question"] as const) {
      expect(presetVisibility(kind, true)).toMatchObject({ show_answers: true, show_exact_rank: false, show_percentile: false, show_high_score_paper: false });
      expect(presetVisibility(kind, false).show_answers).toBe(true);
    }
  });
});
