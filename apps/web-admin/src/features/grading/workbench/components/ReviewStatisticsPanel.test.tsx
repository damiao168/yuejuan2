// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ReviewStatisticsPanel } from "./ReviewStatisticsPanel";

const state = vi.hoisted(() => ({ statistics: {
  // 故意使跨题汇总与当前题差异很大，防止把整场比例再次当成本题平均分。
  correct_rate: 0.99, average_score_rate: 0.88, graded_count: 100,
  questions: [
    { question_id: "first", question_no: "1", average_score: 0, graded_count: 2 },
    { question_id: "second", question_no: "2", average_score: 8.25, graded_count: 4 }
  ], updated_at: "2026-10-29T08:00:00Z"
} }));
vi.mock("../hooks/useReviewStatistics", () => ({ useReviewStatistics: () => ({ ...state, focused: true, refresh: vi.fn() }) }));
const scope = { examId: "exam", tenantId: "tenant", actorId: "teacher", personalScope: true };
beforeEach(() => {
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("逐题平均分展示", () => {
  it("只显示当前题的平均分和份数，换题后不沿用其他题或全场指标", () => {
    const { rerender } = render(<ReviewStatisticsPanel {...scope} questionId="first" questionNo="1" />);
    const summary = screen.getByRole("region", { name: "本人逐题平均分" }).querySelector(".review-statistics-totals")!;
    expect(summary.textContent).toContain("第 1 题平均分0 分");
    expect(summary.textContent).toContain("已评分2 份");
    expect(summary.textContent).not.toMatch(/99|88|100/);
    rerender(<ReviewStatisticsPanel {...scope} questionId="second" questionNo="2" />);
    expect(summary.textContent).toContain("第 2 题平均分8.25 分");
    expect(summary.textContent).toContain("已评分4 份");
    rerender(<ReviewStatisticsPanel {...scope} questionId="ungraded" questionNo="3" />);
    expect(summary.textContent).toContain("第 3 题平均分—");
    expect(summary.textContent).toContain("已评分0 份");
  });

  it("管理员概览直接逐题列出平均分而不显示跨题正确率", () => {
    render(<ReviewStatisticsPanel {...scope} personalScope={false} />);
    const panel = screen.getByRole("region", { name: "本场逐题平均分" });
    expect(within(panel).getByRole("columnheader", { name: "平均分" })).toBeTruthy();
    expect(within(panel).getByRole("row", { name: "第 1 题 0 分 2" })).toBeTruthy();
    expect(within(panel).getByRole("row", { name: "第 2 题 8.25 分 4" })).toBeTruthy();
    expect(panel.textContent).not.toContain("正确率");
  });
});
