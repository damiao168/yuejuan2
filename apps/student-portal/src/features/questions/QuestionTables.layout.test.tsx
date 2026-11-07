import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { StudentQuestion } from "../../api";
import { makeQuestion, makeResult } from "../../test/studentAppBatch8Fixtures";
import { QuestionTables } from "./QuestionTables";

const mocks = vi.hoisted(() => ({ question: vi.fn(), annotations: vi.fn() }));
vi.mock("../../studentSession", () => ({ useStudentSession: () => ({ submitting: false, isSubmitting: () => false, reauthenticate: vi.fn() }) }));
vi.mock("../../api", async importOriginal => ({ ...await importOriginal<typeof import("../../api")>(), getQuestion: mocks.question, listQuestionAnnotations: mocks.annotations }));
vi.mock("../appeals/QuestionAppealForm", () => ({ QuestionAppealForm: () => <form aria-label="复核申请" /> }));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.question.mockResolvedValue({ question: makeQuestion() });
  mocks.annotations.mockResolvedValue({ annotations: [] });
});

function cohort(overrides: Partial<NonNullable<StudentQuestion["cohort"]>> = {}): NonNullable<StudentQuestion["cohort"]> {
  return { sample_size: 25, mean_score_rate: .5, full_score_rate: .1, zero_score_rate: .1, median_score: 10, class_mean_score: 10, school_mean_score: 12.5, ...overrides };
}

