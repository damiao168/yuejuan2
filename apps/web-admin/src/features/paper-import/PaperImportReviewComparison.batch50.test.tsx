// @vitest-environment jsdom
import { cleanup, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { PaperImportDraftQuestion } from "../../api/papers";
import { PaperImportReviewComparison } from "./PaperImportReviewComparison";

function draft(overrides: Partial<PaperImportDraftQuestion> = {}): PaperImportDraftQuestion {
  return { candidate_id: "candidate", question_no: "1", question_type: "short_answer", score: 5,
    stem: "题干", knowledge_points: [], confidence: 1, issues: [], source_refs: [], ...overrides };
}

function cells(container: HTMLElement, label: string) {
  const row = within(container).getByRole("columnheader", { name: `第 1 题 · ${label}` }).closest("tr")!;
  return within(row).getAllByRole("cell");
}

describe("试卷导入冲突比较的结构化值", () => {
  afterEach(cleanup);

  it("递归显示两侧对象、数组、数字和布尔内容，保留未知业务字段但隐藏内部元数据", () => {
    const local = draft({ answer_key: { standard_answer: { answers: [0, false, { custom: "本地补充" }], id: "hidden-id", source_refs: ["hidden-source"] }, equivalent_answers: [], tolerance: {} },
      rubric: { status: "draft", max_score: 5, points: [{ id: "point", description: "必要条件", score: 0, required: false }], deductions: [], examples: [] } });
    const latest = draft({ answer_key: { standard_answer: { answers: [1, true, { custom: "服务器补充" }] }, equivalent_answers: [], tolerance: {} },
      rubric: { status: "draft", max_score: 5, points: [{ id: "point", description: "必要条件", score: 1, required: true }], deductions: [], examples: [] } });
    const view = render(<PaperImportReviewComparison local={[local]} latest={[latest]} />);
    const answer = cells(view.container, "答案");
    expect(answer[0].textContent).toContain("标准答案：answers：0；否；custom：本地补充");
    expect(answer[1].textContent).toContain("标准答案：answers：1；是；custom：服务器补充");
    expect(view.container.textContent).not.toMatch(/\[object Object\]|hidden-id|hidden-source/);
    const rubric = cells(view.container, "评分标准");
    expect(rubric[0].textContent).toBe("状态：草稿；总分：5；评分点：条件：必要条件；分值：0；必需：否；扣分：未填写；示例：未填写");
    expect(rubric[1].textContent).toBe("状态：草稿；总分：5；评分点：条件：必要条件；分值：1；必需：是；扣分：未填写；示例：未填写");
  });

  it.each([{}, { id: "internal-only", source_refs: [] }])("没有可展示字段的历史对象显示未填写，不留空白 %j", value => {
    const local = draft({ answer_key: { standard_answer: value, equivalent_answers: [], tolerance: {} } });
    const latest = draft({ answer_key: { standard_answer: "服务器答案", equivalent_answers: [], tolerance: {} } });
    const view = render(<PaperImportReviewComparison local={[local]} latest={[latest]} />);
    expect(cells(view.container, "答案")[0].textContent).toBe("标准答案：未填写；等价答案：未填写；容差：未填写");
    expect(cells(view.container, "答案")[1].textContent).toContain("服务器答案");
  });

  it("数字零、题型名称和显式清空分别保持含义", () => {
    const local = draft({ question_type: "single_choice", score: 0, stem: "" });
    const latest = draft({ question_type: "multiple_choice", score: 2, stem: "最新题干" });
    const view = render(<PaperImportReviewComparison local={[local]} latest={[latest]} />);
    expect(cells(view.container, "题型").map(cell => cell.textContent)).toEqual(["单选题", "多选题"]);
    expect(cells(view.container, "分值").map(cell => cell.textContent)).toEqual(["0", "2"]);
    expect(cells(view.container, "题干").map(cell => cell.textContent)).toEqual(["未填写", "最新题干"]);
  });

  it("按候选 ID 对应重排后的内容，缺失候选仍提示缺失且不修改输入", () => {
    const local = [draft({ stem: "本地修改" }), draft({ candidate_id: "other", question_no: "2", stem: "另一题" })];
    const latest = [draft({ candidate_id: "other", question_no: "2", stem: "另一题" }), draft({ stem: "服务器修改" })];
    const before = structuredClone({ local, latest });
    const view = render(<PaperImportReviewComparison local={local} latest={latest} />);
    expect(cells(view.container, "题干").map(cell => cell.textContent)).toEqual(["本地修改", "服务器修改"]);
    expect(within(view.container).getAllByRole("row")).toHaveLength(2);
    view.rerender(<PaperImportReviewComparison local={local} latest={[latest[0]]} />);
    expect(cells(view.container, "题干")[1].textContent).toBe("最新结果中已没有此候选");
    expect({ local, latest }).toEqual(before);
  });
});
