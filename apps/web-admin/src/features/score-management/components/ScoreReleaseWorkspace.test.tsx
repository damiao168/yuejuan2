import type { ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { ScoreReleaseGate } from "../../../api/scoreReleases";
import { ScoreReleaseWorkspace } from "./ScoreReleaseWorkspace";

type Props = ComponentProps<typeof ScoreReleaseWorkspace>;
const baseGate: ScoreReleaseGate = { version: "1", passed: true, blocking: [], warnings: [], counts: {}, generated_at: "2026-10-03T00:00:00Z" };

// 本批只验证展示分支；工作流对象只提供界面读取的动作，不运行异步发布或复评请求。
const baseProps: Props = {
  canWrite: true,
  selectedExamId: "exam-1",
  gradeTotal: 1,
  releaseGate: null,
  scoreReleases: [],
  regradeJobs: [],
  actioning: null,
  releaseWorkflow: { openReleaseModal: () => undefined, publishRelease: () => undefined, revokeSharedPaper: () => undefined },
  regradeWorkflow: { openRegradeModal: () => undefined, regradeQuestionOptions: [], transitionRegrade: () => undefined, openRegradeReview: async () => undefined, materializeRegradeRelease: () => undefined }
};

describe("score release gate rendering", () => {
  it("uses clean deep links for question and quality issues without requiring router context", () => {
    const gate = { ...baseGate, passed: false, blocking: [
      { code: "quality", message: "待复核", count: 1, blocking: true, action_route: "quality" as const },
      { code: "questions", message: "待配置", count: 1, blocking: true, action_route: "questions" as const },
    ] };
    const html = renderToStaticMarkup(<ScoreReleaseWorkspace {...baseProps} selectedExamId="exam/1" releaseGate={gate} />);
    expect(html).toContain('href="/admin/exams/exam%2F1/quality"');
    expect(html).toContain('href="/admin/exams/exam%2F1/questions"');
    expect(html).not.toContain('href="#');
  });
  it("keeps an unread gate separate from a passed gate", () => {
    const html = renderToStaticMarkup(<ScoreReleaseWorkspace {...baseProps} />);
    expect(html).toContain("门禁待读取");
    expect(html).toContain("等待发布门禁数据");
    expect(html).not.toContain("当前发布门禁通过");
  });

  it("shows a passed gate when no warnings remain", () => {
    const html = renderToStaticMarkup(<ScoreReleaseWorkspace {...baseProps} releaseGate={baseGate} />);
    expect(html).toContain("门禁通过");
    expect(html).toContain("当前发布门禁通过");
    expect(html).not.toContain("等待发布门禁数据");
  });

  it("keeps a blocked gate in the issue list even when the issue list is empty", () => {
    const html = renderToStaticMarkup(<ScoreReleaseWorkspace {...baseProps} releaseGate={{ ...baseGate, passed: false }} />);
    expect(html).toContain("门禁待处理");
    expect(html).toContain("score-release-gate-list");
    expect(html).not.toContain("当前发布门禁通过");
  });

  it("keeps warnings visible when the gate otherwise passes", () => {
    const gate = { ...baseGate, warnings: [{ code: "warning", message: "复核提醒", count: 1, blocking: false }] };
    const html = renderToStaticMarkup(<ScoreReleaseWorkspace {...baseProps} releaseGate={gate} />);
    expect(html).toContain("门禁通过");
    expect(html).toContain("复核提醒");
    expect(html).not.toContain("当前发布门禁通过");
  });
});