describe("student question table direct answers", () => {
  it("always shows both answers, keeps appeals last, and does not open ordinary question details", () => {
    const question = makeQuestion({ correct_answer: "$x^2$", actual_answer: "第一步\n第二步" });
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect(screen.getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["题号", "标准答案", "实际作答", "我的得分", "班级最高分", "全校最高分", "班级平均分", "全校平均分", "申诉"]);
    const row = screen.getByLabelText(/^第 1 题，/).closest("tr")!;
    expect(row.querySelectorAll(".katex")).toHaveLength(1);
    expect(within(row).getByText("第一步 第二步")).toBeTruthy();
    expect(within(row.lastElementChild as HTMLElement).getByRole("button", { name: "申请本题复核" })).toBeTruthy();
    fireEvent.click(screen.getByLabelText(/^第 1 题，/));
    expect(mocks.question).not.toHaveBeenCalled();
    expect(mocks.annotations).not.toHaveBeenCalled();
    expect(screen.queryByText("查看作答与评分")).toBeNull();
    expect(screen.queryByRole("heading", { name: /成绩复核申请/ })).toBeNull();
  });

  it("the comparison toggle only changes statistics columns and keeps answer columns visible", () => {
    const question = makeQuestion();
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect((screen.getByRole("checkbox", { name: "显示比较数据" }) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByRole("checkbox", { name: "显示比较数据" }));
    expect(screen.getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["题号", "标准答案", "实际作答", "我的得分", "申诉"]);
    fireEvent.click(screen.getByRole("checkbox", { name: "显示比较数据" }));
    expect(screen.getAllByRole("columnheader").map(cell => cell.textContent)).toEqual(["题号", "标准答案", "实际作答", "我的得分", "班级最高分", "全校最高分", "班级平均分", "全校平均分", "申诉"]);
    expect(screen.queryByRole("columnheader", { name: /^失分$/ })).toBeNull();
    expect(screen.getByText("A")).toBeTruthy(); expect(screen.getByText("B")).toBeTruthy();
  });

  it("keeps four statistics in the matching question row, in header order, including zero scores", () => {
    const questions = [makeQuestion({ score_comparison: { class_max_score: 18, school_max_score: 19, class_mean_score: 10, school_mean_score: 12.5 }, cohort: cohort({ class_mean_score: 4, school_mean_score: 5 }) }),
      makeQuestion({ question_id: "question-2", question_no: "2", cohort_unavailable_reason: "not_public", score_comparison: { class_max_score: 0, school_max_score: 0, class_mean_score: 0, school_mean_score: 0 } })];
    render(<QuestionTables examID="exam-1" result={makeResult(questions)} questions={questions} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    const firstRow = screen.getByLabelText(/^第 1 题，/).closest("tr")!;
    const secondRow = screen.getByLabelText(/^第 2 题，/).closest("tr")!;
    expect([...firstRow.querySelectorAll("td.question-statistic")].map(cell => [cell.getAttribute("data-label"), cell.querySelector(".question-statistic-value")?.textContent])).toEqual([
      ["班级最高分", "18"], ["全校最高分", "19"], ["班级平均分", "10"], ["全校平均分", "12.5"]
    ]);
    expect([...secondRow.querySelectorAll(".question-statistic-value")].map(cell => cell.textContent)).toEqual(["0", "0", "0", "0"]);
    expect(screen.queryByRole("columnheader", { name: "参试学生平均分" })).toBeNull();
    expect(screen.queryByRole("columnheader", { name: "群体中位分对比" })).toBeNull();
    expect(firstRow.closest("table")?.classList.contains("has-comparisons")).toBe(true);
  });

  it("shows missing and nonfinite statistics as a dash with accessible reasons instead of substituting scores", () => {
    const question = makeQuestion({ score: 14, max_score: 20, cohort_unavailable_reason: "not_public", cohort: cohort(), score_comparison: { class_max_score: undefined, school_max_score: Number.NaN, class_mean_score: Number.POSITIVE_INFINITY, school_mean_score: Number.NEGATIVE_INFINITY } });
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    const row = screen.getByLabelText(/^第 1 题，/).closest("tr")!;
    const values = [...row.querySelectorAll(".question-statistic-value")];
    expect(values.map(cell => cell.textContent)).toEqual(["—", "—", "—", "—"]);
    expect(values.map(cell => cell.getAttribute("title"))).toEqual(["暂无统计数据", "暂无统计数据", "暂无统计数据", "暂无统计数据"]);
    expect(within(row).getByLabelText("班级最高分：暂无统计数据")).toBeTruthy();
    expect(within(row).getByLabelText("全校最高分：暂无统计数据")).toBeTruthy();
    expect(row.textContent).not.toMatch(/NaN|Infinity|undefined/);
  });

  it("keeps available school statistics when the class cannot be disclosed", () => {
    const question = makeQuestion({ cohort_unavailable_reason: "small_cohort", score_comparison: { school_max_score: 19, school_mean_score: 12.5 } });
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    const row = screen.getByLabelText(/^第 1 题，/).closest("tr")!;
    expect([...row.querySelectorAll(".question-statistic-value")].map(cell => cell.textContent)).toEqual(["—", "19", "—", "12.5"]);
    expect(within(row).getByLabelText("班级平均分：暂无统计数据")).toBeTruthy();
  });

  it("formats frozen choice arrays without inventing answers from unknown objects", () => {
    const questions = [makeQuestion({ question_type: "multiple_choice", correct_answer: '["A","C"]', actual_answer: '[1,2]' }),
      makeQuestion({ question_id: "question-2", question_no: "2", correct_answer: '{"answer":"B"}', actual_answer: "$x=2$" }),
      makeQuestion({ question_id: "question-3", question_no: "3", question_type: "short_answer", correct_answer: "[0,1]", actual_answer: "[1,2]" }),
      makeQuestion({ question_id: "question-4", question_no: "4", question_type: undefined, correct_answer: "[2,3]" })];
    render(<QuestionTables examID="exam-1" result={makeResult(questions)} questions={questions} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect(screen.getByText("A、C")).toBeTruthy();
    expect(screen.getByText("1、2")).toBeTruthy();
    expect(screen.getByText("[0,1]")).toBeTruthy();
    expect(screen.getByText("[1,2]")).toBeTruthy();
    expect(screen.getByText("[2,3]")).toBeTruthy();
    expect(screen.getByText("暂无法直接显示此答案")).toBeTruthy();
    expect(screen.queryByText('{"answer":"B"}')).toBeNull();
    expect(screen.getByLabelText(/^第 2 题，/).closest("tr")!.querySelectorAll(".katex")).toHaveLength(1);
  });

  it("only an explicit appeal reads same-release evidence", async () => {
    const question = makeQuestion();
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "申请本题复核" }));
    await screen.findByRole("form", { name: "复核申请" });
    expect(mocks.question).toHaveBeenCalledWith("exam-1", "question-1", "release-7");
    expect(mocks.annotations).toHaveBeenCalledWith("exam-1", "question-1", "release-7");
  });

  it("shows math fill blanks in their recorded order and keeps formulas, zero and missing positions", () => {
    const question = makeQuestion({ subject: undefined, question_type: "fill_blank", correct_answer: JSON.stringify({ blanks: ["$\\frac{1}{2}$", 0, "[0,1]"] }), actual_answer: JSON.stringify({ answers: ["$x+1$", "", "[1,2]"] }) });
    render(<QuestionTables examID="exam-1" result={makeResult([question])} questions={[question]} fallbackSubject="mathematics" onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    const row = screen.getByLabelText(/^第 1 题，/).closest("tr")!;
    const cells = within(row).getAllByRole("cell");
    expect(cells[1].textContent).toContain("第 2 空：0");
    expect(cells[1].textContent).toContain("第 3 空：[0,1]");
    expect(cells[2].textContent).toContain("第 2 空：未记录");
    expect(cells[2].textContent).toContain("第 3 空：[1,2]");
    expect(row.querySelectorAll(".katex")).toHaveLength(2);
    expect(mocks.question).not.toHaveBeenCalled();
  });

  it("uses compact options only for math multiple choice while leaving fill commas and other subjects intact", () => {
    const questions = [makeQuestion({ question_type: "multiple_choice", correct_answer: '"AC"', actual_answer: "A,C" }),
      makeQuestion({ question_id: "question-2", question_no: "2", question_type: "fill_blank", correct_answer: "1,2", actual_answer: "(0,1)" }),
      makeQuestion({ question_id: "question-3", question_no: "3", subject: "english", question_type: "multiple_choice", correct_answer: "AC", actual_answer: "A,C" })];
    render(<QuestionTables examID="exam-1" result={makeResult(questions)} questions={questions} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect(screen.getAllByText("A、C")).toHaveLength(2);
    expect(screen.getByText("1,2")).toBeTruthy();
    expect(screen.getByText("(0,1)")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "英语" }));
    expect(screen.getByText("AC")).toBeTruthy();
    expect(screen.getByText("A,C")).toBeTruthy();
  });

  it("does not reinterpret bare math intervals or extend fill formatting to other subjects and essay questions", () => {
    const questions = [makeQuestion({ question_type: "fill_blank", correct_answer: "[0,1]", actual_answer: "[1,2]" }),
      makeQuestion({ question_id: "question-2", question_no: "2", question_type: "essay", correct_answer: '{"blanks":["essay"]}' }),
      makeQuestion({ question_id: "question-3", question_no: "3", subject: "english", question_type: "fill_blank", correct_answer: '{"blanks":["word"]}' })];
    render(<QuestionTables examID="exam-1" result={makeResult(questions)} questions={questions} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect(screen.getByText("[0,1]")).toBeTruthy();
    expect(screen.getByText("[1,2]")).toBeTruthy();
    expect(screen.getByText("暂无法直接显示此答案")).toBeTruthy();
    expect(screen.queryByText(/第 1 空/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "英语" }));
    expect(screen.getByText("暂无法直接显示此答案")).toBeTruthy();
    expect(screen.queryByText(/第 1 空/)).toBeNull();
  });

  it("an old current-question link only locates a row even while appeals are closed", () => {
    const question = makeQuestion({ correct_answer: undefined, actual_answer: undefined });
    const result = makeResult([question], { appeal_window: { open: false } });
    render(<QuestionTables examID="exam-1" result={result} questions={[question]} requestedQuestionID={question.question_id} requestedQuestionToken={1} onAppealSubmitted={vi.fn()} onViewCurrent={vi.fn()} />);
    expect(document.activeElement).toBe(screen.getByLabelText(/^第 1 题，/));
    expect(screen.getByText("未提供或未公开")).toBeTruthy();
    expect(screen.getByText("暂无可显示的文字作答")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "申请本题复核" })).toBeNull();
    expect(mocks.question).not.toHaveBeenCalled();
  });

  it("locating a full-score question removes a filter that would hide its row", () => {
    const question = makeQuestion({ score: 20 });
    const result = makeResult([question]);
    const props = { examID: "exam-1", result, questions: [question], onAppealSubmitted: vi.fn(), onViewCurrent: vi.fn() };
    const view = render(<QuestionTables {...props} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "只看有失分的题目" }));
    expect(screen.queryByLabelText(/^第 1 题，/)).toBeNull();
    view.rerender(<QuestionTables {...props} requestedQuestionID={question.question_id} requestedQuestionToken={1} />);
    expect(document.activeElement).toBe(screen.getByLabelText(/^第 1 题，/));
    expect(mocks.question).not.toHaveBeenCalled();
  });
});
