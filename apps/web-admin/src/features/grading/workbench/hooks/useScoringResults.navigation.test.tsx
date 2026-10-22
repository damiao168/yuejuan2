// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ScoringRunItem } from "../../../../api/review";
import { useScoringResults } from "./useScoringResults";

vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: vi.fn() } }) } }));

const choice = { answer_segment_id: "choice", state: "review", question_type: "single_choice", question_no: "1", anonymous_code: "A", recognized_answer: "B" } as ScoringRunItem;
const suggestion = { answer_segment_id: "suggestion", state: "review", question_type: "calculation", question_no: "15", anonymous_code: "A", decision_status: "suggestion_ready", route: "image_ai" } as ScoringRunItem;
const uncertain = { answer_segment_id: "uncertain", state: "review", question_type: "fill_blank", question_no: "14", anonymous_code: "A", recognized_answer: "(2,+∞)", reason_code: "answer_observation_uncertain" } as ScoringRunItem;
const confirmed = { ...uncertain, answer_segment_id: "confirmed", state: "confirmed", decision_status: "auto_confirmed" };
const items = [choice, suggestion, uncertain, confirmed];

describe("结果页待办筛选与导航状态", () => {
  it("识别待办只显示未确定的第14题，切管理视图重绘后保留筛选", () => {
    const { result, rerender } = renderHook(({ scope }) => useScoringResults(scope, { items }), { initialProps: { scope: "exam-A" } });
    act(() => { result.current.setResultState("review"); result.current.setResultReviewFilter("recognition"); });
    expect(result.current.filteredItems.map(item => item.answer_segment_id)).toEqual(["uncertain"]);
    rerender({ scope: "exam-A" });
    expect(result.current.resultReviewFilter).toBe("recognition");
    expect(result.current.filteredItems).toEqual([uncertain]);
  });

  it("建议分筛选不会混入识别不确定，换考试清空上一场筛选", () => {
    const { result, rerender } = renderHook(({ scope }) => useScoringResults(scope, { items }), { initialProps: { scope: "exam-A" } });
    act(() => { result.current.setResultReviewFilter("suggestion"); result.current.setResultState("review"); });
    expect(result.current.filteredItems).toEqual([suggestion]);
    rerender({ scope: "exam-B" });
    expect(result.current.resultReviewFilter).toBe("all");
    expect(result.current.resultState).toBe("all");
    expect(result.current.filteredItems).toHaveLength(4);
  });
});
