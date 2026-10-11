import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { QuestionAppealForm } from "./App";
import {
  dataResponse,
  deferredResponse,
  installApi,
  jsonResponse,
  makeAppeal,
  makeQuestion,
  makeResult,
  resultRoute
} from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// 这四项要求新的原生 output 结构；旧源码预期失败，不计入等价行为通过数。
describe("Batch8 intentional native output semantics", () => {
  it("uses native output for the empty lost-question filter result", async () => {
    const result = makeResult([makeQuestion({ score: 20 })]);
    installApi((call) => resultRoute(call, result));
    render(<App />);
    await screen.findByLabelText("总分");
    fireEvent.click(screen.getByRole("checkbox", { name: "只看有失分的题目" }));
    const status = screen.getByText("本学科没有失分题目，可以切换为查看全部题目。");
    expect(status.tagName).toBe("OUTPUT");
    expect(screen.getAllByRole("status")).toContain(status);
    expect(status.parentElement?.tagName).toBe("P");
    expect(screen.queryByRole("button", { name: /^第 1 题，/ })).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: "只看有失分的题目" }));
    expect(screen.getByRole("button", { name: /^第 1 题，/ })).toBeTruthy();
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("uses native output only while the selected question is loading", async () => {
    const pending = deferredResponse();
    const result = makeResult();
    installApi((call) => call.url.pathname.endsWith("/questions/question-1") ? pending.promise : resultRoute(call, result));
    render(<App />);
    await screen.findByLabelText("总分");
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    const status = screen.getByText("正在加载本题…");
    expect(status.tagName).toBe("OUTPUT");
    expect(screen.getAllByRole("status")).toContain(status);
    expect(status.parentElement?.classList.contains("muted")).toBe(true);
    await act(async () => pending.resolve(dataResponse({ question: makeQuestion() })));
    await screen.findByRole("heading", { name: "申请复核" });
    expect(screen.queryByText("正在加载本题…")).toBeNull();
  });

  it("uses native output for existing-release appeal confirmation without adding a submit control", async () => {
    installApi((call) => call.url.pathname === "/api/v1/student/question-appeals" ? jsonResponse({ appeals: [makeAppeal({ status: "under_review" })] }) : undefined);
    render(<QuestionAppealForm
      examID="exam-1"
      releaseID="release-7"
      releaseVersion={7}
      question={makeQuestion()}
      allowedReasonCodes={["recognition_error"]}
      onSubmitted={vi.fn()}
    />);
    const message = await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    const status = message.closest("output");
    expect(status).not.toBeNull();
    expect(screen.getByRole("status")).toBe(status);
    expect(status?.classList.contains("question-appeal-confirmation")).toBe(true);
    expect(screen.getByText("学校复核中")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
  });

  it("uses native output for the first corner and keeps normalized two-point selection", async () => {
    vi.stubGlobal("PointerEvent", MouseEvent);
    installApi();
    render(<QuestionAppealForm
      examID="exam-1"
      releaseID="release-7"
      releaseVersion={7}
      question={makeQuestion()}
      allowedReasonCodes={["recognition_error"]}
      onSubmitted={vi.fn()}
    />);
    fireEvent.load(screen.getByAltText("本题答题区域"));
    fireEvent.click(screen.getByRole("checkbox", { name: "使用两点点击框选" }));
    const canvas = screen.getByRole("application", { name: "在答题图上框选争议区域" });
    vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue(new DOMRect(10, 20, 100, 200));
    fireEvent.pointerDown(canvas, { clientX: 30, clientY: 60 });
    const status = screen.getByText("起点已选，请点击区域的另一角。");
    expect(status.tagName).toBe("OUTPUT");
    expect(screen.getAllByRole("status")).toContain(status);
    fireEvent.pointerDown(canvas, { clientX: 70, clientY: 140 });
    expect(screen.queryByText("起点已选，请点击区域的另一角。")).toBeNull();
    fireEvent.click(screen.getByText("精确调整区域（百分比）"));
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "距左侧" }).valueAsNumber).toBe(20);
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "距顶部" }).valueAsNumber).toBe(20);
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "宽度" }).valueAsNumber).toBe(40);
    expect(screen.getByRole<HTMLInputElement>("spinbutton", { name: "高度" }).valueAsNumber).toBe(40);
  });
});
