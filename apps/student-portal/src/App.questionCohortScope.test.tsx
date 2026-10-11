import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { StudentQuestion } from "./api";
import { installApi, makeQuestion, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  window.sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

function cohort(sampleSize: number): NonNullable<StudentQuestion["cohort"]> {
  return { sample_size: sampleSize, mean_score_rate: 0, full_score_rate: 0, zero_score_rate: 1, school_mean_score: 0, median_score: 0, class_unavailable_reason: "small_cohort" };
}

describe("逐题参比范围与人数", () => {
  it("每题均分和中位分对比使用各自人数，不把总分参比人数当作本题或班级人数", async () => {
    const result = makeResult([
      makeQuestion({ question_id: "question-1", question_no: "1", score: 0, cohort: cohort(25) }),
      makeQuestion({ question_id: "question-2", question_no: "2", score: 0, cohort: cohort(40) })
    ], { reference: { scope: "exam", sample_size: 300, statistics_available: true } });
    installApi(call => resultRoute(call, result));
    render(<App />);
    await screen.findByLabelText("总分");
    const firstRow = screen.getByRole("button", { name: /^第 1 题，/ }).closest("tr")!;
    const secondRow = screen.getByRole("button", { name: /^第 2 题，/ }).closest("tr")!;
    expect(within(firstRow).getAllByText("本版考试本题 · 参比 25 人")).toHaveLength(2);
    expect(within(secondRow).getAllByText("本版考试本题 · 参比 40 人")).toHaveLength(2);
    const classMean = firstRow.querySelector('[data-label="班级平均分"]')!;
    expect(classMean.textContent).toBe("参比人数不足，不展示");
    expect(firstRow.querySelector('[data-label="参试学生平均分"]')?.textContent).toBe("0本版考试本题 · 参比 25 人");
    expect(firstRow.textContent).not.toContain("300");
    fireEvent.click(within(firstRow).getByRole("button", { name: /^第 1 题，/ }));
    const inspector = (await screen.findByText("对比群体平均 0%")).closest(".answer-inspector")!;
    expect(within(inspector as HTMLElement).getByText("本版考试本题 · 参比 25 人")).toBeTruthy();
  });

  it.each([
    ["not_public", "学校未公开"],
    ["small_cohort", "参比人数不足，不展示"]
  ])("未公开本题统计时保留%s原因，不补0人或借用总人数", async (reason, label) => {
    const result = makeResult([makeQuestion({ cohort_unavailable_reason: reason })], { reference: { scope: "exam", sample_size: 300, statistics_available: true } });
    installApi(call => resultRoute(call, result));
    render(<App />);
    const row = (await screen.findByRole("button", { name: /^第 1 题，/ })).closest("tr")!;
    expect(within(row).getAllByText(label)).toHaveLength(3);
    expect(row.textContent).not.toContain("本版考试本题");
    expect(row.textContent).not.toMatch(/参比 (0|300) 人/);
  });

  it("历史响应缺少人数时明确待提供，不显示0或undefined", async () => {
    const missingCount = { ...cohort(25), sample_size: undefined } as unknown as NonNullable<StudentQuestion["cohort"]>;
    const result = makeResult([makeQuestion({ cohort: missingCount })]);
    installApi(call => resultRoute(call, result));
    render(<App />);
    const row = (await screen.findByRole("button", { name: /^第 1 题，/ })).closest("tr")!;
    expect(within(row).getAllByText("本版考试本题 · 参比人数暂未提供")).toHaveLength(2);
    expect(row.textContent).not.toContain("undefined");
    expect(row.textContent).not.toContain("参比 0 人");
  });
});
