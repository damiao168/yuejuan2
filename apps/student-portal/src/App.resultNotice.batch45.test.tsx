import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { installApi, makeQuestion, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.location.hash = "/exams/exam-1";
  window.sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("第45批新版成绩差异的真实入口", () => {
  it("明确接纳新版后才按新版题序显示共同变分题，0分不遗漏", async () => {
    let result = makeResult([
      makeQuestion({ score: 4 }),
      makeQuestion({ question_id: "removed", question_no: "2", score: 7 }),
      makeQuestion({ question_id: "unchanged", question_no: "3", score: 6 }),
      makeQuestion({ question_id: "changed", question_no: "4", score: 9 })
    ], { total_score: 26 });
    installApi((call) => resultRoute(call, result));
    render(<App />);
    await screen.findByLabelText("总分");
    // 新增、隐藏和未变分题都不进入差异文案，结果需等待原“查看新版”入口明确接纳。
    result = makeResult([
      makeQuestion({ question_id: "added", question_no: "5", score: 8 }),
      makeQuestion({ question_id: "changed", question_no: "4", score: 5 }),
      makeQuestion({ question_id: "unchanged", question_no: "3", score: 6 }),
      makeQuestion({ score: 0 })
    ], { release_id: "release-8", release_version: 8, total_score: 19 });
    await act(async () => window.dispatchEvent(new Event("focus")));
    const accept = await screen.findByRole("button", { name: "查看新版成绩" });
    expect(screen.queryByText(/已更新为第 8 版/)).toBeNull();
    fireEvent.click(accept);
    await screen.findByText("已更新为第 8 版。本科成绩 26 → 19 分；第4题 5分、第1题 0分。仅比较两版均公开的分数。");
  });
});
