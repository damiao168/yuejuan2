// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import { ExamWorkspaceLayout } from "./ExamWorkspaceLayout";

vi.mock("../../school-assistant/SchoolAssistantProvider", () => ({ useSchoolAssistantContext: () => null }));
vi.mock("@edugrade/ui", () => ({
  WorkspaceLayout: ({ header, children }: { header: ReactNode; children: ReactNode }) => <>{header}{children}</>,
  ExamStageIndicator: () => null, QualityIndicator: () => null,
  StatusBadge: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));
vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
  Select: () => null,
  Alert: ({ message }: { message: string }) => <p>{message}</p>
}));

function workspace(overrides: Partial<ExamWorkspaceProjection> = {}): ExamWorkspaceProjection {
  return {
    exam_id: "math", exam_name: "期中数学", exam_session_id: "session 1", exam_session_name: "期中考试", exam_status: "draft", revision: 2,
    stage: "prepare", stages: [], stage_progress: [], blockers: [], warnings: [], counts: { paper_count: 0, question_count: 0, submission_count: 0, failed_submission_count: 0, quality_issue_submission_count: 0, unmatched_submission_count: 0, pending_review_count: 0, pending_arbitration_count: 0 },
    next_actions: [], risk_tier: "unknown", updated_at: "2026-10-04T00:00:00Z", subject_summary: { code: "math", label: "数学", total_score: 150, question_count: 0, configured_question_count: 0, frozen_question_count: 0, risk_tier_source: "none", question_types: {} }, ...overrides
  };
}

async function render(data: ExamWorkspaceProjection) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const navigate = vi.fn();
  await act(async () => root.render(<ExamWorkspaceLayout data={data} section="students" moduleContent={<p>学生范围</p>} onNavigate={navigate} />));
  return { container, navigate, close: async () => { await act(async () => root.unmount()); container.remove(); } };
}

describe("学科工作台整场上下文", () => {
  it("多科工作台返回服务端整场 ID，单科历史考试仍返回独立总览", async () => {
    for (const [data, expected] of [[workspace(), "/exams/overview/session/session%201"], [workspace({ exam_session_id: undefined }), "/exams/overview/standalone/math"]] as const) {
      const view = await render(data);
      try {
        const button = [...view.container.querySelectorAll("button")].find((item) => item.textContent === "整场总览")!;
        await act(async () => button.click());
        expect(view.navigate).toHaveBeenCalledWith(expected);
        expect(view.container.textContent).toContain("数学 · 150 分");
      } finally { await view.close(); }
    }
  });

  it("无法核实归档状态时显示当前只读，不把故障断言为已归档", async () => {
    const view = await render(workspace({ read_only: true, warnings: [{ code: "archive_state_unavailable", title: "归档状态不可用", message: "暂无法确认", severity: "warning", action_label: "刷新", action_route: "/exams" }] }));
    try {
      expect(view.container.textContent).toContain("归档状态暂无法确认，当前学科只读");
      expect(view.container.textContent).not.toContain("考试已归档");
    } finally { await view.close(); }
  });
});
